package eta

import (
	"math"
	"testing"
	"time"

	"flighttrack/internal/airports"
	"flighttrack/internal/opensky"
)

func TestDistanceKnownRoutes(t *testing.T) {
	// Published great-circle distances, in nautical miles.
	cases := []struct {
		from, to string
		want     float64
	}{
		{"LHR", "JFK", 2990},
		{"LHR", "CDG", 188},
		{"SYD", "LAX", 6500},
		{"JFK", "LAX", 2144},
	}
	for _, c := range cases {
		a, ok1 := airports.Lookup(c.from)
		b, ok2 := airports.Lookup(c.to)
		if !ok1 || !ok2 {
			t.Fatalf("missing airport %s or %s", c.from, c.to)
		}
		got := DistanceNM(a.Lat, a.Lon, b.Lat, b.Lon)
		if math.Abs(got-c.want)/c.want > 0.02 {
			t.Errorf("DistanceNM(%s,%s) = %.0f nm, want ~%.0f", c.from, c.to, got, c.want)
		}
	}
}

func TestDistanceIsZeroForSamePoint(t *testing.T) {
	if d := DistanceNM(51.5, -0.5, 51.5, -0.5); d > 0.001 {
		t.Errorf("distance to self = %f, want 0", d)
	}
}

func TestInitialBearing(t *testing.T) {
	// Due north and due east from the equator/prime meridian.
	if b := InitialBearing(0, 0, 10, 0); math.Abs(b-0) > 0.5 && math.Abs(b-360) > 0.5 {
		t.Errorf("north bearing = %.1f, want 0", b)
	}
	if b := InitialBearing(0, 0, 0, 10); math.Abs(b-90) > 0.5 {
		t.Errorf("east bearing = %.1f, want 90", b)
	}
}

func TestCompass(t *testing.T) {
	cases := map[float64]string{0: "N", 45: "NE", 90: "E", 180: "S", 270: "W", 359: "N"}
	for deg, want := range cases {
		if got := Compass(deg); got != want {
			t.Errorf("Compass(%.0f) = %q, want %q", deg, got, want)
		}
	}
}

func TestComputeCruise(t *testing.T) {
	dest, _ := airports.Lookup("JFK")
	lhr, _ := airports.Lookup("LHR")
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

	// Sitting over LHR at 500 kts: ~2990 nm to run, so about 6 hours.
	obs := &opensky.Observation{
		Lat: lhr.Lat, Lon: lhr.Lon, HasPos: true,
		Velocity: 500 / 1.94384, // 500 kts in m/s
		GeoAlt:   11000,
	}
	e := Compute(obs, airports.Airport{}, dest, at)
	if !e.Valid {
		t.Fatalf("estimate not valid: %s", e.Reason)
	}
	if e.Quality != Fair {
		t.Errorf("Quality = %v, want fair", e.Quality)
	}
	hours := e.Remaining.Hours()
	if hours < 5.5 || hours > 6.5 {
		t.Errorf("Remaining = %.2fh, want ~6h", hours)
	}
	if !e.ArrivalUTC.Equal(at.Add(e.Remaining)) {
		t.Error("ArrivalUTC should be the snapshot time plus the remaining time")
	}
	if e.ArrivalUTC.Location() != time.UTC {
		t.Error("ArrivalUTC should be in UTC")
	}
}

func TestComputeRejectsUnusableInput(t *testing.T) {
	dest, _ := airports.Lookup("JFK")
	at := time.Now()

	if e := Compute(nil, airports.Airport{}, dest, at); e.Valid {
		t.Error("nil observation should not produce an estimate")
	}
	if e := Compute(&opensky.Observation{HasPos: false}, airports.Airport{}, dest, at); e.Valid {
		t.Error("missing position should not produce an estimate")
	}
	if e := Compute(&opensky.Observation{HasPos: true, Velocity: 300}, airports.Airport{}, airports.Airport{}, at); e.Valid {
		t.Error("missing destination should not produce an estimate")
	}
	onGround := &opensky.Observation{HasPos: true, OnGround: true, Velocity: 10, Lat: 51, Lon: 0}
	if e := Compute(onGround, airports.Airport{}, dest, at); e.Valid {
		t.Error("an aircraft on the ground should not produce an estimate")
	}
	// A near-stationary aircraft would divide out to an absurd arrival time.
	crawling := &opensky.Observation{HasPos: true, Velocity: 5, Lat: 51, Lon: 0}
	if e := Compute(crawling, airports.Airport{}, dest, at); e.Valid {
		t.Error("a near-zero ground speed should not produce an estimate")
	}
}

func TestComputeMarksCloseInAsRough(t *testing.T) {
	dest, _ := airports.Lookup("JFK")
	// 60 nm out, on approach speed.
	obs := &opensky.Observation{
		Lat: dest.Lat + 1, Lon: dest.Lon, HasPos: true,
		Velocity: 250 / 1.94384,
	}
	e := Compute(obs, airports.Airport{}, dest, time.Now())
	if !e.Valid {
		t.Fatalf("expected a valid estimate, got: %s", e.Reason)
	}
	if e.Quality != Rough {
		t.Errorf("Quality = %v, want rough when close in", e.Quality)
	}
}

