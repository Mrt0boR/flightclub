# Getting flight events onto a phone

Research notes, September 2026. No code was changed to produce this document.

Prices and free-tier limits were checked against vendor documentation at the time of
writing; they move, and a few numbers below are flagged as uncertain. Sources are listed
at the end.

---

## 1. Recommendation up front

**Do this first: point the existing `Webhook` notifier at an ntfy.sh topic. It costs
nothing, needs no account, and requires zero new Go code.**

```
flighttrack watch -flights QF2 -notify console,webhook \
  -webhook-url https://ntfy.sh/flighttrack-<something-long-and-random>
```

Install the ntfy app on the phone (iOS and Android, both free), subscribe to the same
topic string, and takeoff/landing/signal-lost events arrive as push notifications. The
URL is HTTPS, so it already passes `NewWebhook`'s validation unchanged. This is a
ten-minute experiment that tells you whether phone delivery is actually useful before
any code is written.

The catch is cosmetic: `Webhook.Notify` POSTs the marshalled `Event` struct, and ntfy
treats a body posted to a topic URL as literal message text. So the notification reads
as raw JSON rather than "QF2 has taken off". Usable, ugly.

**Then do this second: add a small `notify.Ntfy` type (~60 lines) that sends a proper
title and body.** Sketch in section 6. This is the highest value-per-line change
available, and it keeps working unchanged if you later self-host ntfy or move the
watcher onto a Raspberry Pi.

**On Kafka: no, and the reason is more interesting than "it's overkill".** ntfy *is*
the message broker you are reaching for — a pub/sub topic with retention, where the
publisher and the subscribing phone never talk directly. Kafka is pull-based and would
not remove the need for a push service or an always-on machine; it would add a hop, not
replace one. Full argument in section 4.

**The real problem is not the transport, it is that nothing polls OpenSky when the app
is closed.** A phone notification is worth the most precisely when you are away from the
desk, which is exactly when `flighttrack watch` is not running. Section 5 covers this.
The cheapest honest answer is a Raspberry Pi or an always-on machine at home running
`flighttrack watch` under a service manager. Notably, *whatever* you choose there, the
notifier chosen above does not change — so pick the notifier now and the host later.

One open question that changes a couple of rows below: **is the phone iOS or Android?**
Gotify and UnifiedPush are Android-only. ntfy, Pushover, Telegram and Discord cover both.

---

## 2. Comparison of phone-notification options

