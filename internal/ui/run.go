package ui

// The package entry point: everything the command line needs to start the
// dashboard, and nothing else. The model and its screens stay unexported.

import (
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

// Config is what the command line supplies to the dashboard. Every field is
// optional; the zero value opens on the saved-search list or an empty prompt.
type Config struct {
	Flight      string // flight number as the user typed it
	Origin      string // optional IATA/ICAO code, enables route progress
	Dest        string // optional IATA/ICAO code
	Refresh     time.Duration
	AutoRefresh bool
	WebhookURL  string // optional https endpoint for events
	Dev         bool   // seed a fake flight and enable the landing simulator
}

// Run opens the dashboard and blocks until the user quits. It saves the
// current search back to the history on the way out.
func Run(cfg Config) error {
	m := newModel(cfg.Refresh, cfg.AutoRefresh)

	if cfg.WebhookURL != "" {
		hook, err := notify.NewWebhook(cfg.WebhookURL)
		if err != nil {
			return fmt.Errorf("webhook: %w", err)
		}
		m.webhook = hook
	}
	if cfg.Dev {
		m.seedDevFlight()
	} else if err := m.preseed(cfg.Flight, cfg.Origin, cfg.Dest); err != nil {
		return err
	}
	if m.histWarning != "" {
		m.logf(warnStyle, "history: %s", m.histWarning)
	}

	program := tea.NewProgram(m, tea.WithAltScreen())
	finalState, err := program.Run()
	if err != nil {
		return err
	}
	// Exit save: whatever was on screen at the end becomes the top of the
	// history, cached position included.
	if finalModel, ok := finalState.(model); ok {
		finalModel.persist()
	}
	return nil
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
