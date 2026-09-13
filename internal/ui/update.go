package ui

// The central message loop. Update sorts incoming messages, hands keystrokes
// to whichever screen is showing, and owns the two things that span screens:
// a new API snapshot arriving, and the arrival alert firing.

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"flighttrack/internal/eta"
	"flighttrack/internal/notify"
	"flighttrack/internal/opensky"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		m.now = time.Time(msg)
		var cmds []tea.Cmd
		cmds = append(cmds, tick())
		if m.screen == screenDash && m.autoRefresh && !m.loading && m.now.After(m.nextRefresh) {
			m.loading = true
			m.nextRefresh = m.now.Add(m.refreshIn)
			cmds = append(cmds, m.fetch())
		}
		if c := m.checkArrival(); c != nil {
			cmds = append(cmds, c)
		}
		return m, tea.Batch(cmds...)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case notifyDoneMsg:
		if msg.err != nil {
			m.logf(badStyle, "notify failed: %v", msg.err)
		}
		return m, nil

	case updateMsg:
		if msg.available {
			m.update, m.hasUpdate = msg.release, true
		}
		return m, nil

	case snapshotMsg:
		return m.onSnapshot(msg)

	case tea.KeyMsg:
		return m.onKey(msg)
	}

	return m.forwardToInput(msg)
}

// forwardToInput hands a message the loop above did not claim to whichever
// text input is on screen. In practice this is the cursor blink, which
// textinput drives with its own timer, so dropping these would leave the
// cursor frozen.
//
// Note the assignment back onto m.flightInput: Bubble Tea components are
// values rather than pointers, so Update returns a modified copy instead of
// changing the original. Ignoring the return value silently loses the update.
//
// On the history list and the dashboard there is no input to feed, so cmd
// stays nil, which is Bubble Tea's way of saying there is nothing to do.
func (m model) forwardToInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.screen {
	case screenFlight:
		m.flightInput, cmd = m.flightInput.Update(msg)
	case screenDest:
		m.destInput, cmd = m.destInput.Update(msg)
	case screenDiscordSetup:
		m.discordInput, cmd = m.discordInput.Update(msg)
	}
	return m, cmd
}

// onKey routes a keystroke to the screen that is showing.
func (m model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// ctrl+c always quits, whatever screen is up.
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}

	switch m.screen {
	case screenMain:
		return m.onMainKey(msg)
	case screenHistory:
		return m.onHistoryKey(msg)
	case screenFlight:
		return m.onFlightKey(msg)
	case screenDest:
		return m.onPickerKey(msg)
	case screenDash:
		return m.onDashKey(msg)
	case screenDiscordSetup:
		return m.onDiscordSetupKey(msg)
	case screenSettings:
		return m.onSettingsKey(msg)
	case screenHandbook:
		return m.onHandbookKey(msg)
	}
	return m, nil
}

// onSnapshot folds a fresh API result into the model, raising takeoff and
// landing events by comparing against the previous snapshot.
func (m model) onSnapshot(msg snapshotMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.errMsg = msg.err.Error()
		m.logf(badStyle, "refresh failed: %v", msg.err)
		return m, nil
	}
	m.errMsg = ""
	m.snapTaken = msg.snap.Taken
	m.snapFetched = msg.snap.Fetched
	m.fromCache = false // this is live data now
	m.nextRefresh = time.Now().Add(m.refreshIn)

	obs, found := msg.snap.Lookup(m.flight)
	if !found {
		m.obs = nil
		if m.screen == screenFlight {
			m.errMsg = fmt.Sprintf("%s is not currently visible. It may not be airborne yet, or it is outside ADS-B coverage.", m.flight)
			return m, nil
		}
		m.logf(warnStyle, "%s not in this snapshot", m.flight)
		return m, nil
	}

	cmds := m.phaseChangeCmds(obs)

	onGround := obs.OnGround
	m.prevOnGround = &onGround
	m.obs = obs

	m.recompute()
	if m.screen == screenFlight {
		m.screen = screenDest
		// Ask for the origin first, but only during initial setup and only if
		// it was not already supplied on the command line.
		m.pickOrigin = m.origin.IATA == "" && m.dest.IATA == ""
		m.destInput.Focus()
		m.logf(dimStyle, "found %s, %s", obs.Callsign, phaseWord(obs))
	}
	return m, tea.Batch(cmds...)
}

// phaseChangeCmds compares the new observation against the last one and
// returns the notifications a takeoff or landing should raise. It mutates the
// model's log and arrival flag, so it takes a pointer receiver.
func (m *model) phaseChangeCmds(obs *opensky.Observation) []tea.Cmd {
	if m.prevOnGround == nil || *m.prevOnGround == obs.OnGround {
		return nil
	}

	var cmds []tea.Cmd
	if *m.prevOnGround && !obs.OnGround {
		m.logf(goodStyle, "%s has taken off", m.flight)
		cmds = append(cmds, m.send(notify.Event{
			Kind: "takeoff", Flight: m.flight.String(), Callsign: obs.Callsign,
			Icao24: obs.Icao24, Time: time.Now(),
			Title: fmt.Sprintf("%s has taken off", m.flight),
			Body:  obs.Describe(),
		}))
	} else {
		m.logf(goodStyle, "%s has landed", m.flight)
		cmds = append(cmds, m.send(notify.Event{
			Kind: "landing", Flight: m.flight.String(), Callsign: obs.Callsign,
			Icao24: obs.Icao24, Time: time.Now(),
			Title: fmt.Sprintf("%s has landed", m.flight),
			Body:  obs.Describe(),
		}))
		m.arrivalAlerted = true // no point warning about an arrival now

		// The flight is down; nothing more will change. Left running,
		// auto-refresh would keep spending API credits every few minutes and
		// filling the log with "not in this snapshot". Stop it — unless the
		// landing was reported nowhere near the destination, which means the
		// data is suspect and the next poll should get a chance to correct it.
		if m.autoRefresh && m.landingLooksReal(obs) {
			m.autoRefresh = false
			m.logf(dimStyle, "auto-refresh off, flight down (a to resume)")
		}
	}
	return cmds
}

// landingLooksReal sanity-checks an on-ground report against the destination.
// With no destination set there is nothing to check against, so it trusts the
// feed.
func (m *model) landingLooksReal(obs *opensky.Observation) bool {
	if m.dest.IATA == "" || !obs.HasPos {
		return true
	}
	return eta.DistanceNM(obs.Lat, obs.Lon, m.dest.Lat, m.dest.Lon) < 75
}

// checkArrival fires the approaching-arrival alert once per flight.
func (m *model) checkArrival() tea.Cmd {
	if m.arrivalAlerted || !m.est.Valid || m.obs == nil || m.obs.OnGround {
		return nil
	}
	left := m.est.Countdown(m.now)
	if left <= 0 || left > arrivalAlertAt {
		return nil
	}
	m.arrivalAlerted = true
	m.logf(warnStyle, "%s arriving in about %s", m.flight, eta.FormatDuration(left))
	return m.send(notify.Event{
		Kind: "arriving", Flight: m.flight.String(), Time: time.Now(),
		Title: fmt.Sprintf("%s is arriving soon", m.flight),
		Body: fmt.Sprintf("Estimated arrival at %s in about %s (%s GMT).",
			m.dest.IATA, eta.FormatDuration(left), m.est.ArrivalUTC.Format("15:04")),
	})
}
