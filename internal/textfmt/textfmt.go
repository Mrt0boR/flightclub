// Package textfmt holds small text helpers shared by the interface and the
// command line: compact durations, truncation, and word wrapping for
// fixed-width panels.
package textfmt

import (
	"fmt"
	"strings"
	"time"
)

// ShortAge renders a duration compactly for lists, e.g. "3m", "2h14m".
func ShortAge(age time.Duration) string {
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

// Trunc shortens text to limit characters, marking the cut with a full stop.
func Trunc(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	if limit <= 1 {
		return text[:limit]
	}
	return text[:limit-1] + "."
}

// Wrap breaks text on spaces at width.
func Wrap(text string, width int) string {
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
