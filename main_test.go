package main

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"flighttrack/internal/airports"
	"flighttrack/internal/eta"
	"flighttrack/internal/notify"
	"flighttrack/internal/opensky"
)

func newTestModel(t *testing.T) model {
	t.Helper()
	fi := textinput.New()
	di := textinput.New()
	return model{
		client:      opensky.New("", ""),
		screen:      screenFlight,
		flightInput: fi,
		destInput:   di,
		spin:        spinner.New(),
		now:         time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
		refreshIn:   defaultRefresh,
		width:       120,
		height:      40,
	}
}

// A model mid-flight: airborne over the Atlantic, bound for JFK.
func airborneModel(t *testing.T) model {
	t.Helper()
	m := newTestModel(t)
	dest, ok := airports.Lookup("JFK")
	if !ok {
		t.Fatal("JFK missing from the airport table")
	}
	id, err := opensky.ParseFlight("BA117")
	if err != nil {
		t.Fatal(err)
	}
	m.flight = id
	m.dest = dest
	m.screen = screenDash
	m.obs = &opensky.Observation{
		Icao24: "400a1b", Callsign: "BAW117",
		Lat: 51.4706, Lon: -0.4619, HasPos: true,
		GeoAlt: 11000, Velocity: 500 / 1.94384, Track: 280,
	}
	m.snapFetched = m.now
	m.est = eta.Compute(m.obs, airports.Airport{}, dest, m.snapFetched)
	return m
}

func TestDashboardShowsETAAndCountdown(t *testing.T) {
	m := airborneModel(t)
	if !m.est.Valid {
		t.Fatalf("test setup produced no estimate: %s", m.est.Reason)
	}
	out := m.View()

	for _, want := range []string{"BAW117", "ETA (GMT)", "Countdown", "AIRBORNE", "MENU", "INFO", "JFK"} {
		if !strings.Contains(out, want) {
			t.Errorf("dashboard is missing %q\n---\n%s", want, out)
		}
	}
	// The arrival instant must be shown in GMT.
	if !strings.Contains(out, m.est.ArrivalUTC.Format("15:04:05")) {
		t.Errorf("dashboard does not show the GMT arrival time\n---\n%s", out)
	}
}

// The user asked for flight data on the left and the menu on the right.
func TestDashboardLayoutDataLeftMenuRight(t *testing.T) {
	m := airborneModel(t)
	out := m.View()
	data := strings.Index(out, "BAW117")
	menu := strings.Index(out, "MENU")
	if data < 0 || menu < 0 {
		t.Fatalf("expected both panels to render\n---\n%s", out)
	}
	if data > menu {
		t.Errorf("flight data should render left of the menu, got data at %d and menu at %d", data, menu)
	}
}

// The countdown must come from the system clock, not from a new API call.
func TestCountdownAdvancesWithTheClockAlone(t *testing.T) {
	m := airborneModel(t)
	before := m.est.Countdown(m.now)

	updated, _ := m.Update(tickMsg(m.now.Add(time.Minute)))
	m2 := updated.(model)
	after := m2.est.Countdown(m2.now)

	if diff := before - after; diff != time.Minute {
		t.Errorf("countdown moved by %v after a one-minute tick, want 1m", diff)
	}
	// The estimate itself is untouched: the same arrival instant, no refetch.
	if !m2.est.ArrivalUTC.Equal(m.est.ArrivalUTC) {
		t.Error("a clock tick must not change the arrival estimate")
	}
	if m2.loading {
		t.Error("a clock tick must not start an API fetch while auto-refresh is off")
	}
}

func TestAutoRefreshFiresOnlyWhenDue(t *testing.T) {
	m := airborneModel(t)
	m.autoRefresh = true
	m.nextRefresh = m.now.Add(5 * time.Minute)

	updated, _ := m.Update(tickMsg(m.now.Add(time.Minute)))
	if updated.(model).loading {
		t.Error("auto-refresh fired early")
	}

	updated, _ = m.Update(tickMsg(m.now.Add(6 * time.Minute)))
	m3 := updated.(model)
	if !m3.loading {
		t.Error("auto-refresh did not fire once due")
	}
	if !m3.nextRefresh.After(m.now.Add(6 * time.Minute)) {
		t.Error("the next refresh should be rescheduled after firing")
	}
}

