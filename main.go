// Command flighttrack is a terminal dashboard for following a single flight.
//
// You enter the flight number from your ticket, then the destination airport.
// It takes one snapshot from the OpenSky Network, works out an arrival time,
// and then counts down against your system clock rather than hammering the
// API. A slow background refresh keeps the estimate from drifting; press r for
// an immediate one.
//
//	flighttrack                    # start the dashboard
//	flighttrack -flight BA117 -to JFK
//
// Credentials, if you have them, come from the environment:
//
//	OPENSKY_CLIENT_ID / OPENSKY_CLIENT_SECRET
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
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

// ------------------------------------------------------------------- styles

var (
	colAccent = lipgloss.AdaptiveColor{Light: "#005f87", Dark: "#5fd7ff"}
	colDim    = lipgloss.AdaptiveColor{Light: "#6c6c6c", Dark: "#8a8a8a"}
	colGood   = lipgloss.AdaptiveColor{Light: "#005f00", Dark: "#5fd75f"}
	colWarn   = lipgloss.AdaptiveColor{Light: "#875f00", Dark: "#ffd75f"}
	colBad    = lipgloss.AdaptiveColor{Light: "#870000", Dark: "#ff5f5f"}

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	dimStyle   = lipgloss.NewStyle().Foreground(colDim)
	goodStyle  = lipgloss.NewStyle().Foreground(colGood)
	warnStyle  = lipgloss.NewStyle().Foreground(colWarn)
	badStyle   = lipgloss.NewStyle().Foreground(colBad)
	bigStyle   = lipgloss.NewStyle().Bold(true).Foreground(colAccent)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colDim).
			Padding(0, 1)

	labelStyle = lipgloss.NewStyle().Foreground(colDim).Width(14)

	selStyle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
)

// -------------------------------------------------------------------- model

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

type snapshotMsg struct {
	snap *opensky.Snapshot
	err  error
}

type tickMsg time.Time

type notifyDoneMsg struct{ err error }

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

func (m *model) logf(tone lipgloss.Style, format string, args ...any) {
	m.logs = append(m.logs, logEntry{at: time.Now(), text: fmt.Sprintf(format, args...), tone: tone})
	if len(m.logs) > maxLogLines {
		m.logs = m.logs[len(m.logs)-maxLogLines:]
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, m.spin.Tick, tick()}
	if m.loading {
		cmds = append(cmds, m.fetch())
	}
	return tea.Batch(cmds...)
}

// ------------------------------------------------------------------- update

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		m.now = time.Time(msg)
		var cmds []tea.Cmd
		cmds = append(cmds, tick())
		if m.screen == screenDash && m.autoRefresh && !m.loading && m.now.After(m.nextRefresh) {
			m.loading = true
			m.nextRefresh = m.now.Add(m.refreshIn)
			cmds = append(cmds, m.fetch())
		}
		if c := m.checkArrival(); c != nil {
			cmds = append(cmds, c)
		}
		return m, tea.Batch(cmds...)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case notifyDoneMsg:
		if msg.err != nil {
			m.logf(badStyle, "notify failed: %v", msg.err)
		}
		return m, nil

	case snapshotMsg:
		return m.onSnapshot(msg)

	case tea.KeyMsg:
		return m.onKey(msg)
	}

	// Anything else goes to whichever text input is live.
	var cmd tea.Cmd
	switch m.screen {
	case screenFlight:
		m.flightInput, cmd = m.flightInput.Update(msg)
	case screenDest:
		m.destInput, cmd = m.destInput.Update(msg)
	}
	return m, cmd
}

