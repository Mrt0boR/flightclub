package ui

// The in-app handbook: a scrollable quick reference, so "how do I use this"
// has an answer inside the app itself and not just in the README.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"flighttrack/internal/textfmt"
)

const handbookWidth = 70

// handbookSections is the handbook's content, one paragraph per entry so each
// can be wrapped independently. Blank entries render as a blank line.
var handbookSections = []string{
	"FLIGHTTRACK HANDBOOK",
	"",
	"Give it the flight number from your ticket and it finds the aircraft, shows where it is, estimates an arrival time in GMT, and counts down to it. OpenSky publishes a position and a callsign, nothing else — no route, no schedule — which is why you are asked for the destination yourself.",
	"",
	"SEARCH",
	"From the main menu, Search asks for the flight number, then the origin (optional, enables the route progress bar; press enter blank to skip), then the destination.",
	"",
	"ONCE A FLIGHT IS OPEN",
	"r  refresh now (uses 4 API credits)",
	"n  toggle desktop notifications",
	"a  toggle auto-refresh",
	"o  set or change the origin",
	"d  change the destination",
	"f  track a different flight",
	"q  quit",
	"",
	"The countdown ticks every second off your system clock and touches no network. Only r and the auto-refresh spend API credits.",
	"",
	"RECENTLY TRACKED FLIGHTS",
	"Your five most recent searches, colour-coded by how fresh the saved position is: green under 10 minutes, amber under an hour, red beyond that. Picking one opens instantly on the cached data if it is still usable, otherwise it fetches fresh.",
	"",
	"DISCORD NOTIFICATIONS",
	"Setup Discord webhook takes a channel webhook URL (Discord Server Settings -> Integrations -> Webhooks -> New Webhook), validates it, and remembers it for next time. ctrl+t on that screen sends a real test notification. A webhook posts into a channel — it cannot DM you directly, so a small server or a channel only you can see is the usual way to make it feel private.",
	"",
	"SETTINGS",
	"Pick a colour theme: Default, High Contrast, or Monochrome. Applies immediately and is remembered.",
	"",
	"COMMAND LINE",
	"flighttrack -flight QF2 -to LHR      instant dashboard, no menu",
	"flighttrack watch -flights QF2,CX251 background, no interface, reports takeoffs and landings",
	"flighttrack history                  print saved searches",
	"flighttrack version                  build, and whether it is current",
	"flighttrack -dev                     a fake flight with ctrl+t to simulate its landing, for testing notifications without a real flight",
	"flighttrack help                     every flag",
	"",
	"WHAT IT CANNOT DO",
	"The ETA is a straight-line estimate — great-circle distance divided by current ground speed. It ignores winds, routing, holding and the approach, so it reads optimistic, especially in the last hour. A flight has to be airborne and in ADS-B coverage to be found at all; one that has not pushed back will not appear.",
	"",
	"WHERE YOUR DATA LIVES",
	"Search history and cached positions: %APPDATA%\\flighttrack\\history.json",
	"Discord webhook and theme:           %APPDATA%\\flighttrack\\config.json",
	"Watch-mode flight states:            flighttrack-watch-state.json, in the working directory",
}

func handbookContent() string {
	var b strings.Builder
	for _, para := range handbookSections {
		switch {
		case para == "":
			b.WriteString("\n")
		case len(para) <= handbookWidth:
			// Short lines are left exactly as written: the key-binding list
			// depends on its own spacing for the two columns to line up,
			// which Wrap would collapse.
			b.WriteString(para + "\n")
		default:
			b.WriteString(textfmt.Wrap(para, handbookWidth) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m model) onHandbookKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.screen = screenMain
		return m, nil
	}
	var cmd tea.Cmd
	m.handbook, cmd = m.handbook.Update(msg)
	return m, cmd
}

func (m model) viewHandbook() string {
	width := m.width - 6
	if width > handbookWidth+4 {
		width = handbookWidth + 4
	}
	if width < 20 {
		width = 20
	}
	height := m.height - 4
	if height < 5 {
		height = 5
	}

	m.handbook.Width = width
	m.handbook.Height = height
	m.handbook.SetContent(handbookContent())

	pct := int(m.handbook.ScrollPercent()*100 + 0.5)
	hint := fmt.Sprintf("up/down/pgup/pgdn to scroll    esc to go back    %d%%", pct)
	return m.handbook.View() + "\n" + dimStyle.Render(hint) + "\n"
}