func TestProgressNeedsAnOrigin(t *testing.T) {
	dest, _ := airports.Lookup("JFK")
	lhr, _ := airports.Lookup("LHR")
	obs := &opensky.Observation{Lat: lhr.Lat, Lon: lhr.Lon, HasPos: true, Velocity: 250}

	// Position and destination alone say nothing about how far it has come.
	if e := Compute(obs, airports.Airport{}, dest, time.Now()); e.HasProgress {
		t.Error("progress should be unavailable without an origin")
	}
	if e := Compute(obs, lhr, dest, time.Now()); !e.HasProgress {
		t.Error("progress should be available once an origin is given")
	}
}

func TestProgressAlongARoute(t *testing.T) {
	lhr, _ := airports.Lookup("LHR")
	jfk, _ := airports.Lookup("JFK")
	at := time.Now()

	// Sitting on the origin: nothing flown yet.
	atOrigin := &opensky.Observation{Lat: lhr.Lat, Lon: lhr.Lon, HasPos: true, Velocity: 250}
	if e := Compute(atOrigin, lhr, jfk, at); math.Abs(e.Progress) > 0.01 {
		t.Errorf("progress at the origin = %.3f, want 0", e.Progress)
	}
	// Sitting on the destination: all of it flown.
	atDest := &opensky.Observation{Lat: jfk.Lat, Lon: jfk.Lon, HasPos: true, Velocity: 250}
	if e := Compute(atDest, lhr, jfk, at); math.Abs(e.Progress-1) > 0.01 {
		t.Errorf("progress at the destination = %.3f, want 1", e.Progress)
	}
	// Total route length must match the direct distance.
	e := Compute(atOrigin, lhr, jfk, at)
	if want := DistanceNM(lhr.Lat, lhr.Lon, jfk.Lat, jfk.Lon); math.Abs(e.TotalNM-want) > 1 {
		t.Errorf("TotalNM = %.0f, want %.0f", e.TotalNM, want)
	}
}

// A real route is longer than the great circle and aircraft stray off the
// direct line, so the fraction must never leave 0..1.
func TestProgressIsClamped(t *testing.T) {
	lhr, _ := airports.Lookup("LHR")
	jfk, _ := airports.Lookup("JFK")
	at := time.Now()

	// Far beyond the destination.
	beyond := &opensky.Observation{Lat: jfk.Lat, Lon: jfk.Lon - 40, HasPos: true, Velocity: 250}
	if e := Compute(beyond, lhr, jfk, at); e.Progress < 0 || e.Progress > 1 {
		t.Errorf("progress past the destination = %.3f, want within 0..1", e.Progress)
	}
	// Far behind the origin.
	behind := &opensky.Observation{Lat: lhr.Lat, Lon: lhr.Lon + 40, HasPos: true, Velocity: 250}
	if e := Compute(behind, lhr, jfk, at); e.Progress < 0 || e.Progress > 1 {
		t.Errorf("progress before the origin = %.3f, want within 0..1", e.Progress)
	}
}

func TestProgressRejectsSameOriginAndDestination(t *testing.T) {
	lhr, _ := airports.Lookup("LHR")
	obs := &opensky.Observation{Lat: 51, Lon: 0, HasPos: true, Velocity: 250}
	if e := Compute(obs, lhr, lhr, time.Now()); e.HasProgress {
		t.Error("a zero-length route should not produce progress")
	}
}

// Progress is about position, so it must survive cases where no arrival time
// can be projected, such as an aircraft still parked at the gate.
func TestProgressAvailableWithoutAnArrivalEstimate(t *testing.T) {
	lhr, _ := airports.Lookup("LHR")
	jfk, _ := airports.Lookup("JFK")
	parked := &opensky.Observation{
		Lat: lhr.Lat, Lon: lhr.Lon, HasPos: true, OnGround: true, Velocity: 0,
	}
	e := Compute(parked, lhr, jfk, time.Now())
	if e.Valid {
		t.Fatal("a parked aircraft should not produce an arrival estimate")
	}
	if !e.HasProgress {
		t.Error("progress should still be available for a parked aircraft")
	}
}

func TestCountdownUsesTheClockNotTheAPI(t *testing.T) {
	base := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	e := Estimate{Valid: true, ArrivalUTC: base.Add(2 * time.Hour)}

	if got := e.Countdown(base); got != 2*time.Hour {
		t.Errorf("Countdown at base = %v, want 2h", got)
	}
	if got := e.Countdown(base.Add(90 * time.Minute)); got != 30*time.Minute {
		t.Errorf("Countdown 90m later = %v, want 30m", got)
	}
	// Past the arrival time it must go negative, not wrap or clamp.
	if got := e.Countdown(base.Add(3 * time.Hour)); got != -time.Hour {
		t.Errorf("Countdown past arrival = %v, want -1h", got)
	}
	var invalid Estimate
	if got := invalid.Countdown(base); got != 0 {
		t.Errorf("invalid estimate countdown = %v, want 0", got)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{2*time.Hour + 14*time.Minute + 3*time.Second, "2h 14m 03s"},
		{45*time.Minute + 9*time.Second, "45m 09s"},
		{0, "0m 00s"},
		{-90 * time.Second, "overdue by 1m 30s"},
	}
	for _, c := range cases {
		if got := FormatDuration(c.d); got != c.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
