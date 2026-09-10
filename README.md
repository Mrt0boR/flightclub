# flighttrack

A terminal flight tracker. Enter the flight number from your ticket and it
shows you where the aircraft is, an estimated arrival time in GMT, and a live
countdown — or watches quietly in the background and tells you when a flight
takes off or lands.

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

- Windows 10 or 11 (desktop notifications are Windows-only; everything else is
  cross-platform)
- [Go](https://go.dev/dl/) 1.21 or newer, to build it

## Install

### With the script

From the project folder, in any terminal:

```powershell
.\install.ps1
```

It builds the binary, copies it to `%LOCALAPPDATA%\Programs\flighttrack`, and
adds that folder to your **user** PATH. No administrator rights are needed and
the machine-wide PATH is not touched. Open a new terminal afterwards so the
PATH change takes effect.

If PowerShell refuses to run the script, it is your execution policy. This
allows local scripts for your account only:

```powershell
Set-ExecutionPolicy -Scope CurrentUser RemoteSigned
```

### By hand

```powershell
go build -o flighttrack.exe ./cmd/flighttrack
```

Then run `.\flighttrack.exe` from this folder, or copy the `.exe` anywhere on
your PATH. It is a single self-contained binary — the airport database is
compiled into it, so there are no data files to keep alongside it.

### Update

```powershell
.\install.ps1 -Update
```

Pulls the latest source, rebuilds, and replaces the installed binary. It
refuses if you have uncommitted changes in the source folder.

The dashboard checks GitHub for a newer release at most once a day, caches the
answer, and fails silently if there is no network. When there is one, it shows
a single line in the INFO panel — it never downloads anything or interrupts
you. `flighttrack version` runs the same check on demand. Builds made without
the install script report as `dev` and never check.

### Uninstall

```powershell
.\install.ps1 -Uninstall
```

Add `-Purge` to also delete your saved search history.

## Use it

```powershell
flighttrack                                   # dashboard, with saved searches
flighttrack -flight QF2 -from SYD -to LHR     # straight to the dashboard
flighttrack watch -flights QF2,CX251          # background, no interface
flighttrack history                           # show what is saved
flighttrack version                           # build, and whether it is current
flighttrack help                              # everything
```

### Dashboard

On launch you get your recent searches, colour-coded by how fresh the saved
position is — green under 10 minutes, amber under an hour, red older than that.
Pick one and it opens instantly on cached data if it is still usable; otherwise
it fetches. Press `n` for a new search.

A new search asks for the flight number, then the origin (optional — press
Enter to skip), then the destination.

| Key | Does |
| --- | --- |
| `r` | Refresh now (costs 4 API credits) |
| `n` | Toggle desktop notifications |
| `a` | Toggle auto-refresh |
| `o` | Set or change the origin |
| `d` | Change the destination |
| `f` | Track a different flight |
| `q` | Quit |

The countdown ticks every second off your system clock and touches no network.
Only `r` and the 5-minute auto-refresh cost anything, and the INFO panel shows
your running credit total.

### Watch mode

No interface. Give it several flights and it reports takeoffs and landings.

```powershell
flighttrack watch -flights QF2,CX251,SQ322 -notify console,desktop
```

| Flag | Default | Does |
| --- | --- | --- |
| `-flights` | required | Comma-separated flight numbers |
| `-interval` | `90s` | How often to poll |
| `-notify` | `console` | `console`, `desktop`, `webhook` |
| `-webhook-url` | — | HTTPS endpoint to POST events to |
| `-confirm` | `2` | Polls a change must hold before it is reported |
| `-once` | off | Poll once and exit |

Webhooks receive the full event as JSON. Plain HTTP is refused to anything but
loopback, so events never cross a network in the clear.

## API credentials

Anonymous access gives about 400 credits a day, and a snapshot costs 4 — call
it 100 refreshes. A free account at
[opensky-network.org](https://opensky-network.org) raises that to 4000. Create
an API client, then:

```powershell
setx OPENSKY_CLIENT_ID "your-id"
setx OPENSKY_CLIENT_SECRET "your-secret"
```

Open a new terminal afterwards. Credentials are read from the environment only
— they are never written to disk by this app.

## Where your data lives

| What | Where |
| --- | --- |
| Search history and cached positions | `%APPDATA%\flighttrack\history.json` |
| Watch-mode flight states | `flighttrack-watch-state.json` in the working directory |

The history keeps your five most recent searches. `flighttrack history` prints
it, `flighttrack history -clear` deletes it.

## What it cannot do

Worth knowing before you rely on it.

**It does not know where a flight is going.** OpenSky publishes a callsign and
a position, and nothing else — no route, no schedule, no destination. That is
why you type the destination yourself, and why there is no way to search for
"flights from DUB to LHR".

**The ETA is a straight-line estimate.** Great-circle distance to the
destination divided by current ground speed. It ignores winds, filed routing,
ATC vectoring, holding and the approach, so it reads optimistic — most
noticeably in the last hour. The dashboard labels it `[fair]` in cruise and
`[rough]` when the aircraft is close in or off cruise speed. It is a countdown,
not a promise.

**A flight has to be airborne and in coverage to be found.** ADS-B is
crowd-sourced, with real gaps over oceans, at low altitude, and anywhere
receiver coverage is thin. A flight that has not pushed back will not appear at
all. When contact is lost mid-flight, watch mode says exactly that rather than
guessing that it landed.

**Progress needs an origin.** Without one the app can only show how far the
aircraft has come since you started watching, and it says so on the bar instead
of dressing it up as route progress.

## Building

```powershell
go build -o flighttrack.exe ./cmd/flighttrack   # build
go test ./...                   # tests
go vet ./...                    # vet
```

To see every screen render without launching the app:

```powershell
go test -run TestPreviewRender -v ./internal/ui/
```

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

## Layout

```
cmd/flighttrack/           the command line: dispatch, flags, help
cmd/genairports/           airport table generator
internal/ui/               the dashboard (model, update, screens, panels)
internal/watch/            background watch mode
internal/opensky/          API client, flight-number matching
internal/eta/              great-circle maths, arrival estimates
internal/airports/         embedded airport table
internal/history/          saved searches and position cache
internal/notify/           desktop and webhook notifications
internal/textfmt/          shared text helpers
internal/version/          build stamp and the GitHub release check
```

Inside `internal/ui`, each screen keeps its key handling and its rendering in
one file (`screen_history.go`, `screen_flight.go`, `screen_airport.go`,
`screen_dashboard.go`), with `model.go` holding the state they all share and
`update.go` routing messages between them.
