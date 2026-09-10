package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"

	"flighttrack/internal/airports"
	"flighttrack/internal/eta"
	"flighttrack/internal/opensky"
)

// Prints what each screen actually renders, so the layout can be inspected
// without a terminal. It asserts nothing: run it and look.
//
//	go test -run TestPreviewRender -v .
func TestPreviewRender(t *testing.T) {
	now := time.Date(2026, 9, 6, 1, 30, 0, 0, time.UTC)

	fi := textinput.New()
	fi.Placeholder = "BA117"
	fi.Prompt = "> "
	fi.Width = 24
	fi.SetValue("BA117")

	di := textinput.New()
	di.Placeholder = "JFK"
	di.Prompt = "> "
	di.Width = 32
	di.SetValue("JFK")

	m := model{
		client:      opensky.New("", ""),
		flightInput: fi,
		destInput:   di,
		spin:        spinner.New(),
		now:         now,
		refreshIn:   defaultRefresh,
		width:       118,
		height:      40,
		notifyOn:    true,
		autoRefresh: true,
		nextRefresh: now.Add(3*time.Minute + 12*time.Second),
	}

	fmt.Println("\n########## SCREEN 1: FLIGHT ENTRY ##########")
	m.screen = screenFlight
	fmt.Println(m.View())

	fmt.Println("\n########## SCREEN 2: DESTINATION ##########")
	m.screen = screenDest
	m.obs = &opensky.Observation{Callsign: "BAW117", OnGround: false}
	m.suggestions = airports.Search("JFK", 6)
	fmt.Println(m.View())

	fmt.Println("\n########## SCREEN 3: DASHBOARD (cruise) ##########")
	dest, _ := airports.Lookup("JFK")
	id, _ := opensky.ParseFlight("BA117")
	m.screen = screenDash
	m.flight = id
	m.flightTyped = "BA117" // as the user typed it; the wire says BAW117
	m.dest = dest
	m.obs = &opensky.Observation{
		Icao24: "400a1b", Callsign: "BAW117",
		Lat: 53.1421, Lon: -30.8842, HasPos: true,
		GeoAlt: 11582, Velocity: 490 / 1.94384, Track: 283, VertRate: 0,
	}
	m.snapFetched = now.Add(-92 * time.Second)
	origin, _ := airports.Lookup("LHR")
	m.origin = origin
	m.est = eta.Compute(m.obs, origin, dest, m.snapFetched)
	m.logf(dimStyle, "found BAW117, airborne")
	m.logf(dimStyle, "destination set to JFK")
	fmt.Println(m.View())

	fmt.Println("\n########## SCREEN 3b: DASHBOARD (no origin, fallback bar) ##########")
	noOrigin := m
	noOrigin.origin = airports.Airport{}
	noOrigin.trackStartNM = 2600 // as if tracking began 2600 nm out
	noOrigin.est = eta.Compute(noOrigin.obs, airports.Airport{}, dest, noOrigin.snapFetched)
	fmt.Println(noOrigin.View())

	fmt.Println("\n########## SCREEN 4: DASHBOARD (not visible) ##########")
	m.obs = nil
	m.est = eta.Estimate{}
	m.errMsg = "OpenSky rate limit reached (HTTP 429)"
	m.menuIdx = menuRefresh
	fmt.Println(m.View())

	fmt.Println("\n########## SCREEN 5: DASHBOARD (on ground, no ETA) ##########")
	m.errMsg = ""
	m.obs = &opensky.Observation{
		Icao24: "400a1b", Callsign: "BAW117",
		Lat: 51.4706, Lon: -0.4619, HasPos: true,
		OnGround: true, Velocity: 12 / 1.94384, Track: 90,
	}
	m.est = eta.Compute(m.obs, airports.Airport{}, dest, now)
	m.menuIdx = menuNotify
	fmt.Println(m.View())
}
