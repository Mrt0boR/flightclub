package main

// Small formatting helpers shared by the dashboard, the watcher and the
// subcommands. Nothing here touches model state.

import (
	"fmt"
	"strings"
	"time"

	"flighttrack/internal/opensky"
)

func onOff(b bool) string {
	if b {
		return goodStyle.Render("on")
	}
	return dimStyle.Render("off")
}

func phaseWord(o *opensky.Observation) string {
	if o.OnGround {
		return "on the ground"
	}
	return "airborne"
}

// describe renders an observation as one line of prose, for notification
// bodies and log lines.
func describe(o *opensky.Observation) string {
	var b strings.Builder
	if o.HasPos {
		fmt.Fprintf(&b, "Position %.4f, %.4f. ", o.Lat, o.Lon)
	}
	if o.OnGround {
		fmt.Fprintf(&b, "On the ground at %.0f kts.", o.SpeedKts())
	} else {
		fmt.Fprintf(&b, "Altitude %.0f ft, %.0f kts.", o.AltitudeFt(), o.SpeedKts())
	}
	return b.String()
}

// shortAge renders a duration compactly for lists, e.g. "3m", "2h14m".
func shortAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "."
}

// wrap breaks text on spaces at width, for the fixed-width panels.
func wrap(s string, width int) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var lines []string
	cur := words[0]
	for _, w := range words[1:] {
		if len(cur)+1+len(w) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	return strings.Join(append(lines, cur), "\n")
}
