// Package notify delivers flight events to the user.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Event is one thing worth telling the user about.
type Event struct {
	Kind      string    `json:"kind"` // takeoff | landing | signal_lost | arriving | tracking
	Flight    string    `json:"flight"`
	Callsign  string    `json:"callsign,omitempty"`
	Icao24    string    `json:"icao24,omitempty"`
	Time      time.Time `json:"time"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Lat       float64   `json:"lat,omitempty"`
	Lon       float64   `json:"lon,omitempty"`
	AltitudeM float64   `json:"altitude_m,omitempty"`
	SpeedKts  float64   `json:"speed_kts,omitempty"`
}

// Notifier delivers an event somewhere.
type Notifier interface {
	Notify(Event) error
	Name() string
}

// ------------------------------------------------------------------- toast

// Toast raises a Windows notification-area balloon.
type Toast struct{}

// The text is passed through the environment rather than interpolated into the
// script, so nothing the API returned is ever parsed as PowerShell.
const toastScript = `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$n = New-Object System.Windows.Forms.NotifyIcon
$n.Icon = [System.Drawing.SystemIcons]::Information
$n.BalloonTipTitle = $env:FT_TITLE
$n.BalloonTipText = $env:FT_BODY
$n.Visible = $true
$n.ShowBalloonTip(15000)
Start-Sleep -Seconds 8
$n.Dispose()
`

func (Toast) Name() string { return "desktop" }

// Available reports whether desktop notifications can work here.
func (Toast) Available() bool { return runtime.GOOS == "windows" }

func (Toast) Notify(e Event) error {
	if runtime.GOOS != "windows" {
		return errors.New("desktop notifications are Windows-only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", toastScript)
	// A minimal environment: the script needs nothing else, and this keeps any
	// unrelated secrets in the parent environment out of the child process.
	cmd.Env = []string{
		"FT_TITLE=" + sanitize(e.Title),
		"FT_BODY=" + sanitize(e.Body),
		"SystemRoot=" + systemRoot(),
		"PATH=" + systemRoot() + `\System32;` + systemRoot() + `\System32\WindowsPowerShell\v1.0`,
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("desktop notification failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func systemRoot() string {
	if r := os.Getenv("SystemRoot"); r != "" {
		return r
	}
	return `C:\Windows`
}

// sanitize trims control characters and caps length. Callsigns come off a
// public feed, so nothing from them reaches a UI or a subprocess unfiltered.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' {
			b.WriteRune(' ')
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		if b.Len() > 900 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// ----------------------------------------------------------------- webhook

// Webhook POSTs events as JSON.
type Webhook struct {
	URL    string
	client *http.Client
}

// NewWebhook validates the URL before accepting it. Plain HTTP is refused
// except to loopback, so an event never crosses a network in the clear.
func NewWebhook(raw string) (*Webhook, error) {
	u, err := secureURL(raw)
	if err != nil {
		return nil, fmt.Errorf("webhook URL is not valid: %w", err)
	}
	return &Webhook{
		URL:    u.String(),
		client: &http.Client{Timeout: 20 * time.Second},
	}, nil
}

// secureURL parses raw and enforces the transport rule shared by every HTTP
// notifier: https everywhere, plain http only to loopback, and a host must be
// present. Both NewWebhook and NewNtfy rely on it so the rule stays in one place.
func secureURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "https":
	case "http":
		host := u.Hostname()
		if ip := net.ParseIP(host); (ip == nil || !ip.IsLoopback()) && host != "localhost" {
			return nil, errors.New("refusing plain http to a non-local address: use https")
		}
	default:
		return nil, fmt.Errorf("URL must be https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("URL has no host")
	}
	return u, nil
}

func (*Webhook) Name() string { return "webhook" }

func (w *Webhook) Notify(e Event) error {
	buf, err := json.Marshal(e)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, w.URL, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
	return nil
}

// ------------------------------------------------------------------ console

// Console prints events to stdout. Useful for the headless watcher; the TUI
// uses its own in-app log instead, since writing to stdout would corrupt the
// screen.
type Console struct{}

func (Console) Name() string { return "console" }

func (Console) Notify(e Event) error {
	fmt.Printf("\n=== %s ===\n%s\n%s\n\n", strings.ToUpper(e.Kind), e.Title, e.Body)
	return nil
}

// -------------------------------------------------------------------- set

// Set fans an event out to several notifiers, collecting failures rather than
// letting one broken sink stop the others.
type Set struct {
	mu        sync.Mutex
	notifiers []Notifier
}

// NewSet builds a set.
func NewSet(n ...Notifier) *Set { return &Set{notifiers: n} }

// Add appends a notifier.
func (s *Set) Add(n Notifier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifiers = append(s.notifiers, n)
}

// Names lists the active notifiers.
func (s *Set) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.notifiers))
	for _, n := range s.notifiers {
		out = append(out, n.Name())
	}
	return out
}

// Notify delivers to every notifier and returns whatever went wrong.
func (s *Set) Notify(e Event) []error {
	s.mu.Lock()
	list := append([]Notifier(nil), s.notifiers...)
	s.mu.Unlock()

	var errs []error
	for _, n := range list {
		if err := n.Notify(e); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", n.Name(), err))
		}
	}
	return errs
}
