# Agent runbooks — driving the lab headless

For an agent (or a person) picking the lab up cold: how to bring it up, drive
it without the UI, and read what happened. Every command here has been run
against the lab. The design docs say *why*; this says *how*.

The platform under test is **buddy** (the Infinite monorepo), run locally by
its `infinite-local-runner` on the host. The lab is this repo, in containers.
Paths below: `~/gh/fintechlab-simple` (this repo) and
`~/infinite/buddy/infinite-local-runner` (the runner) — adjust to your
checkout.

## What listens where

| Service | Port | Notes |
| --- | --- | --- |
| console | 8090 | UI and its `/api/*` — the easiest headless surface |
| banking-circle | 8085 | Connect API: mTLS + Basic→Bearer. Use the console or the bridge rather than curling it |
| banking-circle bridge | 8095 | plain HTTP, no auth: `/internal/*` lab hooks |
| worldline | 8084, 2222 | SFT channel over HTTP; SFTP+PGP on 2222 |
| b4b | 8086 | Oversight API (RS512 JWT) |
| aci / verify / verification | 8087 / 8088 / 8089 | |
| bank / settlement / receiver | 8081 / 8083 / 8443 | scaffolding |
| **runner** control API (host) | 3109 | `/status`, `/sim/run`, `/sim/clock`, `/sim/fund-sga` |
| buddy `apps/banking-circle` (host) | 3114 | webhook receiver; internal API under `/api/v1/internal/...` |

The console's service cards list every route each service serves (Vendors →
open a card), from `internal/console/catalogue.go`.

## Lifecycle

```sh
cd ~/gh/fintechlab-simple
make test     # vet + gofmt check + go test ./..., no containers
make up       # rebuild images and recreate the whole lab — after any code change
make start    # restart from the images already built
```

- **Rebuild the whole lab, not one vendor.** Recreating only banking-circle
  leaves b4b's bridge pointing at its old address and payouts time out. Use
  `make up` / `make start`.
- **The console alone is safe to rebuild** (nothing calls it):
  `podman-compose build console && podman rm -f fintechlab-simple_console_1 && podman-compose up -d --no-deps console`.
- **After the lab restarts, restart the runner.** A fresh banking-circle has
  no subscriptions; buddy's `apps/banking-circle` subscribes on boot, so it
  needs the restart to get a new subscription (its id changes every time —
  read it from `GET :8090/api/banking-circle/subscriptions`).

Runner, from `~/infinite/buddy/infinite-local-runner`:

```sh
# start (long-running: background it, output to a file)
node -r ./src/clock-shim.cjs -r @swc-node/register src/up.ts > up.log 2>&1 &
# stop: SIGINT the up.ts process, it takes its children with it
kill -INT "$(pgrep -f '^node -r ./src/clock-shim.cjs -r @swc-node/register src/up.ts$')"
# ready when every process reports up
curl -s localhost:3109/status | python3 -c "import json,sys; d=json.load(sys.stdin); print([(p['name'],p['state']) for p in d['processes']])"
```

## The clock

One clock for the whole stack: the runner's. Every vendor follows it
(`RUNNER_CLOCK_URL` → `GET :3109/sim/clock`, polled every second), so
bookings, files, report and balance dates all move with the platform.

```sh
curl -s localhost:3109/sim/clock                                    # now, offset, business day
curl -s -X POST localhost:3109/sim/clock -d '{"advance":"1h"}'      # also "10m", "-1d"
curl -s -X POST localhost:3109/sim/clock -d '{"at":"2026-09-28T07:45:00Z"}'
curl -s -X POST localhost:3109/sim/clock -d '{"mode":"auto-business-day"}'
curl -s -X POST localhost:3109/sim/clock -d '{"mode":"real"}'       # always put it back
```

- A vendor logs `runnerclock: now … (1h0m0s from real)` when it follows a
  move: `podman logs fintechlab-simple_banking-circle_1 | grep runnerclock`.
- Moving past Worldline's 08:30 / 15:30 slot delivers that slot's file
  within ~5 s. A move undone within 5 s can go unnoticed by the scheduler.
