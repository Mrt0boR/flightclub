// Package version reports which build this is, and quietly checks GitHub for
// a newer release.
//
// The check is deliberately unobtrusive: it runs at most once a day, caches
// its answer, fails silently on any error, and never downloads anything. The
// most it does is put one line on screen.
package version

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Version is stamped at build time:
//
//	go build -ldflags "-X flighttrack/internal/version.Version=v1.2.0"
//
// An unstamped build reports "dev" and never checks for updates, so working
// from source does not nag.
var Version = "dev"

const (
	// releasesURL is the GitHub API endpoint for the newest release.
	releasesURL = "https://api.github.com/repos/Mrt0boR/flightclub/releases/latest"

	// checkEvery caps how often the network is touched. Unauthenticated
	// GitHub allows 60 requests an hour per IP, and an update check is worth
	// nowhere near that.
	checkEvery = 24 * time.Hour

	maxResponseBytes = 1 << 20
)

// Release is a newer version than the one running.
type Release struct {
	Version string `json:"version"`
	URL     string `json:"url"`
}

// IsDev reports whether this is an unstamped build from source.
func IsDev() bool { return Version == "dev" || Version == "" }

// cache is what gets written between checks.
type cache struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest,omitempty"`
	URL       string    `json:"url,omitempty"`
}

// DefaultCachePath returns where the last check is remembered, alongside the
// search history.
func DefaultCachePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "flighttrack", "update-check.json"), nil
}

// Check reports a newer release if there is one. The second return value is
// false whenever there is nothing to say, which covers every failure mode:
// no network, GitHub down, rate limited, no releases published yet, or a
// build from source. Callers are not expected to handle an error, because
// there is nothing useful a user could do about one.
func Check(ctx context.Context, cachePath string) (Release, bool) {
	if IsDev() {
		return Release{}, false
	}

	if cached, fresh := readCache(cachePath); fresh {
		return newerThanRunning(cached)
	}

	latest, err := fetchLatest(ctx)
	if err != nil {
		// Remember the attempt so a persistent failure is not retried on
		// every launch.
		writeCache(cachePath, cache{CheckedAt: time.Now()})
		return Release{}, false
	}
	writeCache(cachePath, cache{CheckedAt: time.Now(), Latest: latest.Version, URL: latest.URL})
	return newerThanRunning(cache{Latest: latest.Version, URL: latest.URL})
}

func newerThanRunning(c cache) (Release, bool) {
	if c.Latest == "" || Compare(c.Latest, Version) <= 0 {
		return Release{}, false
	}
	return Release{Version: c.Latest, URL: c.URL}, true
}

func readCache(path string) (cache, bool) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return cache{}, false
	}
	var c cache
	if err := json.Unmarshal(buf, &c); err != nil {
		return cache{}, false
	}
	if time.Since(c.CheckedAt) > checkEvery || c.CheckedAt.After(time.Now()) {
		return cache{}, false // stale, or the clock moved backwards
	}
	return c, true
}

func writeCache(path string, c cache) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	buf, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(path, buf, 0o600)
}

func fetchLatest(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "flighttrack/"+Version)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()

	// 404 means no releases have been published yet, which is not an error
	// worth surfacing; 403 means rate limited. Both mean "nothing to say".
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github returned %s", resp.Status)
	}

	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Draft   bool   `json:"draft"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&payload); err != nil {
		return Release{}, err
	}
	if payload.TagName == "" || payload.Draft {
		return Release{}, errors.New("no usable release")
	}
	return Release{Version: payload.TagName, URL: payload.HTMLURL}, nil
}

// Compare orders two version strings. It returns a negative number if a is
// older than b, zero if they match, and a positive number if a is newer.
//
// This understands the shape releases actually use — an optional "v", then
// dot-separated numbers, then an optional pre-release suffix — rather than
// full semver. A pre-release sorts before the plain version it precedes, so
// v1.2.0-rc1 is older than v1.2.0.
func Compare(a, b string) int {
	aNums, aPre := splitVersion(a)
	bNums, bPre := splitVersion(b)

	for i := 0; i < len(aNums) || i < len(bNums); i++ {
		if diff := numAt(aNums, i) - numAt(bNums, i); diff != 0 {
			return diff
		}
	}
	switch {
	case aPre == bPre:
		return 0
	case aPre == "": // a is a full release, b is a pre-release of it
		return 1
	case bPre == "":
		return -1
	default:
		return strings.Compare(aPre, bPre)
	}
}

func splitVersion(v string) ([]int, string) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")

	pre := ""
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		pre, v = v[i+1:], v[:i]
	}

	var nums []int
	for _, part := range strings.Split(v, ".") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			break // stop at the first non-numeric component
		}
		nums = append(nums, n)
	}
	return nums, pre
}

func numAt(nums []int, i int) int {
	if i < len(nums) {
		return nums[i]
	}
	return 0
}
