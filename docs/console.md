# The console

`http://127.0.0.1:8090` after `make up`, or `make console`.

A control panel for the lab — the thing that turns a set of containers and
a page of `curl` invocations into something you can hand to someone who
has not read the repo. Views: **System in test** (the home page — one run as
a sequence diagram), **Vendors**, **Platform**, **Verification**, **Banks**,
**Merchants**, **Configuration**.

## What it is, and what it deliberately is not

The console is a **client of every other service and nothing more**. Every
button is exactly one HTTP call to a service's own public API — the same
call a human with `curl` would make, against the same surface a real
vendor would expose.

That constraint is the whole design, and it is worth stating plainly:

- **Nothing in this lab is reachable only through the console.** If a
  state could only be produced by clicking, that state would be untestable,
  and the harness — which is what actually judges this repo — could never
  assert on it.
- **The console being down is never why a scenario cannot run.** It is not
  a dependency of anything. `make harness` does not know it exists.
- **It holds no vendor contract.** Nothing real-shaped lives here. The
  contracts live in `worldline`, `b4b`, `banking-circle` and `aci`, which
  is where you would go to check them against a vendor's documentation.

It owns exactly one piece of state of its own: the merchant registry, in
`console-data/registry.json`. That is a plain file you can read, diff and
delete.

## System in test

The home view, and the one to open first: a **sequence diagram of one
settlement run**. The platform sits in the middle, the vendors it talks to
either side, and each hop between them is an arrow that lights as its own
traffic arrives — the SFTP pull, the payouts into B4B, B4B's bridge into
Banking Circle, the lifecycle callbacks coming back, the notification
batches going out, and the confirmations coming back in.

**The last arrow closes the loop, and it is the one worth watching.**
Banking Circle delivers the same encrypted batches to two places: the lab's
own receiver stub, and whatever the system under test subscribed. Only the
second is evidence that the platform heard anything, so they are two
arrows, split by the endpoint Banking Circle recorded on each delivery. A
green **notification batches** with a grey **payout confirmations** under
it is a complete picture of a real failure: the bank sent it, the lab took
it, and nothing of yours did. A batch whose endpoint cannot be read is
counted on the lab's side — the confirmations arrow claims your listener was
called, and that claim is never a guess.

The lab clock heads the diagram — UTC, Paris, Stockholm and London,
shifted or not, business day or not (amber when it is not, naming the
calendar), held or not — with a link to move it. The platform is whichever
registered plugin settles ([plugins.md](plugins.md)); with none registered
the diagram still draws the vendors' side. And it is drawn as what it is, the system under test:
a wider box with a thicker border and a purple wash, and a solid purple
lifeline where the vendors' are dashed grey.

**The bottom arrow is the reconciliation sweep**: *intraday reconciliation*,
from Banking Circle to the platform, lights when the platform reads the
intraday report — about an hour after a run, or when the lab clock is
moved forward and the sweep triggered. It counts intraday report reads only;
the status fallback and the rejection report are in the Banking Circle
card's *Reconciliation reads* panel. A refused read shows amber.

It is drawn by hand in SVG rather than with a diagram library, for the
reasons in [design-system.md](design-system.md#sequence-diagram): no CDN is
allowed here, and a library that re-renders from a text description cannot
animate one arrow while the others hold still.

**What the colours mean** (they are the design system's, and they are all
data): grey is *nothing has come this way*, green *is happening now* —
one message or a whole batch — dark green *done and fine*, amber *partly
refused*, red *broke*. Everything at rest is grey so that the one
thing that just moved is the one thing you see. A state is held for at
least a second with a glow that ramps up and back down, because a hop that
takes three milliseconds is still worth looking at.

Boxes carry the participant's own title, role, health dot and two counters,
with a `+N` for what this run added. A **red box means that service is
unreachable** — not that something that touched it went wrong; the arrows
carry those verdicts.

At the bottom, **Run report** expands into what the run did: files
collected, stages completed, merchant payouts and the amount settled,
payouts at the bank, callbacks delivered, notification batches,
confirmations to the platform, and **payouts confirmed** as "2 of 6" — the
bank's verdict as the platform's own books record it, reported even when it
is zero, because zero is the number somebody is looking for. Fees are
deliberately absent — they are computed inside the platform's own workers
and never leave them, and the panel says so rather than inventing a figure.

The data is `GET /api/flow`, which reads the vendors' activity rings and the
platform's stage chain (its plugin's `status_path`) and puts them in the shape a sequence needs. The server
counts; the browser remembers what the count was a second ago and lights
what changed.

