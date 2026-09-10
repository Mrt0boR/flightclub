package main

// Colours and text styles for the whole interface. Everything visual is
// defined here so a change lands in one place rather than scattered through
// the render code.

import (
	"github.com/charmbracelet/lipgloss"

	"flighttrack/internal/history"
)

var (
	colAccent = lipgloss.AdaptiveColor{Light: "#005f87", Dark: "#5fd7ff"}
	colDim    = lipgloss.AdaptiveColor{Light: "#6c6c6c", Dark: "#8a8a8a"}
	colGood   = lipgloss.AdaptiveColor{Light: "#005f00", Dark: "#5fd75f"}
	colWarn   = lipgloss.AdaptiveColor{Light: "#875f00", Dark: "#ffd75f"}
	colBad    = lipgloss.AdaptiveColor{Light: "#870000", Dark: "#ff5f5f"}

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	dimStyle   = lipgloss.NewStyle().Foreground(colDim)
	goodStyle  = lipgloss.NewStyle().Foreground(colGood)
	warnStyle  = lipgloss.NewStyle().Foreground(colWarn)
	badStyle   = lipgloss.NewStyle().Foreground(colBad)
	bigStyle   = lipgloss.NewStyle().Bold(true).Foreground(colAccent)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colDim).
			Padding(0, 1)

	labelStyle = lipgloss.NewStyle().Foreground(colDim).Width(14)

	selStyle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
)

// freshnessStyle maps a cache age onto the green/amber/red scale. The red is
// the lighter shade so it stays legible on a dark terminal.
func freshnessStyle(f history.Freshness) lipgloss.Style {
	switch f {
	case history.Fresh:
		return goodStyle
	case history.Aging:
		return warnStyle
	default:
		return badStyle
	}
}
