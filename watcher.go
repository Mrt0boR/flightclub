package main

// The polling engine behind watch mode. It holds one flightState per tracked
// flight and reports the moments that matter: a takeoff, a landing, or a loss
// of contact. The command-line side lives in cmd_watch.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
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

// ------------------------------------------------------------------ storage

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

// ------------------------------------------------------------------ polling

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

// observe folds one observation into one flight's state, raising an event if
// the flight has changed phase or dropped out of coverage.
func (w *watcher) observe(st *flightState, obs *opensky.Observation, now time.Time) {
	if obs == nil {
		w.reportLostContact(st, now)
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
	if !w.confirmChange(st, next) {
		return
	}

	prev := st.Phase
	st.Phase = next
	st.pending, st.pendingCount = "", 0
	w.fire(phaseEvent(st, obs, prev, next, now))
}

// reportLostContact fires once when a flight that was airborne has gone quiet
// for too long. ADS-B coverage has holes, so losing contact is not the same as
// landing and is reported as its own thing rather than guessed at.
func (w *watcher) reportLostContact(st *flightState, now time.Time) {
	if st.Phase != phaseAir || st.lostReported || st.LastSeen.IsZero() ||
		now.Sub(st.LastSeen) <= w.lostAfter {
		return
	}
	st.lostReported = true
	w.fire(notify.Event{
		Kind: "signal_lost", Flight: st.Flight, Callsign: st.Callsign,
		Icao24: st.Icao24, Time: now,
		Title: fmt.Sprintf("%s: contact lost", st.Flight),
		Body: fmt.Sprintf("No ADS-B contact for %s (last seen %s). It may have landed, or simply flown out of receiver coverage.",
			w.lostAfter.Round(time.Minute), st.LastSeen.Local().Format("15:04:05 MST")),
	})
}

// confirmChange reports whether a phase change has now held for long enough to
// be believed, updating the debounce counter as it goes.
func (w *watcher) confirmChange(st *flightState, next phase) bool {
	if next == st.Phase {
		st.pending, st.pendingCount = "", 0
		return false
	}
	if st.pending == next {
		st.pendingCount++
	} else {
		st.pending, st.pendingCount = next, 1
	}
	return st.pendingCount >= w.confirm
}

// phaseEvent builds the notification for a confirmed phase change.
func phaseEvent(st *flightState, obs *opensky.Observation, prev, next phase, now time.Time) notify.Event {
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
	return e
}
