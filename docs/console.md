# The console

`http://127.0.0.1:8090` after `make up`, or `make console`.

A control panel for the lab — the thing that turns a set of containers and
a page of `curl` invocations into something you can hand to someone who
has not read the repo. Views: **Pods** (composable mocked vendors), **Standalone** (deep
protocol `cmd/*` processes), **Banks**, **Merchants**, **Configuration**.

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

## Pods

The preferred model for mocked vendors: each pod is a recipe of

Kernels that declare `inputs:` on their recipe grow a form in their panel
(the ui-plane). File fields POST to the pod's public
`/pod/input/{kernel}/{name}?filename=` route; the console is only a client.

## Standalone (formerly Services)

Every deep-protocol process the lab still runs, grouped by which half it belongs to — vendor
simulations first, because they are the deliverable, then the platform
scaffolding, then supporting pieces. The grouping is the same distinction
[catalogue.md](catalogue.md#which-half-is-which) draws, and the console is
where most people will meet it.

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
| **Banking Circle** | *Payments received* — payouts arriving over the lab bridge, and money landing on the safeguarding accounts · *Notifications sent* — each encrypted batch, its event types, and the endpoint's answer |
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

Two services offer actions, because they are the two that drive the money:

- **Worldline** — run the morning (`ER`) or afternoon (`AR`) cycle now,
  instead of waiting for 08:00.
- **Settlement** — pull from Worldline now, instead of waiting out
  `WORLDLINE_PULL_INTERVAL`.

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
| `CA_FILE`, `CLIENT_CERT`, `CLIENT_KEY` | `certs/…` | mTLS material for Banking Circle |
| `B4B_JWT_PRIVATE_KEY_PATH`, `B4B_JWT_KEY_ID` | `b4b-keys/private.pem`, `b4b-mock-1` | signs the bearer token for beneficiary registration |

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
