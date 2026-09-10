package opensky

import "testing"

func TestParseFlight(t *testing.T) {
	cases := []struct{ in, want string }{
		{"KL1234", "KLM1234"},  // IATA -> ICAO
		{"kl1234", "KLM1234"},  // case insensitive
		{"KL 1234", "KLM1234"}, // spaces tolerated
		{"BA-117", "BAW117"},   // separators tolerated
		{"BA117", "BAW117"},
		{"BAW117", "BAW117"},   // already ICAO
		{"BAW0117", "BAW117"},  // zero padding stripped
		{"AA123", "AAL123"},    // 2-letter must not eat the third letter
		{"AAL123", "AAL123"},   // 3-letter ICAO wins when it has to
		{"U21234", "EZY1234"},  // IATA code containing a digit
		{"3U8888", "CSC8888"},  // IATA code starting with a digit
		{"AAL123A", "AAL123A"}, // trailing suffix letter preserved
		{"DLH400", "DLH400"},
	}
	for _, c := range cases {
		got, err := ParseFlight(c.in)
		if err != nil {
			t.Errorf("ParseFlight(%q): %v", c.in, err)
			continue
		}
		if got.String() != c.want {
			t.Errorf("ParseFlight(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseFlightErrors(t *testing.T) {
	for _, in := range []string{"", "1234", "ZZ123", "KL", "KL12345678901234567890"} {
		if got, err := ParseFlight(in); err == nil {
			t.Errorf("ParseFlight(%q) = %q, want an error", in, got)
		}
	}
}

// A wire callsign and the flight number on a ticket must normalize to the same
// key, or nothing will ever match.
func TestCallsignMatchesFlight(t *testing.T) {
	cases := []struct{ wire, booked string }{
		{"KLM1234 ", "KL1234"},
		{"BAW0117", "BA117"},
		{"DLH400", "LH400"},
		{"EZY1234", "U21234"},
		{"AAL123", "AA123"},
	}
	for _, c := range cases {
		w, ok := ParseCallsign(c.wire)
		if !ok {
			t.Errorf("ParseCallsign(%q) failed", c.wire)
			continue
		}
		b, err := ParseFlight(c.booked)
		if err != nil {
			t.Errorf("ParseFlight(%q): %v", c.booked, err)
			continue
		}
		if w != b {
			t.Errorf("%q parsed as %q but %q parsed as %q", c.wire, w, c.booked, b)
		}
	}
}

// ParseCallsign must never apply the IATA table: a wire callsign is always
// ICAO, so reading "KL1234" off the wire as KLM would be a false match.
func TestParseCallsignRejectsIATA(t *testing.T) {
	for _, in := range []string{"KL1234", "AA123", "N512JB", "", "AB"} {
		if got, ok := ParseCallsign(in); ok && got.Prefix != in[:3] {
			t.Errorf("ParseCallsign(%q) = %q, want rejection or a literal ICAO prefix", in, got)
		}
	}
}

func TestBetterPrefersPositionedAirborne(t *testing.T) {
	withPos := &Observation{HasPos: true, OnGround: true}
	noPos := &Observation{HasPos: false, OnGround: false}
	if !better(withPos, noPos) {
		t.Error("a record with a position should beat one without")
	}
	airborne := &Observation{HasPos: true, OnGround: false}
	grounded := &Observation{HasPos: true, OnGround: true}
	if !better(airborne, grounded) {
		t.Error("an airborne record should beat a stale ground record")
	}
}

func TestUnitConversions(t *testing.T) {
	o := Observation{Velocity: 100, GeoAlt: 1000, VertRate: 5}
	if got := o.SpeedKts(); got < 194 || got > 195 {
		t.Errorf("SpeedKts() = %.2f, want ~194.4", got)
	}
	if got := o.AltitudeFt(); got < 3280 || got > 3281 {
		t.Errorf("AltitudeFt() = %.2f, want ~3280.8", got)
	}
	if got := o.ClimbFPM(); got < 984 || got > 985 {
		t.Errorf("ClimbFPM() = %.2f, want ~984.3", got)
	}
}
