package ui

// Dev mode: a fake flight and a keystroke that fakes its landing, so the
// notification path (desktop toast, webhook, and later the phone push) can be
// exercised on demand instead of waiting for a real aircraft to land.
//
// Enabled with `flighttrack -dev`. Nothing here runs otherwise.

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"flighttrack/internal/airports"
	"flighttrack/internal/opensky"
)

// seedDevFlight drops the dashboard straight onto a synthetic flight on short
// final into Dublin — airborne, descending, ~40 nm out — without touching the
// API. Press ctrl+t to fake the landing.
func (m *model) seedDevFlight() {
	m.dev = true

	id, _ := opensky.ParseFlight("DEV1")
	m.flight = id
	m.flightTyped = "DEV1"

	if a, ok := airports.Lookup("AMS"); ok {
		m.origin = a
	}
	if a, ok := airports.Lookup("DUB"); ok {
		m.dest = a
	}

	onGround := false
	m.prevOnGround = &onGround
	m.obs = &opensky.Observation{
		Icao24:   "dev001",
		Callsign: "DEV1",
		Lat:      53.6808,
		Lon:      -5.2737,
		GeoAlt:   4015, // ~13,175 ft
		BaroAlt:  4015,
		Velocity: 135, // ~263 kts
		Track:    270,
		VertRate: -9.1, // ~ -1790 ft/min
		OnGround: false,
		HasPos:   true,
		Seen:     time.Now(),
	}
	m.snapTaken = time.Now()
	m.snapFetched = time.Now()
	m.screen = screenDash
	m.recompute()
	m.nextRefresh = time.Now().Add(m.refreshIn)

	m.logf(warnStyle, "DEV MODE - ctrl+t simulates a landing")
}

// simulateLanding feeds the dashboard a synthetic on-ground snapshot for the
// dev flight, so the real landing path runs: the event log, the desktop
// notification, and any webhook.
func (m model) simulateLanding() (tea.Model, tea.Cmd) {
	if !m.dev || m.obs == nil {
		return m, nil
	}

	landed := *m.obs
	landed.OnGround = true
	landed.Velocity = 6 // taxi speed, m/s
	landed.VertRate = 0
	landed.Seen = time.Now()
	if m.dest.IATA != "" {
		landed.Lat, landed.Lon = m.dest.Lat, m.dest.Lon
	}

	snap := &opensky.Snapshot{
		Taken:    time.Now(),
		Fetched:  time.Now(),
		Aircraft: map[opensky.FlightID]*opensky.Observation{m.flight: &landed},
	}
	m.logf(dimStyle, "dev: simulating landing")
	return m, func() tea.Msg { return snapshotMsg{snap: snap} }
}