- A move is refused while a settlement run is in flight.
- Elapsed time (tokens, retries, delivery windows) stays on the real clock.

## Run a settlement

```sh
curl -s -X POST localhost:3109/sim/run
# wait for it: running_now is null when done; last_run.root is the root id
curl -s localhost:3109/status | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('running_now'), d['last_run']['root'], d['last_run'].get('error'))"
```

Read the outcome from the settle DB (the runner's Postgres container):

```sh
q() { podman exec infinite-local-runner-postgres-settle psql -U postgres -d settle_db "$@"; }
R=<root id>
q -tAc "select s.status||'/'||t.status, count(*) from settlement_submissions s
        join transactions t on t.id=s.transaction_id
        where t.external_source_reference like 'sttl_v1:$R:%' group by 1"
q -tAc "select stage, status, outcome from settlement_processing_stage
        where root_id='$R' and stage in ('INFINITE_SETTLEMENT','BC_PAYMENT_RECONCILIATION')"
```

Expect the payouts to sit at `IN_PROGRESS` after a run: Banking Circle's
`Processed` webhook usually arrives before buddy has recorded the
`bcPaymentId` (the workers log `No transaction found … skipping`). The sweep
is what resolves them.

## Run the BC payment reconciliation sweep

It picks up sweep stages at least an hour old **by the runner's clock**, so
move the clock, trigger, and move it back:

```sh
curl -s -X POST localhost:3109/sim/clock -d '{"advance":"1h"}'
cd ~/infinite/buddy/infinite-local-runner
node -r ./src/clock-shim.cjs -r @swc-node/register src/trigger-bc-recon.ts   # not an Nx target: run it directly
curl -s -X POST localhost:3109/sim/clock -d '{"mode":"real"}'
```

It logs `open=N resolved=N unresolved=0` per root. Each resolved submission
records how in `raw_webhook_payload->>'report'`: `intraday-reconciliation`
(the report) or `payment-status` (the fallback). The stage stays
`PROCESSING` until a tick finds nothing open, and completes as
`PARTIALLY_PROCESSED` if anything is still open at 19:00 Paris time.

Where the lab shows it:

- **Diagram** (System in test): the bottom arrow, *intraday reconciliation*,
  Banking Circle → platform, lights on each intraday report read. It counts
  those reads only; a refused (400) read turns it amber.
- **Banking Circle card → *Reconciliation reads***: every intraday report,
  rejection report and payment-status call — time, caller, dates, page,
  properties asked for, rows or status returned. Refused calls are amber
  with the parameters they lacked; a status read for an unknown payment is
  amber `not found (404)`. A healthy sweep is one line like
  `intraday report 2026-09-24, page 1 → 8 row(s)` (payouts plus the day's
  incoming lump sums) and no status lines.

## Make Banking Circle misbehave on purpose

Bridge (`:8095`, no auth) or console (`:8090`) — both are one call:

```sh
# the next payouts end Rejected / stay Pending, one each, in order
curl -s -X POST localhost:8095/internal/payments/outcomes -d '{"outcomes":["Rejected","Pending"]}'
curl -s localhost:8095/internal/payments/outcomes                  # what is still queued

# hold one subscription's webhooks, then release them
curl -s -X POST localhost:8090/api/banking-circle/subscriptions/<sub_id>/pause
curl -s -X POST localhost:8090/api/banking-circle/subscriptions/<sub_id>/resume

# after a payout processed: the recipient's bank sends it back, or the scheme reverses it
curl -s -X POST localhost:8095/internal/payments/<bcPaymentId>/return \
     -d '{"reasonCode":"AC04","reasonDescription":"Closed account number"}'
curl -s -X POST localhost:8095/internal/payments/<bcPaymentId>/reverse -d '{"reason":"scheme"}'
curl -s localhost:8090/api/banking-circle/payouts                  # the payouts, and which can be returned/reversed
```

Worked scenario — a rejection and a hang, resolved only by the sweep: pause
buddy's subscription, queue `["Rejected","Pending"]`, run a settlement,
advance 1h and trigger the sweep (one payout goes `REJECTED` with its ledger
reversal, the pending one stays open, the rest `SUCCESS`), then resume the
subscription and check nothing changes twice.

## Reading what happened

```sh
podman logs --since 5m fintechlab-simple_banking-circle_1 | grep -v "GET /health"
sed 's/\x1b\[[0-9;]*m//g' up.log | grep -E "BcWebhookConsumerService|SettlementStatusHandler|BC payment reconciliation"
curl -s "localhost:3114/api/v1/internal/reconciliation/intraday-payments?transactionDate=2026-09-24&fromCreatedAt=2026-09-23T22:00:00.000Z&toCreatedAt=$(date -u +%FT%T.000Z)"
curl -s localhost:3114/api/v1/internal/payments/<bcPaymentId>/status
```

The last two go through buddy's own BC client to the lab's report and status
endpoints — the same path the sweep takes.

The console's panels and diagram are plain JSON underneath:

```sh
# a vendor's activity logs — banking-circle has payments, notifications, reports
curl -s localhost:8090/api/services/banking-circle/activity | python3 -c "
import json,sys
for l in json.load(sys.stdin)['logs']:
    if l['name'] == 'reports':
        for e in l['events'][:5]: print(e['at'], e['op'], e['status'], e['summary'])"
# one diagram arrow: count, when it last moved, and what moved it
curl -s localhost:8090/api/flow | python3 -c "
import json,sys
for s in json.load(sys.stdin)['steps']:
    if s['id'] == 'recon': print(s['count'], s.get('last_at'), s.get('last_summary'))"
```

Step ids, top to bottom: `pull`, `collect`, `ingest`, `fund`, `payouts`,
`bridge`, `callbacks`, `notify`, `confirm`, `reports`, `recon`.

## Screenshots without a browser window

Playwright is in buddy's `node_modules` and its headless Chromium is cached:

```js
// node shot.cjs
const { chromium } = require('/home/andrej/infinite/buddy/node_modules/playwright');
(async () => {
  const b = await chromium.launch({ args: ['--no-sandbox'],
    executablePath: require('fs').readdirSync(process.env.HOME + '/.cache/ms-playwright')
      .filter((d) => d.startsWith('chromium_headless_shell'))
      .map((d) => `${process.env.HOME}/.cache/ms-playwright/${d}/chrome-headless-shell-linux64/chrome-headless-shell`)[0] });
  const p = await b.newPage({ viewport: { width: 1500, height: 1100 } });
  await p.goto('http://127.0.0.1:8090/#platform');          // a fresh page per view: hash-only navigation does not re-route
  await p.click('[data-card="local-runner"]');              // cards open on click; open state is not in the URL
  await p.waitForTimeout(1500);
  await (await p.$('.card.is-open')).screenshot({ path: 'runner.png' });
  await b.close();
})();
```

## Things that look broken and are not

- **Mailer `Service Unavailable` errors** in the runner log: the report
  emails have no mailer locally. Unrelated to payments.
- **`IncomingPaymentProcessed` ignored** by accounts-settlement: buddy does
  not act on incoming payments (including returns) today.
- **A subscription id you used an hour ago is gone:** the lab was restarted.
- **No *intraday reconciliation* arrow and an empty *Reconciliation reads*
  panel right after a run:** the sweep only takes stages an hour old by the
  runner's clock. Advance the clock an hour and trigger it. The panel is
  also emptied by a lab restart (activity logs are in memory).
- **The report has no rows for a late-evening payout on today's date:** the
  bank dates bookings on its business day — CET, 19:00 cutoff, weekends to
  Monday. Ask for the next business day.

## Vendor documentation

Banking Circle's docs (https://docs.bankingcircleconnect.com) are behind a
password — ask the user for it; never commit it. Append `.md` to any page URL
for markdown; API reference pages carry the OpenAPI JSON. What the lab
changed to match them, and why, is in
[docs/ARCHITECTURE-vendor-corrections.md](docs/ARCHITECTURE-vendor-corrections.md),
section H.
