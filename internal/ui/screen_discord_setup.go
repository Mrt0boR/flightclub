package ui

// The Discord webhook setup screen: paste a channel webhook URL, it gets
// validated and saved, and ctrl+t sends a real test notification through it
// without needing -dev mode or a real flight.

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"flighttrack/internal/notify"
)

func (m model) onDiscordSetupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.screen = screenMain
		m.discordInput.Blur()
		return m, nil

	case tea.KeyEnter:
		return m.saveDiscordSetup()
	}

	if msg.String() == "ctrl+t" {
		return m.sendDiscordTest()
	}

	var cmd tea.Cmd
	m.discordInput, cmd = m.discordInput.Update(msg)
	return m, cmd
}

// saveDiscordSetup validates whatever is in the box. An empty box clears the
// saved webhook rather than being treated as an error — that is how you
// remove one.
func (m model) saveDiscordSetup() (tea.Model, tea.Cmd) {
	raw := strings.TrimSpace(m.discordInput.Value())

	if raw == "" {
		m.discord = nil
		m.cfg.DiscordWebhookURL = ""
		m.discordSetupOK = true
		m.discordSetupMsg = "Cleared. Discord notifications are off."
		if err := m.saveConfig(); err != nil {
			m.discordSetupOK = false
			m.discordSetupMsg = "Could not save: " + err.Error()
		}
		return m, nil
	}

	d, err := notify.NewDiscord(raw)
	if err != nil {
		m.discordSetupOK = false
		m.discordSetupMsg = err.Error()
		return m, nil
	}
	m.discord = d
	m.cfg.DiscordWebhookURL = raw
	if err := m.saveConfig(); err != nil {
		m.discordSetupOK = false
		m.discordSetupMsg = "Valid, but could not save: " + err.Error()
		return m, nil
	}
	m.discordSetupOK = true
	m.discordSetupMsg = "Saved. Press ctrl+t to send a test notification."
	return m, nil
}

// sendDiscordTest fires one real notification through whatever is currently
// configured, so setup can be confirmed without -dev mode or a real flight.
func (m model) sendDiscordTest() (tea.Model, tea.Cmd) {
	if m.discord == nil {
		m.discordSetupOK = false
		m.discordSetupMsg = "Nothing to test yet — enter a webhook URL and press enter first."
		return m, nil
	}
	set := notify.NewSet(m.discord)
	event := notify.Event{
		Kind: "tracking", Flight: "TEST1", Time: time.Now(),
		Title: "flighttrack test notification",
		Body:  "If you can see this in Discord, the webhook is working.",
	}
	return m, func() tea.Msg { return deliver(set, event) }
}

func (m model) viewDiscordSetup() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Setup Discord webhook") + "\n")
	b.WriteString(dimStyle.Render("A webhook posts into one Discord channel; it cannot DM you directly.") + "\n")
	b.WriteString(dimStyle.Render("Server Settings -> Integrations -> Webhooks -> New Webhook -> copy the URL.") + "\n\n")

	b.WriteString("Webhook URL (blank clears it):\n\n")
	b.WriteString("  " + m.discordInput.View() + "\n\n")

	if m.discordSetupMsg != "" {
		style := badStyle
		if m.discordSetupOK {
			style = goodStyle
		}
		b.WriteString("  " + style.Render(m.discordSetupMsg) + "\n\n")
	}

	b.WriteString(dimStyle.Render("enter to save    ctrl+t to send a test    esc to go back") + "\n")
	return b.String()
}
