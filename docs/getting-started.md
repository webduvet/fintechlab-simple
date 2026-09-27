# Getting started: your platform against the lab

The README's five-minute start runs the lab on its own. This page is the
loop the lab exists for: a real platform settles a Worldline file through
B4B into Banking Circle, and reconciles what the bank says it did. The
platform here is buddy, run locally by its `infinite-local-runner`; any
platform that reads the same variables works the same way.

Once through, by hand, with the console. Every step is also one `curl`:
[agent.runbooks.md](../agent.runbooks.md) has those, and a list of things
that look broken and are not.

## Before you start

- This repo's prerequisites (README): Go, Podman or Docker, `openssl`,
  `make`.
- A buddy checkout with the runner set up once: from
  `infinite-local-runner/`, `podman compose up -d && ./setup.sh`
  (its README, *Quick start*).
- The ports free: 8080–8095, 8443 and 2222 for the lab, 3109 and 3114 for
  the runner.

## 1. Start the lab

```sh
cd ~/gh/fintechlab-simple
make up
```

Keys and certificates are generated into `keys/` on the first run and kept
after that. To keep them somewhere else, `export LAB_KEYS_DIR=/some/path`
before `make up` — and use that path below instead.

Open the console: <http://127.0.0.1:8090>. Vendors should show every card
green within a few seconds.

## 2. Start the platform, pointed at the lab's keys

```sh
cd ~/infinite/buddy
export FINTECH_SIM_LAB=~/gh/fintechlab-simple/keys     # = LAB_KEYS_DIR
pnpm nx up infinite-local-runner
```

`FINTECH_SIM_LAB` is where the runner reads the lab's mTLS client
certificate for Banking Circle (`certs/`), and where `setup.sh` takes the
B4B and Worldline keys from when it first writes its `.env`. Without it,
Banking Circle refuses the platform's TLS handshake.

In the console, **Platform → Local runner** turns green, and the **Vendors →
Banking Circle** card lists a subscription to
`host.containers.internal:3114`: the platform registered for webhooks.

**Restart the runner whenever the lab restarts.** Banking Circle keeps its
subscriptions in memory, and the platform only subscribes when it starts.

## 3. Run a settlement

Everything happens on **Platform → Local runner**.

1. **The clock.** Settlement only leaves the bank on a business day. If the
   clock note is amber (*not a business day*), set a weekday morning — e.g.
   *Date* `2026-09-25`, *Time* `09:00`, **Set clock**.
2. **Run.** In *settlement files*, press **Run** on
   `worldline-reconciliation-sample.csv` (EUR). The row turns amber with the
   stage it is on; about forty seconds later it shows the root id and the
   payouts.
3. **Watch it.** **System in test** draws the run as it happens: the file
   collected, the safeguarding account funded, payouts to B4B, B4B handing
   them to Banking Circle, and Banking Circle's webhooks to the platform.

After a run the payouts are `IN_PROGRESS` on the platform. That is expected:
Banking Circle's webhook usually arrives before the platform has recorded
which payment it is about. The sweep is what resolves them.

## 4. Reconcile

1. On the clock, press **+1 hour** — a settlement is only swept an hour
   after it ran, by the platform's clock.
2. Press **Run reconciliation sweep**. About twenty seconds later the
   **Reconciliation sweeps** panel on the same card shows one row, e.g.
   *1 root(s) at 2026-09-25 10:00 UTC: 6 payout(s) resolved*.
3. Press **Now** on the clock to put the platform back on real time.

On **Vendors → Banking Circle**, *Reconciliation reads* shows the intraday
report the sweep read, and the diagram's bottom arrow lights.

## 5. Read the result

The payouts, as the platform recorded them:

```sh
R=<root id from the Run row>
podman exec infinite-local-runner-postgres-settle psql -U postgres -d settle_db -tAc \
  "select s.status||'/'||t.status, count(*) from settlement_submissions s
   join transactions t on t.id=s.transaction_id
   where t.external_source_reference like 'sttl_v1:$R:%' group by 1"
```

`SUCCESS/SUCCESS|6` is the happy path: six payouts (five merchants and the
platform's own fee), each confirmed by the bank.

## What next

- **Break it on purpose:** queue a rejected payout, pause the platform's
  webhooks, return or reverse a payout — [agent.runbooks.md](../agent.runbooks.md),
  *Make Banking Circle misbehave on purpose*, and [console.md](console.md).
- **Two currencies at once:** seed the GBP merchant, then **Run all 2
  together** ([agent.runbooks.md](../agent.runbooks.md)).
- **A platform that does not read the lab's disk:** **Connect your
  platform** on the Local runner card downloads one `.env` with every
  address, credential and key inlined.
- **New keys:** `make keys-regenerate`, then restart the runner.
- **The lab on another machine:** [deploy-pod.md](deploy-pod.md).
