package version

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want string // "older", "same", "newer"
	}{
		{"v1.0.0", "v1.0.1", "older"},
		{"v1.0.1", "v1.0.0", "newer"},
		{"v1.2.0", "v1.2.0", "same"},
		{"1.2.0", "v1.2.0", "same"},    // the v is optional
		{"v1.10.0", "v1.9.0", "newer"}, // numeric, not lexical
		{"v2.0.0", "v1.99.99", "newer"},
		{"v1.2", "v1.2.0", "same"}, // missing components are zero
		{"v1.2.0-rc1", "v1.2.0", "older"},
		{"v1.2.0", "v1.2.0-rc1", "newer"},
		{"v1.2.0-rc1", "v1.2.0-rc2", "older"},
	}
	for _, c := range cases {
		got := Compare(c.a, c.b)
		var label string
		switch {
		case got < 0:
			label = "older"
		case got > 0:
			label = "newer"
		default:
			label = "same"
		}
		if label != c.want {
			t.Errorf("Compare(%q, %q) says %s, want %s", c.a, c.b, label, c.want)
		}
	}
}

// A build from source must never nag about updates.
func TestDevBuildNeverChecks(t *testing.T) {
	original := Version
	defer func() { Version = original }()

	Version = "dev"
	if !IsDev() {
		t.Fatal("an unstamped build should report as dev")
	}
	// A cache file that would otherwise trigger an update notice.
	path := writeTestCache(t, cache{CheckedAt: time.Now(), Latest: "v9.9.9", URL: "x"})
	if _, ok := Check(context.Background(), path); ok {
		t.Error("a dev build reported an update")
	}
}

func TestCheckUsesFreshCacheWithoutNetwork(t *testing.T) {
	original := Version
	defer func() { Version = original }()
	Version = "v1.0.0"

	path := writeTestCache(t, cache{
		CheckedAt: time.Now().Add(-time.Hour), // inside the 24h window
		Latest:    "v1.1.0",
		URL:       "https://example.com/release",
	})
	release, ok := Check(context.Background(), path)
	if !ok {
		t.Fatal("expected the cached newer version to be reported")
	}
	if release.Version != "v1.1.0" {
		t.Errorf("reported %q, want v1.1.0", release.Version)
	}
}

func TestCheckSaysNothingWhenUpToDate(t *testing.T) {
	original := Version
	defer func() { Version = original }()
	Version = "v1.1.0"

	path := writeTestCache(t, cache{CheckedAt: time.Now(), Latest: "v1.1.0"})
	if _, ok := Check(context.Background(), path); ok {
		t.Error("reported an update when already on the latest version")
	}

	// And never suggests going backwards.
	path = writeTestCache(t, cache{CheckedAt: time.Now(), Latest: "v1.0.0"})
	if _, ok := Check(context.Background(), path); ok {
		t.Error("reported an older release as an update")
	}
}

// A clock moved backwards must not make a stale cache look fresh forever.
func TestCacheFromTheFutureIsRejected(t *testing.T) {
	path := writeTestCache(t, cache{CheckedAt: time.Now().Add(48 * time.Hour), Latest: "v9.9.9"})
	if _, fresh := readCache(path); fresh {
		t.Error("a cache timestamped in the future was treated as fresh")
	}
}

func TestStaleCacheIsRejected(t *testing.T) {
	path := writeTestCache(t, cache{CheckedAt: time.Now().Add(-48 * time.Hour), Latest: "v9.9.9"})
	if _, fresh := readCache(path); fresh {
		t.Error("a two-day-old cache was treated as fresh")
	}
}

func TestMissingOrCorruptCacheIsHarmless(t *testing.T) {
	dir := t.TempDir()
	if _, fresh := readCache(filepath.Join(dir, "nope.json")); fresh {
		t.Error("a missing cache should not read as fresh")
	}
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{not json"), 0o600)
	if _, fresh := readCache(bad); fresh {
		t.Error("a corrupt cache should not read as fresh")
	}
}

func writeTestCache(t *testing.T, c cache) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "update-check.json")
	buf, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