func (m model) onSnapshot(msg snapshotMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.errMsg = msg.err.Error()
		m.logf(badStyle, "refresh failed: %v", msg.err)
		return m, nil
	}
	m.errMsg = ""
	m.snapTaken = msg.snap.Taken
	m.snapFetched = msg.snap.Fetched
	m.fromCache = false // this is live data now
	m.nextRefresh = time.Now().Add(m.refreshIn)

	obs, found := msg.snap.Lookup(m.flight)
	if !found {
		m.obs = nil
		if m.screen == screenFlight {
			m.errMsg = fmt.Sprintf("%s is not currently visible. It may not be airborne yet, or it is outside ADS-B coverage.", m.flight)
			return m, nil
		}
		m.logf(warnStyle, "%s not in this snapshot", m.flight)
		return m, nil
	}

	var cmds []tea.Cmd

	// Compare against the previous snapshot to catch a takeoff or a landing.
	if m.prevOnGround != nil && *m.prevOnGround != obs.OnGround {
		if *m.prevOnGround && !obs.OnGround {
			m.logf(goodStyle, "%s has taken off", m.flight)
			cmds = append(cmds, m.send(notify.Event{
				Kind: "takeoff", Flight: m.flight.String(), Callsign: obs.Callsign,
				Icao24: obs.Icao24, Time: time.Now(),
				Title: fmt.Sprintf("%s has taken off", m.flight),
				Body:  describe(obs),
			}))
		} else {
			m.logf(goodStyle, "%s has landed", m.flight)
			cmds = append(cmds, m.send(notify.Event{
				Kind: "landing", Flight: m.flight.String(), Callsign: obs.Callsign,
				Icao24: obs.Icao24, Time: time.Now(),
				Title: fmt.Sprintf("%s has landed", m.flight),
				Body:  describe(obs),
			}))
			m.arrivalAlerted = true // no point warning about an arrival now
		}
	}
	onGround := obs.OnGround
	m.prevOnGround = &onGround
	m.obs = obs

	m.recompute()
	if m.screen == screenFlight {
		m.screen = screenDest
		// Ask for the origin first, but only during initial setup and only if
		// it was not already supplied on the command line.
		m.pickOrigin = m.origin.IATA == "" && m.dest.IATA == ""
		m.destInput.Focus()
		m.logf(dimStyle, "found %s, %s", obs.Callsign, phaseWord(obs))
	}
	return m, tea.Batch(cmds...)
}

func (m model) onHistoryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.hist.Entries)
	switch msg.String() {
	case "up", "k":
		if m.histIdx > 0 {
			m.histIdx--
		}
		return m, nil
	case "down", "j":
		if m.histIdx < n-1 {
			m.histIdx++
		}
		return m, nil
	case "n":
		m.screen = screenFlight
		m.flightInput.Focus()
		return m, textinput.Blink
	case "c":
		if err := history.Clear(m.histPath); err != nil {
			m.errMsg = fmt.Sprintf("could not clear history: %v", err)
			return m, nil
		}
		m.hist = &history.File{}
		m.histIdx = 0
		m.screen = screenFlight
		m.flightInput.Focus()
		return m, textinput.Blink
	case "q", "esc":
		return m, tea.Quit
	case "enter", " ":
		if n == 0 {
			m.screen = screenFlight
			m.flightInput.Focus()
			return m, textinput.Blink
		}
		return m.resume(m.hist.Entries[m.histIdx])
	}
	return m, nil
}