func TestMenuTogglesAutoRefresh(t *testing.T) {
	m := airborneModel(t)
	m.autoRefresh = false

	updated, _ := m.activate(menuAuto)
	if !updated.(model).autoRefresh {
		t.Error("menu did not switch auto-refresh on")
	}
	m.autoRefresh = true
	updated, _ = m.activate(menuAuto)
	if updated.(model).autoRefresh {
		t.Error("menu did not switch auto-refresh off")
	}
}

func TestKeyboardShortcutsReachTheMenu(t *testing.T) {
	m := airborneModel(t)
	m.autoRefresh = false

	updated, _ := m.onDashKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if !updated.(model).autoRefresh {
		t.Error("the 'a' shortcut did not toggle auto-refresh")
	}

	updated, _ = m.onDashKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if updated.(model).screen != screenDest {
		t.Error("the 'd' shortcut did not open the destination screen")
	}

	updated, _ = m.onDashKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if updated.(model).screen != screenFlight {
		t.Error("the 'f' shortcut did not open the flight screen")
	}
}

func TestBadFlightNumberShowsAnError(t *testing.T) {
	m := newTestModel(t)
	m.flightInput.SetValue("not-a-flight")
	updated, cmd := m.onKey(tea.KeyMsg{Type: tea.KeyEnter})
	m2 := updated.(model)
	if m2.errMsg == "" {
		t.Error("expected an error message for an unparseable flight number")
	}
	if m2.loading || cmd != nil {
		t.Error("an unparseable flight number must not spend an API call")
	}
}

func TestDestinationScreenSuggestsAirports(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenDest
	m.destInput.Focus()

	for _, r := range "LHR" {
		updated, _ := m.onKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(model)
	}
	if len(m.suggestions) == 0 || m.suggestions[0].IATA != "LHR" {
		t.Fatalf("typing LHR did not suggest LHR, got %v", m.suggestions)
	}

	updated, _ := m.onKey(tea.KeyMsg{Type: tea.KeyEnter})
	m2 := updated.(model)
	if m2.dest.IATA != "LHR" {
		t.Errorf("enter did not select LHR, got %q", m2.dest.IATA)
	}
	if m2.screen != screenDash {
		t.Error("selecting a destination should open the dashboard")
	}
}

func TestProgressBarShowsRouteWhenOriginIsKnown(t *testing.T) {
	m := airborneModel(t)
	origin, _ := airports.Lookup("LHR")
	m.origin = origin
	m.recompute()

	out := m.View()
	if !strings.Contains(out, "Progress") {
		t.Fatalf("no progress bar rendered\n---\n%s", out)
	}
	if !strings.Contains(out, "LHR") || !strings.Contains(out, "nm flown of") {
		t.Errorf("route bar should name the origin and the distance flown\n---\n%s", out)
	}
	if strings.Contains(out, "since tracking began") {
		t.Error("a known origin should give true route progress, not the fallback")
	}
}

func TestProgressBarFallsBackWithoutAnOrigin(t *testing.T) {
	m := airborneModel(t)
	m.trackStartNM = 3000
	out := m.View()

	if !strings.Contains(out, "Progress") {
		t.Fatalf("no progress bar rendered\n---\n%s", out)
	}
	// The fallback must not be passed off as real route progress.
	if !strings.Contains(out, "since tracking began") {
		t.Errorf("the fallback bar must say what it is measuring\n---\n%s", out)
	}
}

func TestNoProgressBarBeforeAnythingIsKnown(t *testing.T) {
	m := airborneModel(t)
	m.origin = airports.Airport{}
	m.trackStartNM = 0
	if strings.Contains(m.View(), "Progress") {
		t.Error("a bar should not appear when there is nothing to measure against")
	}
}

// recompute must remember the first distance so the fallback bar has a
// baseline, and must not move that baseline on later refreshes.
func TestTrackStartIsRecordedOnce(t *testing.T) {
	m := airborneModel(t)
	m.trackStartNM = 0
	m.recompute()
	first := m.trackStartNM
	if first <= 0 {
		t.Fatal("the starting distance was not recorded")
	}
	m.obs.Lat += 5 // the aircraft moves on
	m.recompute()
	if m.trackStartNM != first {
		t.Errorf("the baseline moved from %.0f to %.0f", first, m.trackStartNM)
	}
}

