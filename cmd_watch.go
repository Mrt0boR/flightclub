package main

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
func runWatch(args []string) int {
	log.SetFlags(log.Ltime)

	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: flighttrack watch -flights KL1234,BA117 [options]")
		fmt.Fprintln(os.Stderr, "\nFollows flights in the background and reports takeoffs and landings.")
		fmt.Fprintln(os.Stderr, "\nOptions:")
		fs.PrintDefaults()
	}
	var (
		flights   = fs.String("flights", "", "comma-separated flight numbers, e.g. KL1234,BA117 (required)")
		interval  = fs.Duration("interval", 90*time.Second, "how often to poll OpenSky")
		notifiers = fs.String("notify", "console", "comma-separated: console, desktop, webhook")
		hookURL   = fs.String("webhook-url", os.Getenv("FLIGHTTRACK_WEBHOOK"), "https URL to POST events to")
		confirm   = fs.Int("confirm", 2, "consecutive polls a state change must hold before it is reported")
		lostAfter = fs.Duration("lost-after", 25*time.Minute, "report a loss of contact after this long with no data")
		stateFile = fs.String("state-file", "flighttrack-watch-state.json", "where to persist flight phases across restarts")
		once      = fs.Bool("once", false, "poll once and exit")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if strings.TrimSpace(*flights) == "" {
		fmt.Fprintln(os.Stderr, "error: -flights is required, e.g. flighttrack watch -flights KL1234,BA117")
		fs.Usage()
		return 2
	}

	targets, err := parseTargets(*flights)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}

	set, err := buildNotifiers(*notifiers, *hookURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}

	client := opensky.New(os.Getenv("OPENSKY_CLIENT_ID"), os.Getenv("OPENSKY_CLIENT_SECRET"))
	if !client.Authenticated() {
		log.Print("no OPENSKY_CLIENT_ID/OPENSKY_CLIENT_SECRET set: anonymous access allows about 100 polls a day")
		if *interval < 15*time.Minute && !*once {
			log.Printf("warning: -interval %s exhausts the anonymous quota in roughly %s; register a free API client or use -interval 15m",
				*interval, (100 * *interval).Round(time.Minute))
		}
	}
	if *confirm < 1 {
		*confirm = 1
	}

	w := &watcher{
		client:    client,
		targets:   targets,
		states:    map[string]*flightState{},
		set:       set,
		confirm:   *confirm,
		lostAfter: *lostAfter,
		stateFile: *stateFile,
	}
	w.load()

	names := make([]string, 0, len(targets))
	for _, id := range targets {
		names = append(names, id.String())
	}
	log.Printf("watching %s every %s via %s", strings.Join(names, ", "), *interval, strings.Join(set.Names(), "+"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	w.runOnce(ctx)
	if *once {
		return 0
	}

	ticker := time.NewTicker(*interval)
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
		st := w.states[id.String()]
		if st == nil || st.LastSeen.IsZero() {
			log.Printf("%s: not currently visible", id)
			continue
		}
		log.Printf("%s: %s (callsign %s, last seen %s)", id, st.Phase, st.Callsign,
			st.LastSeen.Local().Format("15:04:05"))
	}
}

// parseTargets turns the -flights list into normalized, de-duplicated ids.
func parseTargets(list string) ([]opensky.FlightID, error) {
	var targets []opensky.FlightID
	seen := map[string]bool{}
	for _, raw := range strings.Split(list, ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		id, err := opensky.ParseFlight(raw)
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
func buildNotifiers(spec, hookURL string) (*notify.Set, error) {
	set := notify.NewSet()
	for _, name := range strings.Split(spec, ",") {
		switch strings.TrimSpace(strings.ToLower(name)) {
		case "":
		case "console", "stdout":
			set.Add(notify.Console{})
		case "desktop", "toast":
			t := notify.Toast{}
			if !t.Available() {
				return nil, fmt.Errorf("desktop notifications are Windows-only")
			}
			set.Add(t)
		case "webhook":
			w, err := notify.NewWebhook(hookURL)
			if err != nil {
				return nil, err
			}
			set.Add(w)
		default:
			return nil, fmt.Errorf("unknown notifier %q", strings.TrimSpace(name))
		}
	}
	if len(set.Names()) == 0 {
		return nil, fmt.Errorf("no notifiers enabled")
	}
	return set, nil
}
