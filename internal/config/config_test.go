package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("a first run should not error, got %v", err)
	}
	if c.DiscordWebhookURL != "" || c.Theme != "" {
		t.Errorf("expected an empty config, got %+v", c)
	}
}

func TestLoadCorruptFileStartsFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err == nil {
		t.Error("a corrupt file should report a problem")
	}
	if c == nil {
		t.Fatal("a corrupt file should still yield a usable empty config")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	c := &Config{DiscordWebhookURL: "https://discord.com/api/webhooks/1/abc", Theme: "high-contrast"}
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.DiscordWebhookURL != c.DiscordWebhookURL || got.Theme != c.Theme {
		t.Errorf("round trip: got %+v, want %+v", got, c)
	}
}

// The file can hold a Discord webhook URL, which is a bearer credential for
// that channel, so it should not be world-readable. Windows ignores POSIX
// modes entirely; there the file is protected by the ACL on the containing
// per-user directory instead.
func TestSaveUsesRestrictivePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes are not enforced on Windows; the directory ACL applies instead")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	c := &Config{DiscordWebhookURL: "https://discord.com/api/webhooks/1/abc"}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("config file mode is %o, want no group or other access", mode)
	}
}

func TestSaveOverwritesPreviousValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	first := &Config{DiscordWebhookURL: "https://discord.com/api/webhooks/1/aaa"}
	if err := first.Save(path); err != nil {
		t.Fatal(err)
	}
	second := &Config{DiscordWebhookURL: "https://discord.com/api/webhooks/2/bbb"}
	if err := second.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.DiscordWebhookURL != second.DiscordWebhookURL {
		t.Errorf("got %q, want the second save to win: %q", got.DiscordWebhookURL, second.DiscordWebhookURL)
	}
}
