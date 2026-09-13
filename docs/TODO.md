# TODO

Running notes on things worth doing, roughly in priority order. Not a
commitment — a place so ideas do not get lost.

## Always-on watch mode (Raspberry Pi / Linux)

A phone notification matters most when you are away from the desk, which is
exactly when the dashboard is not running. The fix is to run `flighttrack
watch` on something that is always on.

The code is already close:

- It cross-compiles today. `GOOS=linux GOARCH=arm64 go build ./cmd/flighttrack`
  produces a working Pi binary. The only OS-specific piece is the Windows
  desktop toast (`internal/notify/toast`), which already reports
  `Available() == false` and returns an error on non-Windows, so
  `watch -notify console,ntfy` runs fine on Linux with nothing stubbed.
- `-state-file` already persists flight phases across restarts.

What is missing:

- [ ] A release step that builds and publishes binaries for
      `linux/arm64`, `linux/amd64` and `windows/amd64` (see the release
      item below — the update checker needs releases anyway).
- [ ] A sample `systemd` unit and a short "run it on a Pi" section in the
      README, or a `deploy/` directory.
- [ ] Decide how credentials reach the Pi — `OPENSKY_CLIENT_ID` /
      `_SECRET` in an `EnvironmentFile`, documented.
- [ ] Confirm the anonymous OpenSky quota (~100 polls/day) is workable for
      a Pi left running, or make registering an API client a prerequisite
      in the docs.

## Notification delivery reliability

**ntfy.sh hosted push: tried, scrapped (2026-09-13).** Messages reached the
server fine (confirmed by curl and by both test priorities showing correctly
tagged), but the phone was only ever updated on manual app refresh — no
background wake, even after checking Instant delivery, battery
unrestriction, and a 15-minute untouched wait. That is ntfy.sh's hosted
infrastructure, not our code, but it makes the free hosted service a dead
end for this. The `notify.Ntfy` code and its branch (`mobile-notifs`) were
removed; the commit is kept at the tag `archive/ntfy-attempt` if any of it
is worth reusing later (`NewNtfy` takes any base URL, so it would work
unchanged against a self-hosted ntfy server rather than ntfy.sh).

Two ways forward, in order of preference:

- [ ] **`notify.Telegram`** (~40 lines). A bot-to-user chat is authenticated
      on both ends and delivery is Telegram's problem, not a free shared
      server's — no battery/background-refresh fighting expected. Token
      from `FLIGHTTRACK_TELEGRAM_TOKEN`, chat id from a flag or env. Try
      this first; it needs no infrastructure of our own.
- [ ] **Self-hosted ntfy on the always-on Pi** (see above), only if
      Telegram is unsuitable for some reason. Self-hosting removes the
      exact failure mode seen here (a free shared server's push reliability
      is not our problem to fix), but it is more to run and maintain than
      just using Telegram.

## Progressive refresh near arrival

From `bugs/buglist.txt`. Takeoff and landing are the events that matter, so
the data should be freshest close to them.

- [ ] When route progress passes ~96%, drop the auto-refresh interval to
      3 minutes; past ~99%, to 1 minute. Restore the normal interval if the
      flight is clearly still far out (a long hold, a diversion).
- [ ] Work out the credit cost of this and cap it. At 4 credits a poll,
      1-minute refresh for the last 20 minutes is 80 credits — most of the
      anonymous daily budget on one arrival. Probably only sensible with a
      registered API client, or gated behind a flag.

## Sharper ETA from an internal clock

Also from `bugs/buglist.txt`. The current ETA is recomputed only when a
snapshot arrives; between snapshots the countdown just ticks down linearly.

- [ ] Keep a running ETA model that updates itself against the system
      clock, and corrects its assumed ground speed each time a real
      snapshot lands (how far did it actually travel vs. how far the model
      predicted). This makes the countdown smoother and lets the refresh
      scheduler reason about "how stale is my position, really" rather than
      just elapsed time.
- [ ] Use that staleness estimate to decide when a refresh is actually
      worth spending credits on — skip a scheduled poll if the model is
      still confident.

## Cut a v1.0.0 release

The update checker (`flighttrack version`, and the INFO-panel line) calls
the GitHub releases API, which 404s until a release exists — so every build
currently reports "up to date" regardless.

- [ ] `git tag v1.0.0 && git push origin v1.0.0`, then create the GitHub
      release. Ideally with the cross-compiled binaries attached (see the
      Pi item).
