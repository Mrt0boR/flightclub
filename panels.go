package main

// The dashboard's three panels. Each takes the column width it has to fit
// into and returns plain text; the borders are added by viewDash.

import (
	"fmt"
	"math"
	"strings"
	"time"

	"flighttrack/internal/eta"
	"flighttrack/internal/history"
)

// flightPanel is the left-hand panel: where the aircraft is, and when it is
// expected to arrive.
func (m model) flightPanel(width int) string {
	var b strings.Builder
	// The heading is the number as typed; the callsign is only worth showing
	// when the wire differs from it.
	heading := m.flightTyped
	if heading == "" {
		heading = m.flight.String()
	}
	b.WriteString(titleStyle.Render(heading))
	if m.obs != nil && m.obs.Callsign != "" && m.obs.Callsign != heading {
		b.WriteString(dimStyle.Render("   callsign " + m.obs.Callsign))
	}
	b.WriteString("\n\n")

	if m.obs == nil {
		b.WriteString(warnStyle.Render("Not currently visible.") + "\n\n")
		b.WriteString(dimStyle.Render("The flight may not be airborne, or it may be\noutside ADS-B receiver coverage. Press r to\ntry again.") + "\n")
		return b.String()
	}

	b.WriteString(m.aircraftLines())
	b.WriteString("\n" + dimStyle.Render(strings.Repeat("-", width)) + "\n\n")

	if m.dest.IATA == "" {
		b.WriteString(dimStyle.Render("No destination set. Press d.") + "\n")
		return b.String()
	}
	b.WriteString(labelStyle.Render("Destination") + m.dest.Label() + "\n")

	if !m.est.Valid {
		b.WriteString("\n" + warnStyle.Render("No arrival estimate") + "\n")
		b.WriteString(dimStyle.Render(wrap(m.est.Reason, width)) + "\n")
		if m.est.DistanceNM > 0 {
			b.WriteString("\n" + labelStyle.Render("Distance") + fmt.Sprintf("%.0f nm", m.est.DistanceNM) + "\n")
		}
		return b.String()
	}

	b.WriteString(m.arrivalLines())

	if bar := m.progressSection(width); bar != "" {
		b.WriteString(bar + "\n\n")
	}

	qualityStyle := dimStyle
	if m.est.Quality == eta.Rough {
		qualityStyle = warnStyle
	}
	b.WriteString(qualityStyle.Render(fmt.Sprintf("[%s] %s", m.est.Quality, wrap(m.est.Reason, width))) + "\n")
	return b.String()
}

// aircraftLines renders where the aircraft is and what it is doing.
func (m model) aircraftLines() string {
	var b strings.Builder

	status := goodStyle.Render("AIRBORNE")
	if m.obs.OnGround {
		status = warnStyle.Render("ON GROUND")
	}
	b.WriteString(labelStyle.Render("Status") + status + "\n")

	if m.obs.HasPos {
		b.WriteString(labelStyle.Render("Position") + fmt.Sprintf("%.4f, %.4f", m.obs.Lat, m.obs.Lon) + "\n")
	}
	if !m.obs.OnGround {
		b.WriteString(labelStyle.Render("Altitude") + fmt.Sprintf("%.0f ft", m.obs.AltitudeFt()) + "\n")
	}
	b.WriteString(labelStyle.Render("Ground speed") + fmt.Sprintf("%.0f kts", m.obs.SpeedKts()) + "\n")

	if climbRate := m.obs.ClimbFPM(); climbRate > 100 {
		b.WriteString(labelStyle.Render("Vertical") + fmt.Sprintf("climbing %.0f ft/min", climbRate) + "\n")
	} else if climbRate < -100 {
		b.WriteString(labelStyle.Render("Vertical") + fmt.Sprintf("descending %.0f ft/min", -climbRate) + "\n")
	}
	b.WriteString(labelStyle.Render("Track") + fmt.Sprintf("%.0f deg %s", m.obs.Track, eta.Compass(m.obs.Track)) + "\n")
	return b.String()
}

// arrivalLines renders the distance to run, the arrival time and the
// countdown. Only called once the estimate is known to be valid.
func (m model) arrivalLines() string {
	var b strings.Builder

	b.WriteString(labelStyle.Render("Distance") + fmt.Sprintf("%.0f nm, bearing %.0f %s",
		m.est.DistanceNM, m.est.BearingDeg, eta.Compass(m.est.BearingDeg)) + "\n\n")

	arrival := m.est.ArrivalUTC
	b.WriteString(labelStyle.Render("ETA (GMT)") + bigStyle.Render(arrival.Format("15:04:05")) +
		dimStyle.Render("  "+arrival.Format("Mon 2 Jan")) + "\n")
	b.WriteString(labelStyle.Render("ETA (local)") + arrival.Local().Format("15:04:05 MST") + "\n\n")

	remaining := m.est.Countdown(m.now)
	countdown := bigStyle.Render(eta.FormatDuration(remaining))
	if remaining < 0 {
		countdown = badStyle.Render(eta.FormatDuration(remaining))
	} else if remaining < arrivalAlertAt {
		countdown = warnStyle.Render(eta.FormatDuration(remaining))
	}
	b.WriteString(labelStyle.Render("Countdown") + countdown + "\n\n")
	return b.String()
}