// resume reopens a past search. If its stored position is still inside the
// cache lifetime the dashboard opens on that, spending nothing; otherwise it
// falls through to a normal fetch.
func (m model) resume(e history.Entry) (tea.Model, tea.Cmd) {
	id, err := opensky.ParseFlight(e.FlightID)
	if err != nil {
		m.errMsg = fmt.Sprintf("stored entry %q is unusable: %v", e.FlightID, err)
		return m, nil
	}
	m.flight = id
	m.flightTyped = e.Flight
	m.origin, m.dest = airports.Airport{}, airports.Airport{}
	if a, ok := airports.Lookup(e.Origin); ok {
		m.origin = a
	}
	if a, ok := airports.Lookup(e.Dest); ok {
		m.dest = a
	}
	m.trackStartNM = 0
	m.arrivalAlerted = false
	m.prevOnGround = nil
	m.errMsg = ""

	now := time.Now()
	if obs := e.Observation(); obs != nil && e.Usable(now) && m.dest.IATA != "" {
		age, _ := e.Age(now)
		m.obs = obs
		m.snapFetched = e.Position.Fetched
		m.snapTaken = e.Position.Fetched
		m.fromCache = true
		m.cacheAge = e.Position.Fetched
		onGround := obs.OnGround
		m.prevOnGround = &onGround
		m.recompute()
		m.screen = screenDash
		m.nextRefresh = now.Add(m.refreshIn)
		m.logf(warnStyle, "opened on cached data, %s old", age.Round(time.Second))
		return m, nil
	}

	// Nothing usable stored: go and get a position.
	m.loading = true
	if m.dest.IATA == "" {
		m.screen = screenDest
		m.pickOrigin = false
		m.destInput.Focus()
	} else {
		m.screen = screenDash
	}
	return m, m.fetch()
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

// leavePicker moves on after an airport was chosen or skipped. During first
// setup it walks origin then destination; once both are known it returns to
// the dashboard.
func (m model) leavePicker() (tea.Model, tea.Cmd) {
	m.errMsg = ""
	m.destInput.SetValue("")
	m.suggestions = nil
	m.suggestIdx = 0

	if m.pickOrigin && m.dest.IATA == "" {
		m.pickOrigin = false // straight on to the destination
		return m, textinput.Blink
	}

	m.pickOrigin = false
	m.screen = screenDash
	m.destInput.Blur()
	m.recompute()
	m.nextRefresh = time.Now().Add(m.refreshIn)
	m.persist() // so a crash or kill does not lose the search
	return m, nil
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

// checkArrival fires the approaching-arrival alert once per flight.
func (m *model) checkArrival() tea.Cmd {
	if m.arrivalAlerted || !m.est.Valid || m.obs == nil || m.obs.OnGround {
		return nil
	}
	left := m.est.Countdown(m.now)
	if left <= 0 || left > arrivalAlertAt {
		return nil
	}
	m.arrivalAlerted = true
	m.logf(warnStyle, "%s arriving in about %s", m.flight, eta.FormatDuration(left))
	return m.send(notify.Event{
		Kind: "arriving", Flight: m.flight.String(), Time: time.Now(),
		Title: fmt.Sprintf("%s is arriving soon", m.flight),
		Body: fmt.Sprintf("Estimated arrival at %s in about %s (%s GMT).",
			m.dest.IATA, eta.FormatDuration(left), m.est.ArrivalUTC.Format("15:04")),
	})
}

func (m model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// ctrl+c always quits, whatever screen is up.
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}

	switch m.screen {
	case screenHistory:
		return m.onHistoryKey(msg)

	case screenFlight:
		switch msg.Type {
		case tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			if m.loading {
				return m, nil
			}
			id, err := opensky.ParseFlight(m.flightInput.Value())
			if err != nil {
				m.errMsg = err.Error()
				return m, nil
			}
			m.flight = id
			m.flightTyped = strings.ToUpper(strings.TrimSpace(m.flightInput.Value()))
			m.errMsg = ""
			m.loading = true
			m.arrivalAlerted = false
			m.prevOnGround = nil
			return m, m.fetch()
		}
		var cmd tea.Cmd
		m.flightInput, cmd = m.flightInput.Update(msg)
		return m, cmd

	case screenDest:
		switch msg.Type {
		case tea.KeyEsc:
			m.screen = screenFlight
			m.errMsg = ""
			m.flightInput.Focus()
			return m, nil
		case tea.KeyUp:
			if m.suggestIdx > 0 {
				m.suggestIdx--
			}
			return m, nil
		case tea.KeyDown:
			if m.suggestIdx < len(m.suggestions)-1 {
				m.suggestIdx++
			}
			return m, nil
		case tea.KeyEnter:
			// Origin is optional, so an empty box skips it rather than erroring.
			if m.pickOrigin && strings.TrimSpace(m.destInput.Value()) == "" {
				return m.leavePicker()
			}
			if len(m.suggestions) == 0 {
				m.errMsg = "no airport matches that; try the 3-letter code from your ticket"
				return m, nil
			}
			picked := m.suggestions[m.suggestIdx]
			if m.pickOrigin {
				m.origin = picked
				m.logf(dimStyle, "origin set to %s", picked.IATA)
			} else {
				m.dest = picked
				m.trackStartNM = 0 // a new destination restarts the fallback bar
				m.logf(dimStyle, "destination set to %s", picked.IATA)
			}
			return m.leavePicker()
		}
		var cmd tea.Cmd
		m.destInput, cmd = m.destInput.Update(msg)
		m.suggestions = airports.Search(m.destInput.Value(), 6)
		if m.suggestIdx >= len(m.suggestions) {
			m.suggestIdx = 0
		}
		return m, cmd

	case screenDash:
		return m.onDashKey(msg)
	}
	return m, nil
}

// Menu items on the dashboard, in display order.
const (
	menuRefresh = iota
	menuNotify
	menuAuto
	menuOrigin
	menuDest
	menuFlight
	menuQuit
	menuCount
)

func (m model) onDashKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.menuIdx > 0 {
			m.menuIdx--
		}
		return m, nil
	case "down", "j":
		if m.menuIdx < menuCount-1 {
			m.menuIdx++
		}
		return m, nil
	case "enter", " ":
		return m.activate(m.menuIdx)
	case "r":
		return m.activate(menuRefresh)
	case "n":
		return m.activate(menuNotify)
	case "a":
		return m.activate(menuAuto)
	case "o":
		return m.activate(menuOrigin)
	case "d":
		return m.activate(menuDest)
	case "f":
		return m.activate(menuFlight)
	case "q", "esc":
		return m, tea.Quit
	}
	return m, nil
}

