package ui

// The flight-number prompt: the first thing a new search asks for.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"flighttrack/internal/opensky"
)

func (m model) onFlightKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
