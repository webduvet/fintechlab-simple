# fintechlab-simple

A **connected** local lab simulating the three third parties a white-label
payment facilitator settles through, so a platform can be built and tested
against them without a vendor contract:

1. **Worldline** (acquirer) cuts the daily settlement file from the card
   transactions it acquired, drops it PGP-encrypted on a real SFTP server
   in two slots (morning + afternoon confirmation), and wires the matching
   lump sum to the safeguarding account.
2. **B4B Payments Oversight** takes the per-outlet payouts, runs its
   regulatory gates, calls back on every phase change, and hands approved
   payments to Banking Circle.
3. **Banking Circle Connect** manages the webhook subscription and delivers
   **batched**, AES-256-GCM encrypted notifications — with the real retry
   schedule, ending in auto-deactivation.

Plus ACI's card-gateway webhook and a merchant-verification stub.

**This repo is the vendors, not the platform.** `settlement` and
`receiver` are stand-ins for your side, kept only so the harness can prove
each hop is really connected — see
[docs/catalogue.md](docs/catalogue.md#which-half-is-which).

This is a **simulation**. IBANs look like `GB00SIM…`. The HMAC secret is `sim-hmac-dev-only`. Nothing here is a bank, a vendor sandbox, or production policy.

Licensed under the [MIT License](LICENSE).

## 5-minute start

Needs: Go 1.22+, Docker **or** Podman, `openssl`, `curl`, `python3`, `make`.

```bash
git clone https://github.com/webduvet/fintechlab-simple.git
cd fintechlab-simple
make up
make console        # http://127.0.0.1:8090
make demo-payment
make harness
```

**`make console`** opens the control panel: which services are up, what
the sim banks hold, and a Merchants view for creating the merchants and
outlets (MIDs) the money flow hangs off — then registering them at B4B,
seeding card trading at the acquirer, cutting the settlement file and
paying it out, in that order, by clicking. Every button is one call to a
service's own public API, so nothing in this lab is reachable only through
a UI. See [docs/console.md](docs/console.md), and
[docs/design-system.md](docs/design-system.md) for the design record behind
its look and feel.

`make up` generates a local CA (if `certs/` is empty) and starts compose.
`make demo-payment` creates a payment with an `Idempotency-Key`, then
**polls the receiver until the webhook for that payment arrives** and
prints it. `make harness` runs the full scenario suite; `make
harness-docker` runs the identical binary as one container attached to the
compose network instead.

That matters: a health ping is not a connected system. The harness fails
loudly if any hop is disconnected, and it asserts things a status code
cannot tell you:

- A settlement file must be **cut by the acquirer**, PGP-encrypted on its
  own SFTP root, and reachable only over the wire. The harness dials the
  same server itself and re-parses the same bytes.
- The per-MID totals inside it must **add up to the lump sum** that landed.
- An **afternoon confirmation file must not be paid out again** — that
  mistake pays every merchant twice.
- Five notifications with a batch size of five must arrive as **one
  encrypted message**, decrypted and counted, not five.
- A subscriber that never answers must be retried on the real schedule,
  **deactivated**, and its missed notifications **redelivered** when it
  comes back.
- A payment whose creditor details contradict its beneficiary must be
  **refused with 422**, and nothing forwarded.

```text
payment-api    :8080         POST /payments   GET /payments/{id}
bank           :8081         ledger + fake accounts
notifier       :8082         enqueue + retries + allowlist
receiver       :8443         HTTPS + HMAC verify + GET /events + raw capture sink
settlement     :8083         platform stand-in: pulls + parses the settlement file, pays out per MID
worldline      :8084, :2222  acquirer: SFT channel over HTTP (:8084) + real SFTP+PGP (:2222)
banking-circle :8085, :8095  bearer API behind optional mTLS (:8085) + B4B-only bridge (:8095)
b4b            :8086         B4B Oversight API: company boarding, beneficiaries, gates, JWT-authed payout rail
aci            :8087         ACI card-gateway webhook: AES-256-GCM encrypted notification
verify         :8088         Merchant verification stub: always-approves by default
console        :8090         control panel: services, sim banks, merchants, configuration
```

Compose file is `compose.yml` (`docker compose` and `podman compose` as far as practical).
See [docs/local-domains.md](docs/local-domains.md) for pointing a real
buddy service at these ports using vendor-shaped domain names instead of
bare `localhost:<port>`.

See [docs/console.md](docs/console.md) for the control panel,
[docs/scenarios/settlement-to-payout.md](docs/scenarios/settlement-to-payout.md)
for the Worldline/Banking Circle half by hand,
[ARCHITECTURE-worldline-acquirer.md](docs/ARCHITECTURE-worldline-acquirer.md)
for who owns the settlement file, and
[ARCHITECTURE-banking-circle-webhooks.md](docs/ARCHITECTURE-banking-circle-webhooks.md)
for subscriptions, batching and the retry schedule.

## Kernels and pods

Beside the services above there is a composable second architecture: a
**kernel** is one brand-generic financial job, a **pod** is a bundle of
kernels behind one address — which is what a vendor actually is — and a
**recipe** is a directory of YAML that names one.

```bash
```

One binary for every vendor: the difference between simulating an acquirer
and a payout rail is a directory of YAML, not a new `cmd/`. Four pods ship
— an acquirer edge, bank rails, EMI oversight and a gateway facade — and
the console's **Pods** view draws each one's kernels, effective
configuration and wiring from the running process.

which is what lets the runtime be described and shipped on its own. See
moved out of the standalone services.

## Tests (no Docker)

```bash
make test
```

Covers HMAC sign/verify, destination allowlists, retry-until-2xx, the
settlement state machine, the Bambora file format **and its parser**
(generate → render → parse round trip, so the two cannot drift), the
two-slot settlement cycle and its per-currency file split, the payout
gates, the real SFTP+PGP transport (key generation, encrypt/decrypt, a
live SSH/SFTP dial, host-key pinning, key *and* password auth), Banking
Circle's subscription model (`If-Match` concurrency, event/target routing,
batch-size rules), its batching and eleven-step retry schedule through to
deactivation and redelivery, its byte-exact AES-256-GCM cipher, B4B's JWT
verification, sanctions gate, creditor-consistency check and payout
lifecycle, ACI's webhook crypto (known-answer-tested against ACI's own
published reference vector), and the verify stub. `make harness` is the
containerizable, black-box version of the same guarantees.

