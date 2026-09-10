package airports

import (
	"math"
	"testing"
)

// These coordinates are checked against the generated table so a bad
// regeneration (shifted columns, truncated file) fails loudly rather than
// quietly producing wrong distances.
func TestLookupKnownAirports(t *testing.T) {
	cases := []struct {
		code     string
		lat, lon float64
	}{
		{"LHR", 51.4706, -0.4619},
		{"JFK", 40.6398, -73.7789},
		{"SYD", -33.9461, 151.1772},
		{"NRT", 35.7647, 140.3863},
		{"GRU", -23.4356, -46.4731},
		{"EGLL", 51.4706, -0.4619}, // ICAO lookup
	}
	for _, c := range cases {
		a, ok := Lookup(c.code)
		if !ok {
			t.Errorf("Lookup(%q) not found", c.code)
			continue
		}
		if math.Abs(a.Lat-c.lat) > 0.05 || math.Abs(a.Lon-c.lon) > 0.05 {
			t.Errorf("Lookup(%q) = %.4f,%.4f, want ~%.4f,%.4f", c.code, a.Lat, a.Lon, c.lat, c.lon)
		}
	}
}

func TestLookupRejectsJunk(t *testing.T) {
	for _, code := range []string{"", "X", "ZZZZZ", "12"} {
		if a, ok := Lookup(code); ok {
			t.Errorf("Lookup(%q) unexpectedly found %q", code, a.IATA)
		}
	}
}

func TestLookupIsCaseAndSpaceInsensitive(t *testing.T) {
	a, ok := Lookup("  lhr ")
	if !ok || a.IATA != "LHR" {
		t.Errorf(`Lookup("  lhr ") = %q, %v; want LHR, true`, a.IATA, ok)
	}
}

func TestTableIsPopulated(t *testing.T) {
	if n := Count(); n < 3000 {
		t.Errorf("Count() = %d, want at least 3000 airports; did the generator run?", n)
	}
}

// Every record must carry a usable position, or an ETA built from it is junk.
func TestAllCoordinatesInRange(t *testing.T) {
	Count() // force load
	for _, a := range all {
		if a.Lat < -90 || a.Lat > 90 || a.Lon < -180 || a.Lon > 180 {
			t.Fatalf("%s has out-of-range coordinates %.4f,%.4f", a.IATA, a.Lat, a.Lon)
		}
		if a.Lat == 0 && a.Lon == 0 {
			t.Fatalf("%s sits at null island", a.IATA)
		}
		if len(a.IATA) != 3 {
			t.Fatalf("bad IATA code %q", a.IATA)
		}
	}
}

func TestSearchFindsByCodeAndCity(t *testing.T) {
	if hits := Search("LHR", 5); len(hits) == 0 || hits[0].IATA != "LHR" {
		t.Errorf(`Search("LHR") did not rank LHR first`)
	}
	if hits := Search("Amsterdam", 5); len(hits) == 0 {
		t.Error(`Search("Amsterdam") found nothing`)
	}
	if hits := Search("", 5); hits != nil {
		t.Error("empty query should return nothing")
	}
	if hits := Search("LHR", 0); hits != nil {
		t.Error("zero limit should return nothing")
	}
}

func TestLabel(t *testing.T) {
	a := Airport{IATA: "LHR", Name: "London Heathrow", City: "London", Country: "GB"}
	want := "LHR - London Heathrow (London, GB)"
	if got := a.Label(); got != want {
		t.Errorf("Label() = %q, want %q", got, want)
	}
}
