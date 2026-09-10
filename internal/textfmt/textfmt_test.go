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
	if got := Trunc("abcdefgh", 4); len(got) != 4 {
		t.Errorf("trunc to 4 gave %q", got)
	}
	if got := Trunc("abc", 10); got != "abc" {
		t.Errorf("short strings should pass through, got %q", got)
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
