# Architecture: Vendor Contract Corrections (buddy-verified)

## Goal

Correct three architectural mismatches found by reading how `buddy` (the real
Infinite/Lightyear monorepo, a sibling workspace, read-only) actually talks to
Worldline, Banking Circle, and B4B Payments. Every fact below is sourced from
buddy's own code (paths cited), not inferred. This doc is the contract for
the corrections; it does not restate what was already right.

## What was wrong

1. **B4B is a payout rail, not an inbound-payment facade.** `payment-api`'s
   `POST /payments` + `Idempotency-Key` shape doesn't correspond to anything
   B4B does. B4B never receives inbound payments in this lab's story; it is
   the vendor Infinite pays merchants *through*.
2. **Banking Circle has no payment-creation endpoint.** Real Banking Circle,
   from buddy's side, is auth + balance-read + webhook-subscription-management
   + webhook receipt. Buddy's entire payout path goes through B4B; Banking
   Circle is B4B's own downstream settlement bank, confirmed independently via
   Banking Circle's *own* webhook, correlated by a `bcPaymentId` that arrives
   *from B4B's webhook first*. There is no "call Banking Circle to pay
   someone" anywhere in buddy.
3. **Worldline settlement is a file format, not a REST reporting API.**
   Buddy uses Worldline/Bambora's European file-based settlement (multi-section
   CSV over SFTP+PGP). The existing `internal/settlement` state machine +
   `GET /reports/settlement` JSON API models Worldline's *North American*
   REST reporting API — a real, different product buddy does not use. Per
   product decision: **keep that layer** (it is still a valid simulation of a
   real product surface) and **add** the file format buddy's real parsers
   consume, described below.

## 1. Worldline: the real Bambora settlement file

Source: `apps/settle-ingest/src/app/templates/worldline.template.ts`,
`worldline-ingest.service.ts`, sample at
`apps/settle-integration-testing/support/fixtures/worldline-sample.csv`.

Multi-section CSV, sections identified by a `RECORD_TYPE` column value, each
section's own header row repeating before its data rows:

```
Settlement  (one row/file): VERSION_NUMBER,RECORD_TYPE,SETTLEMENT_AMOUNT,SETTLEMENT_CURRENCY,VALUE_DATE,NUMBER_OF_ITEMS,TO_ACCOUNT,PAYMENT_REFERENCE
Batch       (repeats):      RECORD_TYPE,BAMBORA_MID,BATCH_REF,PAYREF_EXTENDED,NET_AMOUNT,BATCH_CURRENCY,SETTLEMENT_AMOUNT,SETTLEMENT_CURRENCY,NUMBER_OF_TRANS
TXER        (N per batch):  RECORD_TYPE,BAMBORA_MID,SUBMERCHANT_ID,BATCH_REF,TRANSACTION_REF,BAMBORA_REF,ADDITIONAL_REF_1,ADDITIONAL_REF_2,TRANSACTION_TYPE,TRANSACTION_AMOUNT,TRANSACTION_CURRENCY,FX_RATE,SETTLEMENT_AMOUNT,SETTLEMENT_CURRENCY,CARD_SCHEME_NAME,CARD_USAGE,CARD_CATEGORY,INTERCHANGE_DOMAIN,COUNTRY_MERCHANT,COUNTRY_ISSUER,MCC,ECOM_SECURITY_LEVEL,ADDITIONAL_REF_3,CARD_NUMBER_TRUNCATED,TRANSACTION_DATE,TRANSACTION_TIME,CASHBACK_AMOUNT,PAYREF_EXTENDED,TERMINALID
CB          (trailing, file-wide): RECORD_TYPE,BAMBORA_MID,SUBMERCHANT_ID,ORIGINAL_TRANSACTION_REF,BAMBORA_REF,DISPUTE_TRANSACTION_AMOUNT,DISPUTE_CURRENCY,DISPUTE_SETTLEMENT_AMOUNT,DISPUTE_SETTLEMENT_CURRENCY,DISPUTE_REASON_CODE,DISPUTE_REGISTRATION_DATE,DISPUTE_FEE,DISPUTE_FEE_CURRENCY,ORIGINAL_TRANSACTION_ADDITIONAL_REF_1
```

Values are `"quoted"`. `TRANSACTION_TYPE` observed: `Sale`, `Sale with Cash
Back`. Card scheme values observed: `Visa`, `Mastercard`, `AMEX`.
`INTERCHANGE_DOMAIN`: `Domestic`, `Intraregional`, `Interregional`.

**Non-obvious real semantics (verified against buddy's own parser + unit
tests, not guessed):**

