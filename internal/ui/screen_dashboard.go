package ui

// The dashboard: the menu on the right, its keyboard shortcuts, and the
// two-panel layout. The panels themselves are drawn in panels.go.

import (
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"flighttrack/internal/eta"
	"flighttrack/internal/opensky"
)

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

// activate runs one menu item, whether it was reached by the cursor or by its
// shortcut key.
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
