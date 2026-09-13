package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Same transport rule as every other HTTP notifier: https everywhere, plain
// http only to loopback.
func TestNewDiscordRejectsInsecureURLs(t *testing.T) {
	bad := []string{
		"http://discord.com/api/webhooks/1/abc", // cleartext to the internet
		"ftp://discord.com/api/webhooks/1/abc",  // wrong scheme
		"",
		"not a url at all",
	}
	for _, u := range bad {
		if d, err := NewDiscord(u); err == nil {
			t.Errorf("NewDiscord(%q) was accepted as %q, want rejection", u, d.URL)
		}
	}
}

// A webhook posts into one specific channel, so the URL has to actually be a
// Discord webhook — not a server invite, not some other host entirely. Both
// are easy paste mistakes and should fail immediately, not as a confusing
// runtime error the first time a flight lands.
func TestNewDiscordRejectsNonWebhookURLs(t *testing.T) {
	bad := []string{
		"https://discord.gg/abcd1234",                 // an invite link
		"https://example.com/api/webhooks/1/abc",      // right shape, wrong host
		"https://discord.com/channels/111/222",        // a channel link, not a webhook
		"https://evil-discord.com/api/webhooks/1/abc", // host confusion
	}
	for _, u := range bad {
		if d, err := NewDiscord(u); err == nil {
			t.Errorf("NewDiscord(%q) was accepted as %q, want rejection", u, d.URL)
		}
	}
}

func TestNewDiscordAcceptsRealWebhookURLs(t *testing.T) {
	good := []string{
		"https://discord.com/api/webhooks/123456789012345678/aBcDeF-ghIJKl_mnOPqr",
		"https://discordapp.com/api/webhooks/123456789012345678/aBcDeF-ghIJKl_mnOPqr",
		"https://canary.discord.com/api/webhooks/123456789012345678/aBcDeF-ghIJKl_mnOPqr",
	}
	for _, u := range good {
		if _, err := NewDiscord(u); err != nil {
			t.Errorf("NewDiscord(%q) rejected: %v", u, err)
		}
	}
}

func TestDiscordPostsAnEmbed(t *testing.T) {
	var gotBody []byte
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotBody = mustReadAll(t, r)
		w.WriteHeader(http.StatusNoContent) // Discord's real success response
	}))
	defer srv.Close()

	d := &Discord{URL: srv.URL, client: srv.Client()}
	at := time.Date(2026, 9, 13, 10, 30, 0, 0, time.UTC)
	err := d.Notify(Event{
		Kind: "landing", Flight: "BAW117", Title: "BAW117 has landed",
		Body: "On the ground at Dublin.", Time: at,
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}

	var payload discordPayload
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("server received unparseable JSON: %v\n%s", err, gotBody)
	}
	if len(payload.Embeds) != 1 {
		t.Fatalf("got %d embeds, want 1", len(payload.Embeds))
	}
	embed := payload.Embeds[0]
	if embed.Title != "BAW117 has landed" || embed.Description != "On the ground at Dublin." {
		t.Errorf("embed content = %+v", embed)
	}
	if embed.Color != colorFor["landing"] {
		t.Errorf("landing color = %#x, want %#x", embed.Color, colorFor["landing"])
	}
	if embed.Timestamp != "2026-09-13T10:30:00Z" {
		t.Errorf("timestamp = %q, want RFC3339 UTC", embed.Timestamp)
	}
}

func TestDiscordColorPerKind(t *testing.T) {
	cases := []struct {
		kind string
		want int
	}{
		{"takeoff", 0x3498db},
		{"landing", 0x2ecc71},
		{"arriving", 0xf1c40f},
		{"signal_lost", 0xe74c3c},
		{"tracking", defaultColor},      // not in the table
		{"something-new", defaultColor}, // an event kind that does not exist yet
	}
	for _, c := range cases {
		var got discordPayload
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.Unmarshal(mustReadAll(t, r), &got)
			w.WriteHeader(http.StatusNoContent)
		}))
		d := &Discord{URL: srv.URL, client: srv.Client()}
		if err := d.Notify(Event{Kind: c.kind}); err != nil {
			t.Fatalf("Notify: %v", err)
		}
		srv.Close()
		if got.Embeds[0].Color != c.want {
			t.Errorf("kind %q -> color %#x, want %#x", c.kind, got.Embeds[0].Color, c.want)
		}
	}
}

func TestDiscordReportsServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	d := &Discord{URL: srv.URL, client: srv.Client()}
	if err := d.Notify(Event{Kind: "takeoff"}); err == nil {
		t.Error("a 429 response should surface as an error")
	}
}

// Callsigns come off a public feed; nothing unsanitized should reach Discord.
func TestDiscordSanitizesTitleAndBody(t *testing.T) {
	var got discordPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.Unmarshal(mustReadAll(t, r), &got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	d := &Discord{URL: srv.URL, client: srv.Client()}
	err := d.Notify(Event{
		Kind: "landing", Title: "BAW117\x00 has\r\n landed", Body: "ok\x1b[31m",
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	for _, r := range got.Embeds[0].Title + got.Embeds[0].Description {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("unsanitized control character reached Discord: %+v", got.Embeds[0])
		}
	}
}

func mustReadAll(t *testing.T, r *http.Request) []byte {
	t.Helper()
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("reading request body: %v", err)
	}
	return buf
}

// The shared secureURL helper must keep behaving identically for Webhook
// after Discord started using it too — this is the check that the extraction
// in notify.go was faithful.
func TestSharedURLValidationStillServesWebhook(t *testing.T) {
	if _, err := NewWebhook("https://example.com/hook"); err != nil {
		t.Errorf("a plain https webhook should still be accepted: %v", err)
	}
	if _, err := NewWebhook("http://example.com/hook"); err == nil {
		t.Error("plain http to a non-local address should still be rejected")
	}
}