- The merchant grouping key is **`ADDITIONAL_REF_2`** on TXER rows and
  **`SUBMERCHANT_ID`** on CB rows — two different raw columns feeding one
  logical merchant id. **`BAMBORA_MID` only keys batches** — one `BAMBORA_MID`
  can span multiple merchants. Do not use `BAMBORA_MID` as the merchant key.
- Chargeback sign determines type: negative `DISPUTE_SETTLEMENT_AMOUNT` =
  chargeback, positive = chargeback reversal. Never joined to TXER rows by
  filename/content position — reference only, no computed link required here.
- No `settlement_state`/approved/declined concept exists in the real file or
  its parser — don't try to encode the existing state machine's vocabulary
  into this format.

**Filename** (`apps/acquirer-fts/src/reconciliation-file/reconciliation-file-validator.ts`):
`{YYYYMMDDHHMMSS}_{identifier}_{ER|AR}_{CCY}.csv`, identifier defaults to
`Worldline_Settlement` (configurable), currency 3-letter uppercase. `ER`
("Morning file") and `AR` ("Afternoon file") are the same content shape, just
two delivery slots per day — not different report types.

### Components

- `internal/settlement/bambora.go` — new file. Types for each section
  (`BamboraMeta`, `BamboraBatch`, `BamboraTransaction`, `BamboraChargeback`),
  a `GenerateBambora(ledger *Ledger, merchantID, fromDate, toDate string,
  opts BamboraOptions) *BamboraFile` that reuses the existing `Entry`/`Ledger`
  types from `report.go`, and `func (f *BamboraFile) WriteCSV(w io.Writer)
  error` producing the exact quoted multi-section format above. Since the
  fake bank ledger has no card-scheme/MCC/interchange concept, synthesize
  plausible, deterministic (seeded by transaction ref, not random per run)
  fake card metadata per transaction — clearly a simulation detail, same
  "obviously fake" spirit as `GB00SIM…` IBANs.
- `BamboraFilename(identifier string, at time.Time, fileType, currency
  string) string` implementing the real pattern.
- Wire into `cmd/settlement`: after existing `Generate()` + staging, also
  produce the Bambora-format file and stage it (both through the existing
  HTTP staging endpoints and, per the SFTP work below, the real SFTP+PGP
  root) — additive, does not replace the existing `Report`/CSV/XML/JSON
  output already staged.

## 2. Worldline: real SFTP+PGP transport

Source: `libs/shared/sftp/src/sftp/sftp-client.service.ts` (client:
`ssh2-sftp-client`, upload/download/list/delete/testConnection, password XOR
private-key auth, optional SHA256 host-key pinning), `pgp.service.ts`
(OpenPGP.js v6, binary format, optional signature verification on decrypt),
`apps/acquirer-fts/src/acquirer/acquirer-config.registry.ts` (real remote
paths for `worldline`: upload→`/to_WLNORDIC`, download/disputes/reconciliation
all alias to `/from_WLNORDIC` in that registry — but the reconciliation-file
service's own tests list against `/download` and dispute-case against
`/disputes/download`; **both path shapes exist in the real codebase depending
on which acquirer-fts config vintage you read — expose all of `/download`,
`/disputes/download`, `/to_WLNORDIC`, `/from_WLNORDIC` as real directories so
either convention resolves correctly**), real hosts
`fts-test.aws.bambora.com:22` (UAT) / `fts.bambora.com:22` (PROD).

This lab's existing `sftp-gateway` is HTTP-shaped and stays (buddy's own main
pipeline test harness bypasses real SFTP/PGP too — direct S3 injection, see
`apps/settle-integration-testing`). This section adds a **real** SFTP+PGP
server alongside it, because `acquirer-fts` genuinely speaks the SSH/SFTP
wire protocol and PGP encryption, and unlike the pipeline harness, its own
narrow local test (`scripts/worldline-enrolment-local-test/`) does exercise
that boundary for real.

Implementing SSH/SFTP/PGP from raw stdlib crypto primitives is not
responsible (hand-rolled wire-protocol and crypto-format implementations are
a real security risk, not a style choice). This is the one place in the lab
that takes real dependencies, pinned to versions compatible with `go 1.22`:
`golang.org/x/crypto` (SSH transport, Go team maintained), `github.com/pkg/sftp`
(the de facto Go SFTP protocol implementation, serves a real directory
tree directly — no custom virtual-FS layer needed), `github.com/ProtonMail/go-crypto/openpgp`
(maintained OpenPGP fork, RFC 4880 — interoperable with buddy's OpenPGP.js
by spec, not by shared implementation).

### Components