| Option | Cost | Account? | App install? | Setup friction | Go code needed | Reliability | Who sees the flight data |
|---|---|---|---|---|---|---|---|
| **ntfy.sh (hosted)** | Free; paid tiers from $5/mo billed annually | No account needed — the topic name *is* the credential | Yes, free app (iOS + Android) | Very low: pick a topic, install app, paste URL | **None** to start; ~60 lines for a nice-looking one | Good. 12 h server-side message cache, so an offline phone catches up. Best-effort, no SLA | ntfy.sh operator, plus **Google FCM** (ntfy.sh relays all messages through Firebase for Android) and Apple APNs for iOS |
| **ntfy (self-hosted)** | Free software; cost is whatever the host costs | You run it | Same app, point it at your server | Medium: run a container, TLS cert, DNS | Same code as hosted | Depends entirely on your uptime | You — except iOS still needs APNs, and the Play Store build still uses FCM. Use the F-Droid build to avoid FCM |
| **Pushover** | **$4.99 one-time per platform** (iOS / Android / desktop each), 30-day trial. 10,000 messages/month included free | Yes | Yes, paid app | Low: sign up, copy user key + app token | Small dedicated notifier, or `github.com/gregdel/pushover` | Very good. Long-standing, purpose-built, retries and priority/ack semantics | Pushover Inc., plus FCM/APNs |
| **Gotify (self-hosted)** | Free software | You run it | Yes, **Android only** — no official iOS app | Medium: run the server, TLS, create an app token | Small dedicated notifier (token header + form body) | Depends on your uptime; websocket-based, phone must reconnect | Only you. Best privacy of the realistic options |
| **Telegram bot** | Free | Yes, Telegram account + a bot from @BotFather | Telegram app (probably already installed) | Low-medium: create bot, find your numeric chat ID | Small dedicated notifier (needs `chat_id` + `text`, so the generic Webhook won't do) | Very good in practice | Telegram (Durov's servers). Bot chats are **not** end-to-end encrypted |
| **Discord webhook** | Free | Yes, Discord account + a server you own | Discord app | Low: create webhook in channel settings, copy URL | ~15 lines: the body must be `{"content": "..."}`, not our `Event` | Good. Rate-limited (~5 requests / 2 s per webhook) | Discord. Also anyone else in that channel |
| **Signal** | Free | Signal account | Signal app | **High.** No official bot/API. Requires `signal-cli` or `signal-cli-rest-api` running as a JVM daemon with a registered number or a linked device | Meaningful: shell out to `signal-cli`, or run its REST daemon and POST to it | Fragile. Unofficial, breaks when Signal changes protocol | Nobody — genuinely E2E encrypted. Best privacy, worst ergonomics |
| **Email-to-SMS gateway** | Free (plus an SMTP account) | Email account | None | Was low; now **mostly dead** | SMTP client (`net/smtp`), no HTTP | **Poor and getting worse.** AT&T killed `@txt.att.net` on 2025-06-17; T-Mobile's `@tmomail.net` quietly stopped in late 2024; Verizon's `@vtext.com` is scheduled to shut down 2027-03-31 | Your mail provider, the carrier, and the SMS is plaintext over the network |
| **Firebase Cloud Messaging (FCM)** | Free (Spark plan) | Google Cloud + Firebase project | **You must build and install your own app** | **High.** HTTP v1 API only since the legacy server key was shut down 2024-07-22 — needs a service-account JSON and OAuth2 token minting. Plus writing an Android/iOS client | Substantial: Google auth + your own mobile app | Excellent — it is the underlying transport everything else uses | Google, plus you |
| **Web Push (VAPID)** | Free | No third-party account | No app, but iOS **requires** the page be "Add to Home Screen"-installed (iOS 16.4+) | High: you must host a page + service worker, generate VAPID keys, store the subscription | Real work; `github.com/SherClockHolmes/webpush-go` exists | Good once working; subscriptions expire and need re-registration | Whoever runs the browser push service (Google FCM for Chrome, Mozilla, Apple), plus you |

### Reading of that table

- **ntfy is the right default here.** It is the only row that is free, needs no account,
  works on both phone OSes, and requires literally zero new code to try. Its weakest
  point is privacy — see below.
- **Pushover is the right answer if you want it to Just Work forever** and $4.99 once is
  acceptable. It is the most "product-grade" option: real priority levels, delivery
  retry, acknowledgement. If ntfy.sh's free tier or best-effort delivery ever annoys
  you, this is the upgrade.
- **Gotify is the right answer if privacy dominates and the phone is Android.** It is
  the only option where the flight data never touches a third party you don't run —
  but it costs you a server.
- **Telegram/Discord are fine and free**, and worth knowing you can reach in one small
  notifier each. They are chat apps, so the notification competes with everything else
  in the app, and neither is private.
- **Email-to-SMS should be considered dead.** Do not build on it. This is the clearest
  "no" in the table.
- **FCM and Web Push are the *plumbing*, not the *product*.** Both mean writing and
  distributing your own client. For a single-user desktop tool that is a lot of work to
  reinvent what ntfy already gives you for free.

### A necessary caveat on ntfy's privacy

ntfy.sh is not end-to-end encrypted, and the maintainer says so plainly: if the messages
are sensitive, run your own server. Two specifics worth knowing given the stated privacy
preference:

1. Topic names are the only access control by default. Anyone who guesses or learns the
   topic sees the flight events. Use a long random topic (e.g. 24 random characters),
   not `james-flights`. Rate limiting makes brute force impractical for random names.
2. **ntfy.sh republishes every message through Google Firebase Cloud Messaging** for
   Android delivery. So "avoid Google" is not achieved by using ntfy.sh. Using the
   F-Droid build of the app against a self-hosted server does achieve it.

