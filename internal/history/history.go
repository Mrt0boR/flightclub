// Package history remembers recent searches and the last position seen for
// each, so relaunching the app can offer a list to pick from and open on
// cached data instead of spending API credits.
//
// The file lives in the platform's standard per-user config directory:
// %APPDATA%\flighttrack\history.json on Windows, ~/.config/flighttrack
// elsewhere.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"flighttrack/internal/opensky"
)

const (
	// MaxEntries is how many past searches are kept. Oldest are dropped.
	MaxEntries = 5

	// CacheTTL is how long a stored position stays usable in place of a fresh
	// API call. An hour-old position on a cruising aircraft is several hundred
	// miles out of date, which is why the age is always shown in colour.
	CacheTTL = time.Hour

	// freshFor and agingFor set the green/amber boundaries.
	freshFor = 10 * time.Minute
	agingFor = CacheTTL

	fileVersion = 1
)

// Freshness grades how much a cached position can be trusted.
type Freshness int

const (
	// Fresh means minutes old: usable as-is.
	Fresh Freshness = iota
	// Aging means tens of minutes old: recognisably out of date.
	Aging
	// Stale means past the cache lifetime: shown, but never reused.
	Stale
)

func (f Freshness) String() string {
	switch f {
	case Fresh:
		return "fresh"
	case Aging:
		return "aging"
	default:
		return "stale"
	}
}

// Rate grades an age.
func Rate(age time.Duration) Freshness {
	switch {
	case age < freshFor:
		return Fresh
	case age < agingFor:
		return Aging
	default:
		return Stale
	}
}

// Position is the part of an observation worth keeping between runs.
type Position struct {
	Fetched  time.Time `json:"fetched"`
	Callsign string    `json:"callsign,omitempty"`
	Icao24   string    `json:"icao24,omitempty"`
	Lat      float64   `json:"lat"`
	Lon      float64   `json:"lon"`
	GeoAlt   float64   `json:"geo_alt"`
	Velocity float64   `json:"velocity"`
	Track    float64   `json:"track"`
	VertRate float64   `json:"vert_rate"`
	OnGround bool      `json:"on_ground"`
	HasPos   bool      `json:"has_pos"`
}

// Entry is one remembered search.
type Entry struct {
	Flight   string    `json:"flight"`    // as typed, e.g. "QF2"
	FlightID string    `json:"flight_id"` // normalized callsign, e.g. "QFA2"
	Origin   string    `json:"origin,omitempty"`
	Dest     string    `json:"dest,omitempty"`
	LastUsed time.Time `json:"last_used"`
	Position *Position `json:"position,omitempty"`
}

// Label renders the route for a list, e.g. "QF2  SYD -> LHR".
func (e Entry) Label() string {
	var b strings.Builder
	b.WriteString(e.Flight)
	switch {
	case e.Origin != "" && e.Dest != "":
		fmt.Fprintf(&b, "  %s -> %s", e.Origin, e.Dest)
	case e.Dest != "":
		fmt.Fprintf(&b, "  -> %s", e.Dest)
	}
	return b.String()
}

// Age reports how old the cached position is. Entries with no position report
// false.
func (e Entry) Age(now time.Time) (time.Duration, bool) {
	if e.Position == nil || e.Position.Fetched.IsZero() {
		return 0, false
	}
	d := now.Sub(e.Position.Fetched)
	if d < 0 {
		d = 0 // a clock change should not read as data from the future
	}
	return d, true
}

// Usable reports whether the cached position is recent enough to open on
// instead of calling the API.
func (e Entry) Usable(now time.Time) bool {
	age, ok := e.Age(now)
	return ok && age < CacheTTL
}

// Observation converts the cached position back into the shape the rest of the
// app works with. It returns nil when there is nothing stored.
func (e Entry) Observation() *opensky.Observation {
	p := e.Position
	if p == nil {
		return nil
	}
	return &opensky.Observation{
		Icao24:   p.Icao24,
		Callsign: p.Callsign,
		Lat:      p.Lat,
		Lon:      p.Lon,
		GeoAlt:   p.GeoAlt,
		BaroAlt:  p.GeoAlt,
		Velocity: p.Velocity,
		Track:    p.Track,
		VertRate: p.VertRate,
		OnGround: p.OnGround,
		HasPos:   p.HasPos,
		Seen:     p.Fetched,
	}
}

// SetObservation stores an observation against the entry.
func (e *Entry) SetObservation(obs *opensky.Observation, fetched time.Time) {
	if obs == nil {
		return
	}
	e.Position = &Position{
		Fetched:  fetched,
		Callsign: obs.Callsign,
		Icao24:   obs.Icao24,
		Lat:      obs.Lat,
		Lon:      obs.Lon,
		GeoAlt:   obs.GeoAlt,
		Velocity: obs.Velocity,
		Track:    obs.Track,
		VertRate: obs.VertRate,
		OnGround: obs.OnGround,
		HasPos:   obs.HasPos,
	}
}

// File is the on-disk document.
type File struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// DefaultPath returns the standard per-user location for the history file.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate the user config directory: %w", err)
	}
	return filepath.Join(dir, "flighttrack", "history.json"), nil
}

// Load reads the history file. A missing file is not an error: it returns an
// empty history, which is what a first run should see.
func Load(path string) (*File, error) {
	buf, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &File{Version: fileVersion}, nil
	}
	if err != nil {
		return &File{Version: fileVersion}, err
	}
	var f File
	if err := json.Unmarshal(buf, &f); err != nil {
		// A corrupt file should not stop the app; it starts over instead.
		return &File{Version: fileVersion}, fmt.Errorf("history file is unreadable, starting fresh: %w", err)
	}
	f.Version = fileVersion
	f.Entries = sanitize(f.Entries)
	return &f, nil
}

// sanitize drops entries that are malformed or carry impossible coordinates,
// and enforces the cap. The file is local, but it is still parsed input.
func sanitize(in []Entry) []Entry {
	out := make([]Entry, 0, len(in))
	for _, e := range in {
		if strings.TrimSpace(e.FlightID) == "" {
			continue
		}
		if e.Flight == "" {
			e.Flight = e.FlightID
		}
		if p := e.Position; p != nil {
			if p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
				e.Position = nil
			}
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastUsed.After(out[j].LastUsed) })
	if len(out) > MaxEntries {
		out = out[:MaxEntries]
	}
	return out
}

// Record adds or updates an entry and moves it to the front. Entries are keyed
// on the normalized flight plus its route, so tracking the same flight to a
// different airport is remembered separately.
func (f *File) Record(e Entry) {
	if strings.TrimSpace(e.FlightID) == "" {
		return
	}
	if e.LastUsed.IsZero() {
		e.LastUsed = time.Now()
	}
	kept := make([]Entry, 0, len(f.Entries)+1)
	kept = append(kept, e)
	for _, old := range f.Entries {
		if old.FlightID == e.FlightID && old.Dest == e.Dest && old.Origin == e.Origin {
			continue // replaced by the new one
		}
		kept = append(kept, old)
	}
	if len(kept) > MaxEntries {
		kept = kept[:MaxEntries]
	}
	f.Entries = kept
}

// Save writes the file, creating the directory if needed. It writes to a
// temporary file first so an interrupted write cannot truncate the history.
func (f *File) Save(path string) error {
	if path == "" {
		return errors.New("no history path")
	}
	f.Version = fileVersion
	if len(f.Entries) > MaxEntries {
		f.Entries = f.Entries[:MaxEntries]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	// 0600: this records which flights the user has been following.
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Clear empties the history and removes the file.
func Clear(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
