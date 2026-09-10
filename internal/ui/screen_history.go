package ui

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
	"flighttrack/internal/textfmt"
)

func (m model) onHistoryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	entryCount := len(m.hist.Entries)
	switch msg.String() {
	case "up", "k":
		if m.histIdx > 0 {
			m.histIdx--
		}
		return m, nil
	case "down", "j":
		if m.histIdx < entryCount-1 {
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
		if entryCount == 0 {
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
func (m model) resume(entry history.Entry) (tea.Model, tea.Cmd) {
	id, err := opensky.ParseFlight(entry.FlightID)
	if err != nil {
		m.errMsg = fmt.Sprintf("stored entry %q is unusable: %v", entry.FlightID, err)
		return m, nil
	}
	m.flight = id
	m.flightTyped = entry.Flight
	m.origin, m.dest = airports.Airport{}, airports.Airport{}
	if airport, ok := airports.Lookup(entry.Origin); ok {
		m.origin = airport
	}
	if airport, ok := airports.Lookup(entry.Dest); ok {
		m.dest = airport
	}
	m.trackStartNM = 0
	m.arrivalAlerted = false
	m.prevOnGround = nil
	m.errMsg = ""

	now := time.Now()
	if obs := entry.Observation(); obs != nil && entry.Usable(now) && m.dest.IATA != "" {
		age, _ := entry.Age(now)
		m.obs = obs
		m.snapFetched = entry.Position.Fetched
		m.snapTaken = entry.Position.Fetched
		m.fromCache = true
		m.cacheAge = entry.Position.Fetched
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
	for i, entry := range m.hist.Entries {
		line := fmt.Sprintf("%-22s %s", entry.Label(), freshnessNote(entry, now))
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
func freshnessNote(entry history.Entry, now time.Time) string {
	age, hasPosition := entry.Age(now)
	if !hasPosition {
		return dimStyle.Render("---") + "  " + dimStyle.Render(fmt.Sprintf("%-8s no cached position", ""))
	}

	freshness := history.Rate(age)
	ageStyle := freshnessStyle(freshness)

	// Three blocks, filled according to how fresh the data is.
	var bar string
	switch freshness {
	case history.Fresh:
		bar = ageStyle.Render("###")
	case history.Aging:
		bar = ageStyle.Render("##") + dimStyle.Render("#")
	default:
		bar = ageStyle.Render("#") + dimStyle.Render("##")
	}

	note := ageStyle.Render(fmt.Sprintf("%-8s", textfmt.ShortAge(age)))
	if freshness == history.Stale {
		note += dimStyle.Render(" will refetch")
	} else {
		note += dimStyle.Render(" cached")
	}
	return bar + "  " + note
}
