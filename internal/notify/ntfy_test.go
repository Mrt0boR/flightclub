package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// NewNtfy must reject exactly what NewWebhook rejects: that is the proof the
// extracted secureURL helper is shared, not duplicated.
func TestNewNtfyRejectsInsecureURLs(t *testing.T) {
	bad := []string{
		"http://example.com/topic",  // cleartext to the internet
		"ftp://example.com/topic",   // wrong scheme
		"file:///c:/windows/hosts",  // local file
		"javascript:alert(1)",       // not a transport
		"https://",                  // no host
		"",                          //
		"not a url at all",          //
		"https://ntfy.sh",           // no topic
		"https://ntfy.sh/",          // no topic
		"https://ntfy.sh/a/b/topic", // not a single topic segment
	}
	for _, u := range bad {
		if n, err := NewNtfy(u, ""); err == nil {
			t.Errorf("NewNtfy(%q) accepted as base=%q topic=%q, want rejection", u, n.Base, n.Topic)
		}
	}
}

func TestNewNtfySplitsTopicURL(t *testing.T) {
	n, err := NewNtfy("https://ntfy.sh/my-topic", "")
	if err != nil {
		t.Fatalf("NewNtfy: %v", err)
	}
	if n.Base != "https://ntfy.sh" {
		t.Errorf("Base = %q, want https://ntfy.sh", n.Base)
	}
	if n.Topic != "my-topic" {
		t.Errorf("Topic = %q, want my-topic", n.Topic)
	}

	// A self-hosted server on loopback is allowed over plain http.
	n, err = NewNtfy("http://localhost:8080/alerts", "")
	if err != nil {
		t.Fatalf("NewNtfy loopback: %v", err)
	}
	if n.Base != "http://localhost:8080" || n.Topic != "alerts" {
		t.Errorf("got Base=%q Topic=%q, want http://localhost:8080 / alerts", n.Base, n.Topic)
	}
}

func TestNtfyPostsJSONToRoot(t *testing.T) {
	var path, contentType, auth string
	var got ntfyMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		contentType = r.Header.Get("Content-Type")
		auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n, err := NewNtfy(srv.URL+"/flighttrack-abc", "tk_secret")
	if err != nil {
		t.Fatalf("NewNtfy: %v", err)
	}
	e := Event{Kind: "takeoff", Flight: "BAW117", Title: "BAW117 has taken off", Body: "Climbing out of LHR", Time: time.Now()}
	if err := n.Notify(e); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if path != "/" {
		t.Errorf("posted to %q, want /", path)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
	if auth != "Bearer tk_secret" {
		t.Errorf("Authorization = %q, want Bearer tk_secret", auth)
	}
	if got.Topic != "flighttrack-abc" {
		t.Errorf("topic = %q, want flighttrack-abc", got.Topic)
	}
	if got.Title != e.Title || got.Message != e.Body {
		t.Errorf("got title=%q message=%q, want %q / %q", got.Title, got.Message, e.Title, e.Body)
	}
	if got.Priority != 3 || len(got.Tags) != 1 || got.Tags[0] != "airplane_departure" {
		t.Errorf("takeoff mapped to priority=%d tags=%v, want 3 / [airplane_departure]", got.Priority, got.Tags)
	}
}

func TestNtfyMapsKindToPriority(t *testing.T) {
	cases := map[string]int{
		"takeoff":          3,
		"landing":          3,
		"arriving":         4,
		"signal_lost":      4,
		"tracking":         2,
		"some_future_kind": 3, // unknown defaults to 3, does not error
	}
	for kind, want := range cases {
		if got := mappingFor(kind).priority; got != want {
			t.Errorf("mappingFor(%q).priority = %d, want %d", kind, got, want)
		}
	}
}

func TestNtfyReportsServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	n, err := NewNtfy(srv.URL+"/topic", "")
	if err != nil {
		t.Fatalf("NewNtfy: %v", err)
	}
	if err := n.Notify(Event{Kind: "takeoff"}); err == nil {
		t.Error("a 500 response should surface as an error")
	}
}

func TestNtfySanitizesControlCharacters(t *testing.T) {
	var got ntfyMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n, err := NewNtfy(srv.URL+"/topic", "")
	if err != nil {
		t.Fatalf("NewNtfy: %v", err)
	}
	if err := n.Notify(Event{Kind: "takeoff", Title: "BAW117\x00\x1b[31m up", Body: "line1\r\nline2"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	for _, r := range got.Title + got.Message {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("control character %q survived into %q / %q", r, got.Title, got.Message)
		}
	}
	if !strings.Contains(got.Title, "BAW117") {
		t.Errorf("sanitize lost the useful title text: %q", got.Title)
	}
}