Reserving a topic name (so nobody else can publish to it) requires an account and a paid
tier. On the free tier, anyone who knows your topic can also *send* to it — worth
knowing, though the consequence is spam, not disclosure.

### Free-tier numbers (verify before relying on them)

- **ntfy.sh free:** reported as 250 messages/day, 5 emails/day, 2 MB attachments, and a
  request rate limit of a 60-request burst then 1 request per 10 seconds. **Flagged as
  uncertain** — these numbers come from a GitHub issue complaining that ntfy.sh's own
  homepage does not publish the free-tier limits, and I could not find them stated
  authoritatively. For this workload (single digits of events per day) any of these
  limits is enormous headroom, so the uncertainty does not matter in practice.
- **ntfy paid:** Supporter $6/mo or $5/mo annual (2,500 msg/day, 3 reserved topics);
  Pro $12/$10 (20k/day); Business $25/$20 (50k/day).
- **Pushover:** $4.99 one-time per platform; 10,000 messages/month free for all users;
  teams are $5/user/month (not relevant here).

---

## 3. Which options fit the existing `Webhook` notifier

`Webhook.Notify` does exactly one thing: `POST` with `Content-Type: application/json`
and a body that is `json.Marshal(Event)`. So the question for each service is "does it
accept an arbitrary JSON body at a URL you can put in a config flag?"

**Works today, zero code:**

- **ntfy** (hosted or self-hosted, any topic URL). The body is shown verbatim as the
  message text. So you get a notification containing the raw JSON. It works; it just
  looks like a debug log. Note this relies on ntfy treating a body posted to a *topic*
  URL as plain text rather than parsing it — that is what the docs describe (JSON is
  only parsed when posted to the *root* URL), but it is worth a 30-second manual test
  with `curl` before assuming.
- **Anything that accepts arbitrary JSON**: Home Assistant webhooks, n8n, Node-RED, IFTTT
  Webhooks, Zapier catch hooks, a self-hosted relay. Useful as an escape hatch — if a
  service isn't supported, a two-line n8n/HA flow bridges it.

**Almost works — needs a body shape, so ~10-20 lines:**

- **Discord**: needs `{"content": "..."}`. Everything else about the request is identical.
  This is the cheapest *pretty* option after ntfy.
- **Slack incoming webhooks**: needs `{"text": "..."}`. Same story. (The existing test
  file already has a Slack URL in its accept-list, so this was clearly anticipated.)

**Needs a dedicated notifier type:**

- **ntfy done properly** — to get a real title, priority and tag you either set
  `X-Title` / `X-Priority` / `X-Tags` headers, or post a structured JSON document to the
  server root with a `topic` field. Either way `Webhook` cannot express it.
- **Telegram** — requires `chat_id` and `text` fields; the token sits in the URL path.
- **Pushover** — requires `token`, `user`, `title`, `message` as form fields.
- **Gotify** — requires an app token header and `title`/`message`/`priority` fields.
- **Signal, FCM, Web Push, email/SMTP** — different protocols entirely.

**A note on the existing URL validation.** `NewWebhook` refusing plain HTTP to
non-loopback is a genuinely good constraint and every option above is compatible with
it: ntfy.sh, Pushover, Telegram, Discord and Gotify are all HTTPS-only endpoints. A
self-hosted ntfy or Gotify on the LAN would be the one friction point — it would need
either a real certificate, or an SSH tunnel to loopback, or a deliberate widening of
the rule (e.g. also permitting RFC1918 addresses). I would not widen the rule; getting
a cert from Let's Encrypt or Caddy is easy and the current rule is a good one to keep.

---

## 4. Kafka: a fair verdict

### What Kafka actually is

Kafka is a distributed, durable, partitioned **append-only log**. Producers append
records to topic partitions; consumers read at their own pace by tracking an offset into
that log. Its distinguishing properties are:

- **Retention and replay.** Records stay for a configured period regardless of whether
  anyone consumed them, and a consumer can rewind and re-read history.
- **Fan-out to independent consumer groups.** Ten different systems can each read the
  same stream at their own pace, with no coordination between them and no change to the
  producer.