## What this is not

- Not a byte-accurate Banking Circle, Worldline, B4B, or ACI API replica
  overall — vendor-shaped, not vendor-exact, and several pieces (Banking
  Circle's webhook crypto, B4B's JWT auth and wire types, Worldline's
  Bambora file format and SFTP+PGP transport, ACI's webhook crypto) are
  deliberately protocol-accurate where a real integration was read to
  confirm the exact wire shape. No vendor doc credentials were used; see
  [ARCHITECTURE-vendor-corrections.md](docs/ARCHITECTURE-vendor-corrections.md)
  and [ARCHITECTURE-phase3-corrections.md](docs/ARCHITECTURE-phase3-corrections.md)
  for exactly what is and isn't grounded, and
  [ARCHITECTURE-banking-circle.md](docs/ARCHITECTURE-banking-circle.md)
  (superseded — historical record only).
- Not the platform's own virtual-account, fee or reconciliation logic —
  this lab mocks the third parties, not the platform built against them.
  `settlement` is a stand-in that exists so the harness has something to
  prove the vendors are connected *to*; it is not a model of any real
  platform, and it does not grow.
- Not a Worldline Acquiring REST API. The settlement file over SFTP is the
  whole Worldline integration today; the REST surface is a documented seam.
- Not a merchant-verification vendor (KYC/AML/sanctions) — `verify`
  always approves by default, a documented sample/stub, not a simulation
  of any real check.
- Not a place for real customer data, API tokens, or vendor doc passwords.
- Not a public-CIDR webhook policy. See [docs/security/allowlists.md](docs/security/allowlists.md).

Read [docs/index.md](docs/index.md) for the vision and [docs/scenarios/payment-to-webhook.md](docs/scenarios/payment-to-webhook.md) for the happy-path tutorial.
