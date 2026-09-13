# flighttrack

A terminal flight tracker. Give it the flight number from your ticket and it
finds the aircraft, shows where it is, estimates an arrival time in GMT, and
counts down to it — or, in watch mode, sits quietly in the background and tells
you when a flight takes off or lands.

Data comes from the [OpenSky Network](https://opensky-network.org), which is
free and open. No account is required to start.

```
╭──────────────────────────────────────────────────────╮╭─────────────────────╮
│ QF2   callsign QFA2                                  ││ MENU                │
│                                                      ││                     │
│ Status        AIRBORNE                               ││ > Refresh now       │
│ Altitude      38950 ft                               ││   Notifications  on │
│ Ground speed  586 kts                                ││   Auto-refresh   on │
│                                                      ││   Change origin SYD │
│ Destination   LHR - London Heathrow (London, GB)     ││   Change destination│
│ Distance      2140 nm, bearing 298 NW                ││   Change flight     │
│                                                      ││   Quit              │
│ ETA (GMT)     09:14:22  Sun 6 Sep                    ││                     │
│ Countdown     3h 41m 07s                             ││ INFO                │
│                                                      ││ data age   1m32s    │
│ Progress      SYD ==============........ LHR   64%   ││ api calls  8 / 400  │
│               6820 nm flown of 10620 nm              ││ clock      05:33 GMT│
╰──────────────────────────────────────────────────────╯╰─────────────────────╯
 r refresh   n notify   a auto   o origin   d destination   f flight   q quit
```

## Requirements

- Windows 10 or 11 for desktop notifications; everything else is cross-platform.
- [Go](https://go.dev/dl/) 1.24 or newer, to build it.

## Install

### With the script

From the project folder, in any terminal:

```powershell
.\install.ps1
```

The script builds the binary, copies it to
`%LOCALAPPDATA%\Programs\flighttrack`, and adds that folder to your **user**
PATH. No administrator rights are needed and the machine-wide PATH is left
alone. Open a new terminal afterwards for the PATH change to take effect.

If PowerShell refuses to run the script, the cause is your execution policy.
This allows local scripts for your account only:

```powershell
Set-ExecutionPolicy -Scope CurrentUser RemoteSigned
```

Other flags: `-NoPath` installs without touching PATH; `-Update` and
`-Uninstall` are covered below.

### By hand

```powershell
go build -o flighttrack.exe ./cmd/flighttrack
```

Run `.\flighttrack.exe` from this folder, or copy the `.exe` anywhere on your
PATH. It is a single self-contained binary — the airport database is compiled
in, so there are no data files to keep next to it.

### Update

```powershell
.\install.ps1 -Update
```

This pulls the latest source, rebuilds, and replaces the installed binary. It
refuses if you have uncommitted changes in the source folder.

Separately, the dashboard checks GitHub for a newer release at most once a day,
caches the answer, and fails silently with no network. When an update exists it
shows a single line in the INFO panel; it never downloads anything or
interrupts you. `flighttrack version` runs the same check on demand. Builds
made without the install script report as `dev` and never check.

### Uninstall

```powershell
.\install.ps1 -Uninstall
```

Add `-Purge` to also delete your saved search history.

## Use it

```powershell
flighttrack                                   # dashboard, with saved searches
flighttrack -flight QF2 -from SYD -to LHR      # dashboard, preseeded
flighttrack watch -flights QF2,CX251           # background, no interface
flighttrack history                            # show saved searches
flighttrack version                            # build, and whether it is current
flighttrack help                               # full option list
```

### Dashboard

On launch you get your recent searches, colour-coded by how fresh the saved
position is: green under 10 minutes, amber under an hour, red beyond that. Pick
one and it opens straight away on cached data if that is still usable,
otherwise it fetches. Press `n` to start a new search, which asks for the
flight number, then the origin (optional — press Enter to skip), then the
destination.

The same starting point can be given on the command line:

| Flag | Default | Does |
| --- | --- | --- |
| `-flight` | — | Flight number to open on, e.g. `BA117` |
| `-from` | — | Origin airport code; enables the route progress bar |
| `-to` | — | Destination airport code |
| `-refresh` | `5m` | How often to auto-refresh (minimum `1m`) |
| `-no-auto-refresh` | off | Start with auto-refresh disabled |
| `-webhook` | — | HTTPS URL to POST takeoff and landing events to |
| `-discord` | — | Discord channel webhook URL, for phone push via Discord's app |

Once the dashboard is open:

| Key | Does |
| --- | --- |
| `r` | Refresh now (uses 4 API credits) |
| `n` | Toggle desktop notifications |
| `a` | Toggle auto-refresh |
| `o` | Set or change the origin |
| `d` | Change the destination |
| `f` | Track a different flight |
| `q` | Quit |

The countdown ticks every second off your system clock and touches no network.
Only `r` and the auto-refresh spend API credits, and the INFO panel shows your
running total.

### Watch mode

No interface. Give it several flights and it reports their takeoffs and
landings.

```powershell
flighttrack watch -flights QF2,CX251,SQ322 -notify console,desktop
```

| Flag | Default | Does |
| --- | --- | --- |
| `-flights` | required | Comma-separated flight numbers |
| `-interval` | `90s` | How often to poll OpenSky |
| `-notify` | `console` | Comma-separated: `console`, `desktop`, `webhook`, `discord` |
| `-webhook-url` | — | HTTPS endpoint to POST events to |
| `-discord-url` | — | Discord channel webhook URL |
| `-confirm` | `2` | Consecutive polls a change must hold before it is reported |
| `-lost-after` | `25m` | Report a loss of contact after this long with no data |
| `-state-file` | `flighttrack-watch-state.json` | Where flight phases are persisted across restarts |
| `-once` | off | Poll once and exit |

Webhooks receive the full event as JSON. Plain HTTP is refused to anything but
loopback, so events never cross a network in the clear.

### Phone notifications

The `discord` sink posts events into a Discord channel as a coloured embed
(blue takeoff, green landing, gold arriving-soon, red signal-lost), and
Discord's own mobile app handles delivery to your phone — which is the part
that matters, since it is the same infrastructure your regular messages use
rather than a shared free push service.

A Discord webhook posts into a channel; it cannot DM you directly, so the
practical way to get a private, DM-like experience is a small server with
just yourself in it (or a channel only you can see there), with mobile
notifications turned on for it.

1. In that server: **Server Settings → Integrations → Webhooks → New
   Webhook**, pick the channel, copy the webhook URL.
2. Point flighttrack at it:

```powershell
flighttrack -flight QF2 -to LHR -discord https://discord.com/api/webhooks/123.../abcXYZ
flighttrack watch -flights QF2,CX251 -notify console,discord -discord-url https://discord.com/api/webhooks/123.../abcXYZ
```

The URL can also come from `FLIGHTTRACK_DISCORD`. Treat it like a password —
anyone with it can post into that channel — but unlike a bot token it can't
do anything beyond that one channel.

## API credentials

Anonymous access gives about 400 credits a day, and a snapshot costs 4 — call
it 100 refreshes. A free account at
[opensky-network.org](https://opensky-network.org) raises that to 4000. Create
an API client, then:

```powershell
setx OPENSKY_CLIENT_ID "your-id"
setx OPENSKY_CLIENT_SECRET "your-secret"
```

Open a new terminal afterwards. Credentials are read from the environment only;
this app never writes them to disk.

## Where your data lives

| What | Where |
| --- | --- |
| Search history and cached positions | `%APPDATA%\flighttrack\history.json` |
| Watch-mode flight states | `flighttrack-watch-state.json` in the working directory |

The history keeps your five most recent searches. `flighttrack history` prints
it; `flighttrack history -clear` deletes it.

## What it cannot do

Worth knowing before you rely on it.

**It does not know where a flight is going.** OpenSky publishes a callsign and
a position, and nothing else — no route, no schedule, no destination. That is
why you type the destination yourself, and why there is no way to search for
"flights from DUB to LHR".

**The ETA is a straight-line estimate.** It is the great-circle distance to the
destination divided by current ground speed. It ignores winds, filed routing,
ATC vectoring, holding and the approach, so it reads optimistic — most
noticeably in the last hour. The dashboard labels it `[fair]` in cruise and
`[rough]` when the aircraft is close in or off cruise speed. It is a countdown,
not a promise.

**A flight has to be airborne and in coverage to be found.** ADS-B is
crowd-sourced, with real gaps over oceans, at low altitude, and anywhere
receiver coverage is thin. A flight that has not pushed back does not appear at
all. When contact is lost mid-flight, watch mode says exactly that rather than
guessing that it landed.

**Progress needs an origin.** Without one the app can only show how far the
aircraft has come since you started watching, and it says so on the bar
instead of dressing it up as route progress.

## Building

```powershell
go build -o flighttrack.exe ./cmd/flighttrack   # build
go test ./...                                    # tests
go vet ./...                                     # vet
```

To render every screen without launching the app:

```powershell
go test -run TestPreviewRender -v ./internal/ui/
```

`flighttrack -dev` opens the dashboard on a fake flight without touching the
API; pressing `ctrl+t` then simulates its landing, which is the way to test
that notifications fire.

### Regenerating the airport table

`internal/airports/data_gen.go` holds 4,570 airports compiled into the binary,
generated from the public-domain
[OurAirports](https://github.com/davidmegginson/ourairports-data) dataset. To
refresh it, download `airports.csv` from that repository and run:

```powershell
go run ./cmd/genairports -in airports.csv -out internal/airports/data_gen.go
```

The CSV is not needed afterwards. Tests check known coordinates and route
distances, so a bad regeneration fails loudly rather than quietly producing
wrong ETAs.

### Layout

```
cmd/flighttrack/           the command line: dispatch, flags, help
cmd/genairports/           airport table generator
internal/ui/               the dashboard (model, update, screens, panels)
internal/watch/            background watch mode
internal/opensky/          API client, flight-number matching
internal/eta/              great-circle maths, arrival estimates
internal/airports/         embedded airport table
internal/history/          saved searches and position cache
internal/notify/           desktop, webhook and Discord notifications
internal/textfmt/          shared text helpers
internal/version/          build stamp and the GitHub release check
```

Inside `internal/ui`, each screen keeps its key handling and its rendering in
one file (`screen_history.go`, `screen_flight.go`, `screen_airport.go`,
`screen_dashboard.go`), with `model.go` holding the shared state and
`update.go` routing messages between them.
