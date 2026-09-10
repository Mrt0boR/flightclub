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
	pendingPhase phase
	pendingCount int
	lostReported bool
}

type watcher struct {
	client    *opensky.Client
	targets   []opensky.FlightID
	states    map[string]*flightState
	notifiers *notify.Set
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
	var savedStates map[string]*flightState
	if err := json.Unmarshal(buf, &savedStates); err != nil {
		log.Printf("warning: ignoring unreadable state file %s: %v", w.stateFile, err)
		return
	}
	for _, id := range w.targets {
		if saved, ok := savedStates[id.String()]; ok && saved != nil {
			saved.pendingPhase, saved.pendingCount = "", 0
			w.states[id.String()] = saved
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
	tmpPath := w.stateFile + ".tmp"
	if err := os.WriteFile(tmpPath, buf, 0o600); err != nil {
		log.Printf("warning: cannot write state file: %v", err)
		return
	}
	if err := os.Rename(tmpPath, w.stateFile); err != nil {
		log.Printf("warning: cannot replace state file: %v", err)
	}
}

// ------------------------------------------------------------------ polling

func (w *watcher) fire(event notify.Event) {
	for _, err := range w.notifiers.Notify(event) {
		log.Printf("notify: %v", err)
	}
}

func (w *watcher) poll(ctx context.Context) error {
	snapshot, err := w.client.Fetch(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, id := range w.targets {
		key := id.String()
		state, ok := w.states[key]
		if !ok {
			state = &flightState{Flight: key, Phase: phaseUnknown}
			w.states[key] = state
		}
		obs, _ := snapshot.Lookup(id)
		w.observe(state, obs, now)
	}
	w.save()
	return nil
}

// observe folds one observation into one flight's state, raising an event if
// the flight has changed phase or dropped out of coverage.
func (w *watcher) observe(state *flightState, obs *opensky.Observation, now time.Time) {
	if obs == nil {
		w.reportLostContact(state, now)
		return
	}

	state.LastSeen = obs.Seen
	state.Icao24 = obs.Icao24
	state.Callsign = obs.Callsign
	state.lostReported = false

	nextPhase := phaseAir
	if obs.OnGround {
		nextPhase = phaseGround
	}
	if !w.confirmChange(state, nextPhase) {
		return
	}

	prevPhase := state.Phase
	state.Phase = nextPhase
	state.pendingPhase, state.pendingCount = "", 0
	w.fire(phaseEvent(state, obs, prevPhase, nextPhase, now))
}

// reportLostContact fires once when a flight that was airborne has gone quiet
// for too long. ADS-B coverage has holes, so losing contact is not the same as
// landing and is reported as its own thing rather than guessed at.
func (w *watcher) reportLostContact(state *flightState, now time.Time) {
	if state.Phase != phaseAir || state.lostReported || state.LastSeen.IsZero() ||
		now.Sub(state.LastSeen) <= w.lostAfter {
		return
	}
	state.lostReported = true
	w.fire(notify.Event{
		Kind: "signal_lost", Flight: state.Flight, Callsign: state.Callsign,
		Icao24: state.Icao24, Time: now,
		Title: fmt.Sprintf("%s: contact lost", state.Flight),
		Body: fmt.Sprintf("No ADS-B contact for %s (last seen %s). It may have landed, or simply flown out of receiver coverage.",
			w.lostAfter.Round(time.Minute), state.LastSeen.Local().Format("15:04:05 MST")),
	})
}

// confirmChange reports whether a phase change has now held for long enough to
// be believed, updating the debounce counter as it goes.
func (w *watcher) confirmChange(state *flightState, nextPhase phase) bool {
	if nextPhase == state.Phase {
		state.pendingPhase, state.pendingCount = "", 0
		return false
	}
	if state.pendingPhase == nextPhase {
		state.pendingCount++
	} else {
		state.pendingPhase, state.pendingCount = nextPhase, 1
	}
	return state.pendingCount >= w.confirm
}

// phaseEvent builds the notification for a confirmed phase change.
func phaseEvent(state *flightState, obs *opensky.Observation, prevPhase, nextPhase phase, now time.Time) notify.Event {
	event := notify.Event{
		Flight: state.Flight, Callsign: obs.Callsign, Icao24: obs.Icao24, Time: now,
		Lat: obs.Lat, Lon: obs.Lon, AltitudeM: obs.GeoAlt, SpeedKts: obs.SpeedKts(),
	}
	switch {
	case prevPhase == phaseGround && nextPhase == phaseAir:
		event.Kind = "takeoff"
		event.Title = fmt.Sprintf("%s has taken off", state.Flight)
		event.Body = fmt.Sprintf("Airborne at %s. %s", now.Local().Format("15:04:05 MST"), describe(obs))
	case prevPhase == phaseAir && nextPhase == phaseGround:
		event.Kind = "landing"
		event.Title = fmt.Sprintf("%s has landed", state.Flight)
		event.Body = fmt.Sprintf("On the ground at %s. %s", now.Local().Format("15:04:05 MST"), describe(obs))
	default:
		event.Kind = "tracking"
		event.Title = fmt.Sprintf("%s: now tracking", state.Flight)
		event.Body = fmt.Sprintf("Picked up %s, currently %s. %s", obs.Callsign, nextPhase, describe(obs))
	}
	return event
}
