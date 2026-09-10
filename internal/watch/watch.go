package watch

// The `flighttrack watch` subcommand: argument handling and the polling loop.
// The state machine it drives lives in watcher.go.

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"flighttrack/internal/notify"
	"flighttrack/internal/opensky"
)

// runWatch returns a process exit code rather than calling os.Exit, so main
// stays in charge of shutdown.
func Run(args []string) int {
	log.SetFlags(log.Ltime)

	flags := flag.NewFlagSet("watch", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: flighttrack watch -flights KL1234,BA117 [options]")
		fmt.Fprintln(os.Stderr, "\nFollows flights in the background and reports takeoffs and landings.")
		fmt.Fprintln(os.Stderr, "\nOptions:")
		flags.PrintDefaults()
	}
	var (
		flightList   = flags.String("flights", "", "comma-separated flight numbers, e.g. KL1234,BA117 (required)")
		pollEvery    = flags.Duration("interval", 90*time.Second, "how often to poll OpenSky")
		notifierList = flags.String("notify", "console", "comma-separated: console, desktop, webhook, ntfy")
		webhookURL   = flags.String("webhook-url", os.Getenv("FLIGHTTRACK_WEBHOOK"), "https URL to POST events to")
		ntfyURL      = flags.String("ntfy-url", os.Getenv("FLIGHTTRACK_NTFY"), "ntfy topic URL, e.g. https://ntfy.sh/my-topic (token from FLIGHTTRACK_NTFY_TOKEN)")
		confirmPolls = flags.Int("confirm", 2, "consecutive polls a state change must hold before it is reported")
		lostAfter    = flags.Duration("lost-after", 25*time.Minute, "report a loss of contact after this long with no data")
		stateFile    = flags.String("state-file", "flighttrack-watch-state.json", "where to persist flight phases across restarts")
		pollOnce     = flags.Bool("once", false, "poll once and exit")
	)
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if strings.TrimSpace(*flightList) == "" {
		fmt.Fprintln(os.Stderr, "error: -flights is required, e.g. flighttrack watch -flights KL1234,BA117")
		flags.Usage()
		return 2
	}

	targets, err := parseTargets(*flightList)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}

	notifiers, err := buildNotifiers(*notifierList, *webhookURL, *ntfyURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}

	client := opensky.New(os.Getenv("OPENSKY_CLIENT_ID"), os.Getenv("OPENSKY_CLIENT_SECRET"))
	if !client.Authenticated() {
		log.Print("no OPENSKY_CLIENT_ID/OPENSKY_CLIENT_SECRET set: anonymous access allows about 100 polls a day")
		if *pollEvery < 15*time.Minute && !*pollOnce {
			log.Printf("warning: -interval %s exhausts the anonymous quota in roughly %s; register a free API client or use -interval 15m",
				*pollEvery, (100 * *pollEvery).Round(time.Minute))
		}
	}
	if *confirmPolls < 1 {
		*confirmPolls = 1
	}

	w := &watcher{
		client:    client,
		targets:   targets,
		states:    map[string]*flightState{},
		notifiers: notifiers,
		confirm:   *confirmPolls,
		lostAfter: *lostAfter,
		stateFile: *stateFile,
	}
	w.load()

	names := make([]string, 0, len(targets))
	for _, id := range targets {
		names = append(names, id.String())
	}
	log.Printf("watching %s every %s via %s",
		strings.Join(names, ", "), *pollEvery, strings.Join(notifiers.Names(), "+"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	w.runOnce(ctx)
	if *pollOnce {
		return 0
	}

	ticker := time.NewTicker(*pollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Print("shutting down")
			w.save()
			return 0
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

// runOnce polls once and reports where each tracked flight stands.
func (w *watcher) runOnce(ctx context.Context) {
	pollCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	if err := w.poll(pollCtx); err != nil {
		if ctx.Err() == nil {
			log.Printf("poll failed: %v", err)
		}
		return
	}
	for _, id := range w.targets {
		state := w.states[id.String()]
		if state == nil || state.LastSeen.IsZero() {
			log.Printf("%s: not currently visible", id)
			continue
		}
		log.Printf("%s: %s (callsign %s, last seen %s)", id, state.Phase, state.Callsign,
			state.LastSeen.Local().Format("15:04:05"))
	}
}

// parseTargets turns the -flights list into normalized, de-duplicated ids.
func parseTargets(flightList string) ([]opensky.FlightID, error) {
	var targets []opensky.FlightID
	seen := map[string]bool{}
	for _, entered := range strings.Split(flightList, ",") {
		if strings.TrimSpace(entered) == "" {
			continue
		}
		id, err := opensky.ParseFlight(entered)
		if err != nil {
			return nil, err
		}
		if !seen[id.String()] {
			seen[id.String()] = true
			targets = append(targets, id)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no valid flight numbers given")
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].String() < targets[j].String() })
	return targets, nil
}

// buildNotifiers turns the -notify list into a set of delivery targets.
func buildNotifiers(notifierList, webhookURL, ntfyURL string) (*notify.Set, error) {
	notifiers := notify.NewSet()
	for _, name := range strings.Split(notifierList, ",") {
		switch strings.TrimSpace(strings.ToLower(name)) {
		case "":
		case "console", "stdout":
			notifiers.Add(notify.Console{})
		case "desktop", "toast":
			toast := notify.Toast{}
			if !toast.Available() {
				return nil, fmt.Errorf("desktop notifications are Windows-only")
			}
			notifiers.Add(toast)
		case "webhook":
			hook, err := notify.NewWebhook(webhookURL)
			if err != nil {
				return nil, err
			}
			notifiers.Add(hook)
		case "ntfy":
			n, err := notify.NewNtfy(ntfyURL, os.Getenv("FLIGHTTRACK_NTFY_TOKEN"))
			if err != nil {
				return nil, err
			}
			notifiers.Add(n)
		default:
			return nil, fmt.Errorf("unknown notifier %q", strings.TrimSpace(name))
		}
	}
	if len(notifiers.Names()) == 0 {
		return nil, fmt.Errorf("no notifiers enabled")
	}
	return notifiers, nil
}
