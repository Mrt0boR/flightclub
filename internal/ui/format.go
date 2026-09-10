package ui

// Wording helpers that depend on this package's styles.

import "flighttrack/internal/opensky"

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
