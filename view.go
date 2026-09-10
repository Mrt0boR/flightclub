package main

// View picks which screen to render. Each screen's own rendering lives beside
// its key handling in the matching screen_*.go file.

func (m model) View() string {
	switch m.screen {
	case screenHistory:
		return m.viewHistory()
	case screenFlight:
		return m.viewFlight()
	case screenDest:
		return m.viewDest()
	default:
		return m.viewDash()
	}
}
