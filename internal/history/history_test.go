package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"flighttrack/internal/opensky"
)

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("a first run should not error, got %v", err)
	}
	if len(f.Entries) != 0 {
		t.Errorf("expected an empty history, got %d entries", len(f.Entries))
	}
}

func TestLoadCorruptFileStartsFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err == nil {
		t.Error("a corrupt file should report a problem")
	}
	if f == nil || len(f.Entries) != 0 {
		t.Error("a corrupt file should still yield a usable empty history")
	}
}

func TestRecordCapsAtMaxEntries(t *testing.T) {
	var f File
	base := time.Now()
	for i := 0; i < MaxEntries+5; i++ {
		f.Record(Entry{
			Flight:   "X",
			FlightID: string(rune('A'+i)) + "AA100",
			LastUsed: base.Add(time.Duration(i) * time.Minute),
		})
	}
	if len(f.Entries) != MaxEntries {
		t.Fatalf("kept %d entries, want the cap of %d", len(f.Entries), MaxEntries)
	}
	// The most recently recorded must be first.
	if f.Entries[0].FlightID != string(rune('A'+MaxEntries+4))+"AA100" {
		t.Errorf("newest entry is %q, want it at the front", f.Entries[0].FlightID)
	}
}

func TestRecordReplacesTheSameSearch(t *testing.T) {
	var f File
	f.Record(Entry{Flight: "QF2", FlightID: "QFA2", Origin: "SYD", Dest: "LHR"})
	f.Record(Entry{Flight: "BA117", FlightID: "BAW117", Dest: "JFK"})
	f.Record(Entry{Flight: "QF2", FlightID: "QFA2", Origin: "SYD", Dest: "LHR"})

	if len(f.Entries) != 2 {
		t.Fatalf("got %d entries, want 2: repeating a search should not duplicate it", len(f.Entries))
	}
	if f.Entries[0].FlightID != "QFA2" {
		t.Error("the repeated search should move to the front")
	}
}

// The same flight to a different airport is a different search.
func TestRecordKeepsDifferentRoutesApart(t *testing.T) {
	var f File
	f.Record(Entry{Flight: "QF2", FlightID: "QFA2", Dest: "LHR"})
	f.Record(Entry{Flight: "QF2", FlightID: "QFA2", Dest: "SIN"})
	if len(f.Entries) != 2 {
		t.Errorf("got %d entries, want 2", len(f.Entries))
	}
}

func TestRecordIgnoresEmptyFlight(t *testing.T) {
	var f File
	f.Record(Entry{Flight: "", FlightID: "  "})
	if len(f.Entries) != 0 {
		t.Error("an entry with no flight id should not be stored")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "history.json")
	fetched := time.Now().Add(-5 * time.Minute).Round(time.Second)

	var f File
	e := Entry{Flight: "QF2", FlightID: "QFA2", Origin: "SYD", Dest: "LHR", LastUsed: time.Now()}
	e.SetObservation(&opensky.Observation{
		Callsign: "QFA2", Icao24: "7c1234",
		Lat: 40.5, Lon: 45.5, GeoAlt: 11800, Velocity: 300, Track: 290, HasPos: true,
	}, fetched)
	f.Record(e)

	if err := f.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(got.Entries))
	}
	back := got.Entries[0]
	if back.Flight != "QF2" || back.Origin != "SYD" || back.Dest != "LHR" {
		t.Errorf("route did not survive the round trip: %+v", back)
	}
	obs := back.Observation()
	if obs == nil || obs.Lat != 40.5 || obs.Lon != 45.5 || !obs.HasPos {
		t.Errorf("position did not survive the round trip: %+v", obs)
	}
	if !back.Position.Fetched.Equal(fetched) {
		t.Errorf("fetched time = %v, want %v", back.Position.Fetched, fetched)
	}
}

// The file records which flights the user has been following, so it should not
// be world readable. Windows ignores POSIX modes entirely: there the file is
// protected by the ACL on %APPDATA%\flighttrack, which is per-user by default.
func TestSaveUsesRestrictivePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes are not enforced on Windows; the directory ACL applies instead")
	}
	path := filepath.Join(t.TempDir(), "history.json")
	var f File
	f.Record(Entry{Flight: "QF2", FlightID: "QFA2"})
	if err := f.Save(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("history file mode is %o, want no group or other access", mode)
	}
}

