// Package opensky is a small client for the OpenSky Network live state API,
// plus the flight-number handling needed to match a passenger ticket against
// the callsign an aircraft actually transmits.
//
// OpenSky is free and open. Anonymous access allows roughly 400 credits a day
// and a full snapshot costs 4, so about 100 calls. A free account at
// https://opensky-network.org raises that to 4000 credits (~1000 calls).
package opensky

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	tokenURL  = "https://auth.opensky-network.org/auth/realms/opensky-network/protocol/openid-connect/token"
	statesURL = "https://opensky-network.org/api/states/all"

	// A full snapshot is tens of megabytes of JSON. The cap stops a malformed
	// or hostile response from growing without bound.
	maxResponseBytes = 128 << 20

	// CreditsPerSnapshot is what OpenSky charges for an unfiltered /states/all.
	CreditsPerSnapshot = 4
)

// ---------------------------------------------------------------- flight ids

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

// ------------------------------------------------------------- observations

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

// ------------------------------------------------------------------- client

// Client talks to OpenSky. The zero value is not usable; call New.
type Client struct {
	http       *http.Client
	id, secret string

	mu           sync.Mutex
	token        string
	tokenExpires time.Time
	credits      int // rough running total spent by this process
}

// New builds a client. Empty credentials mean anonymous access.
func New(clientID, clientSecret string) *Client {
	return &Client{
		http:   &http.Client{Timeout: 90 * time.Second},
		id:     strings.TrimSpace(clientID),
		secret: strings.TrimSpace(clientSecret),
	}
}

// Authenticated reports whether credentials were supplied.
func (c *Client) Authenticated() bool { return c.id != "" && c.secret != "" }

// CreditsUsed reports roughly how many API credits this process has spent.
func (c *Client) CreditsUsed() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.credits
}

// DailyCredits is the quota that applies to this client.
func (c *Client) DailyCredits() int {
	if c.Authenticated() {
		return 4000
	}
	return 400
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExpires) {
		return c.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.id},
		"client_secret": {c.secret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		// The response body can echo the request; report the status only so a
		// secret never lands in a log or an on-screen error.
		return "", fmt.Errorf("OpenSky rejected the API credentials (%s)", resp.Status)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	if tok.AccessToken == "" {
		return "", errors.New("token response contained no access_token")
	}
	ttl := time.Duration(tok.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	c.token = tok.AccessToken
	// Refresh a minute early so a request never races the expiry.
	c.tokenExpires = time.Now().Add(ttl - time.Minute)
	return c.token, nil
}

type statesResponse struct {
	Time   int64               `json:"time"`
	States [][]json.RawMessage `json:"states"`
}

// Fetch pulls one complete snapshot. OpenSky offers no server-side callsign
// filter, so the whole feed is retrieved and matched locally.
func (c *Client) Fetch(ctx context.Context) (*Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, statesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.Authenticated() {
		tok, err := c.accessToken(ctx)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach OpenSky: %w", err)
	}
	defer resp.Body.Close()

	c.mu.Lock()
	c.credits += CreditsPerSnapshot
	c.mu.Unlock()

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("OpenSky rate limit reached (HTTP 429): slow the refresh down, or add API credentials for a larger quota")
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("OpenSky refused the request (%s): check OPENSKY_CLIENT_ID and OPENSKY_CLIENT_SECRET", resp.Status)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("OpenSky returned %s", resp.Status)
	}

	var sr statesResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&sr); err != nil {
		return nil, fmt.Errorf("could not read the OpenSky response: %w", err)
	}

	now := time.Now()
	taken := time.Unix(sr.Time, 0).UTC()
	if sr.Time == 0 {
		taken = now.UTC()
	}
	snap := &Snapshot{
		Taken:    taken,
		Fetched:  now,
		Aircraft: make(map[FlightID]*Observation, len(sr.States)),
		Total:    len(sr.States),
	}
	for _, row := range sr.States {
		if len(row) < 12 {
			continue
		}
		callsign := strings.TrimSpace(rawString(row[1]))
		if callsign == "" {
			continue
		}
		id, ok := ParseCallsign(callsign)
		if !ok {
			continue
		}
		lon, lonOK := rawFloat(row[5])
		lat, latOK := rawFloat(row[6])
		baro, _ := rawFloat(row[7])
		geo := baro
		if len(row) > 13 {
			if g, ok := rawFloat(row[13]); ok {
				geo = g
			}
		}
		vel, _ := rawFloat(row[9])
		trk, _ := rawFloat(row[10])
		vr, _ := rawFloat(row[11])

		obs := &Observation{
			Icao24:   strings.TrimSpace(rawString(row[0])),
			Callsign: callsign,
			Country:  strings.TrimSpace(rawString(row[2])),
			Lat:      lat,
			Lon:      lon,
			BaroAlt:  baro,
			GeoAlt:   geo,
			OnGround: rawBool(row[8]),
			Velocity: vel,
			Track:    trk,
			VertRate: vr,
			HasPos:   lonOK && latOK,
			Seen:     taken,
		}
		// A flight number can appear twice: a positioning leg, or the previous
		// rotation still decaying out of the feed.
		if prev, dup := snap.Aircraft[id]; dup && !better(obs, prev) {
			continue
		}
		snap.Aircraft[id] = obs
	}
	return snap, nil
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

func rawString(r json.RawMessage) string {
	var s string
	if json.Unmarshal(r, &s) == nil {
		return s
	}
	return ""
}

func rawFloat(r json.RawMessage) (float64, bool) {
	var f float64
	if json.Unmarshal(r, &f) == nil {
		return f, true
	}
	return 0, false
}

func rawBool(r json.RawMessage) bool {
	var b bool
	if json.Unmarshal(r, &b) == nil {
		return b
	}
	return false
}
