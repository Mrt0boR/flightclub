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

ntfy.sh's free hosted push has been unreliable in testing — messages reach
the server fine (confirmed), but the phone is often not woken until the app
is opened. This is ntfy.sh's infrastructure, not our code.

- [ ] Add a `notify.Telegram` sink (~40 lines, same shape as
      `internal/notify/ntfy.go`). A bot-to-user chat is authenticated on
      both ends, delivery is Telegram's problem, and there is no public
      topic to leak. Token from `FLIGHTTRACK_TELEGRAM_TOKEN`, chat id from
      a flag or env.
- [ ] Document the reserved-topic + `FLIGHTTRACK_NTFY_TOKEN` route for
      people who want to stay on ntfy but close the "anyone can post to my
      topic" hole.
- [ ] Consider a self-hosted ntfy note in the Pi section — if the Pi is
      already running, it can host ntfy too.

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
