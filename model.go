package main

// The dashboard's state, the messages that drive it, and the commands that
// produce those messages. Behaviour lives in update.go and the screen_*.go
// files; this is what they all operate on.

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"flighttrack/internal/airports"
	"flighttrack/internal/eta"
	"flighttrack/internal/history"
	"flighttrack/internal/notify"
	"flighttrack/internal/opensky"
)

type screen int

const (
	screenHistory screen = iota
	screenFlight
	screenDest
	screenDash
)

const (
	// How long the dashboard waits before pulling a fresh snapshot.
	defaultRefresh = 5 * time.Minute
	// How close to arrival the "arriving soon" alert fires.
	arrivalAlertAt = 15 * time.Minute
	maxLogLines    = 8
)

type logEntry struct {
	at   time.Time
	text string
	tone lipgloss.Style
}

type model struct {
	client *opensky.Client

	screen  screen
	loading bool
	errMsg  string

	flightInput textinput.Model
	destInput   textinput.Model
	spin        spinner.Model

	flight      opensky.FlightID
	flightTyped string // what the user actually entered, for display
	origin      airports.Airport
	dest        airports.Airport
	suggestions []airports.Airport
	suggestIdx  int
	pickOrigin  bool // the airport picker is choosing an origin, not a destination

	// Distance when tracking began, so a bar can still be shown for a flight
	// with no origin set. Reset whenever the flight or destination changes.
	trackStartNM float64

	obs         *opensky.Observation
	snapTaken   time.Time
	snapFetched time.Time
	est         eta.Estimate
	now         time.Time

	menuIdx     int
	notifyOn    bool
	autoRefresh bool
	refreshIn   time.Duration
	nextRefresh time.Time

	toast          notify.Toast
	webhook        *notify.Webhook
	prevOnGround   *bool
	arrivalAlerted bool
	logs           []logEntry

	hist        *history.File
	histPath    string
	histIdx     int
	fromCache   bool      // the shown position came from disk, not the API
	cacheAge    time.Time // when the cached position was originally fetched
	histWarning string

	width, height int
}

// ----------------------------------------------------------------- messages

type snapshotMsg struct {
	snap *opensky.Snapshot
	err  error
}

type tickMsg time.Time

type notifyDoneMsg struct{ err error }

// ----------------------------------------------------------------- commands

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) fetch() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		snap, err := client.Fetch(ctx)
		return snapshotMsg{snap: snap, err: err}
	}
}

// send delivers a notification off the UI goroutine, since a desktop balloon
// blocks for several seconds.
func (m model) send(e notify.Event) tea.Cmd {
	if !m.notifyOn {
		return nil
	}
	set := notify.NewSet(m.toast)
	if m.webhook != nil {
		set.Add(m.webhook)
	}
	return func() tea.Msg {
		errs := set.Notify(e)
		if len(errs) > 0 {
			return notifyDoneMsg{err: errs[0]}
		}
		return notifyDoneMsg{}
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, m.spin.Tick, tick()}
	if m.loading {
		cmds = append(cmds, m.fetch())
	}
	return tea.Batch(cmds...)
}

// ------------------------------------------------------------ state helpers

func (m *model) logf(tone lipgloss.Style, format string, args ...any) {
	m.logs = append(m.logs, logEntry{at: time.Now(), text: fmt.Sprintf(format, args...), tone: tone})
	if len(m.logs) > maxLogLines {
		m.logs = m.logs[len(m.logs)-maxLogLines:]
	}
}

// recompute refreshes the estimate from the current observation, and remembers
// the distance at the first fix so a bar can still be drawn for a flight whose
// origin was never supplied.
func (m *model) recompute() {
	if m.obs == nil || m.dest.IATA == "" {
		return
	}
	m.est = eta.Compute(m.obs, m.origin, m.dest, m.snapFetched)
	if m.trackStartNM == 0 && m.est.DistanceNM > 0 {
		m.trackStartNM = m.est.DistanceNM
	}
}

// persist writes the current search, and whatever position is on screen, back
// to the history file. Called when the dashboard opens and again on exit.
func (m model) persist() {
	if m.histPath == "" || m.hist == nil || m.flight.IsZero() {
		return
	}
	e := history.Entry{
		Flight:   m.flightTyped,
		FlightID: m.flight.String(),
		Origin:   m.origin.IATA,
		Dest:     m.dest.IATA,
		LastUsed: time.Now(),
	}
	if e.Flight == "" {
		e.Flight = m.flight.String()
	}
	// Only store a position that actually came from the API this run; writing
	// a cached one back would keep resetting its age.
	if m.obs != nil && !m.fromCache && !m.snapFetched.IsZero() {
		e.SetObservation(m.obs, m.snapFetched)
	} else if m.obs != nil && m.fromCache {
		e.SetObservation(m.obs, m.cacheAge)
	}
	m.hist.Record(e)
	if err := m.hist.Save(m.histPath); err != nil {
		// Nothing to show at exit; the dashboard reports it while running.
		return
	}
}
