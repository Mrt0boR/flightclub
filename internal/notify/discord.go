package notify

// Discord delivers events to a Discord channel via an incoming webhook.
//
// A webhook posts into one channel of a server (guild) — it cannot DM a
// user, because Discord's webhook API is scoped to channels by design. The
// usual way to get a private, DM-like experience is a server with just
// yourself in it (or a channel only you can see), with mobile notifications
// on for that channel. A true bot-DM is possible but needs an account-level
// bot token and a shared server before Discord will allow it; a webhook URL
// is scoped to exactly one channel and can't do anything else, which is why
// this is the notifier built here.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Discord posts events to one Discord channel via its webhook URL.
type Discord struct {
	URL    string
	client *http.Client
}

// discordPayload is what Discord's webhook execute endpoint accepts. Using an
// embed rather than plain content gives a title, a coloured accent and a
// timestamp instead of one flat block of text.
type discordPayload struct {
	Embeds []discordEmbed `json:"embeds"`
}

type discordEmbed struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Color       int    `json:"color"`
	Timestamp   string `json:"timestamp"`
}

// colorFor maps an event kind to a Discord embed colour (decimal RGB).
// Unknown kinds fall through to grey rather than erroring, so a future event
// kind still gets delivered, just uncoloured.
var colorFor = map[string]int{
	"takeoff":     0x3498db, // blue
	"landing":     0x2ecc71, // green
	"arriving":    0xf1c40f, // gold
	"signal_lost": 0xe74c3c, // red
}

const defaultColor = 0x95a5a6 // grey

// NewDiscord validates a webhook URL before accepting it. Plain HTTP is
// refused except to loopback, same rule as every other HTTP notifier, and the
// host is checked against Discord's own domains so a pasted invite link or
// channel URL fails immediately rather than as a confusing runtime error.
func NewDiscord(rawURL string) (*Discord, error) {
	u, err := secureURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("discord webhook URL is not valid: %w", err)
	}
	host := strings.ToLower(u.Hostname())
	if host != "discord.com" && host != "discordapp.com" && !strings.HasSuffix(host, ".discord.com") {
		return nil, fmt.Errorf("%q does not look like a Discord webhook URL (expected discord.com)", u.Hostname())
	}
	if !strings.Contains(u.Path, "/webhooks/") {
		return nil, fmt.Errorf("URL does not look like a webhook: expected a /webhooks/ path, e.g. https://discord.com/api/webhooks/123.../abcXYZ")
	}
	return &Discord{
		URL:    u.String(),
		client: &http.Client{Timeout: 20 * time.Second},
	}, nil
}

func (*Discord) Name() string { return "discord" }

func (d *Discord) Notify(e Event) error {
	color, ok := colorFor[e.Kind]
	if !ok {
		color = defaultColor
	}
	payload := discordPayload{Embeds: []discordEmbed{{
		Title:       sanitize(e.Title),
		Description: sanitize(e.Body),
		Color:       color,
		Timestamp:   e.Time.UTC().Format(time.RFC3339),
	}}}

	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, d.URL, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("discord returned %s", resp.Status)
	}
	return nil
}
