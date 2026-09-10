// Command flighttrack is a terminal flight tracker built on the OpenSky
// Network live states API.
//
// The dashboard takes the flight number from your ticket, finds the aircraft,
// works out an arrival time, and counts down against your system clock rather
// than hammering the API. Watch mode does the same job with no interface,
// reporting only takeoffs and landings.
//
//	flighttrack                                   # dashboard
//	flighttrack -flight QF2 -from SYD -to LHR     # dashboard, preseeded
//	flighttrack watch -flights QF2,CX251          # background
//	flighttrack history                           # saved searches
//
// Credentials, if you have them, come from the environment:
//
//	OPENSKY_CLIENT_ID / OPENSKY_CLIENT_SECRET
//
// Source layout:
//
//	model.go             dashboard state, messages, commands
//	update.go            the message loop and key routing
//	view.go              which screen renders
//	screen_*.go          one file per screen: its keys and its rendering
//	panels.go            the dashboard's panels
//	styles.go            colours and text styles
//	format.go            small shared formatting helpers
//	cmd_watch.go         the watch subcommand
//	watcher.go           the polling engine behind it
//	cmd_history.go       the history subcommand
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"flighttrack/internal/airports"
	"flighttrack/internal/history"
	"flighttrack/internal/notify"
	"flighttrack/internal/opensky"
)

func main() {
	// One binary, several modes. A bare invocation opens the dashboard, which
	// is what most runs want, so subcommands are only checked for explicitly.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "watch":
			os.Exit(runWatch(os.Args[2:]))
		case "history":
			os.Exit(runHistory(os.Args[2:]))
		case "help", "--help":
			registerDashFlags() // so the option list is populated
			usage()
			return
		}
	}
	runDashboard()
}

func usage() {
	fmt.Fprintln(os.Stderr, `flighttrack - follow flights using the OpenSky Network

Usage:
  flighttrack [options]              open the dashboard (default)
  flighttrack watch [options]        follow flights in the background, no interface
  flighttrack history [-clear]       show or wipe saved searches
  flighttrack help                   this message

Dashboard options:`)
	flag.PrintDefaults()
	fmt.Fprintln(os.Stderr, `
Examples:
  flighttrack
  flighttrack -flight QF2 -to LHR
  flighttrack watch -flights QF2,CX251 -notify console,desktop
  flighttrack watch -help

Credentials, if you have them, come from the environment:
  OPENSKY_CLIENT_ID / OPENSKY_CLIENT_SECRET`)
}

// ---------------------------------------------------------------- dashboard

// dashFlags holds the dashboard's options. Registering them is split out from
// parsing so `flighttrack help` can list them without starting the dashboard.
type dashFlags struct {
	flight  *string
	origin  *string
	dest    *string
	refresh *time.Duration
	hook    *string
	noAuto  *bool
}

func registerDashFlags() dashFlags {
	flag.Usage = usage
	return dashFlags{
		flight:  flag.String("flight", "", "flight number to start on, e.g. BA117"),
		origin:  flag.String("from", "", "origin airport code, e.g. DUB (optional, enables the route progress bar)"),
		dest:    flag.String("to", "", "destination airport code, e.g. JFK"),
		refresh: flag.Duration("refresh", defaultRefresh, "how often to auto-refresh the snapshot"),
		hook:    flag.String("webhook", os.Getenv("FLIGHTTRACK_WEBHOOK"), "optional https URL to POST events to"),
		noAuto:  flag.Bool("no-auto-refresh", false, "start with auto-refresh disabled"),
	}
}

func runDashboard() {
	flags := registerDashFlags()
	flag.Parse()

	if *flags.refresh < time.Minute {
		fmt.Fprintln(os.Stderr, "refresh interval must be at least 1m, to stay inside the free API quota")
		os.Exit(2)
	}

	m := newModel(*flags.refresh, !*flags.noAuto)

	if *flags.hook != "" {
		hook, err := notify.NewWebhook(*flags.hook)
		if err != nil {
			fmt.Fprintf(os.Stderr, "webhook: %v\n", err)
			os.Exit(2)
		}
		m.webhook = hook
	}
	if err := m.preseed(*flags.flight, *flags.origin, *flags.dest); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}
	if m.histWarning != "" {
		m.logf(warnStyle, "history: %s", m.histWarning)
	}

	program := tea.NewProgram(m, tea.WithAltScreen())
	finalState, err := program.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "flighttrack: %v\n", err)
		os.Exit(1)
	}
	// Exit save: whatever was on screen at the end becomes the top of the
	// history, cached position included.
	if finalModel, ok := finalState.(model); ok {
		finalModel.persist()
	}
}

// newModel builds the dashboard's starting state: inputs, spinner, API client
// and whatever history is on disk.
func newModel(refresh time.Duration, autoRefresh bool) model {
	flightInput := textinput.New()
	flightInput.Placeholder = "BA117"
	flightInput.CharLimit = 16
	flightInput.Width = 24
	flightInput.Prompt = "> "
	flightInput.Focus()

	destInput := textinput.New()
	destInput.Placeholder = "JFK"
	destInput.CharLimit = 40
	destInput.Width = 32
	destInput.Prompt = "> "

	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = lipgloss.NewStyle().Foreground(colAccent)

	hist, histPath, histWarning := loadHistory()

	return model{
		client:      opensky.New(os.Getenv("OPENSKY_CLIENT_ID"), os.Getenv("OPENSKY_CLIENT_SECRET")),
		screen:      screenFlight,
		hist:        hist,
		histPath:    histPath,
		histWarning: histWarning,
		flightInput: flightInput,
		destInput:   destInput,
		spin:        spin,
		now:         time.Now(),
		notifyOn:    notify.Toast{}.Available(),
		autoRefresh: autoRefresh,
		refreshIn:   refresh,
	}
}

// loadHistory reads the saved searches. A missing or unreadable file is never
// fatal: the dashboard opens with an empty list and says so in its log.
func loadHistory() (*history.File, string, string) {
	path, err := history.DefaultPath()
	if err != nil {
		return &history.File{}, "", err.Error()
	}
	loaded, err := history.Load(path)
	if err != nil {
		return loaded, path, err.Error()
	}
	return loaded, path, ""
}

// preseed applies the command-line flight and airports, and decides which
// screen to open on.
func (m *model) preseed(flight, origin, dest string) error {
	if origin != "" {
		airport, ok := airports.Lookup(origin)
		if !ok {
			return fmt.Errorf("unknown airport code %q", origin)
		}
		m.origin = airport
	}
	if dest != "" {
		airport, ok := airports.Lookup(dest)
		if !ok {
			return fmt.Errorf("unknown airport code %q", dest)
		}
		m.dest = airport
	}
	if flight != "" {
		id, err := opensky.ParseFlight(flight)
		if err != nil {
			return err
		}
		m.flight = id
		m.flightTyped = strings.ToUpper(strings.TrimSpace(flight))
		m.loading = true
		return nil
	}
	if len(m.hist.Entries) > 0 {
		// Nothing named on the command line and there is a past search to
		// offer, so start on the list rather than an empty prompt.
		m.screen = screenHistory
	}
	return nil
}