- **Ordering within a partition**, and horizontal scale by adding partitions.
- **Very high throughput** — the design point is hundreds of thousands to millions of
  records per second.

Its natural home is an organisation where many services produce events and many
independent teams consume them, and where being able to replay last Tuesday's stream
into a new consumer is worth real operational cost.

### What it would genuinely buy this application

Being fair, there are two real things:

1. **Durability of undelivered events.** If the phone notification fails, the event is
   still in the log and something can retry it later. Right now, `Set.Notify` collects
   the error, `watcher.fire` logs it, and the event is gone.
2. **Decoupling the producer from the delivery mechanism.** The watcher would append
   events and stop caring who consumes them; adding a second delivery channel would not
   touch the watcher.

Both are legitimate goals. Neither requires Kafka.

### What it would not buy — the decisive point

**Kafka is pull-based. It cannot push to a phone.** A consumer process has to be running
and connected to read from the log, and that consumer would then have to call
ntfy/Pushover/FCM to actually reach the handset. So inserting Kafka does not remove the
always-on component described in section 5 — it *adds* one (the broker) on top of it,
and still leaves you needing a push service at the end of the chain. The architecture
goes from

```
watcher ──HTTPS──> ntfy ──FCM/APNs──> phone
```

to

```
watcher ──> Kafka broker ──> consumer process ──HTTPS──> ntfy ──FCM/APNs──> phone
```

Two more things to keep running, and the same phone-facing hop at the end.

The other properties are similarly unused. Ordering across partitions is irrelevant with
one flight and four event kinds. Throughput is irrelevant: OpenSky's anonymous quota is
roughly 100 polls a day, and a tracked flight produces on the order of *four* events in
its lifetime. Replay of a takeoff notification from three days ago has no value —
these events are worthless the moment they are stale.

### What it would cost

- **Self-hosted.** Modern Kafka in KRaft mode no longer needs ZooKeeper, which helps,
  but it is still a JVM service wanting on the order of a gigabyte of RAM, disk with
  retention management, and broker/topic configuration. It would not fit on an Oracle
  free-tier micro instance (1 GB); it would fit on the free ARM shape (2 OCPU / 12 GB)
  with room to spare. Either way it is a permanent server to patch and monitor, in
  service of a desktop utility that fires a handful of messages a day.
- **Hosted.** Options have thinned. **Upstash's serverless Kafka — the obvious "cheap
  managed Kafka" answer — entered deprecation on 2024-09-11 and was discontinued
  2025-03-11.** Confluent Cloud, Aiven and Redpanda Cloud offer trial credits rather
  than a durable free tier; expect a recurring bill in the tens of dollars per month
  once credits lapse. That is an order of magnitude more than the entire rest of this
  system costs.

### The lighter middle ground, if the real goal is decoupling or reliable delivery

This is the useful part of the question. Ranked by how little they cost:

1. **Realise that ntfy already is the broker.** ntfy is publish/subscribe over HTTP:
   the app publishes to a topic, the phone subscribes to it, they never talk directly,
   and messages are cached server-side (12 h by default) so a phone that is off or out
   of signal receives them on reconnect. That is topic-based decoupling with retention —
   the two Kafka properties that mattered — delivered as a free HTTP POST with no
   infrastructure. If the instinct behind "Kafka or something like that" was "I want a
   broker in the middle", the recommendation in section 1 already satisfies it.
2. **A local outbox with retry, if you want delivery durability inside the app.** Append
   each event as a JSON line to a file next to the existing
   `flighttrack-watch-state.json`, mark it delivered on success, and re-attempt
   undelivered lines on the next poll tick. This is perhaps 60 lines of Go, adds no
   infrastructure, and fits the codebase's existing habit of persisting state to disk
   atomically via temp-file-and-rename. It gives you the durability property Kafka was
   being considered for, for this workload's actual volume.
3. **MQTT (Mosquitto) or NATS, if you specifically want a broker you own.** Both are
   single small binaries — Mosquitto is a few megabytes of C, NATS a single Go binary —
   with a fraction of Kafka's footprint, and MQTT has retained messages and QoS levels
   that map well onto "tell my phone when it reconnects". Still needs an always-on host
   and an MQTT client app on the phone, which is strictly more setup than ntfy for
   strictly less phone-side polish. Reasonable if the Pi in section 5 already exists.