// First-run setup asks for the origin, then the destination.
func TestSetupWalksOriginThenDestination(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenDest
	m.pickOrigin = true
	m.destInput.Focus()

	for _, r := range "DUB" {
		updated, _ := m.onKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(model)
	}
	updated, _ := m.onKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if m.origin.IATA != "DUB" {
		t.Fatalf("origin = %q, want DUB", m.origin.IATA)
	}
	if m.screen != screenDest || m.pickOrigin {
		t.Fatal("after the origin it should stay on the picker, now asking for a destination")
	}

	for _, r := range "LHR" {
		updated, _ := m.onKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(model)
	}
	updated, _ = m.onKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if m.dest.IATA != "LHR" {
		t.Errorf("destination = %q, want LHR", m.dest.IATA)
	}
	if m.screen != screenDash {
		t.Error("both airports chosen should open the dashboard")
	}
}

// Origin is optional: a blank entry moves on rather than erroring.
func TestOriginCanBeSkipped(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenDest
	m.pickOrigin = true

	updated, _ := m.onKey(tea.KeyMsg{Type: tea.KeyEnter})
	m2 := updated.(model)

	if m2.errMsg != "" {
		t.Errorf("skipping the origin should not be an error, got %q", m2.errMsg)
	}
	if m2.origin.IATA != "" {
		t.Error("skipping should leave the origin unset")
	}
	if m2.pickOrigin || m2.screen != screenDest {
		t.Error("skipping the origin should move on to the destination")
	}
}

// A flight the feed cannot see should say so plainly rather than render blanks.
func TestDashboardHandlesMissingAircraft(t *testing.T) {
	m := airborneModel(t)
	m.obs = nil
	m.est = eta.Estimate{}
	out := m.View()
	if !strings.Contains(out, "Not currently visible") {
		t.Errorf("expected a clear not-visible message\n---\n%s", out)
	}
}

// A landing seen between two snapshots must raise exactly one event.
func TestSnapshotTransitionDetectsTakeoff(t *testing.T) {
	m := airborneModel(t)
	m.notifyOn = false // keep the test off the notification path
	onGround := true
	m.prevOnGround = &onGround

	snap := &opensky.Snapshot{
		Taken:    m.now,
		Fetched:  m.now,
		Aircraft: map[opensky.FlightID]*opensky.Observation{m.flight: m.obs},
	}
	updated, _ := m.onSnapshot(snapshotMsg{snap: snap})
	m2 := updated.(model)

	var found bool
	for _, l := range m2.logs {
		if strings.Contains(l.text, "taken off") {
			found = true
		}
	}
	if !found {
		t.Errorf("a ground-to-air transition did not log a takeoff, logs: %v", m2.logs)
	}
	if m2.prevOnGround == nil || *m2.prevOnGround {
		t.Error("the stored phase should now be airborne")
	}
}

func TestArrivalAlertFiresOnce(t *testing.T) {
	m := airborneModel(t)
	m.notifyOn = false
	m.est.ArrivalUTC = m.now.Add(10 * time.Minute) // inside the alert window

	if cmd := m.checkArrival(); cmd != nil && m.arrivalAlerted == false {
		t.Error("checkArrival should mark the alert as sent")
	}
	if !m.arrivalAlerted {
		t.Fatal("the arrival alert did not fire")
	}
	before := len(m.logs)
	m.checkArrival()
	if len(m.logs) != before {
		t.Error("the arrival alert fired twice")
	}
}

func TestWrap(t *testing.T) {
	got := wrap("the quick brown fox jumps", 10)
	for _, line := range strings.Split(got, "\n") {
		if len(line) > 10 {
			t.Errorf("line %q exceeds width 10", line)
		}
	}
	if wrap("", 10) != "" {
		t.Error("wrapping empty text should give empty text")
	}
}

func TestTrunc(t *testing.T) {
	if got := trunc("abcdefgh", 4); len(got) != 4 {
		t.Errorf("trunc to 4 gave %q", got)
	}
	if got := trunc("abc", 10); got != "abc" {
		t.Errorf("short strings should pass through, got %q", got)
	}
}

// Notifications must not be attempted when the user has them switched off.
func TestSendIsInertWhenNotificationsAreOff(t *testing.T) {
	m := airborneModel(t)
	m.notifyOn = false
	if cmd := m.send(notify.Event{Kind: "takeoff"}); cmd != nil {
		t.Error("send should do nothing while notifications are off")
	}
}
