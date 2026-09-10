package opensky

// The data shapes the rest of the app works with: one aircraft as the network
// last saw it, and a complete read of the sky.

import "time"

// Observation is one aircraft as OpenSky last saw it.
type Observation struct {
	Icao24   string
	Callsign string
	Lat, Lon float64
	BaroAlt  float64 // metres
	GeoAlt   float64 // metres
	OnGround bool
	Velocity float64 // m/s over the ground
	Track    float64 // degrees true
	VertRate float64 // m/s, positive is climbing
	Country  string  // registration country
	HasPos   bool
	Seen     time.Time // when OpenSky produced this snapshot
}

// SpeedKts returns ground speed in knots.
func (o Observation) SpeedKts() float64 { return o.Velocity * 1.94384 }

// AltitudeFt returns altitude in feet.
func (o Observation) AltitudeFt() float64 { return o.GeoAlt * 3.28084 }

// ClimbFPM returns vertical rate in feet per minute.
func (o Observation) ClimbFPM() float64 { return o.VertRate * 196.85 }

// Snapshot is one complete read of the sky.
type Snapshot struct {
	Taken    time.Time // OpenSky's own timestamp for the data
	Fetched  time.Time // when this process received it
	Aircraft map[FlightID]*Observation
	Total    int // aircraft in the feed, including ones with no usable callsign
}

// Age reports how long ago the snapshot was fetched.
func (s Snapshot) Age() time.Duration { return time.Since(s.Fetched) }

// Lookup finds one flight in the snapshot.
func (s Snapshot) Lookup(id FlightID) (*Observation, bool) {
	o, ok := s.Aircraft[id]
	return o, ok
}

// better reports whether a is a more trustworthy record of a flight than b.
func better(a, b *Observation) bool {
	if a.HasPos != b.HasPos {
		return a.HasPos
	}
	if a.OnGround != b.OnGround {
		return !a.OnGround
	}
	return a.Seen.After(b.Seen)
}
