# Banking Circle twin — standalone ↔ pod

**Status:** side-by-side (transitional). **Harness defaults to `pod-bank-rails` (:9085 mTLS).**  
**Branch context:** `BC-cutover` / `kernel-pod-review` / pods via `make up-pods`.

## What maps to what

| Role | Process | Ports | How you run it |
| --- | --- | --- | --- |
| **Standalone** (deep Connect protocol) | `cmd/bankingcircle` | `:8085` mTLS API, `:8095` internal | `make up` |
| **Pod twin** (kernel composition) | `cmd/pod` + `recipes/bank-rails` | `:9085` mTLS API, `:9185` plain (console) | `make up-pods` or `make pod POD=recipes/bank-rails` |

Console: Standalone card **Banking Circle** links to recipe twin `bank-rails`. Pod card is **`pod-bank-rails`**.

Standalone is **not** deleted. Compose settlement / standalone B4B still bridge to `:8095`. The harness observes the pod Connect surface; when needed it drives `POST /internal/handoff` itself (same body oversight would POST) until that compose path is rewired.

## Conceptual parity (jobs)

| Job | Standalone | `pod-bank-rails` |
| --- | --- | --- |
| Safeguarding / bank books | in-process ledger | `kind: ledger` id `books` |
| VIBAN / alias resolve | accounts API | `account-directory` id `vibans` |
| Payment lifecycle → OutgoingPaymentProcessed | payment engine (notification-type states) | `payment-orchestrator` stages use Connect names |
| Book on OutgoingPaymentProcessed (not webhook) | engine rules | wiring: `Fact.CaseApproved` when `state==OutgoingPaymentProcessed` |
| Notification subscribe / retry / batch | full Connect self-service + AES-GCM | `webhook-notifier` CRUD + If-Match / rowVersion; seals via `scheme-message-codec` (`aes-256-gcm`) |
| Settlement window / funding check | related balance flows | `settlement-window` + `/sim/funding` |
| Pull status / recon evidence | `GET /payments`, `GET /reconciliation` | `GET /api/v1/payments/{id}`; `GET /reconciliation` (orchestrator cases) |
| Account balances | `GET .../balances` | `Command.GetBalance` → `GET /api/v1/accounts/{accountId}/balances` (+ `/accounts/...` alias) |
| Basic→Bearer authorize | authorize + token store | `http-ingress` mode `issued-bearer` |
| Handoff from Oversight | internal payment create | `POST /internal/handoff` (`auth: none` lab seam) |
| mTLS terminate | `TLS_CERT` / `BC_MTLS` on `:8085` | `POD_TLS_*` / `POD_MTLS` on `:9085` (plain `:9185` for console) |

## Harness cutover (this branch)

Defaults in `internal/harness` / `make harness`:

| Env | Default (cutover) | Standalone override |
| --- | --- | --- |
| `BANKING_CIRCLE_URL` | `https://127.0.0.1:9085` | `https://127.0.0.1:8085` |
| `BANKING_CIRCLE_INTERNAL_URL` | `https://127.0.0.1:9085` (lab seams on same listener) | `http://127.0.0.1:8095` |
| `BANKING_CIRCLE_TARGET` | inferred from URL (`pod` if `:9085`) | `standalone` |
| `RECEIVER_DELIVERY_URL` | `https://receiver:8443` when host harness + pod | same as `RECEIVER_URL` |

```bash
make up && make up-pods          # standalones + pod-bank-rails
make harness                     # go run against published ports; BC → :9085
# or compare against standalone:
BANKING_CIRCLE_URL=https://127.0.0.1:8085 \
BANKING_CIRCLE_INTERNAL_URL=http://127.0.0.1:8095 \
BANKING_CIRCLE_TARGET=standalone \
  make harness
```

### What the harness proves on the pod

- **Authorize** — Basic→Bearer on `:9085` with lab client cert (issued-bearer)
- **Payout hop** — after settlement/B4B reaches `B4BTMApproved`, fund via `POST /sim/funding`, handoff via `POST /internal/handoff`, poll `GET /reconciliation` for `OutgoingPaymentProcessed`, assert merchant balance via balances DTO
- **Subscription CRUD** — create (201), encryption key `*Hidden*`, duplicate endpoint 409, If-Match / rowVersion
- **Batching + AES-256-GCM** — five `Fact.TransferPosted` events → one sealed POST; harness decrypts with the subscription key (scheme-message-codec)
- **Retry → deactivate → reactivate** — clock `scale: 86400` compresses the eleven-step schedule; `POST .../reactivate` redelivers retained notifications

### Remaining gaps (still standalone-only or partial)

| Gap | Notes |
| --- | --- |
| `BC_DELIVERY_CONFIG` file | Pod uses recipe retry schedule + clock scale instead |
| Connect `subscriptionEvent` / event-target / clienttest | Not ported; active pod subscriptions receive configured bus Facts |
| `/sim/subscription/{id}/notifications` | Pod uses `/sim/funding` (+ `/sim/notifications/flush`) or full-batch auto-flush |
| `/sim/emails` deactivation mail | Not on pod; harness skips that assert against the twin |
| `/sim/subscription/{id}/pending` | No pending introspection route; retention proven via reactivate + capture |
| Compose B4B → pod handoff | Standalone `b4b` still posts `:8095`; harness drives pod handoff itself for the payout scenario |
| Notification type for funding path | Batching demo uses `Fact.TransferPosted` (not `OutgoingPaymentProcessed`); CaseAdvanced carries Connect stage names when present |
| Partial batch auto-flush | `flush: 0s` under compressed clock so sequential funding can fill a batch; lab flush covers partials |