- New `internal/wlsftp` package: SSH host key generation (ed25519, on first
  run, persisted), SSH server config accepting **either** a configured
  password **or** a configured authorized public key (matching
  `AcquirerSftpConfig`'s password-XOR-privateKey shape), PGP keypair
  generation for "the mock Worldline" (persisted, printable so a real
  buddy deployment could be pointed at this lab by setting
  `WORLDLINE_PGP_PUBLIC_KEY` to it), optional configured "Infinite" public
  key for verifying uploaded file signatures (log-and-skip if unset, same
  graceful-degradation style as the rest of this lab).
- Directory root mirrors real paths: `download/`, `disputes/download/`,
  `to_WLNORDIC/`, `from_WLNORDIC/` under the SFTP server's root — served by
  `pkg/sftp.NewServer` against that real directory (default filesystem
  handlers, no custom Handlers needed).
- Files placed into `download/` (reconciliation) are PGP-encrypted+signed
  **at staging time** (when `cmd/settlement` writes them), so what's on disk
  is already exactly what a real SFTP download would hand back — no
  encrypt-on-read layer needed.
- `cmd/sftp-gateway` gains `WORLDLINE_SFTP_PORT` (default matching buddy's own
  `WORLDLINE_SFTP_PORT=2222` default), `WORLDLINE_SFTP_HOST_KEY_PATH`,
  `WORLDLINE_SFTP_AUTHORIZED_KEY` / `WORLDLINE_SFTP_PASSWORD`,
  `WORLDLINE_PGP_PUBLIC_KEY_PATH` (mock Worldline's own public key, printed at
  startup), `WORLDLINE_PGP_PRIVATE_KEY_PATH`, `INFINITE_PGP_PUBLIC_KEY_PATH`
  (optional, for upload signature verification). Runs the SSH listener
  alongside the existing HTTP API in the same process (same pattern as
  `receiver` running one protocol; this service now runs two).

## 3. Banking Circle: remove the fake payments endpoint, rebuild the webhook

Source: `apps/banking-circle/src/modules/{auth,outbound/accounts,webhooks/bc-webhook,webhooks/bc-subscription}/**`.

- **Remove** `POST /payments` and the `Received/Processing/Processed/
  Rejected/Returned` lifecycle entirely — this endpoint and lifecycle do not
  exist in real Banking Circle from buddy's side.
- **Auth (outbound, buddy→BC; our mock is the server here so it verifies
  this):** mTLS (client cert required — Go stdlib `crypto/tls`,
  `ClientAuth: tls.RequireAndVerifyClientCert`, no extra dependency) +
  `GET /api/v1/authorizations/authorize` with `Authorization: Basic
  base64(username:password)` → `{access_token, expires_in, token_type}`.
  Every subsequent call requires `Authorization: Bearer <token>` **and** the
  client cert.
- **Balance:** `GET /api/v1/accounts/{accountId}/balances?pageNumber=&pageSize=`
  → `{result: [{type:"CurrentBalance", currency, beginOfDayAmount,
  financialDate, intraDayAmount, lastTransactionTimestamp,
  blockedBalanceAmount?}], pageInfo:{currentPage,pageSize,rowCount?}}`.
- **Subscription lifecycle** (buddy must subscribe before receiving
  webhooks): `GET/POST /api/v1/notificationselfservice/subscription`,
  `GET/PUT(.../activate)/DELETE /api/v1/notificationselfservice/subscription/{id}`,
  `POST /api/v1/notificationselfservice/subscriptionEvent`. `PUT
  .../deactivate` exists in real BC but buddy never calls it — implement it
  anyway (harmless, real).
- **Webhook delivery** (our mock is the sender): `POST` to the configured
  subscription endpoint, `Content-Type: application/octet-stream`, raw AES-256-GCM
  ciphertext body, headers `Nonce` / `AuthenticationTag` / `Checksum` (all
  base64). Algorithm, exact: JSON-marshal the notification batch to a string;
  UTF-16LE-encode that string to bytes; AES-256-GCM-encrypt those bytes with
  a random 12-byte nonce, key = the raw UTF-8 bytes of the 32-character
  configured key (**not** base64-decoded — this is what real BC does,
  confirmed against buddy's decrypt code, even though the env var doc
  comment everywhere says "base64"); `Checksum` = base64(SHA-256(UTF-8 bytes
  of the original JSON string)). Send ciphertext and auth tag as **separate**
  values (Go's GCM `Seal` appends the tag to the ciphertext by default —
  split them before sending, since BC's own wire format keeps them apart).
- **Notification payload**: `{notifications: [{eventId, subscriptionId?,
  subscriptionEventId?, notificationType, timestamp, targetId?, payment?,
  payload?}]}`. The `payment` object has the two shapes of the vendor's
  payload examples (docs/payload-examples), and amounts are JSON numbers
  throughout:
  - **Booked** (`OutgoingPaymentBooked`, `IncomingPaymentBooked`):
    `{paymentId, transactionReference, valueDate, transactionDate, amount,
    currency, transfer?:{remittanceInformation}}` — no status, no parties.
    `amount` is signed by the effect on the balance: a payout's booking is
    negative, money in and a reversal's booking positive.
  - **Status events** (`OutgoingPaymentProcessed`, `OutgoingPaymentRejected`,
    `MissingFunding`, `Reversed`, `IncomingPaymentProcessed`):
    `{paymentId, transactionReference, status, return, debtorInformation,
    creditorInformation, transfer?}`. `status` is the payment's status
    (`Processed`, `Rejected`, `MissingFunding`, `Reversed`), not the event
    name. Only our side is set: money leaving the safeguarding account
    carries `debtorInformation: {accountId, debitAmount, instruction:
    {amount}}` with `creditorInformation: null`; money arriving carries
    `creditorInformation: {accountId, creditAmount}` with
    `debtorInformation: null`. `return` is `true` on an incoming return
    payment and null otherwise. `transfer.amount` is present on processed,
    reversed and incoming processed payments (nothing was transferred on a
    rejection or missing funding), and `transfer.remittanceInformation`
    wherever there is remittance.
  - `transactionReference` is the bank's own reference (`010F10…`, the
    report's `paymentReferenceNumber`); a sender's reference (a Worldline
    lump sum's, a return's "RETURN OF PAYMENT" lines) travels in the
    remittance information.
- **`notificationType`** — the full 13-value closed set (send only these):
  `IncomingPaymentProcessed`, `IncomingPaymentBooked`,
  `OutgoingPaymentProcessed`, `OutgoingPaymentBooked`,
  `OutgoingPaymentRejected`, `MissingFunding`, `Reversed`, `PaymentRouting`,
  `PaymentStatus`, `OutgoingDirectDebitPendingProcessing`,
  `AccountHolderVerification`, `CaseEvents`, `AgencyBankingWhitelistResult`.
  This lab only needs to *drive* the outgoing-payment subset
  (`OutgoingPaymentBooked → OutgoingPaymentProcessed | OutgoingPaymentRejected`,
  plus `Reversed`, `MissingFunding`) — implement the rest of the enum for
  wire-shape completeness but no lifecycle needs to produce them.
- **The B4B↔BC bridge**: Banking Circle has no idea a payment exists until
  something (in this lab: the B4B mock, once its own payment reaches
  `B4BTMApproved`) tells it to. Model this as: B4B mock, on approving a
  payment, calls a small internal endpoint on this lab's Banking Circle mock
  (`POST /internal/payments` — lab-only, not a real BC endpoint, just the
  seam between our two mocks) carrying the real BC `paymentId` it's about to
  reference in its own webhook `banking_circle_api_response.paymentId`, and
  Banking Circle then drives that payment's notification lifecycle
  (`OutgoingPaymentBooked → Processed|Rejected`) independently, on its own
  delay, matching the real "two independent webhooks, joined by a
  Banking-Circle-originated id" shape.

## 4. B4B: build the real mock (payment-api's role, corrected)

Source: `apps/accounts-settlement/src/settlement/merchants/b4b-*.ts`,
`apps/gateway/src/modules/b4b/callback/**`, buddy's own
`apps/settle-integration-testing/src/b4b-mock-server.ts` (existing minimal
mock — canned response only, no webhook simulation; this lab's version is a
fuller lifecycle, per product decision).

- **Auth (inbound; our mock is the server, verifies this):** `Authorization:
  Bearer <JWT>`, `alg: RS512`, `kid` header must match a configured key id,
  claim `{aud: "b4b-payments"}`. Verify via Go stdlib `crypto/rsa` +
  `crypto/sha512` (manual 3-part JWT parse + `rsa.VerifyPKCS1v15` — no JWT
  library needed for verification-only).
- `GET /oversight/v1/beneficiaries/{id}` → `{id, external_ref?, account_name,
  account_number, financial_institution, sanctions_status?}`.
- `POST /oversight/v1/payments` request: `{external_ref, beneficiary_id,
  company_id, callback_url, sca_applied, amount:{amount,currency},
  currencyOfTransfer, debtorViban:{account}, debtorAccount?:{account,
  financialInstitution?}, creditorAccount:{account,financialInstitution},
  creditorName, chargeBearer:"SHA"|"DEBT"|"CRED", requestedExecutionDate}`.
  Response (202): `{id, status:"B4BAccepted", payload:{amount,
  currencyOfTransfer, debtorViban, creditorAccount, creditorName,
  debtorReference?}}`.
- **Status enum** (exact wire strings): `B4BAccepted → B4BSanctionsPending →
  B4BSanctionsApproved → B4BTMPending → B4BTMApproved | B4BFailed`. Progress
  through this automatically after a configurable delay (mirroring Banking
  Circle's `PROCESSING_DELAY` pattern already in this lab).
- **Webhook** (our mock is the sender, POSTs to a configured
  `B4B_CALLBACK_URL`): body `{id, status, payload?, banking_circle_api_response?:
  {paymentId?, status?, error?}}`. Only the `B4BTMApproved` event carries
  `banking_circle_api_response.paymentId` — this is the seam into the
  Banking Circle mock described above (call its internal endpoint with the
  same generated `paymentId` at the moment this webhook fires).
- Fire one webhook per state transition (`B4BAccepted` immediately on
  create, then the rest on delay), matching this lab's existing
  fire-every-transition convention (`notifier`, `banking-circle`).

## Existing Patterns Followed

Everywhible not called out above: Go 1.22, `env()`/`shortID()`/`logReq()`
per-binary duplication, `internal/httputilx` responses, `internal/money` for
decimals, `internal/retry`+`internal/allowlist`+`internal/webhook` for any
signed-and-retried delivery, fully-qualified Docker base images, non-root
`USER 65532:65532`, `internal/waitfor` instead of compose `depends_on` for
any cross-service startup ordering, gofmt/vet clean, tests matching existing
tone.

## See Also

- `ARCHITECTURE-banking-circle.md` — superseded by section 3 above for the
  webhook/lifecycle/auth model; its ledger/account seed shape (VIBANs,
  `bc_acc_worldline`/`bc_acc_merchant`) stays.
- `ARCHITECTURE-settlement-report.md`, `-settlement-state-machine.md`,
  `-reconciliation-api.md`, `-sftp-staging.md`, `-worldline-sftp-channel.md` —
  unchanged; this doc adds to, not replaces, all five.

## Addendum: resolved integration gaps (resolved before build dispatch)

Six gaps found while re-reading this doc against the actual current
`cmd/settlement` / `internal/bankingcircle` code, closed here so all four
build tasks share one answer instead of improvising four different ones.

### A. Settlement calls B4B directly now, not Banking Circle

`cmd/settlement`'s existing `submitPayout()` POSTs straight to Banking
Circle's `/payments` — that endpoint is being removed (section 2). Redirect
it to B4B's `POST /oversight/v1/payments` instead (settlement is this lab's
stand-in for `accounts-settlement`, the real caller). Concretely:

- Settlement calls `GET {B4B_URL}/oversight/v1/beneficiaries/ben_{merchantID}`
  first (real shape calls this before paying), then
  `POST {B4B_URL}/oversight/v1/payments` with `external_ref` set to the
  settlement record's own ID, `beneficiary_id: "ben_{merchantID}"`,
  `callback_url: "{B4B_CALLBACK_URL}"` (default
  `http://settlement:8083/internal/b4b-webhook`), `amount` from the
  record's `SettlementNetAmount`, `debtorViban` a fixed lab constant,
  `creditorAccount` derived from the merchant ID, `chargeBearer: "SHA"`,
  `requestedExecutionDate` = today (UTC, `YYYY-MM-DD`).
- Every call carries `Authorization: Bearer <RS512 JWT>`: manually built
  (header `{alg:"RS512",kid:B4B_JWT_KEY_ID}`, claim `{aud:"b4b-payments"}`),
  signed via stdlib `crypto/rsa`+`crypto/sha512` against a private key read
  from `B4B_JWT_PRIVATE_KEY_PATH` — the mirror image of the verification
  B4B itself implements (section 3), no JWT library either side.
- Settlement's existing `a.store.SetPayout(rec.ID, payoutID, payoutState)`
  (already implemented, do not add a new store method) is reused as-is:
  `payoutID` = B4B's returned `id`, `payoutState` = B4B's `status` string,
  updated again whenever `POST /internal/b4b-webhook` receives a later
  transition for that `id`. Correlate the webhook back to a settlement
  record by linear-scanning `a.store.List()` for `PayoutID == body.id`
  (small N, matches this store's existing List-then-filter style
  elsewhere).
- Remove the `bcURL`/`bcClient` app fields and `BANKING_CIRCLE_URL` env var
  entirely — settlement no longer talks to Banking Circle at all, direct or
  otherwise. New env vars: `B4B_URL` (default `http://b4b:8086`),
  `B4B_JWT_PRIVATE_KEY_PATH` (default `/b4b-keys/private.pem`),
  `B4B_JWT_KEY_ID` (default `b4b-mock-1` — must equal B4B's own
  `B4B_JWT_KEY_ID` default, see D below), `B4B_CALLBACK_URL`.

### B. B4B->Banking Circle bridge gets one more field: `externalRef`

The bridge payload in section 2 is amended to add a pass-through field:

```
POST http://banking-circle:8085/internal/payments
{"paymentId":"bcp_xxx","accountId":"<the beneficiary's account UUID, see addendum G>","amount":"123.45","currency":"EUR","externalRef":"<verbatim external_ref from B4B's own payment-creation request>"}
```

Banking Circle's internal handler stores `externalRef` on its existing
`Payment.SettlementID` field (already present, already serialized as
`json:"settlementId,omitempty"` — do not rename the Go field or its JSON
key). This means `GET /payments` / `GET /reconciliation` keep exposing
`settlementId` exactly as before, so existing harness correlation-by-
`settlementId` code needs zero changes once B4B is what populates it
instead of settlement calling Banking Circle directly. Without this field
the settlement<->Banking-Circle-payment link is unrecoverable — this is
not optional.

### C. Banking Circle runs two listeners, not one

mTLS (`ClientAuth: tls.RequireAndVerifyClientCert`) applies to an entire
`http.Server`, not per-route — so it cannot cover both the real-shaped API
(which must require a client cert, matching real Banking Circle) and
`/internal/payments` (which must NOT, per section 2's "no auth on this
seam" — B4B has no client cert). Run two listeners in one process:

- `LISTEN` (default `:8085`, unchanged port): mTLS, serves
  `/api/v1/authorizations/authorize`, `/api/v1/accounts/*`,
  `/api/v1/notificationselfservice/*` — every real-shaped endpoint from
  section 2.
- `INTERNAL_LISTEN` (default `:8095`, new, plain HTTP, no TLS): serves only
  `POST /internal/payments` (the B4B bridge) and a manual test hook
  equivalent to the old `POST /payments/{id}/return` — e.g.
  `POST /internal/payments/{id}/reverse` — for the harness to
  deterministically exercise the `Reversed` notification path, the same
  purpose `Return()` served before.

### D. B4B's JWT keypair is shared via a directory, not a value

B4B generates (if missing) and persists an RSA keypair under
`B4B_JWT_KEYS_DIR` (default `/b4b-keys`) as `private.pem`/`public.pem`,
printing the public key and `kid` at startup. `B4B_JWT_KEY_ID` default on
**both** the B4B mock and settlement is the literal string `b4b-mock-1` —
two different `package main` binaries, so this is a matched default, not a
shared constant (same duplication convention as `env()`/`shortID()`
elsewhere in this repo). The directory becomes a Docker volume shared
read-write (B4B) / read-only (settlement) — wiring that volume in
`compose.yml` is the integration owner's job, not this task's.

### E. PGP encryption direction (mock-Worldline's `download/` files)

Real Worldline encrypts settlement files *to the recipient's* (Infinite's)
PGP public key, and signs with its own private key. This lab has no
separate "Infinite" keypair by default, so: encrypt to
`INFINITE_PGP_PUBLIC_KEY_PATH`'s public key **if configured**, else
self-encrypt to mock-Worldline's own generated public key. Always sign
with mock-Worldline's own private key regardless. This makes decryption
possible out of the box for anything holding mock-Worldline's own
(generated, persisted, printed-at-startup) private key — e.g. a future
harness SFTP+PGP client — with zero extra keypair setup, while still
supporting a real separate recipient key when one is configured.

### F. B4B beneficiaries auto-vivify

There is no beneficiary registration step in this lab (buddy's real
registration flow is out of scope). `GET /oversight/v1/beneficiaries/{id}`
returns a deterministic synthetic beneficiary for **any** requested `id`
(fake `account_name`/`account_number`/`financial_institution` derived from
`id`, `sanctions_status: "CLEAR"`) — same "seed on first read" philosophy
as `bank`'s fake accounts. Settlement derives the id it requests as
`"ben_" + merchantID` (A above); B4B does not need to know or validate
that derivation, it accepts whatever id it is asked for.

### G. Banking Circle account ids are UUIDs

Every Banking Circle account id in this lab is a UUID, because every real
one is — and, more to the point, because every service between the platform
and this simulation validates it as one before forwarding. buddy's
`apps/banking-circle` answers

```
GET /api/v1/internal/accounts/bc_acc_sga_eur/balances
→ 400 {"message":"Validation failed (uuid is expected)"}
```

so the previous scheme (`bc_acc_sga_eur`, `bc_acc_` + beneficiary id) could
not be reached *through* the platform's own Banking Circle service at all.
It could only be reached by going around it, straight to this lab's
no-auth bridge on 8095 — which meant the one integration the lab exists to
exercise was the one it never exercised. The mismatch stayed invisible for
as long as the only caller was the SGA balance check, because this lab
happens to serve that same path shape itself; it surfaced the moment a
second route (the BC payment reconciliation sweep) was added and 404'd.

- **Safeguarding accounts** are fixed, obviously-synthetic constants ending
  in the currency's ISO 4217 numeric code, so they are readable at a glance
  and still structurally valid:
  `00000000-0000-4000-8000-000000000978` (EUR, 978) and
  `…000000000826` (GBP, 826). `internal/bankingcircle.SGAAccountEUR` /
  `SGAAccountGBP`, and the `BC_SAFEGUARDING_ACCOUNT_ID_{EUR,GBP}` defaults
  in `compose.yml` and `cmd/settlement`.
- **Derived accounts** — the per-merchant creditor accounts B4B names when
  it bridges a payout — come from `bankingcircle.AccountIDFor(key)`, RFC
  4122 v5 over a fixed lab namespace (`internal/uuidx`). Deterministic, so
  B4B naming a creditor and the harness later checking that merchant's
  balance land on the same account without talking to each other; and real
  v5 rather than an invented hash-to-hex, so any other implementation
  reproduces it. A key that is already a UUID passes through unchanged:
  when the platform holds a real account id, that *is* the account, and
  re-deriving would pay someone else.
- **A non-UUID account id is refused**, not coerced and not
  auto-vivified: `ErrInvalidAccountID` from `Ledger.GetOrCreate`, and 400
  (not 404) from both balance routes. Auto-vivification is the one door
  that opens for an unseen id, which makes it the one place a typo mints a
  funded, perfectly balanced account belonging to nobody. 400 rather than
  404 is deliberate — "no such account" sends the reader looking for
  missing data, "that is not an account id" sends them to their own
  configuration, which is where the fault is.

Clean cutover, no aliases. Accepting the old ids would preserve exactly the
configuration that cannot work against the real vendor, which is the
failure this lab is built to catch.

### H. Banking Circle reconciliation reads

Every date the lab puts on a booking — both reports' transaction and value
dates, the booked webhooks' `valueDate`/`transactionDate` — is Banking
Circle's business day, not the UTC calendar day: Central European time
(read as Europe/Paris), with the day ending at 19:00 ("Payments after 19:00
CET will appear in the next business day's report") and weekends rolling to
Monday. Bank holidays are not modelled; the docs do not list them.

A payment sweep (buddy's `BC_PAYMENT_RECONCILIATION` stage) confirms
payouts without webhooks, from three vendor-shaped reads on the mTLS
listener, all derived from the same payment records the notifications are:

- `GET /api/v1/reports/intraday-reconciliation-paged-report` — booked and
  processed payments, no status field. `processedTimestamp` is null while a
  payment is only booked (the real report's pendingProcessing) and set once
  it is processed. A scheme reversal (`/internal/payments/{id}/reverse`)
  leaves the payment's DBIT row in place and adds a second DBIT row on the
  same paymentId with `debitAmount` negated, on the day of the reversal,
  its reason in `statusReasonDescription` — the docs' "negative equivalent
  of the original amount debited". Subscribers get a second
  `OutgoingPaymentBooked` for the reversal booking (same paymentId and
  transactionReference), then `Reversed`, as the vendor's payload examples
  describe. `return` stays null: the docs reserve it
  for an incoming return payment, a separate payment (see the return hook
  below). As on the real
  bank, `FromTransactionDate`, `ToTransactionDate`, `FromCreatedAt`,
  `ToCreatedAt`, `PageNumber` and `PageSize` are required: a missing or
  malformed one is a 400 ProblemDetails (shape below, with the rejection
  report), not a defaulted page. `paymentId`, `processedTimestamp` and
  `return` are not in the default property list, so they come back null
  unless requested with
  `PropertiesIncluded` (or everything, with an empty `PropertiesExcluded`);
  a property not selected is sent as null, as in the reference's example.
  Values follow the docs too: `creditDebitIndicator` is `DBIT`/`CRDT`,
  `return` is `true` or null (never `false`), `paymentReferenceNumber` is
  the bank's own reference (`010F10…`, assigned when the payment is
  accepted, opaque to clients), and `clientOrderId` is null — the bank only
  fills it for FX trades. In both reports `account` is the account's IBAN
  (from the ledger), not its id; the id is only what `AccountId` filters
  on.
- `GET /api/v1/reports/rejection-report` — outgoing payments instructed
  on `TransactionDate` (required) that could not be processed, with every
  property the reference lists (`pIdChanneluser`/`pTxndate` spelled as it
  spells them; `pIdChanneluser` and `customerId` null, since the lab has no
  users or customer ids). No `paymentId`: `paymentReferenceNumber`, the
  bank's `010F10…` reference, is the handle back. `status` is `Rejected`,
  `Insufficient Funds` (missing funding) or `Received` (pending
  processing, with an empty `statusReason`); the guide says blank for
  pending, and this follows the API reference's example instead.
  `sourceType` is `Single payment` and `fileReferenceNumber` `""`, as in
  that example. `IncludeReceived` and `IncludeMissingFunds` (default true)
  drop their kind, and `ExcludeBooked` drops pending payments that have
  already booked. `IncludeReversals` covers direct-debit reversals only,
  which the lab has none of, so an outgoing reversal never appears. Both
  reports answer a bad request with the reference's 400 example shape:
  `type` rfc7231, `extensions.traceId`, and `errors` keyed by camelCase
  parameter ("A value for the 'TransactionDate' parameter or property was
  not provided.").
- `GET /api/v1/payments/singles/{payment-id}/status` — `{"status": ...}` in
  Banking Circle's `PaymentStatus` vocabulary (`PendingProcessing`,
  `Processed`, `Rejected`, `MissingFunding`, `Reversed`); 404 for an
  unknown id. The sweep's fallback for anything the intraday report does
  not show as processed, rejections in particular.

The engine only rejects a payout when its ledger move fails, so the
internal listener has one more lab-only hook, beside `/reverse`:
`POST /internal/payments/outcomes` with `{"outcomes": ["Rejected",
"Pending"]}` makes the next outgoing payments, one each and in order, end
`OutgoingPaymentRejected` (no money moves) or stay `OutgoingPaymentBooked`
for good. `GET` on the same path shows what is still queued. Pair it with
`/sim/subscription/{id}/pause` to leave the sweep as the only path that
resolves a payout.

`POST /internal/payments/{id}/return` (body optional: `{"reasonCode":
"AC04", "reasonDescription": "Closed account number"}`) is the beneficiary's
bank sending a processed payout back. A return is not a status in Banking
Circle, so the payout stays `OutgoingPaymentProcessed` (its status read
still says `Processed`). The money comes back as a new incoming payment,
with its own paymentId and reference and `return: true`. It is booked and
processed like any incoming payment (`IncomingPaymentBooked`, then
`IncomingPaymentProcessed`), and its webhook carries `"return": true` and
remittance lines "RETURN OF PAYMENT", the payout's reference, and the reason
when given. On the report it is a CRDT row with `return: true`, those lines
in `paymentDetails1-4`, and `/RETN/`, `/<code>/<description>` and
`/MREF/<payout reference>` in `additionalRemittanceInformation1-3`. Only a
processed payout that has not already come back can be returned (409
otherwise), and a returned payout can no longer be reversed. The money moves
back in this service's ledger only; the beneficiary bank service the payout
was forwarded to is not debited.

### Port/env registry (new pieces only)

| Service | Port(s) | New env vars |
| --- | --- | --- |
| `b4b` (new) | 8086 | `B4B_JWT_KEYS_DIR`, `B4B_JWT_KEY_ID`, `WEBHOOK_ALLOWLIST`, `B4B_FORCE_FAILURE_BENEFICIARY_IDS`, `BANKING_CIRCLE_URL` (its `/internal/payments` bridge target, default `http://banking-circle:8095`) |
| `banking-circle` | 8085 (mTLS), 8095 (plain, new) | `INTERNAL_LISTEN`, plus mTLS cert/key/client-CA paths |
| `sftp-gateway` | 8084 (unchanged), 2222 (new, SSH) | `WORLDLINE_SFTP_PORT`, `WORLDLINE_SFTP_HOST_KEY_PATH`, `WORLDLINE_SFTP_AUTHORIZED_KEY`/`_PASSWORD`, `WORLDLINE_PGP_PUBLIC_KEY_PATH`, `WORLDLINE_PGP_PRIVATE_KEY_PATH`, `INFINITE_PGP_PUBLIC_KEY_PATH` |
| `settlement` | 8083 (unchanged) | `WORLDLINE_SETTLEMENT_IDENTIFIER`, `B4B_URL`, `B4B_JWT_PRIVATE_KEY_PATH`, `B4B_JWT_KEY_ID`, `B4B_CALLBACK_URL` — and `BANKING_CIRCLE_URL` is **removed** |

