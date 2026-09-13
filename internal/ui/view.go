package ui

// View picks which screen to render. Each screen's own rendering lives beside
// its key handling in the matching screen_*.go file.

func (m model) View() string {
	switch m.screen {
	case screenMain:
		return m.viewMain()
	case screenHistory:
		return m.viewHistory()
	case screenFlight:
		return m.viewFlight()
	case screenDest:
		return m.viewDest()
	case screenDiscordSetup:
		return m.viewDiscordSetup()
	case screenSettings:
		return m.viewSettings()
	case screenHandbook:
		return m.viewHandbook()
	default:
		return m.viewDash()
	}
}