4. **Redis Streams / Postgres-backed queues** (`pgmq`, River). Sensible if you already
   run one of those. You don't, so this means a new dependency for no benefit.

### When Kafka would become the right answer

The requirement that flips this is **turning flighttrack into a hosted multi-user
service.** Concretely, Kafka starts earning its keep when several of these are true:

- Many users' flights are being polled centrally, producing events continuously rather
  than in bursts of four.
- **Several independent consumers** need the same event stream: a push-delivery service,
  an email digest builder, a history/analytics store, a model that improves ETA
  predictions from observed arrivals. This is the single strongest signal — one producer
  and one consumer is a function call; one producer and five unrelated consumers is a log.
- You need **replay/backfill**: a new consumer must process the last 30 days of events
  when it is deployed, or a consumer bug requires reprocessing.
- Delivery must survive consumer downtime with an audit trail, for support or billing.
- Volume reaches tens of thousands of events a day and the delivery workers need to
  scale independently of the pollers.
- There is a team, or at least an on-call rotation, to operate it.

Even then, the honest sequencing is: start with a Postgres-backed queue or a managed
queue (SQS, Google Pub/Sub, NATS JetStream), and move to Kafka when the *fan-out to
independent consumers* requirement actually appears. Most services that adopt Kafka
early would have been fine with a database table for another two years.

**Verdict: no for the current app, and the reason is that Kafka solves fan-out and
replay, while this app's actual problems are "reach a phone" and "be running at all".
ntfy solves the first for free and section 5 addresses the second. Revisit if
flighttrack becomes a hosted service with multiple downstream consumers.**

---

## 5. The real gap: notifications only fire while the app is running

Both event sources — `watcher.poll` in `internal/watch/watcher.go` and the Bubble Tea
model's `phaseChangeCmds` in `internal/ui/update.go` — only produce events as a side
effect of a poll that the running process performs. Close the terminal and the flight
takes off unobserved. A phone notification is most valuable when you're away from the
desk, which is exactly the state in which nothing is polling. **No choice of notifier
fixes this.** Something has to be awake and polling OpenSky.

Three things make this less painful than it sounds:

- The event logic is already headless. `flighttrack watch` is a plain CLI loop with no
  TUI dependency, so "run it somewhere else" is a deployment question, not a code
  question.
- State already survives restarts via `-state-file`, with a debounce counter that is
  deliberately reset on load — so a watcher that is restarted doesn't re-announce a
  takeoff it already reported.
- Go cross-compiles trivially. `GOOS=linux GOARCH=arm64 go build ./cmd/flighttrack`
  produces a static binary for a Raspberry Pi from the Windows machine with no toolchain
  setup.

### Options

