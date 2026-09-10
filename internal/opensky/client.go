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
