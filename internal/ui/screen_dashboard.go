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

// shortcutItem maps an action key to the menu item it triggers.
var shortcutItem = map[string]int{
	"enter": -1, // -1 means "whatever the cursor is on"
	" ":     -1,
	"r":     menuRefresh,
	"n":     menuNotify,
	"a":     menuAuto,
	"o":     menuOrigin,
	"d":     menuDest,
	"f":     menuFlight,
}

func (m model) onDashKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	// Navigation is fine to hold down, so it does not go through the guard.
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
	case "q", "esc":
		return m, tea.Quit
	case "ctrl+t":
		return m.simulateLanding()
	}

	item, isAction := shortcutItem[key]
	if !isAction {
		return m, nil
	}
	// One press per tap: ignore the key-repeat stream from a held key.
	if m.heldKey(key) {
		return m, nil
	}
	if item == -1 {
		item = m.menuIdx
	}
	return m.activate(item)
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

	footer := " r refresh   n notify   a auto   o origin   d destination   f flight   q quit"
	if m.dev {
		footer = " DEV: ctrl+t simulates a landing  |  q quit"
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right) + "\n" +
		dimStyle.Render(footer) + "\n"
}
