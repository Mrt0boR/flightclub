package ui

// The top-level entry screen. Everything else — a new search, the saved
// searches, Discord setup, theming, the in-app handbook — hangs off this
// menu, so it is what you see whenever the app opens without a flight named
// on the command line.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"flighttrack/internal/textfmt"
)

// Menu items on the main screen, in display order.
const (
	mainSearch = iota
	mainRecent
	mainDiscordSetup
	mainSettings
	mainHandbook
	mainQuit
	mainCount
)

func (m model) onMainKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.mainMenuIdx > 0 {
			m.mainMenuIdx--
		}
		return m, nil
	case "down", "j":
		if m.mainMenuIdx < mainCount-1 {
			m.mainMenuIdx++
		}
		return m, nil
	case "enter", " ":
		return m.activateMain(m.mainMenuIdx)
	case "q", "esc":
		return m, tea.Quit
	}
	return m, nil
}

// activateMain runs one main-menu item, whether reached by the cursor or by
// typing its shortcut.
func (m model) activateMain(item int) (tea.Model, tea.Cmd) {
	switch item {
	case mainSearch:
		m.screen = screenFlight
		m.flightInput.SetValue("")
		m.errMsg = ""
		m.flightInput.Focus()
		return m, textinput.Blink

	case mainRecent:
		m.screen = screenHistory
		m.errMsg = ""
		return m, nil

	case mainDiscordSetup:
		m.screen = screenDiscordSetup
		m.discordInput.SetValue(m.cfg.DiscordWebhookURL)
		// Show the start of the URL, not the end: that is the part worth
		// glancing at to confirm which webhook is saved.
		m.discordInput.CursorStart()
		m.discordSetupMsg = ""
		m.discordInput.Focus()
		return m, textinput.Blink

	case mainSettings:
		m.screen = screenSettings
		for i, name := range themeOrder {
			if name == currentTheme {
				m.settingsIdx = i
			}
		}
		return m, nil

	case mainHandbook:
		m.screen = screenHandbook
		m.handbook.GotoTop()
		return m, nil

	case mainQuit:
		return m, tea.Quit
	}
	return m, nil
}

func (m model) viewMain() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("flighttrack") + "\n")
	b.WriteString(dimStyle.Render("Live flight tracking via the OpenSky Network") + "\n\n")

	items := make([]string, mainCount)
	items[mainSearch] = "Search"
	items[mainRecent] = "Recently tracked flights"
	if n := len(m.hist.Entries); n > 0 {
		items[mainRecent] += dimStyle.Render(fmt.Sprintf("  (%d saved)", n))
	}
	items[mainDiscordSetup] = "Setup Discord webhook"
	if m.discord != nil || m.cfg.DiscordWebhookURL != "" {
		items[mainDiscordSetup] += "  " + goodStyle.Render("configured")
	} else {
		items[mainDiscordSetup] += "  " + dimStyle.Render("not set")
	}
	items[mainSettings] = "Settings"
	items[mainHandbook] = "Handbook"
	items[mainQuit] = "Quit"

	for i, item := range items {
		if i == m.mainMenuIdx {
			b.WriteString(selStyle.Render("> "+item) + "\n")
		} else {
			b.WriteString("  " + item + "\n")
		}
	}

	if m.histWarning != "" {
		b.WriteString("\n" + warnStyle.Render(textfmt.Wrap("history: "+m.histWarning, 60)) + "\n")
	}
	if m.hasUpdate {
		b.WriteString("\n" + goodStyle.Render("update "+m.update.Version+" available") +
			dimStyle.Render("  (run install.ps1 -Update)") + "\n")
	}

	b.WriteString("\n" + dimStyle.Render("up/down to choose    enter to select    q to quit") + "\n")
	return b.String()
}
