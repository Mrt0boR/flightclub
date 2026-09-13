package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"flighttrack/internal/airports"
	"flighttrack/internal/config"
	"flighttrack/internal/eta"
	"flighttrack/internal/history"
	"flighttrack/internal/notify"
	"flighttrack/internal/opensky"
)

func newTestModel(t *testing.T) model {
	t.Helper()
	fi := textinput.New()
	di := textinput.New()
	ci := textinput.New()
	return model{
		client:       opensky.New("", ""),
		screen:       screenFlight,
		flightInput:  fi,
		destInput:    di,
		discordInput: ci,
		handbook:     viewport.New(handbookWidth, 20),
		hist:         &history.File{},
		cfg:          &config.Config{},
		spin:         spinner.New(),
		now:          time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
		refreshIn:    defaultRefresh,
		width:        120,
		height:       40,
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

// Notifications must not be attempted when the user has them switched off.
func TestSendIsInertWhenNotificationsAreOff(t *testing.T) {
	m := airborneModel(t)
	m.notifyOn = false
	if cmd := m.send(notify.Event{Kind: "takeoff"}); cmd != nil {
		t.Error("send should do nothing while notifications are off")
	}
}

// Holding an action key produces a stream of identical KeyMsgs. Only the first
// should do anything; the rest are the OS key-repeat and must be dropped, or a
// held `r` spends API credits on every repeat.
func TestHeldActionKeyIsDebounced(t *testing.T) {
	m := airborneModel(t)
	m.autoRefresh = false

	press := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}

	updated, cmd := m.onDashKey(press)
	m = updated.(model)
	if cmd == nil {
		t.Fatal("the first press of r should start a refresh")
	}
	if !m.loading {
		t.Fatal("the first press should set loading")
	}

	// Immediate repeats, as from a held key.
	for i := 0; i < 5; i++ {
		updated, cmd = m.onDashKey(press)
		m = updated.(model)
		if cmd != nil {
			t.Fatalf("repeat %d of a held r started another refresh", i+1)
		}
	}

	// A deliberate press after the debounce window is honoured again.
	m.lastActionAt = m.lastActionAt.Add(-2 * keyDebounce)
	m.loading = false
	_, cmd = m.onDashKey(press)
	if cmd == nil {
		t.Error("a fresh press after the debounce window should refresh again")
	}
}

// Navigation keys are meant to be held, so they are not debounced.
func TestNavigationKeysAreNotDebounced(t *testing.T) {
	m := airborneModel(t)
	m.menuIdx = 0
	for i := 0; i < 3; i++ {
		updated, _ := m.onDashKey(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(model)
	}
	if m.menuIdx != 3 {
		t.Errorf("three held downs moved the cursor to %d, want 3", m.menuIdx)
	}
}

// Once a flight has landed near its destination, auto-refresh should switch
// itself off rather than keep polling a flight that has arrived.
func TestAutoRefreshStopsAfterLanding(t *testing.T) {
	m := airborneModel(t)
	m.notifyOn = false
	m.autoRefresh = true
	airborne := false
	m.prevOnGround = &airborne

	dest, _ := airports.Lookup("JFK")
	landed := &opensky.Observation{
		Icao24: "400a1b", Callsign: "BAW117",
		Lat: dest.Lat, Lon: dest.Lon, HasPos: true,
		OnGround: true, Velocity: 6,
	}
	snap := &opensky.Snapshot{
		Taken:    m.now,
		Fetched:  m.now,
		Aircraft: map[opensky.FlightID]*opensky.Observation{m.flight: landed},
	}
	updated, _ := m.onSnapshot(snapshotMsg{snap: snap})
	m2 := updated.(model)

	if m2.autoRefresh {
		t.Error("auto-refresh should be off after a landing at the destination")
	}
	var loggedLanding bool
	for _, l := range m2.logs {
		if strings.Contains(l.text, "has landed") {
			loggedLanding = true
		}
	}
	if !loggedLanding {
		t.Error("the landing was not logged")
	}
}

// A spurious on-ground report far from the destination is likely bad data, so
// auto-refresh should keep running to give the next poll a chance to correct.
func TestAutoRefreshSurvivesImplausibleLanding(t *testing.T) {
	m := airborneModel(t)
	m.notifyOn = false
	m.autoRefresh = true
	airborne := false
	m.prevOnGround = &airborne

	// On the ground mid-Atlantic, nowhere near JFK.
	landed := &opensky.Observation{
		Icao24: "400a1b", Callsign: "BAW117",
		Lat: 45.0, Lon: -30.0, HasPos: true, OnGround: true, Velocity: 6,
	}
	snap := &opensky.Snapshot{
		Taken: m.now, Fetched: m.now,
		Aircraft: map[opensky.FlightID]*opensky.Observation{m.flight: landed},
	}
	updated, _ := m.onSnapshot(snapshotMsg{snap: snap})
	if !updated.(model).autoRefresh {
		t.Error("auto-refresh should survive an implausible landing report")
	}
}

// -dev seeds a flight straight onto the dashboard, and ctrl+t drives it
// through the real landing path.
func TestDevModeSimulatesLanding(t *testing.T) {
	m := newTestModel(t)
	m.notifyOn = false
	m.seedDevFlight()

	if m.screen != screenDash || m.obs == nil || m.obs.OnGround {
		t.Fatal("dev mode should open on a dashboard tracking an airborne flight")
	}

	updated, cmd := m.onDashKey(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = updated.(model)
	if cmd == nil {
		t.Fatal("ctrl+t should produce a synthetic snapshot")
	}

	msg := cmd()
	snapMsg, ok := msg.(snapshotMsg)
	if !ok {
		t.Fatalf("expected a snapshotMsg, got %T", msg)
	}
	updated, _ = m.onSnapshot(snapMsg)
	m = updated.(model)

	var landed bool
	for _, l := range m.logs {
		if strings.Contains(l.text, "has landed") {
			landed = true
		}
	}
	if !landed {
		t.Errorf("ctrl+t did not drive the flight to a landing, logs: %v", m.logs)
	}
}

// ctrl+t does nothing outside dev mode.
func TestSimulateLandingIsDevOnly(t *testing.T) {
	m := airborneModel(t)
	_, cmd := m.onDashKey(tea.KeyMsg{Type: tea.KeyCtrlT})
	if cmd != nil {
		t.Error("ctrl+t should be inert when not in dev mode")
	}
}

// ------------------------------------------------------------- navigation
//
// The main menu sits above everything else now. Esc should walk back toward
// it one level at a time; only the top screens (main, history) treat a bare
// "q" as an immediate quit, since every other screen has a live text input
// where "q" is just a letter.

func TestMainMenuIsTheDefaultEntryScreen(t *testing.T) {
	m := newModel(defaultRefresh, true)
	if m.screen != screenMain {
		t.Errorf("a fresh model should open on the main menu, got screen %d", m.screen)
	}
}

func TestPreseedingAFlightSkipsTheMainMenu(t *testing.T) {
	m := newModel(defaultRefresh, true)
	if err := m.preseed("BA117", "", "LHR"); err != nil {
		t.Fatal(err)
	}
	if m.screen != screenFlight {
		t.Errorf("a preseeded flight should skip straight to the flight screen, got %d", m.screen)
	}
	if !m.loading {
		t.Error("a preseeded flight should start loading immediately")
	}
}

func TestEscFromFlightReturnsToMainMenu(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenFlight
	updated, _ := m.onFlightKey(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.(model).screen != screenMain {
		t.Error("esc on the flight screen should return to the main menu, not quit")
	}
}

func TestEscFromHistoryReturnsToMainMenuButQQuits(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenHistory

	updated, _ := m.onHistoryKey(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.(model).screen != screenMain {
		t.Error("esc on the history screen should return to the main menu")
	}

	_, cmd := m.onHistoryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	quitMsg := cmd()
	if _, ok := quitMsg.(tea.QuitMsg); !ok {
		t.Errorf("q on the history screen should quit, got %T", quitMsg)
	}
}

func TestMainMenuNavigatesToEachDestination(t *testing.T) {
	cases := []struct {
		item int
		want screen
	}{
		{mainSearch, screenFlight},
		{mainRecent, screenHistory},
		{mainDiscordSetup, screenDiscordSetup},
		{mainSettings, screenSettings},
		{mainHandbook, screenHandbook},
	}
	for _, c := range cases {
		m := newTestModel(t)
		m.screen = screenMain
		updated, _ := m.activateMain(c.item)
		if got := updated.(model).screen; got != c.want {
			t.Errorf("activateMain(%d) -> screen %d, want %d", c.item, got, c.want)
		}
	}
}

func TestMainMenuQuits(t *testing.T) {
	m := newTestModel(t)
	_, cmd := m.activateMain(mainQuit)
	if cmd == nil {
		t.Fatal("quit should produce a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("the quit item should send tea.Quit")
	}
}

// ------------------------------------------------------------- discord setup

func TestDiscordSetupSavesAValidURL(t *testing.T) {
	m := newTestModel(t)
	m.cfgPath = "" // saveConfig is a no-op without a path; the point here is validation + m.discord
	m.discordInput.SetValue("https://discord.com/api/webhooks/123456789012345678/aBcDeF")

	updated, _ := m.saveDiscordSetup()
	m2 := updated.(model)
	if m2.discord == nil {
		t.Fatal("a valid webhook URL should configure m.discord")
	}
	if !m2.discordSetupOK {
		t.Errorf("expected success, got message %q", m2.discordSetupMsg)
	}
	if m2.cfg.DiscordWebhookURL == "" {
		t.Error("the URL should be recorded on cfg for saving")
	}
}

func TestDiscordSetupRejectsABadURL(t *testing.T) {
	m := newTestModel(t)
	m.discordInput.SetValue("https://discord.gg/not-a-webhook")

	updated, _ := m.saveDiscordSetup()
	m2 := updated.(model)
	if m2.discord != nil {
		t.Error("an invalid URL must not configure m.discord")
	}
	if m2.discordSetupOK {
		t.Error("an invalid URL should not report success")
	}
	if m2.discordSetupMsg == "" {
		t.Error("expected an error message explaining the rejection")
	}
}

// An empty box is how you remove a previously configured webhook, not an
// error.
func TestDiscordSetupClearsOnEmptyInput(t *testing.T) {
	m := newTestModel(t)
	m.discord = &notify.Discord{URL: "https://discord.com/api/webhooks/1/abc"}
	m.cfg.DiscordWebhookURL = "https://discord.com/api/webhooks/1/abc"
	m.discordInput.SetValue("")

	updated, _ := m.saveDiscordSetup()
	m2 := updated.(model)
	if m2.discord != nil {
		t.Error("clearing the box should clear m.discord")
	}
	if m2.cfg.DiscordWebhookURL != "" {
		t.Error("clearing the box should clear the saved URL too")
	}
	if !m2.discordSetupOK {
		t.Error("clearing is a successful action, not an error")
	}
}

func TestDiscordTestSendNeedsAConfiguredWebhookFirst(t *testing.T) {
	m := newTestModel(t)
	updated, cmd := m.sendDiscordTest()
	if cmd != nil {
		t.Error("a test send with nothing configured should not produce a command")
	}
	if updated.(model).discordSetupOK {
		t.Error("expected a message explaining nothing is configured yet")
	}
}

func TestDiscordTestSendFiresWhenConfigured(t *testing.T) {
	m := newTestModel(t)
	m.discord = &notify.Discord{URL: "https://discord.com/api/webhooks/1/abc"}
	_, cmd := m.sendDiscordTest()
	if cmd == nil {
		t.Fatal("expected a command that sends the test notification")
	}
}

func TestEscFromDiscordSetupReturnsToMainMenu(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenDiscordSetup
	updated, _ := m.onDiscordSetupKey(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.(model).screen != screenMain {
		t.Error("esc on discord setup should return to the main menu")
	}
}

// ------------------------------------------------------------------ settings

func TestSettingsAppliesAndRemembersATheme(t *testing.T) {
	defer applyTheme(DefaultTheme) // do not leak the picked theme into other tests
	m := newTestModel(t)
	m.screen = screenSettings
	m.settingsIdx = 0

	// Move to a non-default theme and pick it.
	for themeOrder[m.settingsIdx] == DefaultTheme && m.settingsIdx < len(themeOrder)-1 {
		m.settingsIdx++
	}
	picked := themeOrder[m.settingsIdx]

	updated, _ := m.onSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m2 := updated.(model)
	if currentTheme != picked {
		t.Errorf("currentTheme = %q, want %q", currentTheme, picked)
	}
	if m2.cfg.Theme != picked {
		t.Errorf("cfg.Theme = %q, want %q", m2.cfg.Theme, picked)
	}
}

func TestEscFromSettingsReturnsToMainMenu(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenSettings
	updated, _ := m.onSettingsKey(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.(model).screen != screenMain {
		t.Error("esc on settings should return to the main menu")
	}
}

// ------------------------------------------------------------------ handbook

func TestHandbookOpensAndGoesBack(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenMain
	updated, _ := m.activateMain(mainHandbook)
	m2 := updated.(model)
	if m2.screen != screenHandbook {
		t.Fatal("expected the handbook to open")
	}
	if !strings.Contains(m2.viewHandbook(), "FLIGHTTRACK HANDBOOK") {
		t.Error("the handbook should render its content")
	}

	updated, _ = m2.onHandbookKey(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.(model).screen != screenMain {
		t.Error("esc on the handbook should return to the main menu")
	}
}

// ------------------------------------------------------------- info panel

func TestInfoPanelShowsDiscordStatus(t *testing.T) {
	m := airborneModel(t)
	if strings.Contains(m.infoPanel(30), "configured") {
		t.Error("with no discord notifier, the panel should not claim one is configured")
	}
	m.discord = &notify.Discord{URL: "https://discord.com/api/webhooks/1/abc"}
	if !strings.Contains(m.infoPanel(30), "configured") {
		t.Error("with a discord notifier set, the panel should say so")
	}
}