| Option | Cost | Effort | Verdict |
|---|---|---|---|
| **Leave `flighttrack watch` running on the desktop** | Free | None | The honest first step. Real caveat: Windows sleep suspends the process, so it only works if the PC stays awake. Register it as a Scheduled Task at logon so it survives reboots. Also mind the OpenSky quota — anonymous access is ~100 polls/day, so a 90 s interval exhausts it in about 2.5 hours; the code already warns about this. Register free OpenSky API credentials before doing this in earnest. |
| **Raspberry Pi / old laptop / NAS at home** | ~£15–£80 once (a Pi Zero 2 W is ample), then pennies of electricity — 2–5 W | An evening: cross-compile, copy the binary, write a ~10-line systemd unit | **Best value, and the recommendation** if the desktop-always-on option isn't acceptable. Genuinely free ongoing, no cloud account, OpenSky credentials never leave the house, and it pairs naturally with a self-hosted ntfy later if privacy matters more than convenience. |
| **Small VPS** | Hetzner CX22 ≈ €3.79/mo; Oracle Cloud Always Free ARM = £0 | Similar to the Pi, plus provisioning | Fine. On Oracle's free tier read the small print: Always Free ARM is now **2 OCPU / 12 GB** total, and Oracle **reclaims instances whose 95th-percentile CPU, network and memory utilisation all stay under 20 % over 7 days** — a process that sleeps 90 s between HTTP calls is exactly that profile. So "free forever" is conditional, and a reclaimed instance means silently missed flights. Hetzner at €3.79 is more honest money for less anxiety. Fly.io is no longer an option here: its free allowances were discontinued in October 2024 in favour of a short trial. |
| **GitHub Actions cron** | Free on public repos; **effectively not free on private ones** | Medium — plus solving state persistence | **Poor fit; I'd rule it out.** Four problems. (a) Minimum interval is 5 minutes and scheduled runs are routinely *delayed* under load, especially on the hour — bad for a time-sensitive takeoff alert. (b) Private-repo minutes: the Free plan includes 2,000 Linux minutes/month; even a 15-minute cron burning one minute a run is ~2,880 minutes/month, so you are over the allowance, and the default $0 spending limit means jobs simply start failing. (c) Public repos get unlimited minutes but then your configuration is public, and **scheduled workflows are auto-disabled after 60 days without repository activity** — silent failure again. (d) There is no persistent disk, so `-state-file` has to be committed back to the repo or shoved into the Actions cache. |
| **Cloud Run + Cloud Scheduler (or Cloudflare Workers cron)** | Genuinely free at this volume — Cloud Run's always-free tier is 2M requests/month, Cloud Scheduler gives 3 free jobs per billing account; Cloudflare Workers' free plan allows cron triggers within a 100k requests/day allowance | Highest of the realistic options | Workable but a project. Cloud Run can run the existing Go binary as a container triggered by Scheduler, with state in a small bucket or Firestore — that's real but not enormous work. Cloudflare Workers is worse: it would mean porting the poller to JavaScript or fighting Go-to-WASM, plus KV for state. Both mean OpenSky credentials live in someone's cloud. Reach for this only if you specifically want no hardware at home. *Free-tier figures here are the ones I am least confident about — verify before committing.* |

### Recommended sequencing

1. Add ntfy delivery (section 6). Test it by running `watch` on the desktop.
2. If it proves useful, get OpenSky API credentials so the poll interval can be sane.
3. Move `flighttrack watch` onto a Pi or an always-on home machine under systemd. Nothing
   about the notifier changes — that is the point of having chosen it first.
4. Only if privacy becomes the dominant concern, self-host ntfy on that same box and
   change one URL.

---

## 6. Concrete sketch: adding a `notify.Ntfy` notifier

### New file: `internal/notify/ntfy.go`

```go
// Ntfy publishes events to an ntfy topic (ntfy.sh or a self-hosted server).
type Ntfy struct {
    Base   string // e.g. "https://ntfy.sh" — scheme+host, no path
    Topic  string // e.g. "flighttrack-7Xq2..."
    token  string // optional access token, sent as X-Access-Token
    client *http.Client
}

// NewNtfy splits a topic URL such as https://ntfy.sh/mytopic into a server
// base and a topic, applying the same transport rules as NewWebhook.
func NewNtfy(rawURL, token string) (*Ntfy, error)

func (*Ntfy) Name() string { return "ntfy" }
func (n *Ntfy) Notify(e Event) error
```

Design points, each with a reason:

- **`Notify` should POST a JSON document to the server *root*, not to the topic URL**
  — `{"topic": ..., "title": ..., "message": ..., "priority": ..., "tags": [...]}`.
  The alternative is setting `X-Title`/`X-Priority`/`X-Tags` headers on a POST to the
  topic URL, but HTTP header values are ASCII, and titles built from `obs.Describe()`
  may not be. The JSON form is UTF-8-clean and avoids the whole question. Marshal a
  small unexported `ntfyMessage` struct; do not hand-build the JSON.
- **Reuse the existing URL validation.** Extract the scheme/host switch currently inside
  `NewWebhook` (notify.go lines 132–144) into an unexported helper — something like
  `func secureURL(raw string) (*url.URL, error)` — and have both `NewWebhook` and
  `NewNtfy` call it. This is a pure refactor: the existing
  `TestNewWebhookRejectsInsecureURLs` and `TestNewWebhookAcceptsHTTPSAndLoopback` should
  pass untouched, which is the check that the refactor was faithful. It also means a
  self-hosted ntfy inherits the HTTPS-except-loopback rule for free.
