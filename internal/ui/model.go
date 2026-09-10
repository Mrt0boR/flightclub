package ui

// The dashboard's state, the messages that drive it, and the commands that
// produce those messages. Behaviour lives in update.go and the screen_*.go
// files; this is what they all operate on.

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"flighttrack/internal/airports"
	"flighttrack/internal/eta"
	"flighttrack/internal/history"
	"flighttrack/internal/notify"
	"flighttrack/internal/opensky"
	"flighttrack/internal/version"
)

type screen int

const (
	screenHistory screen = iota
	screenFlight
	screenDest
	screenDash
)

const (
	// How long the dashboard waits before pulling a fresh snapshot.
	defaultRefresh = 5 * time.Minute
	// How close to arrival the "arriving soon" alert fires.
	arrivalAlertAt = 15 * time.Minute
	maxLogLines    = 8
	// Repeats of the same action key faster than this are taken as key-repeat
	// from a held key, not separate presses.
	keyDebounce = 350 * time.Millisecond
)

type logEntry struct {
	at   time.Time
	text string
	tone lipgloss.Style
}

type model struct {
	client *opensky.Client

	screen  screen
	loading bool
	errMsg  string

	flightInput textinput.Model
	destInput   textinput.Model
	spin        spinner.Model

	flight      opensky.FlightID
	flightTyped string // what the user actually entered, for display
	origin      airports.Airport
	dest        airports.Airport
	suggestions []airports.Airport
	suggestIdx  int
	pickOrigin  bool // the airport picker is choosing an origin, not a destination

	// Distance when tracking began, so a bar can still be shown for a flight
	// with no origin set. Reset whenever the flight or destination changes.
	trackStartNM float64

	obs         *opensky.Observation
	snapTaken   time.Time
	snapFetched time.Time
	est         eta.Estimate
	now         time.Time

	menuIdx     int
	notifyOn    bool
	autoRefresh bool
	refreshIn   time.Duration
	nextRefresh time.Time

	// Terminals cannot tell a held key from repeated taps, so an action key
	// that repeats within keyDebounce of itself is treated as one press. This
	// stops a held `r` from spending API credits on every repeat, and a held
	// `n` from flickering a toggle.
	lastActionKey string
	lastActionAt  time.Time

	// dev mode seeds a fake flight and enables ctrl+t to simulate a landing,
	// so notifications can be exercised without waiting for a real one.
	dev bool

	toast          notify.Toast
	webhook        *notify.Webhook
	prevOnGround   *bool
	arrivalAlerted bool
	logs           []logEntry

	hist        *history.File
	histPath    string
	histIdx     int
	fromCache   bool      // the shown position came from disk, not the API
	cacheAge    time.Time // when the cached position was originally fetched
	histWarning string

	// Set only if a newer release exists. Shown as one line; never acted on.
	update    version.Release
	hasUpdate bool

	width, height int
}

// ----------------------------------------------------------------- messages

type snapshotMsg struct {
	snap *opensky.Snapshot
	err  error
}

type tickMsg time.Time

type notifyDoneMsg struct{ err error }

// updateMsg carries the result of the release check. Absent or failed checks
// simply never produce one worth showing.
type updateMsg struct {
	release   version.Release
	available bool
}

// ----------------------------------------------------------------- commands

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// A tea.Cmd is itself a function: `type Cmd func() Msg`. Returning one hands
// Bubble Tea a parcel of work to run on its own goroutine; whatever it returns
// arrives back at Update as a message. That is why the two functions below
// return a function rather than doing the work themselves — an API call or a
// desktop balloon would otherwise block the interface while it ran.
//
// Each one is a one-line wrapper around a plain function, so the actual work
// stays readable and can be called directly from a test.

func (m model) fetch() tea.Cmd {
	client := m.client
	return func() tea.Msg { return fetchSnapshot(client) }
}

// fetchSnapshot pulls one snapshot from the API and wraps the result, error
// included, as a message.
func fetchSnapshot(client *opensky.Client) tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	snap, err := client.Fetch(ctx)
	return snapshotMsg{snap: snap, err: err}
}

