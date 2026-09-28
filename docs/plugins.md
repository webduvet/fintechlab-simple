# Plugins: putting your platform in the lab's console

The lab simulates vendors. The system under test is somebody else's, so
its card in the console is too: a platform **registers itself** with the
console, and the console draws its card, forwards its buttons and puts it
in the System in test diagram. The lab knows no platform by name. A new
button on a platform is a change to that platform, never a release of the
lab.

buddy's `infinite-local-runner` is the reference plugin (its `src/up.ts`,
section *the lab's console*).

Two things a platform does with the lab, both outbound from the platform:

| | Call | Why |
| --- | --- | --- |
| Register | `POST {console}/api/plugins`, renewed every `renew_seconds` | its card, buttons and diagram |
| Follow the clock | `GET {clock}/clock`, every second | its business dates agree with the vendors' |

Neither needs the lab to reach the platform first, so both work from a
laptop to a lab on another machine. What does need the lab to reach the
platform is the card's live parts — its health, its logs, its buttons —
which the console calls at the `base_url` the plugin gave (see *Reaching the
platform*).

## Register

```http
POST /api/plugins
Content-Type: application/json

{ …descriptor… }
```

```json
{"id": "infinite-local-runner", "state": "live", "ttl_seconds": 30, "renew_seconds": 10, "clock": "follows"}
```

Send the same descriptor again every `renew_seconds`. It is the same call:
registering is renewing, and a changed descriptor replaces the old one. A
registration not renewed within `ttl_seconds` is **lapsed**; the card stays,
grey. On a clean shutdown:

```http
DELETE /api/plugins/{id}          → 204
```

and the card goes grey at once, **stopped**, showing the plugin's
`start_hint`. The console keeps registrations in memory: after a console
restart the card is back within one renewal.

`400` carries the reason for a descriptor it refuses.

## The descriptor

The first block is a catalogue entry — the same fields every vendor card is
drawn from.

| Field | | |
| --- | --- | --- |
| `id` | required | lower-case letters, digits, dashes; not one of the lab's own service ids |
| `name` | required | the card title |
| `summary` | | one paragraph: what this platform is |
| `base_url` | required | where **the console** reaches the platform (http/https) |
| `browse_url` | | where a **person's browser** does; the card's *Open* link |
| `health_path` | required | a `2xx` here is *up* |
| `activity_path` | | logs in the lab's activity shape (below); one panel per log |
| `ports`, `transport`, `auth`, `swap_for`, `docs` | | shown on the card, as for a vendor |
| `endpoints` | | `[{"method", "path", "note"}]`, the table at the foot of the card |
| `clock` | required | `"follows"` or `"wall"` — see *The clock* |
| `start_hint` | | shown when it is not running: how to start it |
| `actions` | | buttons, below |
| `settlement` | | a platform that settles Worldline files, below |
| `empty_hints` | | `{"<log name>": "what would put something here"}` |

Every path is **a path on `base_url`**: absolute, no `..`, no host. The
console appends it to `base_url` and calls nothing else, which is what
makes forwarding a button safe. Every string is text; the console escapes
it. A plugin cannot bring markup — if it needs a component the console
does not have, the component goes into
[design-system.md](design-system.md) first, for every plugin.

### Actions

```json
"actions": [
  {"id": "sweep", "label": "Run reconciliation sweep", "path": "/sim/sweep",
   "primary": true, "note": "Started — the outcome lands in Reconciliation sweeps."}
]
```

| Field | |
| --- | --- |
| `id`, `label`, `path` | required; ids unique |
| `method` | `POST` (default), `PUT` or `DELETE` — an action changes something |
| `primary` | the card's filled button |
| `confirm` | asked first, in these words |
| `note` | the toast when the platform's answer has no `note` of its own |

The button calls `POST /api/plugins/{id}/actions/{action}`; the console
forwards the body to `method base_url+path` and answers with the platform's
status and body, verbatim. Buttons show only while the platform is up. At
most eight.

### Settlement

A platform that settles Worldline files gets the files table on its card
and is the platform the System in test diagram draws:

```json
"settlement": {
  "files_path": "/sim/files",
  "run_path": "/sim/run",
  "status_path": "/status",
  "runs_log": "runs",
  "stages": {
    "ingest": ["SETTLEMENT_FILE_INGESTION", "DAILY_MOVEMENT_PROCESSING"],
    "reports": ["DAILY_SETTLEMENT_REPORT", "…"]
  }
}
```

`GET files_path` (the console proxies it at
`GET /api/plugins/{id}/files`):