func (m model) activate(item int) (tea.Model, tea.Cmd) {
	switch item {
	case menuRefresh:
		if m.loading {
			return m, nil
		}
		m.loading = true
		m.logf(dimStyle, "manual refresh (%d credits)", opensky.CreditsPerSnapshot)
		return m, m.fetch()

	case menuNotify:
		m.notifyOn = !m.notifyOn
		if m.notifyOn && !m.toast.Available() {
			m.notifyOn = false
			m.logf(badStyle, "desktop notifications are Windows-only")
			return m, nil
		}
		m.logf(dimStyle, "notifications %s", onOff(m.notifyOn))
		return m, nil

	case menuAuto:
		m.autoRefresh = !m.autoRefresh
		if m.autoRefresh {
			m.nextRefresh = time.Now().Add(m.refreshIn)
		}
		m.logf(dimStyle, "auto-refresh %s", onOff(m.autoRefresh))
		return m, nil

	case menuOrigin, menuDest:
		m.pickOrigin = item == menuOrigin
		m.screen = screenDest
		m.destInput.SetValue("")
		m.suggestions = nil
		m.suggestIdx = 0
		m.destInput.Focus()
		return m, textinput.Blink

	case menuFlight:
		m.screen = screenFlight
		m.flightInput.SetValue("")
		m.flightInput.Focus()
		m.obs = nil
		m.est = eta.Estimate{}
		m.prevOnGround = nil
		m.arrivalAlerted = false
		return m, textinput.Blink

	case menuQuit:
		return m, tea.Quit
	}
	return m, nil
}

// --------------------------------------------------------------------- view

func (m model) View() string {
	switch m.screen {
	case screenHistory:
		return m.viewHistory()
	case screenFlight:
		return m.viewFlight()
	case screenDest:
		return m.viewDest()
	default:
		return m.viewDash()
	}
}

// freshnessStyle maps a cache age onto the green/amber/red scale. The red is
// the lighter shade so it stays legible on a dark terminal.
func freshnessStyle(f history.Freshness) lipgloss.Style {
	switch f {
	case history.Fresh:
		return goodStyle
	case history.Aging:
		return warnStyle
	default:
		return badStyle
	}
}

func (m model) viewHistory() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("flighttrack") + "\n")
	b.WriteString(dimStyle.Render("Previously searched") + "\n\n")

	now := time.Now()
	for i, e := range m.hist.Entries {
		age, has := e.Age(now)

		var bar, note string
		if has {
			f := history.Rate(age)
			s := freshnessStyle(f)
			// Three blocks, filled according to how fresh the data is.
			switch f {
			case history.Fresh:
				bar = s.Render("###")
			case history.Aging:
				bar = s.Render("##") + dimStyle.Render("#")
			default:
				bar = s.Render("#") + dimStyle.Render("##")
			}
			note = s.Render(fmt.Sprintf("%-8s", shortAge(age)))
			if f == history.Stale {
				note += dimStyle.Render(" will refetch")
			} else {
				note += dimStyle.Render(" cached")
			}
		} else {
			bar = dimStyle.Render("---")
			note = dimStyle.Render(fmt.Sprintf("%-8s no cached position", ""))
		}

		line := fmt.Sprintf("%-22s %s  %s", e.Label(), bar, note)
		if i == m.histIdx {
			b.WriteString(selStyle.Render("> ") + line + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	if len(m.hist.Entries) == 0 {
		b.WriteString(dimStyle.Render("  nothing saved yet") + "\n")
	}

	if m.errMsg != "" {
		b.WriteString("\n  " + badStyle.Render(m.errMsg) + "\n")
	}

	b.WriteString("\n" + dimStyle.Render("enter to reopen    n for a new search    c to clear history    q to quit") + "\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("green under 10m, amber under 1h, red older and refetched on open (%s)",
		m.histPath)) + "\n")
	return b.String()
}