- **Run both fields through the existing `sanitize`.** Callsigns come off a public feed;
  the file's own comments make the point that untrusted text is scrubbed before it
  reaches a UI. A notification body on a phone is a UI.
- **Map `Event.Kind` to an ntfy priority and tag.** A table, not a chain of ifs:
  `signal_lost` and `arriving` → priority 4 (high) so they break through Do Not Disturb;
  `takeoff` and `landing` → 3 (default); `tracking` → 2 (low), since "now tracking" is
  informational and should not buzz a pocket. Tags give the notification an emoji:
  `airplane_departure`, `airplane_arriving`, `warning`. Default unknown kinds to
  priority 3 rather than erroring, so a future event kind still gets delivered.
- **Reuse the `Webhook` client shape**: a `*http.Client` with a ~20 s timeout, read and
  discard a bounded amount of the response body (as `Webhook.Notify` does at line 168 —
  this matters for connection reuse), and turn any status ≥ 300 into an error so `Set`
  records it.
- **Token handling.** Take the token from an environment variable
  (`FLIGHTTRACK_NTFY_TOKEN`) only, never a command-line flag — flags land in the process
  table and in shell history. Note that the free tier does not need a token at all; this
  is for reserved topics or a self-hosted server with ACLs.

### Wiring: `internal/watch/watch.go`

- Add `-ntfy-url` to the flag block (watch.go lines 34–43), defaulting to
  `os.Getenv("FLIGHTTRACK_NTFY")`, exactly mirroring how `-webhook-url` defaults to
  `FLIGHTTRACK_WEBHOOK` at line 38.
- Add `case "ntfy":` to `buildNotifiers` (lines 165–192), alongside the existing
  `"webhook"` case, returning the error from `NewNtfy` unchanged so a bad URL is a clean
  exit-code-2 startup failure rather than a runtime surprise.
- Update the `-notify` usage string at line 37 to list `ntfy`, and the example in
  `main.go`'s usage text.

Nothing in `watcher.go` changes at all — `w.fire` already fans out through `notify.Set`,
and `Set.Notify` already continues past a failing sink, which is the behaviour you want
when the phone path is down but the console still works.

### Wiring: the TUI

`internal/ui/model.go` currently holds a single `webhook *notify.Webhook` field and
`model.send` builds a fresh `notify.Set` per event from `m.toast` plus that optional
webhook. Two ways forward:

- *Minimal:* add a parallel `ntfy *notify.Ntfy` field and a second `if != nil { set.Add }`
  in `send`. Two lines, matches the existing shape.
- *Slightly better:* replace both pointer fields with `extra []notify.Notifier` (or a
  prebuilt `*notify.Set`) constructed once in `cmd/flighttrack/main.go`, so `send`
  becomes `notify.NewSet(append([]notify.Notifier{m.toast}, m.extra...)...)` and adding
  a third sink later needs no change to `ui` at all. Given that this is the second
  optional sink, I'd take this one — it is the point at which the pointer-per-sink
  pattern stops paying for itself. It also removes a duplicated construction path
  between `watch` and `ui`.

Either way `main.go` gains an `-ntfy` flag next to the existing `-webhook` flag
(main.go line 110) and the same construct-or-exit-2 handling at lines 126–133.

### Tests to add, in the style of `notify_test.go`

- `NewNtfy` rejects the same bad URLs `NewWebhook` does (`http://` to the internet,
  `ftp://`, `file://`, empty host) — the natural way to prove the extracted `secureURL`
  helper is shared rather than duplicated.
- `NewNtfy` correctly splits `https://ntfy.sh/mytopic` into base and topic, and rejects
  a URL with no topic path.
- An `httptest` server asserting the posted JSON carries the right `topic`, `title`,
  `message`, and that the request went to `/` — the direct analogue of
  `TestWebhookPostsJSON`. (`httptest` serves on 127.0.0.1, which the loopback exception
  already allows, so this works without weakening validation for tests.)