Do **not** delete the standalone until compose peers (settlement/B4B) and the remaining Connect-depth surfaces above are on the pod with the same assertions.

## Now on the pod (Connect-depth knobs)

- **Ledger balances** — `Command.GetBalance` / `Fact.BalanceRead` (+ list)
- **HTTP auth** — `issued-bearer`: Basic authorize issues in-memory tokens; API routes require Bearer
- **Notification self-service** — list/get/update/activate/deactivate/delete with `If-Match` / `rowVersion`; wire body uses Connect field names (`eventId`, `subscriptionId`, `notificationType`, `payment`)
- **AES-256-GCM envelopes** — `scheme-message-codec` owns seal/open; notifier `signing.mode: aes-256-gcm` composes it. **Webhook 2xx ≠ MoR** — bank books book on `OutgoingPaymentProcessed`; programme MoR stays `pod-ledger-recon`
- **GET /reconciliation** — evidence report from orchestrator cases (date + account); does not book customer/virtual books
- **mTLS on main listen** — `POD_TLS_CERT` / `POD_TLS_KEY` / `POD_CA_FILE` / `POD_MTLS` (require\|optional\|off). Dual listen: TLS `:9085` + plain `POD_PLAIN_LISTEN=:9185`
- **Clock scale** — `recipes/bank-rails/kernels/clock.yaml` `scale: 86400` (match compose `BC_TIME_SCALE`)

## Domain rules this twin respects

1. Webhook 2xx / AES-GCM success keeps the subscription alive; it does **not** book programme MoR.
2. Subscription CRUD is integrator-owned — no Oversight auto-subscribe.
3. Recon GET / payment status are **evidence** ports for ledger-recon; bank-rails books the SGA only.
4. mTLS is transport on the Connect surface (`:9085`); plain `:9185` is lab-only for console.

## Console shows "unknown kernel kind"?

Recipes are bind-mounted; the console **binary** is baked into an image. After
a new kernel lands (e.g. `scheme-message-codec`), run:

```bash
make rebuild
make up-pods
```

## Run them side by side

```bash
make up          # standalones including banking-circle :8085 / :8095
make up-pods     # adds pod-bank-rails :9085 (mTLS) + :9185 (plain)
# or foreground only the twin:
make pod POD=recipes/bank-rails
make harness     # BC scenarios → :9085
```

### Plain / console schematic (`:9185`)

Console `POD_HOST_POD_BANK_RAILS` points at `:9185`. After PR #4 the plain
listener carries **only** the pod's own introspection (`/health`, `/pod*`)
— not recipe money routes. Publishing both ports used to make the client
certificate optional for funding, handoff and balances; those stay on the
mTLS listener.

```bash
curl -s http://127.0.0.1:9185/health
curl -s http://127.0.0.1:9185/pod | jq '.name, .kernels[].id'
```

### Connect API + mTLS (`:9085`)

Same surface as standalone banking-circle — present the lab client cert.
Lab `/sim/*` and `/internal/*` are `auth: none` on this listener (still
behind mTLS when `POD_MTLS=require`):

```bash
TOKEN=$(curl -s --cacert certs/ca.pem \
  --cert certs/client.pem --key certs/client-key.pem \
  -u harness:harness \
  https://127.0.0.1:9085/api/v1/authorizations/authorize | jq -r .access_token)

curl -s --cacert certs/ca.pem \
  --cert certs/client.pem --key certs/client-key.pem \
  -X POST https://127.0.0.1:9085/sim/funding \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"from":"external:acquirer-settlement","to":"sga-eur","amount_minor":50000,"currency":"EUR","reference":"lump-demo"}'

curl -s --cacert certs/ca.pem \
  --cert certs/client.pem --key certs/client-key.pem \
  -H "Authorization: Bearer $TOKEN" \
  https://127.0.0.1:9085/api/v1/accounts/sga-eur/balances
```

(`curl -k` skips CA verify but still needs `--cert`/`--key` when `POD_MTLS=require`.)

## North star

Replace standalone Banking Circle with `pod-bank-rails` **after** compose peers and remaining Connect-depth gaps above pass the same harness assertions. Until then: harness on the pod; prove remaining quirks on both surfaces side by side. Standalone is **not** removed.

## Status vocabulary (mapped)

`pod-bank-rails` payment stages use Connect notification-type names:

| Stage | Role |
| --- | --- |
| `OutgoingPaymentBooked` | First rail signal (not the bank-books gate) |
| `OutgoingPaymentProcessed` | Terminal; wiring posts **bank** books |
| `OutgoingPaymentRejected` | Failed walk |

Standalone `banking-circle` keeps running alongside (`make up` + `make up-pods`) so both appear on the console board.