// shortAge renders a duration compactly for the list, e.g. "3m", "2h14m".
func shortAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func (m model) viewFlight() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("flighttrack") + "\n")
	b.WriteString(dimStyle.Render("Live flight tracking via the OpenSky Network") + "\n\n")
	b.WriteString("Flight number from your ticket:\n\n")
	b.WriteString("  " + m.flightInput.View() + "\n\n")
	if m.loading {
		b.WriteString("  " + m.spin.View() + dimStyle.Render(" searching the sky...") + "\n")
	}
	if m.errMsg != "" {
		b.WriteString("  " + badStyle.Render(m.errMsg) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("examples: BA117   KL1234   LH400   DLH400 (ICAO callsign)") + "\n")
	b.WriteString(dimStyle.Render("enter to search    esc to quit") + "\n")
	return b.String()
}

func (m model) viewDest() string {
	var b strings.Builder
	if m.pickOrigin {
		b.WriteString(titleStyle.Render("Origin") + "\n")
	} else {
		b.WriteString(titleStyle.Render("Destination") + "\n")
	}
	if m.obs != nil {
		b.WriteString(dimStyle.Render(fmt.Sprintf("Tracking %s, %s", m.obs.Callsign, phaseWord(m.obs))) + "\n")
	}
	if m.pickOrigin {
		b.WriteString(dimStyle.Render("Optional. Only used to show how far along the route the flight is.") + "\n\n")
		b.WriteString("Departure airport:\n\n")
	} else {
		b.WriteString(dimStyle.Render("Needed for the arrival estimate. OpenSky does not publish routes.") + "\n\n")
		b.WriteString("Arrival airport:\n\n")
	}
	b.WriteString("  " + m.destInput.View() + "\n\n")

	for i, a := range m.suggestions {
		line := "  " + a.Label()
		if i == m.suggestIdx {
			b.WriteString(selStyle.Render("> "+a.Label()) + "\n")
		} else {
			b.WriteString(dimStyle.Render(line) + "\n")
		}
	}
	if m.errMsg != "" {
		b.WriteString("\n  " + badStyle.Render(m.errMsg) + "\n")
	}
	hint := "type a code or city    up/down to choose    enter to confirm    esc to go back"
	if m.pickOrigin {
		hint = "type a code or city    up/down to choose    enter to confirm    enter blank to skip"
	}
	b.WriteString("\n" + dimStyle.Render(hint) + "\n")
	return b.String()
}

func (m model) viewDash() string {
	total := m.width
	if total < 80 {
		total = 80
	}
	rightW := 34
	leftW := total - rightW - 6
	if leftW < 40 {
		leftW = 40
	}

	leftBody := m.flightPanel(leftW - 2) // less the panel padding
	rightBody := m.menuPanel() + "\n\n" + m.infoPanel(rightW-2)

	// Both boxes are drawn to the taller of the two, so their borders line up
	// however much content each happens to hold.
	h := max(lipgloss.Height(leftBody), lipgloss.Height(rightBody))

	left := panelStyle.Width(leftW).Height(h).Render(leftBody)
	right := panelStyle.Width(rightW).Height(h).Render(rightBody)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right) + "\n" +
		dimStyle.Render(" r refresh   n notify   a auto   o origin   d destination   f flight   q quit") + "\n"
}