```json
{"dir": "apps/settle-ingest/local",
 "files": [{"name": "…csv", "currency": "EUR", "mids": ["1","2"], "total": "6660.27",
            "default": true, "seeded": true, "missing_mids": [], "seed_command": "…",
            "in_flight": null, "last_run": {"root": "…", "payouts": {"count": 6, "total": "…"},
                                          "finished_at": "…", "error": null}}],
 "seeded_error": null}
```

`POST run_path` with `{"files": ["…csv", …]}` — the console's
`POST /api/plugins/{id}/run` forwards it — answers `202`
`{"runs": [{"currency", "run"}]}`, or the platform's own `409` for a
currency already running.

`GET status_path`:

```json
{"running_now": null,
 "last_run": {"id": "…", "root": "…", "started_at": "…", "finished_at": "…", "error": null,
              "stages": [{"stage": "SETTLEMENT_FILE_INGESTION", "status": "COMPLETED"}],
              "payouts": {"count": 6, "total": "27698.78", "statuses": {"SUCCESS": 6}}}}
```

`stages` maps the diagram's two platform-internal arrows — `ingest` and
`reports` — to the platform's own stage names. `runs_log` names the
activity log whose total is the diagram's *runs*.

### Activity logs

`GET activity_path` returns the shape every vendor serves
(`internal/activity`):

```json
{"logs": [{"name": "runs", "title": "Settlement runs", "note": "…", "total": 4,
           "labels": {"warn": "with failed stages", "bad": "failed"},
           "last": {…}, "events": [{"seq", "at", "op", "summary", "status", "detail", "peer"}]}]}
```

`status` is `ok`, `warn` or `bad`; `at` is wall-clock time.

## The clock

The lab owns one business clock: the `clock` service, `:8096`. Every
vendor follows its offset, so a payout booked "on Monday" by the platform
books on Monday at the bank.

- **`"clock": "follows"`** — the platform polls `GET /clock` and applies
  `offset_ms` to its own business time (buddy's runner writes it to the
  file its clock shim watches). Only the offset changes hands, so a
  follower anywhere is right to within its poll.
- **`"clock": "wall"`** — the platform cannot be moved (a deployed stack).
  While one is live, the console refuses to move the lab clock: the
  vendors would be on another day from the platform they serve.

A platform that must not have the clock moved under it — a settlement run
in flight — **holds** it:

```http
POST /clock/holds   {"holder": "infinite-local-runner", "reason": "settlement in flight: EUR …", "ttl_seconds": 900}
DELETE /clock/holds/infinite-local-runner
```

Moves are refused with `409` and the holder's reason until it is released
or the hold lapses (at most an hour), so a platform killed mid-run cannot
freeze the lab.

`GET /clock`:

```json
{"offset_ms": -225000000, "offset_hours": -62.5, "now": "2026-09-25T09:00:00Z",
 "mode": "pinned", "reason": "…", "business_day": true,
 "calendars": [{"code": "GB", "zone": "Europe/London", "date": "2026-09-25", "weekday": "Fri", "business_day": true}],
 "holds": []}
```

`POST /clock` takes one of `{"at": "<RFC 3339>"}`, `{"advance": "1h"}`,
`{"mode": "real"}` or `{"mode": "auto-business-day"}` (the most recent
instant that is a business day on every calendar, recomputed as time
passes). The console's clock card makes the same calls through
`/api/clock`.

## Reaching the platform

Three addresses, because three different parties dial them. For buddy's
runner:

| Who dials | Variable (runner) | Default |
| --- | --- | --- |
| the platform → the console | `FINTECH_SIM_LAB_CONSOLE_URL` | `http://127.0.0.1:8090` |
| the platform → the clock | `FINTECH_SIM_LAB_CLOCK_URL` | `http://127.0.0.1:8096/clock` |
| the console → the platform | `RUNNER_ADVERTISED_URL` → `base_url` | `http://host.containers.internal:3109` |
| a browser → the platform | `RUNNER_BROWSE_URL` → `browse_url` | `http://127.0.0.1:3109` |

`host.containers.internal` is how the containerised console reaches the
host. With the lab on another machine, the first two point at that machine
and `base_url` must be an address it can reach — a tunnel (cloudflared,
ngrok, pinggy) to the platform's control port does it. Registration and
the clock need no tunnel; only the card's live parts do.

## Trust

The console has no authentication, like every lab service. Anyone who can
reach it can register a plugin, and the console will then call that
plugin's `base_url` — only on the declared paths, only with the methods
above. Run the lab where only the people using it can reach it
([deploy-pod.md](deploy-pod.md)).
