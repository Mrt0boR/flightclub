package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// An event must never leave the machine in the clear, so plain http is only
// allowed to loopback.
func TestNewWebhookRejectsInsecureURLs(t *testing.T) {
	bad := []string{
		"http://example.com/hook",  // cleartext to the internet
		"ftp://example.com/hook",   // wrong scheme
		"file:///c:/windows/hosts", // local file
		"javascript:alert(1)",      // not a transport at all
		"https://",                 // no host
		"",                         //
		"not a url at all",         //
	}
	for _, u := range bad {
		if w, err := NewWebhook(u); err == nil {
			t.Errorf("NewWebhook(%q) was accepted as %q, want rejection", u, w.URL)
		}
	}
}

func TestNewWebhookAcceptsHTTPSAndLoopback(t *testing.T) {
	good := []string{
		"https://example.com/hook",
		"https://hooks.slack.com/services/x/y/z",
		"http://localhost:8080/hook",
		"http://127.0.0.1:9000/hook",
	}
	for _, u := range good {
		if _, err := NewWebhook(u); err != nil {
			t.Errorf("NewWebhook(%q) rejected: %v", u, err)
		}
	}
}

func TestWebhookPostsJSON(t *testing.T) {
	var got Event
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// httptest serves on 127.0.0.1, which the loopback exception allows.
	hook, err := NewWebhook(srv.URL)
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	want := Event{Kind: "takeoff", Flight: "BAW117", Title: "BAW117 has taken off", Time: time.Now()}
	if err := hook.Notify(want); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
	if got.Kind != want.Kind || got.Flight != want.Flight {
		t.Errorf("server received %+v, want kind=%q flight=%q", got, want.Kind, want.Flight)
	}
}

func TestWebhookReportsServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	hook, err := NewWebhook(srv.URL)
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	if err := hook.Notify(Event{Kind: "takeoff"}); err == nil {
		t.Error("a 500 response should surface as an error")
	}
}

// Callsigns come off a public feed and end up in a subprocess environment, so
// control characters are stripped before they get there.
func TestSanitizeStripsControlCharacters(t *testing.T) {
	in := "BAW117\x00 has\r\n taken\x1b[31m off"
	got := sanitize(in)
	for _, r := range got {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("sanitize left control character %q in %q", r, got)
		}
	}
	if !strings.Contains(got, "BAW117") {
		t.Errorf("sanitize(%q) = %q, lost the useful text", in, got)
	}
}

func TestSanitizeCapsLength(t *testing.T) {
	if got := sanitize(strings.Repeat("a", 5000)); len(got) > 1000 {
		t.Errorf("sanitize returned %d bytes, want it capped near 900", len(got))
	}
}

// One broken sink must not stop the others.
type failing struct{}

func (failing) Name() string       { return "failing" }
func (failing) Notify(Event) error { return http.ErrNotSupported }

type counting struct{ n *int }

func (counting) Name() string         { return "counting" }
func (c counting) Notify(Event) error { *c.n++; return nil }

func TestSetContinuesPastFailures(t *testing.T) {
	var n int
	s := NewSet(failing{}, counting{&n}, failing{})
	errs := s.Notify(Event{Kind: "takeoff"})
	if n != 1 {
		t.Errorf("working notifier ran %d times, want 1", n)
	}
	if len(errs) != 2 {
		t.Errorf("got %d errors, want 2", len(errs))
	}
	if names := s.Names(); len(names) != 3 {
		t.Errorf("Names() = %v, want 3 entries", names)
	}
}