func (m model) flightPanel(w int) string {
	var b strings.Builder
	// The heading is the number as typed; the callsign is only worth showing
	// when the wire differs from it.
	head := m.flightTyped
	if head == "" {
		head = m.flight.String()
	}
	b.WriteString(titleStyle.Render(head))
	if m.obs != nil && m.obs.Callsign != "" && m.obs.Callsign != head {
		b.WriteString(dimStyle.Render("   callsign " + m.obs.Callsign))
	}
	b.WriteString("\n\n")

	if m.obs == nil {
		b.WriteString(warnStyle.Render("Not currently visible.") + "\n\n")
		b.WriteString(dimStyle.Render("The flight may not be airborne, or it may be\noutside ADS-B receiver coverage. Press r to\ntry again.") + "\n")
		return b.String()
	}

	status := goodStyle.Render("AIRBORNE")
	if m.obs.OnGround {
		status = warnStyle.Render("ON GROUND")
	}
	b.WriteString(labelStyle.Render("Status") + status + "\n")
	if m.obs.HasPos {
		b.WriteString(labelStyle.Render("Position") + fmt.Sprintf("%.4f, %.4f", m.obs.Lat, m.obs.Lon) + "\n")
	}
	if !m.obs.OnGround {
		b.WriteString(labelStyle.Render("Altitude") + fmt.Sprintf("%.0f ft", m.obs.AltitudeFt()) + "\n")
	}
	b.WriteString(labelStyle.Render("Ground speed") + fmt.Sprintf("%.0f kts", m.obs.SpeedKts()) + "\n")
	if v := m.obs.ClimbFPM(); v > 100 {
		b.WriteString(labelStyle.Render("Vertical") + fmt.Sprintf("climbing %.0f ft/min", v) + "\n")
	} else if v < -100 {
		b.WriteString(labelStyle.Render("Vertical") + fmt.Sprintf("descending %.0f ft/min", -v) + "\n")
	}
	b.WriteString(labelStyle.Render("Track") + fmt.Sprintf("%.0f deg %s", m.obs.Track, eta.Compass(m.obs.Track)) + "\n")

	b.WriteString("\n" + dimStyle.Render(strings.Repeat("-", w)) + "\n\n")

	if m.dest.IATA == "" {
		b.WriteString(dimStyle.Render("No destination set. Press d.") + "\n")
		return b.String()
	}
	b.WriteString(labelStyle.Render("Destination") + m.dest.Label() + "\n")

	if !m.est.Valid {
		b.WriteString("\n" + warnStyle.Render("No arrival estimate") + "\n")
		b.WriteString(dimStyle.Render(wrap(m.est.Reason, w)) + "\n")
		if m.est.DistanceNM > 0 {
			b.WriteString("\n" + labelStyle.Render("Distance") + fmt.Sprintf("%.0f nm", m.est.DistanceNM) + "\n")
		}
		return b.String()
	}

	b.WriteString(labelStyle.Render("Distance") + fmt.Sprintf("%.0f nm, bearing %.0f %s",
		m.est.DistanceNM, m.est.BearingDeg, eta.Compass(m.est.BearingDeg)) + "\n\n")

	arr := m.est.ArrivalUTC
	b.WriteString(labelStyle.Render("ETA (GMT)") + bigStyle.Render(arr.Format("15:04:05")) +
		dimStyle.Render("  "+arr.Format("Mon 2 Jan")) + "\n")
	b.WriteString(labelStyle.Render("ETA (local)") + arr.Local().Format("15:04:05 MST") + "\n\n")

	left := m.est.Countdown(m.now)
	cd := bigStyle.Render(eta.FormatDuration(left))
	if left < 0 {
		cd = badStyle.Render(eta.FormatDuration(left))
	} else if left < arrivalAlertAt {
		cd = warnStyle.Render(eta.FormatDuration(left))
	}
	b.WriteString(labelStyle.Render("Countdown") + cd + "\n\n")

	if bar := m.progressSection(w); bar != "" {
		b.WriteString(bar + "\n\n")
	}

	q := dimStyle
	if m.est.Quality == eta.Rough {
		q = warnStyle
	}
	b.WriteString(q.Render(fmt.Sprintf("[%s] %s", m.est.Quality, wrap(m.est.Reason, w))) + "\n")
	return b.String()
}

// progressSection draws the journey bar. With an origin it is true route
// progress; without one it can only show how far the aircraft has come since
// tracking started, which is labelled as such rather than passed off as more.
func (m model) progressSection(w int) string {
	frac, from, to, caption := 0.0, "", "", ""

	switch {
	case m.est.HasProgress:
		frac = m.est.Progress
		from, to = m.est.Origin.IATA, m.est.Destination.IATA
		caption = fmt.Sprintf("%.0f nm flown of %.0f nm", m.est.TotalNM-m.est.DistanceNM, m.est.TotalNM)

	case m.trackStartNM > 0 && m.est.DistanceNM > 0:
		frac = (m.trackStartNM - m.est.DistanceNM) / m.trackStartNM
		if frac < 0 {
			frac = 0 // the aircraft has moved away from the destination
		}
		from, to = "start", m.est.Destination.IATA
		caption = "since tracking began - set an origin for true route progress"

	default:
		return ""
	}

	// The label column, the two endpoint markers and the percentage all take
	// space away from the bar itself.
	barW := w - 14 - len(from) - len(to) - 8
	if barW < 8 {
		barW = 8
	}
	filled := int(math.Round(frac * float64(barW)))
	if filled > barW {
		filled = barW
	}

	var b strings.Builder
	b.WriteString(labelStyle.Render("Progress"))
	b.WriteString(dimStyle.Render(from + " "))
	b.WriteString(goodStyle.Render(strings.Repeat("=", filled)))
	b.WriteString(dimStyle.Render(strings.Repeat(".", barW-filled)))
	b.WriteString(dimStyle.Render(" "+to) + fmt.Sprintf("  %3.0f%%", frac*100))
	b.WriteString("\n" + labelStyle.Render("") + dimStyle.Render(wrap(caption, w-14)))
	return b.String()
}

