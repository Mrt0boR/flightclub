// Package eta estimates an arrival time from a live position and a
// destination.
//
// This is deliberately simple: great-circle distance to the destination
// divided by current ground speed. It is not an airline ETA. It does not know
// the filed route, the winds, ATC vectoring, holding, or the approach, and it
// assumes the aircraft flies straight to the field at its present speed. In
// practice it reads optimistic, most noticeably in the last hour of a flight
// when the aircraft slows and gets vectored. Treat it as a good-enough
// countdown, not a promise.
package eta

import (
	"fmt"
	"math"
	"time"

	"flighttrack/internal/airports"
	"flighttrack/internal/opensky"
)

const earthRadiusNM = 3440.065

// Quality describes how much trust an estimate deserves.
type Quality int

const (
	// Unavailable means no estimate could be produced at all.
	Unavailable Quality = iota
	// Rough means the aircraft is manoeuvring, slow, or very close in, so the
	// straight-line assumption is at its weakest.
	Rough
	// Fair means the aircraft is in cruise and the estimate is as good as this
	// method gets.
	Fair
)

func (q Quality) String() string {
	switch q {
	case Fair:
		return "fair"
	case Rough:
		return "rough"
	default:
		return "unavailable"
	}
}

// Estimate is an arrival prediction made at a fixed moment.
type Estimate struct {
	Valid       bool
	Quality     Quality
	Reason      string // why it is unavailable or rough, for display
	Origin      airports.Airport
	Destination airports.Airport
	DistanceNM  float64
	BearingDeg  float64 // initial great-circle bearing to destination
	SpeedKts    float64
	Remaining   time.Duration // flight time left at the moment of the snapshot
	ArrivalUTC  time.Time     // absolute arrival instant, in UTC
	BasedOn     time.Time     // the snapshot instant this was computed from

	// Route progress, only available when an origin was supplied. Without one
	// the aircraft's position and destination say nothing about how far it has
	// already come.
	HasProgress bool
	TotalNM     float64 // origin to destination, great circle
	Progress    float64 // 0 to 1, fraction of TotalNM behind the aircraft
}

// Countdown reports the time left as of now. It reads the system clock rather
// than the API, so it keeps ticking between refreshes.
func (e Estimate) Countdown(now time.Time) time.Duration {
	if !e.Valid {
		return 0
	}
	return e.ArrivalUTC.Sub(now)
}

// Compute produces an estimate from one observation. A zero origin is fine;
// it only means no route progress can be worked out.
func Compute(obs *opensky.Observation, origin, dest airports.Airport, at time.Time) Estimate {
	e := Estimate{Origin: origin, Destination: dest, BasedOn: at}
	switch {
	case obs == nil:
		e.Reason = "no position data for this flight"
		return e
	case !obs.HasPos:
		e.Reason = "the aircraft is being tracked but is not reporting a position"
		return e
	case dest.IATA == "":
		e.Reason = "no destination set"
		return e
	}

	e.DistanceNM = DistanceNM(obs.Lat, obs.Lon, dest.Lat, dest.Lon)
	e.BearingDeg = InitialBearing(obs.Lat, obs.Lon, dest.Lat, dest.Lon)
	e.SpeedKts = obs.SpeedKts()

	// Progress needs the whole route, so it is available whether or not an
	// arrival time can be projected: a parked aircraft still has a position.
	if origin.IATA != "" && origin.IATA != dest.IATA {
		e.TotalNM = DistanceNM(origin.Lat, origin.Lon, dest.Lat, dest.Lon)
		if e.TotalNM > 0 {
			// Real routes are longer than the great circle, and an aircraft
			// can sit outside the direct line, so this is clamped rather than
			// allowed to read below zero or above one.
			e.Progress = clamp((e.TotalNM-e.DistanceNM)/e.TotalNM, 0, 1)
			e.HasProgress = true
		}
	}

	if obs.OnGround {
		e.Reason = "the aircraft is on the ground, so there is no airborne speed to project"
		return e
	}
	// Below this, dividing by ground speed produces absurd numbers.
	if e.SpeedKts < 40 {
		e.Reason = "ground speed is too low to project an arrival"
		return e
	}

	hours := e.DistanceNM / e.SpeedKts
	e.Remaining = time.Duration(hours * float64(time.Hour))
	e.ArrivalUTC = at.UTC().Add(e.Remaining)
	e.Valid = true

	switch {
	case e.DistanceNM < 150:
		e.Quality = Rough
		e.Reason = "close in: descent and approach will add time this ignores"
	case e.SpeedKts < 250:
		e.Quality = Rough
		e.Reason = "below cruise speed: climbing, descending or manoeuvring"
	default:
		e.Quality = Fair
		e.Reason = "cruise: straight-line estimate, excludes winds and routing"
	}
	return e
}

// DistanceNM returns the great-circle distance in nautical miles.
func DistanceNM(lat1, lon1, lat2, lon2 float64) float64 {
	p1, p2 := rad(lat1), rad(lat2)
	dp := rad(lat2 - lat1)
	dl := rad(lon2 - lon1)
	a := math.Sin(dp/2)*math.Sin(dp/2) +
		math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return earthRadiusNM * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// InitialBearing returns the initial true bearing in degrees, 0-360.
func InitialBearing(lat1, lon1, lat2, lon2 float64) float64 {
	p1, p2 := rad(lat1), rad(lat2)
	dl := rad(lon2 - lon1)
	y := math.Sin(dl) * math.Cos(p2)
	x := math.Cos(p1)*math.Sin(p2) - math.Sin(p1)*math.Cos(p2)*math.Cos(dl)
	return math.Mod(deg(math.Atan2(y, x))+360, 360)
}

func rad(d float64) float64 { return d * math.Pi / 180 }
func deg(r float64) float64 { return r * 180 / math.Pi }

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Compass turns a bearing into an eight-point compass label.
func Compass(bearing float64) string {
	points := []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}
	i := int(math.Mod(bearing/45+0.5, 8))
	if i < 0 {
		i += 8
	}
	return points[i]
}

// FormatDuration renders a duration as a countdown, e.g. "2h 14m 03s".
// Negative durations render as "overdue by ...".
func FormatDuration(d time.Duration) string {
	overdue := d < 0
	if overdue {
		d = -d
	}
	d = d.Round(time.Second)
	h := int(d / time.Hour)
	m := int(d/time.Minute) % 60
	s := int(d/time.Second) % 60

	var out string
	if h > 0 {
		out = fmt.Sprintf("%dh %02dm %02ds", h, m, s)
	} else {
		out = fmt.Sprintf("%dm %02ds", m, s)
	}
	if overdue {
		return "overdue by " + out
	}
	return out
}
