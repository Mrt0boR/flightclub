package main

// Small formatting helpers shared by the dashboard, the watcher and the
// subcommands. Nothing here touches model state.

import (
	"fmt"
	"strings"
	"time"

	"flighttrack/internal/opensky"
)

func onOff(enabled bool) string {
	if enabled {
		return goodStyle.Render("on")
	}
	return dimStyle.Render("off")
}

func phaseWord(obs *opensky.Observation) string {
	if obs.OnGround {
		return "on the ground"
	}
	return "airborne"
}

// describe renders an observation as one line of prose, for notification
// bodies and log lines.
func describe(obs *opensky.Observation) string {
	var b strings.Builder
	if obs.HasPos {
		fmt.Fprintf(&b, "Position %.4f, %.4f. ", obs.Lat, obs.Lon)
	}
	if obs.OnGround {
		fmt.Fprintf(&b, "On the ground at %.0f kts.", obs.SpeedKts())
	} else {
		fmt.Fprintf(&b, "Altitude %.0f ft, %.0f kts.", obs.AltitudeFt(), obs.SpeedKts())
	}
	return b.String()
}

// shortAge renders a duration compactly for lists, e.g. "3m", "2h14m".
func shortAge(age time.Duration) string {
	switch {
	case age < time.Minute:
		return fmt.Sprintf("%ds", int(age.Seconds()))
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(age.Hours()), int(age.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(age.Hours()/24))
	}
}

// trunc shortens text to limit characters, marking the cut with a full stop.
func trunc(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	if limit <= 1 {
		return text[:limit]
	}
	return text[:limit-1] + "."
}

// wrap breaks text on spaces at width, for the fixed-width panels.
func wrap(text string, width int) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	var lines []string
	line := words[0]
	for _, word := range words[1:] {
		if len(line)+1+len(word) > width {
			lines = append(lines, line)
			line = word
			continue
		}
		line += " " + word
	}
	return strings.Join(append(lines, line), "\n")
}
