package main

// The airport picker. One screen serves both the origin and the destination;
// m.pickOrigin decides which is being chosen, since the two differ only in
// wording and in whether the answer is optional.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"flighttrack/internal/airports"
)

func (m model) onPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
