package opensky

// Flight numbers as they appear on a ticket, and the ICAO callsigns aircraft
// actually transmit. Matching one to the other is the whole job of this file.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Flights are quoted to passengers in IATA form (KL1234) but transmitted over
// ADS-B as an ICAO callsign (KLM1234). This covers the common carriers;
// anything else can be given as the 3-letter ICAO callsign directly.
var iataToICAO = map[string]string{
	"AA": "AAL", "AC": "ACA", "AF": "AFR", "AI": "AIC", "AM": "AMX",
	"AS": "ASA", "AY": "FIN", "AZ": "ITY", "BA": "BAW", "BR": "EVA",
	"CI": "CAL", "CX": "CPA", "CZ": "CSN", "DL": "DAL", "EI": "EIN",
	"EK": "UAE", "ET": "ETH", "EW": "EWG", "EY": "ETD", "FR": "RYR",
	"GA": "GIA", "HA": "HAL", "IB": "IBE", "JL": "JAL", "KE": "KAL",
	"KL": "KLM", "LA": "LAN", "LH": "DLH", "LO": "LOT", "LX": "SWR",
	"MH": "MAS", "MU": "CES", "NH": "ANA", "NZ": "ANZ", "OS": "AUA",
	"PR": "PAL", "QF": "QFA", "QR": "QTR", "SK": "SAS", "SQ": "SIA",
	"SU": "AFL", "SV": "SVA", "TG": "THA", "TK": "THY", "TP": "TAP",
	"UA": "UAL", "UX": "AEA", "VA": "VOZ", "VS": "VIR", "WN": "SWA",
	"WS": "WJA", "B6": "JBU", "F9": "FFT", "NK": "NKS", "U2": "EZY",
	"W6": "WZZ", "VY": "VLG", "HV": "TRA", "DY": "NOZ", "D8": "IBK",
	"PC": "PGT", "3U": "CSC", "6E": "IGO", "SG": "SEJ", "G9": "ABY",
	"FZ": "FDB", "OU": "CTN", "A3": "AEE", "BT": "BTI", "JU": "ASL",
	"RO": "ROT", "OK": "CSA", "SN": "BEL", "TO": "TVF", "LS": "EXS",
	"X3": "TUI", "DE": "CFG", "EN": "DLA", "KM": "AMC", "ME": "MEA",
}

// AddAirline registers an extra IATA to ICAO mapping. It is not safe to call
// concurrently with parsing, so callers should do it during startup.
func AddAirline(iata, icao string) {
	iataToICAO[strings.ToUpper(strings.TrimSpace(iata))] = strings.ToUpper(strings.TrimSpace(icao))
}

// prefix: 3-letter ICAO, or 2-char IATA (which may contain one digit).
// The 2-char alternative is listed first so "AAL123" is not read as "AA" + "L123".
var flightRe = regexp.MustCompile(`^(?:([A-Z][A-Z0-9]|[0-9][A-Z])|([A-Z]{3}))0*([0-9]{1,4})([A-Z]{0,2})$`)

// maxFlightInput caps user input before it reaches the matcher.
const maxFlightInput = 16

// FlightID is a normalized flight number: an ICAO airline prefix plus the
// numeric part with leading zeros stripped, so "BA117", "BAW117" and the
// zero-padded "BAW0117" seen on the wire all compare equal.
type FlightID struct {
	Prefix string // ICAO airline designator, e.g. "BAW"
	Number int
	Suffix string // rare trailing letter, e.g. the "A" in "AAL123A"
}

func (f FlightID) String() string {
	return fmt.Sprintf("%s%d%s", f.Prefix, f.Number, f.Suffix)
}

// IsZero reports whether the ID is unset.
func (f FlightID) IsZero() bool { return f.Prefix == "" }

// ParseFlight turns a flight number as printed on a ticket into a FlightID.
func ParseFlight(s string) (FlightID, error) {
	clean := strings.ToUpper(strings.TrimSpace(s))
	if len(clean) > maxFlightInput {
		return FlightID{}, fmt.Errorf("flight number %q is too long", s)
	}
	clean = strings.NewReplacer(" ", "", "-", "", "/", "").Replace(clean)
	m := flightRe.FindStringSubmatch(clean)
	if m == nil {
		return FlightID{}, fmt.Errorf("cannot read %q as a flight number (expected something like KL1234)", s)
	}
	prefix := m[2] // ICAO branch
	if m[1] != "" {
		icao, ok := iataToICAO[m[1]]
		if !ok {
			return FlightID{}, fmt.Errorf("airline code %q is not in the table: enter the 3-letter ICAO callsign instead, e.g. KLM1234", m[1])
		}
		prefix = icao
	}
	n, err := strconv.Atoi(m[3])
	if err != nil {
		return FlightID{}, fmt.Errorf("bad flight number in %q: %w", s, err)
	}
	return FlightID{Prefix: prefix, Number: n, Suffix: m[4]}, nil
}

// ParseCallsign reads a callsign off the wire. Unlike ParseFlight it never
// consults the IATA table: transmitted callsigns are always ICAO form, and
// guessing otherwise would create false matches.
func ParseCallsign(s string) (FlightID, bool) {
	clean := strings.ToUpper(strings.TrimSpace(s))
	if len(clean) < 4 || len(clean) > maxFlightInput {
		return FlightID{}, false
	}
	m := flightRe.FindStringSubmatch(clean)
	if m == nil || m[2] == "" {
		return FlightID{}, false
	}
	n, err := strconv.Atoi(m[3])
	if err != nil {
		return FlightID{}, false
	}
	return FlightID{Prefix: m[2], Number: n, Suffix: m[4]}, true
}
