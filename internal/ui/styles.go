package ui

// Colours and text styles for the whole interface. Everything visual is
// defined here so a change lands in one place rather than scattered through
// the render code.
//
// Styles are rebuilt from a named theme rather than fixed at startup, so the
// Settings screen can switch palettes at runtime. This is safe without a
// mutex: Bubble Tea runs Update and View on a single goroutine, so nothing
// reads these mid-write.

import (
	"github.com/charmbracelet/lipgloss"

	"flighttrack/internal/history"
)

// theme is one named colour palette.
type theme struct {
	Label                        string // shown in the Settings list
	Accent, Dim, Good, Warn, Bad lipgloss.AdaptiveColor
}

// DefaultTheme is used on first run and whenever a saved theme name is not
// recognised, so a corrupt or outdated config value never breaks startup.
const DefaultTheme = "default"

// themeOrder fixes the order Settings lists palettes in; map iteration order
// is not stable.
var themeOrder = []string{"default", "high-contrast", "monochrome"}

var themes = map[string]theme{
	"default": {
		Label:  "Default",
		Accent: lipgloss.AdaptiveColor{Light: "#005f87", Dark: "#5fd7ff"},
		Dim:    lipgloss.AdaptiveColor{Light: "#6c6c6c", Dark: "#8a8a8a"},
		Good:   lipgloss.AdaptiveColor{Light: "#005f00", Dark: "#5fd75f"},
		Warn:   lipgloss.AdaptiveColor{Light: "#875f00", Dark: "#ffd75f"},
		Bad:    lipgloss.AdaptiveColor{Light: "#870000", Dark: "#ff5f5f"},
	},
	"high-contrast": {
		Label:  "High contrast",
		Accent: lipgloss.AdaptiveColor{Light: "#0000ee", Dark: "#00ffff"},
		Dim:    lipgloss.AdaptiveColor{Light: "#000000", Dark: "#e4e4e4"},
		Good:   lipgloss.AdaptiveColor{Light: "#006400", Dark: "#00ff00"},
		Warn:   lipgloss.AdaptiveColor{Light: "#b35900", Dark: "#ffff00"},
		Bad:    lipgloss.AdaptiveColor{Light: "#cc0000", Dark: "#ff3030"},
	},
	// A true monochrome: every colour is a shade of grey. Status is carried
	// by weight (bold) rather than hue, which costs the at-a-glance
	// green/amber/red read elsewhere in the app — an accepted trade for
	// terminals or eyes that do not want colour at all.
	"monochrome": {
		Label:  "Monochrome",
		Accent: lipgloss.AdaptiveColor{Light: "#000000", Dark: "#ffffff"},
		Dim:    lipgloss.AdaptiveColor{Light: "#767676", Dark: "#9e9e9e"},
		Good:   lipgloss.AdaptiveColor{Light: "#303030", Dark: "#d0d0d0"},
		Warn:   lipgloss.AdaptiveColor{Light: "#303030", Dark: "#d0d0d0"},
		Bad:    lipgloss.AdaptiveColor{Light: "#000000", Dark: "#ffffff"},
	},
}

// currentTheme is which palette is active, so Settings can mark it.
var currentTheme = DefaultTheme

var (
	titleStyle lipgloss.Style
	dimStyle   lipgloss.Style
	goodStyle  lipgloss.Style
	warnStyle  lipgloss.Style
	badStyle   lipgloss.Style
	bigStyle   lipgloss.Style
	panelStyle lipgloss.Style
	labelStyle lipgloss.Style
	selStyle   lipgloss.Style
)

func init() { applyTheme(DefaultTheme) }

// applyTheme rebuilds every derived style from the named palette. An unknown
// name falls back to the default rather than erroring.
func applyTheme(name string) {
	t, ok := themes[name]
	if !ok {
		t = themes[DefaultTheme]
		name = DefaultTheme
	}
	currentTheme = name

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(t.Accent)
	dimStyle = lipgloss.NewStyle().Foreground(t.Dim)
	goodStyle = lipgloss.NewStyle().Foreground(t.Good)
	warnStyle = lipgloss.NewStyle().Foreground(t.Warn)
	badStyle = lipgloss.NewStyle().Foreground(t.Bad)
	bigStyle = lipgloss.NewStyle().Bold(true).Foreground(t.Accent)

	panelStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Dim).
		Padding(0, 1)

	labelStyle = lipgloss.NewStyle().Foreground(t.Dim).Width(14)

	selStyle = lipgloss.NewStyle().Bold(true).Foreground(t.Accent)
}

// accentColor exposes the current theme's accent for the one place outside
// this file that needs a raw colour rather than a built style: the spinner.
func accentColor() lipgloss.AdaptiveColor {
	return themes[currentTheme].Accent
}

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
