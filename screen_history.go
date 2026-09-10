package main

// The opening screen: previously searched flights, colour-coded by how fresh
// the position saved against each one is. Picking a fresh entry reopens the
// dashboard without spending an API call.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"flighttrack/internal/airports"
	"flighttrack/internal/history"
	"flighttrack/internal/opensky"
)

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
		return m.startNewSearch()
	case "c":
		if err := history.Clear(m.histPath); err != nil {
			m.errMsg = fmt.Sprintf("could not clear history: %v", err)
			return m, nil
		}
		m.hist = &history.File{}
		m.histIdx = 0
		return m.startNewSearch()
	case "q", "esc":
		return m, tea.Quit
	case "enter", " ":
		if n == 0 {
			return m.startNewSearch()
		}
		return m.resume(m.hist.Entries[m.histIdx])
	}
	return m, nil
}

// startNewSearch leaves the history list for the flight-number prompt.
func (m model) startNewSearch() (tea.Model, tea.Cmd) {
	m.screen = screenFlight
	m.flightInput.Focus()
	return m, textinput.Blink
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

func (m model) viewHistory() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("flighttrack") + "\n")
	b.WriteString(dimStyle.Render("Previously searched") + "\n\n")

	now := time.Now()
	for i, e := range m.hist.Entries {
		line := fmt.Sprintf("%-22s %s", e.Label(), freshnessNote(e, now))
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

// freshnessNote renders one entry's staleness: a three-block bar plus the age
// and what will happen when it is opened.
func freshnessNote(e history.Entry, now time.Time) string {
	age, has := e.Age(now)
	if !has {
		return dimStyle.Render("---") + "  " + dimStyle.Render(fmt.Sprintf("%-8s no cached position", ""))
	}

	f := history.Rate(age)
	s := freshnessStyle(f)

	// Three blocks, filled according to how fresh the data is.
	var bar string
	switch f {
	case history.Fresh:
		bar = s.Render("###")
	case history.Aging:
		bar = s.Render("##") + dimStyle.Render("#")
	default:
		bar = s.Render("#") + dimStyle.Render("##")
	}

	note := s.Render(fmt.Sprintf("%-8s", shortAge(age)))
	if f == history.Stale {
		note += dimStyle.Render(" will refetch")
	} else {
		note += dimStyle.Render(" cached")
	}
	return bar + "  " + note
}