// progressSection draws the journey bar. With an origin it is true route
// progress; without one it can only show how far the aircraft has come since
// tracking started, which is labelled as such rather than passed off as more.
func (m model) progressSection(width int) string {
	fraction, from, to, caption := 0.0, "", "", ""

	switch {
	case m.est.HasProgress:
		fraction = m.est.Progress
		from, to = m.est.Origin.IATA, m.est.Destination.IATA
		caption = fmt.Sprintf("%.0f nm flown of %.0f nm", m.est.TotalNM-m.est.DistanceNM, m.est.TotalNM)

	case m.trackStartNM > 0 && m.est.DistanceNM > 0:
		fraction = (m.trackStartNM - m.est.DistanceNM) / m.trackStartNM
		if fraction < 0 {
			fraction = 0 // the aircraft has moved away from the destination
		}
		from, to = "start", m.est.Destination.IATA
		caption = "since tracking began - set an origin for true route progress"

	default:
		return ""
	}

	// The label column, the two endpoint markers and the percentage all take
	// space away from the bar itself.
	barWidth := width - 14 - len(from) - len(to) - 8
	if barWidth < 8 {
		barWidth = 8
	}
	filled := int(math.Round(fraction * float64(barWidth)))
	if filled > barWidth {
		filled = barWidth
	}

	var b strings.Builder
	b.WriteString(labelStyle.Render("Progress"))
	b.WriteString(dimStyle.Render(from + " "))
	b.WriteString(goodStyle.Render(strings.Repeat("=", filled)))
	b.WriteString(dimStyle.Render(strings.Repeat(".", barWidth-filled)))
	b.WriteString(dimStyle.Render(" "+to) + fmt.Sprintf("  %3.0f%%", fraction*100))
	b.WriteString("\n" + labelStyle.Render("") + dimStyle.Render(wrap(caption, width-14)))
	return b.String()
}

// menuPanel is the top of the right-hand column.
func (m model) menuPanel() string {
	items := make([]string, menuCount)
	items[menuRefresh] = "Refresh now"
	items[menuNotify] = "Notifications  " + onOff(m.notifyOn)
	items[menuAuto] = "Auto-refresh   " + onOff(m.autoRefresh)
	items[menuOrigin] = "Set origin"
	if m.origin.IATA != "" {
		items[menuOrigin] = "Change origin  " + dimStyle.Render(m.origin.IATA)
	}
	items[menuDest] = "Change destination"
	items[menuFlight] = "Change flight"
	items[menuQuit] = "Quit"

	var b strings.Builder
	b.WriteString(titleStyle.Render("MENU") + "\n\n")
	for i, item := range items {
		if i == m.menuIdx {
			b.WriteString(selStyle.Render("> "+item) + "\n")
		} else {
			b.WriteString("  " + item + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// infoPanel is the bottom of the right-hand column: how old the data is, what
// it has cost, and recent events.
func (m model) infoPanel(width int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("INFO") + "\n\n")

	// Everything here is measured against m.now, the same clock the countdown
	// uses, so the two can never disagree on screen.
	if m.loading {
		b.WriteString(m.spin.View() + dimStyle.Render(" fetching...") + "\n")
	} else if m.snapFetched.IsZero() {
		b.WriteString(dimStyle.Render("no data yet") + "\n")
	} else {
		dataAge := m.now.Sub(m.snapFetched).Round(time.Second)
		if dataAge < 0 {
			dataAge = 0
		}
		ageStyle := freshnessStyle(history.Rate(dataAge))
		b.WriteString(dimStyle.Render("data age   ") + ageStyle.Render(shortAge(dataAge)))
		if m.fromCache {
			b.WriteString(warnStyle.Render(" cached"))
		}
		b.WriteString("\n")
	}

	if m.autoRefresh {
		untilRefresh := m.nextRefresh.Sub(m.now).Round(time.Second)
		if untilRefresh < 0 {
			untilRefresh = 0
		}
		b.WriteString(dimStyle.Render("next auto  ") + untilRefresh.String() + "\n")
	} else {
		b.WriteString(dimStyle.Render("next auto  off") + "\n")
	}

	b.WriteString(dimStyle.Render("api calls  ") +
		fmt.Sprintf("%d credits / %d", m.client.CreditsUsed(), m.client.DailyCredits()) + "\n")
	apiMode := "anonymous"
	if m.client.Authenticated() {
		apiMode = "authenticated"
	}
	b.WriteString(dimStyle.Render("api mode   ") + apiMode + "\n")
	b.WriteString(dimStyle.Render("clock      ") + m.now.UTC().Format("15:04:05") + " GMT" + "\n")

	if m.errMsg != "" {
		b.WriteString("\n" + badStyle.Render(wrap(m.errMsg, width)) + "\n")
	}

	if len(m.logs) > 0 {
		b.WriteString("\n" + titleStyle.Render("EVENTS") + "\n\n")
		for _, entry := range m.logs {
			// The timestamp takes 9 columns of the line.
			b.WriteString(dimStyle.Render(entry.at.Format("15:04:05")+" ") +
				entry.tone.Render(trunc(entry.text, width-9)) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
