package textfmt

import (
	"strings"
	"testing"
	"time"
)

func TestWrap(t *testing.T) {
	got := Wrap("the quick brown fox jumps", 10)
	for _, line := range strings.Split(got, "\n") {
		if len(line) > 10 {
			t.Errorf("line %q exceeds width 10", line)
		}
	}
	if Wrap("", 10) != "" {
		t.Error("wrapping empty text should give empty text")
	}
}

func TestTrunc(t *testing.T) {
	// Short enough: passes straight through.
	if got := Trunc("abc", 10); got != "abc" {
		t.Errorf("short strings should pass through, got %q", got)
	}
	// The bug this fixes: cutting mid-word.
	if got := Trunc("RYR3AC arriving in about 8m", 23); got != "RYR3AC arriving in…" {
		t.Errorf("Trunc should cut on a word boundary, got %q", got)
	}
	// A single long word has nowhere good to cut, so it just gets shortened.
	if got := Trunc("supercalifragilistic", 8); got != "superca…" {
		t.Errorf("long single word: got %q", got)
	}
	// Never ends on a bare space before the ellipsis.
	if got := Trunc("one two three", 8); strings.HasSuffix(got, " …") {
		t.Errorf("trailing space before ellipsis: %q", got)
	}
	if Trunc("anything", 0) != "" {
		t.Error("a zero limit should give empty text")
	}
}

func TestShortAge(t *testing.T) {
	cases := []struct {
		age  time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{90 * time.Minute, "1h30m"},
		{50 * time.Hour, "2d"},
	}
	for _, c := range cases {
		if got := ShortAge(c.age); got != c.want {
			t.Errorf("ShortAge(%v) = %q, want %q", c.age, got, c.want)
		}
	}
}