// send delivers a notification off the UI goroutine, since a desktop balloon
// blocks for several seconds. It returns nil when notifications are off, and
// a nil command is Bubble Tea's way of saying there is nothing to do.
func (m model) send(e notify.Event) tea.Cmd {
	if !m.notifyOn {
		return nil
	}
	set := notify.NewSet(m.toast)
	if m.webhook != nil {
		set.Add(m.webhook)
	}
	return func() tea.Msg { return deliver(set, e) }
}

// deliver sends one event to every configured notifier and reports the first
// failure, if there was one.
func deliver(set *notify.Set, e notify.Event) tea.Msg {
	errs := set.Notify(e)
	if len(errs) > 0 {
		return notifyDoneMsg{err: errs[0]}
	}
	return notifyDoneMsg{}
}

// checkUpdate asks GitHub whether a newer release exists. It runs off the UI
// goroutine like any other command, is capped to one network call a day by
// the version package, and stays silent on every failure.
func checkUpdate() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		path, err := version.DefaultCachePath()
		if err != nil {
			return updateMsg{}
		}
		release, available := version.Check(ctx, path)
		return updateMsg{release: release, available: available}
	}
}

// Init is one of the three methods Bubble Tea requires on a model, alongside
// Update (in update.go) and View (in view.go). It runs once at startup and
// returns the work that should begin immediately: the cursor blinking, the
// spinner animating, the once-a-second clock tick that drives the countdown,
// and a one-shot check for a newer release. tea.Batch starts them together.
func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, m.spin.Tick, tick(), checkUpdate()}
	if m.loading {
		// A flight was named on the command line, so go and find it now
		// rather than waiting for the first tick.
		cmds = append(cmds, m.fetch())
	}
	return tea.Batch(cmds...)
}

// ------------------------------------------------------------ state helpers

func (m *model) logf(tone lipgloss.Style, format string, args ...any) {
	m.logs = append(m.logs, logEntry{at: time.Now(), text: fmt.Sprintf(format, args...), tone: tone})
	if len(m.logs) > maxLogLines {
		m.logs = m.logs[len(m.logs)-maxLogLines:]
	}
}

// heldKey reports whether this action key has repeated within keyDebounce of
// its last press — i.e. the user is leaning on it rather than tapping. It
// records the press as a side effect, so call it once per keystroke.
func (m *model) heldKey(key string) bool {
	now := time.Now()
	held := key == m.lastActionKey && now.Sub(m.lastActionAt) < keyDebounce
	m.lastActionKey, m.lastActionAt = key, now
	return held
}

// recompute refreshes the estimate from the current observation, and remembers
// the distance at the first fix so a bar can still be drawn for a flight whose
// origin was never supplied.
func (m *model) recompute() {
	if m.obs == nil || m.dest.IATA == "" {
		return
	}
	m.est = eta.Compute(m.obs, m.origin, m.dest, m.snapFetched)
	if m.trackStartNM == 0 && m.est.DistanceNM > 0 {
		m.trackStartNM = m.est.DistanceNM
	}
}

// persist writes the current search, and whatever position is on screen, back
// to the history file. Called when the dashboard opens and again on exit.
func (m model) persist() {
	if m.histPath == "" || m.hist == nil || m.flight.IsZero() {
		return
	}
	e := history.Entry{
		Flight:   m.flightTyped,
		FlightID: m.flight.String(),
		Origin:   m.origin.IATA,
		Dest:     m.dest.IATA,
		LastUsed: time.Now(),
	}
	if e.Flight == "" {
		e.Flight = m.flight.String()
	}
	// Only store a position that actually came from the API this run; writing
	// a cached one back would keep resetting its age.
	if m.obs != nil && !m.fromCache && !m.snapFetched.IsZero() {
		e.SetObservation(m.obs, m.snapFetched)
	} else if m.obs != nil && m.fromCache {
		e.SetObservation(m.obs, m.cacheAge)
	}
	m.hist.Record(e)
	if err := m.hist.Save(m.histPath); err != nil {
		// Nothing to show at exit; the dashboard reports it while running.
		return
	}
}