- A table test over `Event.Kind` → priority, including an unknown kind defaulting to 3.
- A 500 response surfaces as an error, mirroring `TestWebhookReportsServerErrors`.

### Effort estimate

Roughly 60 lines of implementation, 20 lines of wiring, 80 lines of test, plus a
paragraph in the README. Half a day including the manual end-to-end check against a real
phone. The `secureURL` extraction is the only change to existing code and it is
behaviour-preserving.

---

## 7. Things I am not certain about

Flagged rather than guessed:

- **ntfy.sh free-tier limits** (250 messages/day etc.). Sourced from a GitHub issue whose
  entire point is that these limits are not clearly published. Immaterial at this
  volume, but do not quote them as fact.
- **Whether ntfy returns a JSON body posted to a topic URL as literal message text.**
  The documentation says JSON is only parsed at the root URL, which implies yes, but the
  "zero code today" claim in section 1 rests on it. Test with `curl` first.
- **Cloud Run / Cloud Scheduler / Cloudflare Workers free-tier figures.** Assembled from
  secondary sources and summaries; cloud free tiers change quietly. Verify on the vendor
  pricing page before building on them.
- **Oracle's Always Free ARM allocation** (2 OCPU / 12 GB) — this appears to be a
  reduction from the previously widely-cited 4 OCPU / 24 GB. The current Oracle
  documentation supports the lower figure, but check your own tenancy.
- **Pushover's per-platform pricing.** Stated as $4.99 one-time per platform; whether a
  purchase covers a family/multiple devices on the same platform was not verified.
- **Whether the target phone is iOS or Android.** Not established. It changes whether
  Gotify and UnifiedPush are viable at all, and it changes which push relay ultimately
  sees the message.
- **The user's tolerance for leaving a machine on.** The entire section 5 recommendation
  hinges on it and it wasn't stated.

---

## Sources

- [ntfy — Publishing documentation](https://docs.ntfy.sh/publish/)
- [ntfy — FAQ (FCM relay, topic security, message caching)](https://docs.ntfy.sh/faq/)
- [ntfy.sh — pricing](https://ntfy.sh/)
- [ntfy issue #1167 — free tier limits are not documented on the homepage](https://github.com/binwiederhier/ntfy/issues/1167)
- [gotfy — Go client for ntfy](https://github.com/AnthonyHewins/gotfy)
- [Pushover — pricing](https://pushover.net/pricing)
- [Gotify / UnifiedPush Android status](https://unifiedpush.org/users/distributors/gotify/)
- [Carrier email-to-SMS gateway shutdown status, 2026](https://sigspan.com/carrier-gateway-shutdown)
- [SMSEagle — AT&T, Verizon and T-Mobile email-to-text withdrawal](https://www.smseagle.eu/2025/05/08/no-support-for-email-to-text-from-att-verizon-and-t-mobile-what-are-your-alternatives/)
- [Firebase — migrate from legacy FCM APIs to HTTP v1](https://firebase.google.com/docs/cloud-messaging/migrate-v1)
- [MagicBell — PWA iOS push limitations, 2026](https://www.magicbell.com/blog/pwa-ios-limitations-safari-support-complete-guide)
- [Upstash — announcing Upstash Workflow and deprecating Upstash Kafka](https://upstash.com/blog/workflow-kafka)
- [Oracle Cloud — Always Free resources and idle reclamation policy](https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm)
- [GitHub Docs — events that trigger workflows (`schedule` limitations)](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows)
- [GitHub Docs — about billing for GitHub Actions](https://docs.github.com/billing/managing-billing-for-github-actions/about-billing-for-github-actions)
- [Fly.io — discontinued plans](https://fly.io/docs/about/discontinued-plans/)
- [Google Cloud — free tier features](https://cloud.google.com/free)
- [Google Cloud Scheduler — pricing](https://cloud.google.com/scheduler/pricing)
- [Cloudflare Workers cron trigger limits, 2026](https://runhooks.app/blog/cloudflare-workers-cron-triggers-limits/)
