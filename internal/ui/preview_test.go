package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"

	"flighttrack/internal/airports"
	"flighttrack/internal/config"
	"flighttrack/internal/eta"
	"flighttrack/internal/history"
	"flighttrack/internal/notify"
	"flighttrack/internal/opensky"
	"flighttrack/internal/version"
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

	ci := textinput.New()
	ci.Placeholder = "https://discord.com/api/webhooks/..."
	ci.Prompt = "> "
	ci.Width = 50

	m := model{
		client:       opensky.New("", ""),
		flightInput:  fi,
		destInput:    di,
		discordInput: ci,
		handbook:     viewport.New(handbookWidth, 20),
		hist:         &history.File{},
		cfg:          &config.Config{},
		spin:         spinner.New(),
		now:          now,
		refreshIn:    defaultRefresh,
		width:        118,
		height:       40,
		notifyOn:     true,
		autoRefresh:  true,
		nextRefresh:  now.Add(3*time.Minute + 12*time.Second),
	}

	fmt.Println("########## SCREEN 0: MAIN MENU (nothing saved yet) ##########")
	m.screen = screenMain
	fmt.Println(m.View())

	fmt.Println("\n########## SCREEN 0b: MAIN MENU (history + discord configured) ##########")
	withSaved := m
	withSaved.hist = &history.File{Entries: []history.Entry{
		{Flight: "QF2", Origin: "SYD", Dest: "LHR"},
		{Flight: "BA117", Dest: "JFK"},
	}}
	withSaved.cfg = &config.Config{DiscordWebhookURL: "https://discord.com/api/webhooks/1/abc"}
	withSaved.discord = &notify.Discord{URL: withSaved.cfg.DiscordWebhookURL}
	fmt.Println(withSaved.View())

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

	fmt.Println("\n########## SCREEN 3c: DASHBOARD (update available) ##########")
	withUpdate := m
	withUpdate.hasUpdate = true
	withUpdate.update = version.Release{Version: "v1.4.0"}
	fmt.Println(withUpdate.View())

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

	fmt.Println("\n########## SCREEN 6: SETUP DISCORD WEBHOOK (empty) ##########")
	m.screen = screenDiscordSetup
	m.discordSetupMsg = ""
	fmt.Println(m.View())

	fmt.Println("\n########## SCREEN 6b: SETUP DISCORD WEBHOOK (saved) ##########")
	saved := m
	saved.discordInput.SetValue("https://discord.com/api/webhooks/123456789012345678/aBcDeF")
	saved.discordInput.CursorStart() // matches what activateMain does when reopening a saved URL
	saved.discordSetupOK = true
	saved.discordSetupMsg = "Saved. Press ctrl+t to send a test notification."
	fmt.Println(saved.View())

	fmt.Println("\n########## SCREEN 6c: SETUP DISCORD WEBHOOK (rejected) ##########")
	rejected := m
	rejected.discordInput.SetValue("https://discord.gg/not-a-webhook")
	rejected.discordSetupOK = false
	rejected.discordSetupMsg = `"discord.gg" does not look like a Discord webhook URL (expected discord.com)`
	fmt.Println(rejected.View())

	fmt.Println("\n########## SCREEN 7: SETTINGS ##########")
	m.screen = screenSettings
	m.settingsIdx = 1
	fmt.Println(m.View())

	fmt.Println("\n########## SCREEN 8: HANDBOOK ##########")
	m.screen = screenHandbook
	fmt.Println(m.View())
}