## Vendors, Platform, Verification

Three views over the same catalogue, one per kind — the distinction
[catalogue.md](catalogue.md#which-half-is-which) draws, promoted from
headings on one page to the navigation itself. They used to be three
sections under a nav item called *Standalone*, a name left over from an
architecture this tree no longer has.

- **Vendors** — the third parties the lab simulates. The deliverable.
- **Platform** — your own stack: the platform under test, which registers
  its own card here and is listed first because it is the thing under test,
  then the lab clock, then the stand-ins that exist so a hop can be proved
  connected.
- **Verification** — the checks a merchant passes before any money exists.
  Their own view rather than filed under vendors, because they are what an
  operator goes and flips an outcome on.

Each view carries four tiles: up, down and not reporting **for that
section**, and the whole lab's `up/total` in accent. Splitting the list took
away the one screen that answered *is everything up?*, and that answer is
worth keeping somewhere you always are. The nav badges carry the same
per-section count.

Each row carries a live health state, the last two minutes of probe
outcomes as a sparkline, and the round-trip latency. Expanding one shows
where it is reached, its ports and transport, how it authenticates, its
main endpoints, and — the field that matters most — **how to point it at
the real thing**.

Four states, and the difference between them is deliberate:

| State | Means |
| --- | --- |
| **up** | answered 2xx |
| **down** | refused, timed out, or answered non-2xx — the reason is shown, because a 401 from a credentialed listener is a different problem from a refused connection |
| **unknown** | not probed yet |
| **not probed** | no health endpoint at all (the CA is a one-shot script) |

A service that has not been probed yet is never reported as down. A status
page that cries wolf on start-up is a status page people learn to ignore.

### Activity: what the platform just did

Three vendors keep a short history of the traffic that reached them, and an
expanded card shows it as a panel per conversation:

| Service | Panels |
| --- | --- |
| **B4B Payments** | *Payouts received* — every call to the payments API and what was answered · *Callbacks sent* — each lifecycle callback, and whether the client took it |
| **Banking Circle** | *Payments received* — payouts arriving over the lab bridge, and money landing on the safeguarding accounts · *Notifications sent* — each encrypted batch, its event types, and the endpoint's answer, plus whatever a paused subscription is holding · *Reconciliation reads* — every intraday report, rejection report and payment-status call: when, what was asked (dates, page, properties) and what went back; a refused one amber with the parameters it lacked · *Payouts* — the outgoing payments, with *Return* and *Reverse* on the processed ones |
| **Worldline** | *File exchange* — every SFTP session the platform opened, and the files it listed, collected or delivered |

This exists because the three most common questions during a run —
*did the payout arrive, did the callback get taken, did anyone actually
collect the file* — were each answerable only by tailing a container's
stdout, and one of them (a callback refused at the client's door) is
completely invisible from the settlement side. A payout that was accepted
perfectly and then had every callback rejected looks, from the platform,
exactly like nothing happening.

The panels read from each service's own `GET /sim/activity`, which the
console proxies at `/api/services/{id}/activity`. Nothing is persisted:
it is a ring of the last few hundred events per log, so it is a window onto
a run and never a record of one. Refusals are amber and failures red;
an ordinary accepted call is left uncoloured, so the two that went wrong
are the two you see. A service that keeps no log shows no panel, and its
endpoint answers `501` rather than an empty list.

Three cards offer actions, because they are the ones that drive the money:

- **Worldline** — run the morning (`ER`) or afternoon (`AR`) cycle now,
  instead of waiting for 08:00.
- **Settlement** — pull from Worldline now, instead of waiting out
  `WORLDLINE_PULL_INTERVAL`.
- **Banking Circle** — *Fund safeguarding accounts*: credit each empty
  safeguarding account 1,000,000 through the bank's internal bridge
  (`CONSOLE_BC_INTERNAL_URL`), the wire Worldline makes on its morning slot.
  An account already holding money is left alone, and the toast says which.

And a registered platform brings its own.

### The platform under test: a plugin

The console knows no platform by name. A platform **registers its own
card** — `POST /api/plugins` with a descriptor, renewed every ten seconds —
and the console draws it at the top of the Platform view with the same
components as every other card: summary, where it is reached, its logs as
activity panels, its endpoints, and the buttons it declared, each forwarded
to the path it declared and nowhere else. [plugins.md](plugins.md) is the
contract; buddy's `infinite-local-runner` is the reference plugin and
registers on start (`pnpm nx up infinite-local-runner`).

A card's **Registered** row says how that stands: *live* (renewed within
thirty seconds), *lapsed* (stopped renewing) or *stopped* (it said goodbye
on shutdown). A lapsed or stopped platform is grey, not red — it is not a
lab service that is down — and shows the platform's own words on how to
start it where its buttons were. No platform registered is a note on the
Platform view saying how to register one.

A platform that settles gets the **settlement files** table: one row per
file it can run (its `files_path`), with currency, the MIDs it pays, its
total, and its last run (root, payouts, when).

- **Run** on a row — one `POST /api/plugins/{id}/run {"files":[name]}`,
  forwarded to the platform's `run_path`. The runner uploads the file under
  a name not used before, triggers the pipeline, waits for the balance
  check, funds the safeguarding accounts, ticks the check and follows the
  stages to the end. It answers `202` at once: the run takes about forty
  seconds, and the part worth watching is the traffic arriving in the vendor
  panels, not a spinner. While it runs it **holds the lab clock**.
- **Run all N together** — still one call, with every file that can run
  (the first free, seeded one per currency). The platform starts them in
  the same moment, which is how two currencies arrive on a real morning and
  the case a single run never exercises.

A row has no *Run* while its currency has a run in flight (one per
currency; a second is the platform's `409`). A file whose MIDs have no
merchant would settle nothing, so it has no *Run* either: the row says which
MIDs are missing and a note gives the platform's seed command.

buddy's runner declares one more button, **Run reconciliation sweep**
(primary): one tick of the platform's hourly BC payment reconciliation,
which is what moves payouts from `IN_PROGRESS` to `SUCCESS` (or
`REJECTED`). A tick takes about twenty seconds, so the runner answers `202`
at once and the outcome appears in its **Reconciliation sweeps** panel —
per root, how many payouts were open and how many it resolved; amber when
some stay open. A root is only swept an hour after its settlement *by the
lab clock*: after a run, press *+1 hour* on the Lab clock card, sweep, then
*Now*.

### The lab clock

Settlement only runs on a business day, which makes the calendar a test
input rather than an obstacle. The **Lab clock** card (the `clock` service,
`:8096`) says what time the lab thinks it is — in UTC, and in Paris,
Stockholm and London, where the platform's and the bank's calendars live —
whether it is a business day on every calendar (GB and SE, with their bank
holidays), and whether anything holds it. The buttons — *Now* (back to the
real clock), *+10 min*, *+30 min*, *+1 hour*, *±1 day*, *Nearest business
day*, *Move to Sunday* — and the *Date (UTC)* / *Time (UTC)* fields with
*Set clock* are each one `POST /api/clock`, forwarded to the clock
service's `POST /clock` (`advance`, `at` or `mode`). Its *Clock changes*
panel is every move and every hold.

Every vendor follows it (`LAB_CLOCK_URL`), so bookings, files and report
dates move together, within a second and without a restart; a registered
platform with `"clock": "follows"` follows the same service — buddy's
runner writes the offset into the file every one of its processes watches.
The lab starts on *auto-business-day* (`CLOCK_START` in compose.yml): the
most recent business-day instant, so a weekend evening still settles.

A move is refused while a settlement run holds the clock (the clock
service's `409`, with the holder's reason), and while a platform registered
on the wall clock is live — that platform cannot follow, and moving the
vendors would put them on a different day from it.

Moving the clock drives scheduled vendor work too: move past Worldline's
morning or afternoon slot (08:30 / 15:30) and that slot's file is delivered
within a few seconds, as it would have been when the day got there.

The scenario worth knowing: move to a Sunday and run, and the balance check
completes as `NON_BUSINESS_DAY` with **0 payouts**; advance a day and run
again, and the same money settles — in one measured pair, 6 payouts of
55,397.56, exactly twice the usual daily figure, because Sunday's movements
were booked and not paid.

### Connect your platform: the .env and the keys

Pointing a platform at the lab takes a handful of variables and, for three
vendors, key material. Each vendor card carries a nested **Connect your
platform** panel: which variables it sets, in the platform's own names
(`BC_API_BASE_URL`, `B4B_JWT_PRIVATE_KEY`, `WORLDLINE_SFTP_HOST_KEY_FINGERPRINT`
…), a **Download fintechlab-<vendor>.env** link, and — for Banking Circle,
B4B and Worldline — each key or certificate file on its own row. The
**Configuration** view carries it for every vendor at once, with
**Download fintechlab.env** — it is the lab's, not any one platform's.

- **Keys are live, the rest is compose.yml.** PEMs and base64 certificates
  are read from the lab's keys directory at the moment you click, so they
  are the ones it is running with; the SFTP host key's fingerprint is
  derived from its public key. Addresses, account ids and lab credentials
  are what compose.yml ships — the console cannot read another container's
  environment — and a test holds the two together.
- **The host is the one you came in on.** Open the console at
  `lab.example.test:8090` and the file says `lab.example.test`. `?host=` on
  any of the links picks another.
- **Choices are commented out.** `NODE_EXTRA_CA_CERTS` is a path, not a
  value; the AML stub replaces the platform's own verification-service. Both
  are in the file with a note, and neither is set.
- **A key not generated yet** is a note on its row and in the file, never a
  blank that looks like a value.
- Only client-side files are offered. See
  [security/ca-and-tls.md](security/ca-and-tls.md#what-the-console-hands-out).

The same thing, headless:

```sh
curl -OJ localhost:8090/api/connect/env                         # every vendor → fintechlab.env
curl -OJ localhost:8090/api/connect/env/banking-circle          # one vendor
curl -OJ localhost:8090/api/connect/files/banking-circle/fintechlab-ca.pem
curl -s  localhost:8090/api/connect                             # what each kit sets and offers (no values)
```

### Banking Circle: who is subscribed

Expanding the Banking Circle card shows its notification subscriptions
before its activity panels: the **endpoint** it will POST to, the **event
types** behind it (an inactive one is amber, and a count appears when the
event is narrowed to specific targets), the subscription's **status**, and
the **queue depth** if anything is not moving.

The queue column names two different stalls, because they are undone
differently. **Retained** is what the vendor kept when it gave up on an
endpoint that never answered and deactivated the subscription; it clears by
reactivating. **Queued** is what an operator paused; it clears by pressing
release.

This is the half of the vendor that is invisible until it is wrong. A
subscription is a URL the bank calls, and *nobody subscribed* and
*subscribed, pointing at the wrong host* both look the same from the
settlement side — silence. A service that subscribes on boot (as
`apps/banking-circle` does: authenticate, list, subscribe if there is none)
will have pointed its `BC_*` configuration somewhere else, and the empty
state says so rather than showing a blank table.

**Send test** fires the vendor's own
`POST /api/v1/notificationselfservice/clienttest/{id}` — a reachability
probe, not an event, so it is not filtered by event type or target. The
vendor answers `200 "sent"` whether or not anything took it, so the console
then reads Banking Circle's own notification log and reports what the
endpoint actually did:

```
Endpoint took it     batch of 1 to https://receiver:8443/raw-events — PaymentStatus
Nothing took it      … — Post "https://receiver:8443/raw-events": dial tcp: lookup …
```

### Pausing notifications

**Pause** stops Banking Circle delivering to one subscription. Everything it
would have sent waits, in order, and **Release** sends it — cut into the
same batches it would have gone out in, oldest first.

This exists because a webhook that arrives four milliseconds after the
event it describes is a webhook nobody can watch arrive. Paused, the gap
between "the bank did something" and "the platform was told" is as wide as
you want it, which is where the interesting questions live: what does the
platform show while a payout is settled at the bank but unconfirmed, does
it recover when eleven notifications land at once, and does it cope with
them arriving in order but late. Breaking a subscriber to find out means
breaking it, and a broken subscriber also loses the notifications.

The pause is the vendor's own switch —
`POST /sim/subscription/{id}/pause` and `/resume`, under `/sim` because the
real API has nothing like it — not something the console remembers. That
matters: while it is paused, the queued notifications appear in **Notifications
sent** below, amber, named by event type, with the number still waiting:

```
notification.queued   queued: 2 notification(s) for … — OutgoingPaymentBooked ×2 — delivery is paused
notification.pause    delivery to … resumed — 2 notification(s) released
notification          batch of 2 to … — OutgoingPaymentBooked ×2
```

A pause that hid the notifications would be a worse lie than a broken
endpoint, since a quiet log reads as *nothing happened*. It stops delivery
and nothing else: payments still process, notifications are still produced,
matched against subscriptions and ordered. They just wait.

### Payouts: return and reverse

The card's **Payouts** panel lists Banking Circle's outgoing payments,
newest first, and offers the two things that can happen to one after it
has been processed. **Return** is the beneficiary's bank sending it back:
the payout stays processed, and the money arrives as a new incoming payment
flagged `return`, the way the real bank models it. **Reverse** is the
scheme undoing it: the payout becomes reversed, with a second booking.
Only a processed payout that has not already come back gets the buttons;
the console decides that from the vendor's own record, so it never offers
a call the vendor would refuse.

Each button is one call to the vendor's own hook,
`POST /sim/payments/{id}/return` or `/reverse` on the credentialed listener
(the same handlers as `/internal/payments/{id}/…` on the bridge), through
`POST /api/banking-circle/payouts/{id}/return|reverse` here. Both are
irreversible, so the confirm says so rather than asking whether you are
sure. Pair either with a paused subscription to watch the platform find out
late.

## Banks

Two "sim banks", and they are not the same kind of thing:

- **Core ledger** (`bank`) — scaffolding. A generic in-memory bank with
  fake `GB00SIM…` IBANs. You can open accounts here.
- **Banking Circle** — a vendor. Its accounts are where the safeguarding
  money actually sits. Read over mTLS with a real `Basic` → `Bearer`
  exchange, exactly as a client would; the console has no privileged back
  door into a vendor's state, which is what keeps the view honest.

Banking Circle has **no account-opening endpoint**, so the console does not
offer one, and says why. An account there appears when it is first paid
into. The API returns `501`, not `500`, for that call: a UI that cannot
tell "this vendor has no such endpoint" from "this call failed" will offer
a retry button forever.

## Merchants

The hierarchy the platform sells through: **distributor → partner →
merchant → outlet**. The outlet is the unit the money cares about —
Worldline calls it a submerchant and identifies it by **MID**, and the
platform pays out per MID, not per merchant.

Before this view, MIDs were string literals in tests. That is fine for a
scenario and useless for a demo, where the first question is "who am I
paying?".

Creating a merchant generates everything you did not supply — address,
post code, MCC, contact, and per outlet a MID, a terminal id and a payout
account. All of it is **derived from the record's own id**, not drawn from
a random source, so the same merchant always looks the same: a screenshot,
a reloaded registry and a test fixture agree. All of it is obviously fake —
`GB00SIM` accounts, `.test` domains (RFC 2606, so they can never be
mailed), streets nobody can post to.

Per merchant:

| Action | What it actually does |
| --- | --- |
| **Register outlets at B4B** | `POST /oversight/v1/beneficiaries` per outlet, keyed on the MID, signed with the same RS512 JWT settlement uses. Results are reported per outlet: a partial run is a normal outcome and you need to see which one failed. |
| **Seed card payments** | `POST /sim/transactions` on Worldline. This is the **acquirer's** own data posted to the acquirer — the platform never learns of a transaction except through the settlement file, and nothing here shortcuts that. Dated yesterday, because Worldline settles T+1. |
| **Sanctions dropdown** | `PUT /sim/beneficiaries/{mid}/sanctions`. Move an outlet to `fail` and watch the payout gate stop it, rather than reading that it would. |
| **Suspend / Delete** | Registry-local. Deleting does **not** unwind anything already registered at B4B — the rail has no beneficiary-delete endpoint, and pretending otherwise would be a lie about what it supports. |

The registry is on disk and B4B's beneficiary store is in memory, so after
`b4b` restarts an outlet shown here as registered is one B4B has
forgotten. Re-registering is safe: a repeated `external_ref` corrects the
record rather than creating a second payee.

The `external_ref` is the **MID**, and that matters. B4B mints its own
beneficiary id, so a platform that keys payouts on its own reference has
only that reference to look up by — B4B resolves either. Without it,
`settlement` would look up an id nothing had registered, land on an
auto-vivified record, and pay against account details nobody supplied
while the registered ones sat unread. Which is precisely what it did
before the console existed to notice.

### The five-minute demo

1. **Merchants** → *Create merchant*, two outlets.
2. *Register outlets at B4B* — each MID comes back with a beneficiary and
   a `pass`.
3. *Seed 5 card payments / outlet*.
4. **Services** → Worldline → *Run morning cycle (ER)*. One file per
   currency, covering every MID that traded, PGP-encrypted onto Worldline's
   SFTP root, with the lump sum credited to the safeguarding account.
5. **Services** → Settlement → *Pull from Worldline now*. It dials SFTP,
   authenticates, downloads, decrypts, parses, splits per MID and pays each
   outlet through B4B.
6. **Banks** → Banking Circle — the safeguarding balance moved.

Every one of those steps is a plain HTTP call you can make yourself; the
console just saves you finding it.

## Configuration

Three things, kept strictly apart by whether they are **live** or merely
**documented** — a console that shows a stale value as if it were live will
send you debugging the wrong thing for an hour:

- **Banking Circle notification delivery** — the retry table, rendered
  with both the documented wait and the compressed one, so you can see that
  `time_scale` turns a 48-hour story into a few seconds. Asked of the
  *service* (`GET /sim/delivery-config`), not read from the file, because
  `BC_TIME_SCALE` in compose.yml overrides the file's `time_scale` — a
  panel that read only the file would confidently report a schedule nobody
  is on. The file is the fallback when the service cannot be reached, and
  says so. Read-only either way: `banking-circle` loads the file at
  start-up, so an edit that appeared to take effect immediately would be a
  lie. Edit it on the host and restart that one service.
- **Worldline file-exchange channel** — genuinely live and genuinely
  editable (`GET`/`PUT /config` on the worldline service).
- **Environment reference** — the knobs that shape the lab, grouped by
  service and described, so finding one does not mean grepping a 400-line
  compose file. Labelled as *what compose.yml ships*, never as a reading of
  the running container. The console's own environment is the one block
  shown live, and it says so.

## Configuring the console itself

| Variable | Default | What it does |
| --- | --- | --- |
| `LISTEN` | `:8090` | listen address |
| `CONSOLE_URL_<SERVICE>` | the published `127.0.0.1` port | where to reach a peer (`CONSOLE_URL_BANKING_CIRCLE`, etc.) |
| `CONSOLE_BROWSE_HOST` | `127.0.0.1` | the host the published ports are on, for the "open in a browser" links |
| `CONSOLE_STATE_FILE` | `console-data/registry.json` | the merchant registry |
| `CONSOLE_PROBE_INTERVAL` | `5s` | how often to health-check |
| `CONSOLE_BC_DELIVERY_CONFIG` | `config/banking-circle.json` | the delivery table to display |
| `CONSOLE_KEYS_DIR` | `keys` | the lab's keys directory, for the connect-your-platform downloads |
| `CONSOLE_BC_INTERNAL_URL` | `http://127.0.0.1:8095` | Banking Circle's internal bridge, for *Fund safeguarding accounts* |
| `CONSOLE_URL_CLOCK` | `http://127.0.0.1:8096` | the lab clock, for the clock card and `/api/clock` |
| `CA_FILE`, `CLIENT_CERT`, `CLIENT_KEY` | `keys/certs/…` | mTLS material for Banking Circle |
| `B4B_JWT_PRIVATE_KEY_PATH`, `B4B_JWT_KEY_ID` | `keys/b4b-keys/private.pem`, `b4b-mock-1` | signs the bearer token for beneficiary registration |

Missing credentials are **not** fatal. Without the B4B key, merchants can
still be created and the view says why they cannot be registered; without
the CA, the Banking Circle panels report the error instead of vanishing. A
control panel that refuses to start because one peer's credentials are
missing is worse than one that tells you which.

## The UI

One HTML file, one stylesheet, one script, embedded in the binary with
`//go:embed`. No framework, no bundler, no CDN — the lab's promise is one
`make up` on a laptop with no network, and a package registry in that path
would be the first thing to break. A test asserts the assets reference no
external host.

Dark by default with a light theme behind the toggle; purple is the accent
throughout, and status colour is reserved for status, so nothing decorative
is ever green or red.

The look and feel is written down in
[design-system.md](design-system.md) — tokens, layout, components, the
behaviour contracts (polling that does not eat a half-typed form, errors
shown verbatim, four health states) and the copy voice — so another app in
this family comes out the same without reading this one's stylesheet. That
document is the source of truth; `app.css` is one instance of it, and the
last section lists where the console has not caught up yet.
