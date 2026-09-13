package ui

// The Settings screen: pick a colour theme from the presets in styles.go.
// Applies immediately and is saved so it survives a restart.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) onSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.settingsIdx > 0 {
			m.settingsIdx--
		}
		return m, nil
	case "down", "j":
		if m.settingsIdx < len(themeOrder)-1 {
			m.settingsIdx++
		}
		return m, nil
	case "enter", " ":
		name := themeOrder[m.settingsIdx]
		applyTheme(name)
		m.cfg.Theme = name
		if err := m.saveConfig(); err != nil {
			m.errMsg = "could not save theme: " + err.Error()
		} else {
			m.errMsg = ""
		}
		return m, nil
	case "esc", "q":
		m.screen = screenMain
		return m, nil
	}
	return m, nil
}

func (m model) viewSettings() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Settings") + "\n")
	b.WriteString(dimStyle.Render("Colour theme") + "\n\n")

	for i, name := range themeOrder {
		t := themes[name]
		mark := "  "
		if name == currentTheme {
			mark = goodStyle.Render("* ")
		}
		line := mark + t.Label
		if i == m.settingsIdx {
			b.WriteString(selStyle.Render("> "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}

	if m.errMsg != "" {
		b.WriteString("\n  " + badStyle.Render(m.errMsg) + "\n")
	}

	b.WriteString("\n" + dimStyle.Render("* current    up/down to choose    enter to apply    esc to go back") + "\n")
	return b.String()
}
