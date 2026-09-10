package notify

// Ntfy delivers events to an ntfy topic (ntfy.sh or a self-hosted server) as a
// proper push notification with a title, priority and tag.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Ntfy publishes events to an ntfy topic.
type Ntfy struct {
	Base   string // e.g. "https://ntfy.sh" — scheme+host, no path
	Topic  string // e.g. "flighttrack-7Xq2..."
	token  string // optional access token, sent as an Authorization: Bearer header
	client *http.Client
}

// ntfyMessage is the JSON document ntfy parses when POSTed to the server root.
type ntfyMessage struct {
	Topic    string   `json:"topic"`
	Title    string   `json:"title"`
	Message  string   `json:"message"`
	Priority int      `json:"priority"`
	Tags     []string `json:"tags,omitempty"`
}

// ntfyMapping is the priority and tag for an event kind.
type ntfyMapping struct {
	priority int
	tag      string
}

// kindMap maps Event.Kind to an ntfy priority (1 min .. 5 max) and an emoji
// tag. Unknown kinds fall through to defaultMapping rather than erroring, so a
// future event kind is still delivered.
var kindMap = map[string]ntfyMapping{
	"takeoff":     {3, "airplane_departure"},
	"landing":     {3, "airplane_arriving"},
	"arriving":    {4, "airplane_arriving"},
	"signal_lost": {4, "warning"},
	"tracking":    {2, "eyes"},
}

var defaultMapping = ntfyMapping{priority: 3, tag: "bell"}

func mappingFor(kind string) ntfyMapping {
	if m, ok := kindMap[kind]; ok {
		return m
	}
	return defaultMapping
}

// NewNtfy splits a topic URL such as https://ntfy.sh/mytopic into a server base
// and a topic, applying the same transport rules as NewWebhook. The token, if
// any, must come from the FLIGHTTRACK_NTFY_TOKEN environment variable — never a
// flag, since flags land in the process table and shell history.
func NewNtfy(rawURL, token string) (*Ntfy, error) {
	u, err := secureURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("ntfy URL is not valid: %w", err)
	}
	topic := strings.Trim(u.Path, "/")
	if topic == "" || strings.Contains(topic, "/") {
		return nil, fmt.Errorf("ntfy URL must be a single topic path, e.g. https://ntfy.sh/my-topic")
	}
	base := *u
	base.Path = ""
	base.RawQuery = ""
	base.Fragment = ""
	return &Ntfy{
		Base:   strings.TrimRight(base.String(), "/"),
		Topic:  topic,
		token:  strings.TrimSpace(token),
		client: &http.Client{Timeout: 20 * time.Second},
	}, nil
}

func (*Ntfy) Name() string { return "ntfy" }

func (n *Ntfy) Notify(e Event) error {
	m := mappingFor(e.Kind)
	msg := ntfyMessage{
		Topic:    n.Topic,
		Title:    sanitize(e.Title),
		Message:  sanitize(e.Body),
		Priority: m.priority,
	}
	if m.tag != "" {
		msg.Tags = []string{m.tag}
	}
	buf, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	// POST the JSON document to the server root, not the topic URL: ntfy only
	// parses JSON at the root, and header-based titles (X-Title) are ASCII-only
	// whereas a callsign-derived title may not be.
	req, err := http.NewRequest(http.MethodPost, n.Base+"/", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if n.token != "" {
		req.Header.Set("Authorization", "Bearer "+n.token)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy returned %s", resp.Status)
	}
	return nil
}