func TestUsableRespectsTheCacheLifetime(t *testing.T) {
	now := time.Now()
	fresh := Entry{Position: &Position{Fetched: now.Add(-10 * time.Minute)}}
	stale := Entry{Position: &Position{Fetched: now.Add(-2 * time.Hour)}}
	none := Entry{}

	if !fresh.Usable(now) {
		t.Error("a ten-minute-old position should be usable")
	}
	if stale.Usable(now) {
		t.Error("a two-hour-old position should not be usable")
	}
	if none.Usable(now) {
		t.Error("an entry with no position should never be usable")
	}
}

// A clock change backwards must not make stored data look like the future.
func TestAgeNeverGoesNegative(t *testing.T) {
	now := time.Now()
	e := Entry{Position: &Position{Fetched: now.Add(time.Hour)}}
	age, ok := e.Age(now)
	if !ok {
		t.Fatal("expected an age")
	}
	if age < 0 {
		t.Errorf("age = %v, want it clamped to zero", age)
	}
}

func TestRateBoundaries(t *testing.T) {
	cases := []struct {
		age  time.Duration
		want Freshness
	}{
		{time.Second, Fresh},
		{9 * time.Minute, Fresh},
		{11 * time.Minute, Aging},
		{59 * time.Minute, Aging},
		{time.Hour, Stale},
		{5 * time.Hour, Stale},
	}
	for _, c := range cases {
		if got := Rate(c.age); got != c.want {
			t.Errorf("Rate(%v) = %v, want %v", c.age, got, c.want)
		}
	}
}

// Anything reaching Load is parsed input, even though it is local.
func TestSanitizeDropsImpossibleCoordinates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	bad := File{
		Version: 1,
		Entries: []Entry{
			{Flight: "A", FlightID: "AAA1", Position: &Position{Lat: 999, Lon: 999, HasPos: true}},
			{Flight: "B", FlightID: "BBB2", Position: &Position{Lat: 51, Lon: 0, HasPos: true}},
			{Flight: "C", FlightID: ""}, // no id at all
		},
	}
	buf, _ := json.Marshal(bad)
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Entries) != 2 {
		t.Fatalf("got %d entries, want the unusable one dropped", len(f.Entries))
	}
	for _, e := range f.Entries {
		if e.FlightID == "AAA1" && e.Position != nil {
			t.Error("an out-of-range position should have been discarded")
		}
	}
}

func TestLoadEnforcesTheCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	var big File
	for i := 0; i < 40; i++ {
		big.Entries = append(big.Entries, Entry{
			Flight: "X", FlightID: "AAA" + string(rune('0'+i%10)),
			LastUsed: time.Now().Add(-time.Duration(i) * time.Minute),
		})
	}
	buf, _ := json.Marshal(big)
	os.WriteFile(path, buf, 0o600)

	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Entries) > MaxEntries {
		t.Errorf("loaded %d entries, want at most %d", len(f.Entries), MaxEntries)
	}
}

func TestClearIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	var f File
	f.Record(Entry{Flight: "QF2", FlightID: "QFA2"})
	if err := f.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := Clear(path); err != nil {
		t.Fatalf("first clear: %v", err)
	}
	if err := Clear(path); err != nil {
		t.Errorf("clearing an already-cleared history should succeed, got %v", err)
	}
}

func TestLabel(t *testing.T) {
	cases := []struct {
		e    Entry
		want string
	}{
		{Entry{Flight: "QF2", Origin: "SYD", Dest: "LHR"}, "QF2  SYD -> LHR"},
		{Entry{Flight: "QF2", Dest: "LHR"}, "QF2  -> LHR"},
		{Entry{Flight: "QF2"}, "QF2"},
	}
	for _, c := range cases {
		if got := c.e.Label(); got != c.want {
			t.Errorf("Label() = %q, want %q", got, c.want)
		}
	}
}
