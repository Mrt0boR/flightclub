package main

// The headless side of the app: no interface, several flights at once, speaks
// up only when one takes off or lands. Invoked as `flighttrack watch`.

import (
	"context"
	"encoding/json"
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

type phase string

const (
	phaseUnknown phase = "unknown"
	phaseGround  phase = "ground"
	phaseAir     phase = "air"
)

type flightState struct {
	Flight   string    `json:"flight"`
	Phase    phase     `json:"phase"`
	LastSeen time.Time `json:"last_seen"`
	Icao24   string    `json:"icao24,omitempty"`
	Callsign string    `json:"callsign,omitempty"`

	// Debounce: on_ground flickers during the ground roll, so a change is only
	// accepted once it has held for `confirm` polls.
	pending      phase
	pendingCount int
	lostReported bool
}

type watcher struct {
	client    *opensky.Client
	targets   []opensky.FlightID
	states    map[string]*flightState
	set       *notify.Set
	confirm   int
	lostAfter time.Duration
	stateFile string
}

func (w *watcher) load() {
	if w.stateFile == "" {
		return
	}
	buf, err := os.ReadFile(w.stateFile)
	if err != nil {
		return // first run
	}
	var saved map[string]*flightState
	if err := json.Unmarshal(buf, &saved); err != nil {
		log.Printf("warning: ignoring unreadable state file %s: %v", w.stateFile, err)
		return
	}
	for _, id := range w.targets {
		if s, ok := saved[id.String()]; ok && s != nil {
			s.pending, s.pendingCount = "", 0
			w.states[id.String()] = s
		}
	}
}

func (w *watcher) save() {
	if w.stateFile == "" {
		return
	}
	buf, err := json.MarshalIndent(w.states, "", "  ")
	if err != nil {
		return
	}
	tmp := w.stateFile + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		log.Printf("warning: cannot write state file: %v", err)
		return
	}
	if err := os.Rename(tmp, w.stateFile); err != nil {
		log.Printf("warning: cannot replace state file: %v", err)
	}
}

func (w *watcher) fire(e notify.Event) {
	for _, err := range w.set.Notify(e) {
		log.Printf("notify: %v", err)
	}
}

func (w *watcher) poll(ctx context.Context) error {
	snap, err := w.client.Fetch(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, id := range w.targets {
		key := id.String()
		st, ok := w.states[key]
		if !ok {
			st = &flightState{Flight: key, Phase: phaseUnknown}
			w.states[key] = st
		}
		obs, _ := snap.Lookup(id)
		w.observe(st, obs, now)
	}
	w.save()
	return nil
}

func (w *watcher) observe(st *flightState, obs *opensky.Observation, now time.Time) {
	if obs == nil {
		// ADS-B coverage has holes. Losing contact is not the same as landing,
		// so it is reported as its own thing rather than guessed at.
		if st.Phase == phaseAir && !st.lostReported && !st.LastSeen.IsZero() &&
			now.Sub(st.LastSeen) > w.lostAfter {
			st.lostReported = true
			w.fire(notify.Event{
				Kind: "signal_lost", Flight: st.Flight, Callsign: st.Callsign,
				Icao24: st.Icao24, Time: now,
				Title: fmt.Sprintf("%s: contact lost", st.Flight),
				Body: fmt.Sprintf("No ADS-B contact for %s (last seen %s). It may have landed, or simply flown out of receiver coverage.",
					w.lostAfter.Round(time.Minute), st.LastSeen.Local().Format("15:04:05 MST")),
			})
		}
		return
	}

	st.LastSeen = obs.Seen
	st.Icao24 = obs.Icao24
	st.Callsign = obs.Callsign
	st.lostReported = false

	next := phaseAir
	if obs.OnGround {
		next = phaseGround
	}
	if next == st.Phase {
		st.pending, st.pendingCount = "", 0
		return
	}
	if st.pending == next {
		st.pendingCount++
	} else {
		st.pending, st.pendingCount = next, 1
	}
	if st.pendingCount < w.confirm {
		return
	}

	prev := st.Phase
	st.Phase = next
	st.pending, st.pendingCount = "", 0

	e := notify.Event{
		Flight: st.Flight, Callsign: obs.Callsign, Icao24: obs.Icao24, Time: now,
		Lat: obs.Lat, Lon: obs.Lon, AltitudeM: obs.GeoAlt, SpeedKts: obs.SpeedKts(),
	}
	switch {
	case prev == phaseGround && next == phaseAir:
		e.Kind = "takeoff"
		e.Title = fmt.Sprintf("%s has taken off", st.Flight)
		e.Body = fmt.Sprintf("Airborne at %s. %s", now.Local().Format("15:04:05 MST"), describe(obs))
	case prev == phaseAir && next == phaseGround:
		e.Kind = "landing"
		e.Title = fmt.Sprintf("%s has landed", st.Flight)
		e.Body = fmt.Sprintf("On the ground at %s. %s", now.Local().Format("15:04:05 MST"), describe(obs))
	default:
		e.Kind = "tracking"
		e.Title = fmt.Sprintf("%s: now tracking", st.Flight)
		e.Body = fmt.Sprintf("Picked up %s, currently %s. %s", obs.Callsign, next, describe(obs))
	}
	w.fire(e)
}

// runWatch is the `flighttrack watch` subcommand. It returns a process exit
// code rather than calling os.Exit, so main stays in charge of shutdown.
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

	var targets []opensky.FlightID
	seen := map[string]bool{}
	for _, raw := range strings.Split(*flights, ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		id, err := opensky.ParseFlight(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 2
		}
		if !seen[id.String()] {
			seen[id.String()] = true
			targets = append(targets, id)
		}
	}
	if len(targets) == 0 {
		fmt.Fprintln(os.Stderr, "error: no valid flight numbers given")
		return 2
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].String() < targets[j].String() })

	set := notify.NewSet()
	for _, name := range strings.Split(*notifiers, ",") {
		switch strings.TrimSpace(strings.ToLower(name)) {
		case "":
		case "console", "stdout":
			set.Add(notify.Console{})
		case "desktop", "toast":
			t := notify.Toast{}
			if !t.Available() {
				fmt.Fprintln(os.Stderr, "error: desktop notifications are Windows-only")
				return 2
			}
			set.Add(t)
		case "webhook":
			w, err := notify.NewWebhook(*hookURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 2
			}
			set.Add(w)
		default:
			fmt.Fprintf(os.Stderr, "error: unknown notifier %q\n", strings.TrimSpace(name))
			return 2
		}
	}
	if len(set.Names()) == 0 {
		fmt.Fprintln(os.Stderr, "error: no notifiers enabled")
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

	run := func() {
		pollCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		if err := w.poll(pollCtx); err != nil {
			if ctx.Err() == nil {
				log.Printf("poll failed: %v", err)
			}
			return
		}
		for _, id := range targets {
			st := w.states[id.String()]
			if st == nil || st.LastSeen.IsZero() {
				log.Printf("%s: not currently visible", id)
				continue
			}
			log.Printf("%s: %s (callsign %s, last seen %s)", id, st.Phase, st.Callsign,
				st.LastSeen.Local().Format("15:04:05"))
		}
	}

	run()
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
			run()
		}
	}
}