func (m model) menuPanel() string {
	items := make([]string, menuCount)
	items[menuRefresh] = "Refresh now"
	items[menuNotify] = "Notifications  " + onOff(m.notifyOn)
	items[menuAuto] = "Auto-refresh   " + onOff(m.autoRefresh)
	items[menuOrigin] = "Set origin"
	if m.origin.IATA != "" {
		items[menuOrigin] = "Change origin  " + dimStyle.Render(m.origin.IATA)
	}
	items[menuDest] = "Change destination"
	items[menuFlight] = "Change flight"
	items[menuQuit] = "Quit"

	var b strings.Builder
	b.WriteString(titleStyle.Render("MENU") + "\n\n")
	for i, it := range items {
		if i == m.menuIdx {
			b.WriteString(selStyle.Render("> "+it) + "\n")
		} else {
			b.WriteString("  " + it + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m model) infoPanel(w int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("INFO") + "\n\n")

	// Everything here is measured against m.now, the same clock the countdown
	// uses, so the two can never disagree on screen.
	if m.loading {
		b.WriteString(m.spin.View() + dimStyle.Render(" fetching...") + "\n")
	} else if m.snapFetched.IsZero() {
		b.WriteString(dimStyle.Render("no data yet") + "\n")
	} else {
		age := m.now.Sub(m.snapFetched).Round(time.Second)
		if age < 0 {
			age = 0
		}
		s := freshnessStyle(history.Rate(age))
		b.WriteString(dimStyle.Render("data age   ") + s.Render(shortAge(age)))
		if m.fromCache {
			b.WriteString(warnStyle.Render(" cached"))
		}
		b.WriteString("\n")
	}

	if m.autoRefresh {
		in := m.nextRefresh.Sub(m.now).Round(time.Second)
		if in < 0 {
			in = 0
		}
		b.WriteString(dimStyle.Render("next auto  ") + in.String() + "\n")
	} else {
		b.WriteString(dimStyle.Render("next auto  off") + "\n")
	}

	b.WriteString(dimStyle.Render("api calls  ") +
		fmt.Sprintf("%d credits / %d", m.client.CreditsUsed(), m.client.DailyCredits()) + "\n")
	mode := "anonymous"
	if m.client.Authenticated() {
		mode = "authenticated"
	}
	b.WriteString(dimStyle.Render("api mode   ") + mode + "\n")
	b.WriteString(dimStyle.Render("clock      ") + m.now.UTC().Format("15:04:05") + " GMT" + "\n")

	if m.errMsg != "" {
		b.WriteString("\n" + badStyle.Render(wrap(m.errMsg, w)) + "\n")
	}

	if len(m.logs) > 0 {
		b.WriteString("\n" + titleStyle.Render("EVENTS") + "\n\n")
		for _, l := range m.logs {
			// The timestamp takes 9 columns of the line.
			b.WriteString(dimStyle.Render(l.at.Format("15:04:05")+" ") + l.tone.Render(trunc(l.text, w-9)) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ------------------------------------------------------------------ helpers

func onOff(b bool) string {
	if b {
		return goodStyle.Render("on")
	}
	return dimStyle.Render("off")
}

func phaseWord(o *opensky.Observation) string {
	if o.OnGround {
		return "on the ground"
	}
	return "airborne"
}

func describe(o *opensky.Observation) string {
	var b strings.Builder
	if o.HasPos {
		fmt.Fprintf(&b, "Position %.4f, %.4f. ", o.Lat, o.Lon)
	}
	if o.OnGround {
		fmt.Fprintf(&b, "On the ground at %.0f kts.", o.SpeedKts())
	} else {
		fmt.Fprintf(&b, "Altitude %.0f ft, %.0f kts.", o.AltitudeFt(), o.SpeedKts())
	}
	return b.String()
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "."
}

// wrap breaks text on spaces at width, for the fixed-width panels.
func wrap(s string, width int) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var lines []string
	cur := words[0]
	for _, w := range words[1:] {
		if len(cur)+1+len(w) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	return strings.Join(append(lines, cur), "\n")
}

// --------------------------------------------------------------------- main

// runHistory is the `flighttrack history` subcommand: show what is stored, or
// wipe it.
func runHistory(args []string) int {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	doClear := fs.Bool("clear", false, "delete the history file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path, err := history.DefaultPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	if *doClear {
		if err := history.Clear(path); err != nil {
			fmt.Fprintf(os.Stderr, "could not clear history: %v\n", err)
			return 1
		}
		fmt.Printf("history cleared (%s)\n", path)
		return 0
	}
	f, err := history.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
	}
	fmt.Println(path)
	if len(f.Entries) == 0 {
		fmt.Println("(empty)")
		return 0
	}
	now := time.Now()
	for _, e := range f.Entries {
		age, has := e.Age(now)
		switch {
		case !has:
			fmt.Printf("  %-22s no cached position\n", e.Label())
		case e.Usable(now):
			fmt.Printf("  %-22s %s old (%s, reusable)\n", e.Label(), shortAge(age), history.Rate(age))
		default:
			fmt.Printf("  %-22s %s old (stale, will refetch)\n", e.Label(), shortAge(age))
		}
	}
	return 0
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

func main() {
	// One binary, two modes. A bare invocation opens the dashboard, which is
	// what most runs want, so the subcommand is only checked for explicitly.
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
	f := registerDashFlags()
	flightArg, originArg, destArg := f.flight, f.origin, f.dest
	refreshArg, hookArg, noAuto := f.refresh, f.hook, f.noAuto
	flag.Parse()

	if *refreshArg < time.Minute {
		fmt.Fprintln(os.Stderr, "refresh interval must be at least 1m, to stay inside the free API quota")
		os.Exit(2)
	}

	fi := textinput.New()
	fi.Placeholder = "BA117"
	fi.CharLimit = 16
	fi.Width = 24
	fi.Prompt = "> "
	fi.Focus()

	di := textinput.New()
	di.Placeholder = "JFK"
	di.CharLimit = 40
	di.Width = 32
	di.Prompt = "> "

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colAccent)

	histPath, pathErr := history.DefaultPath()
	hist := &history.File{}
	var histWarning string
	if pathErr != nil {
		histWarning = pathErr.Error()
		histPath = ""
	} else if loaded, err := history.Load(histPath); err != nil {
		histWarning = err.Error() // unreadable file: carry on with an empty one
		hist = loaded
	} else {
		hist = loaded
	}

	m := model{
		client:      opensky.New(os.Getenv("OPENSKY_CLIENT_ID"), os.Getenv("OPENSKY_CLIENT_SECRET")),
		screen:      screenFlight,
		hist:        hist,
		histPath:    histPath,
		histWarning: histWarning,
		flightInput: fi,
		destInput:   di,
		spin:        sp,
		now:         time.Now(),
		notifyOn:    notify.Toast{}.Available(),
		autoRefresh: !*noAuto,
		refreshIn:   *refreshArg,
	}

	if *hookArg != "" {
		w, err := notify.NewWebhook(*hookArg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "webhook: %v\n", err)
			os.Exit(2)
		}
		m.webhook = w
	}

	// Preseed from flags so the dashboard can come up without typing.
	if *originArg != "" {
		a, ok := airports.Lookup(*originArg)
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown airport code %q\n", *originArg)
			os.Exit(2)
		}
		m.origin = a
	}
	if *destArg != "" {
		a, ok := airports.Lookup(*destArg)
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown airport code %q\n", *destArg)
			os.Exit(2)
		}
		m.dest = a
	}
	if *flightArg != "" {
		id, err := opensky.ParseFlight(*flightArg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(2)
		}
		m.flight = id
		m.flightTyped = strings.ToUpper(strings.TrimSpace(*flightArg))
		m.loading = true
	} else if len(m.hist.Entries) > 0 {
		// Nothing named on the command line and there is a past search to
		// offer, so start on the list rather than an empty prompt.
		m.screen = screenHistory
	}
	if m.histWarning != "" {
		m.logf(warnStyle, "history: %s", m.histWarning)
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "flighttrack: %v\n", err)
		os.Exit(1)
	}
	// Exit save: whatever was on screen at the end becomes the top of the
	// history, cached position included.
	if fm, ok := final.(model); ok {
		fm.persist()
	}
}
