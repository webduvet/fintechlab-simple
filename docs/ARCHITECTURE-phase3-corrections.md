# Architecture: Phase 3 Corrections (payout-facilitator flow, buddy-verified)

## Goal

Verify Phase 2's mocks against the actual business flow the platform runs
(payment facilitator / white-label acquiring, safeguarding-account model) and
close the gaps. Every fact below is sourced from buddy's own code (paths
cited), not inferred. This doc is the contract for Phase 3; it does not
restate Phase 2 (`ARCHITECTURE-vendor-corrections.md`), it corrects and
extends it.

## The business flow (as described, buddy-confirmed)

Infinite is a payment facilitator / white-label platform. Merchants (and
their outlets — `MID` in the Worldline settlement file) receive money through
it. Worldline is the acquirer; Banking Circle holds the safeguarding account
(SGA) Worldline pays into; ACI is the online card gateway; B4B is the payout
rail; a merchant-verification step gates payout.

```
Worldline (acquirer)
  -> lump sum lands in Banking Circle SGA          [1]
  -> settlement file pushed to SFTP                [2]
Banking Circle
  -> webhook: funds landed (IncomingPayment*)       [3]  <- was NOT modeled at all
platform (buddy, out of scope for this lab)
  -> retrieves settlement file + confirms funds
  -> processes: internal virtual-account bookkeeping [platform-only, not mocked]
  -> per merchant: verify (compliance check)         [4]  <- was NOT mocked
  -> per merchant: B4B oversight API payment          [5]  built Phase 2, wire bugs fixed here
B4B
  -> sanctions/TM lifecycle -> bridges into Banking Circle [6] built Phase 2
Banking Circle
  -> drives the outgoing payment lifecycle, webhook   [7]  built Phase 2

separately:
ACI (card gateway)
  -> customer pays (Visa / Shopify)
  -> webhook notification                             [8]  <- was NOT mocked
  -> recorded, matched against the settlement file for fees later [platform-only]
```

**What this lab mocks**: Worldline (file + SFTP+PGP), Banking Circle (SGA
ledger + webhook + reconciliation), ACI (webhook), B4B (payout rail),
merchant-verification (stub, always-positive). **What it does not**: the
platform's own virtual-account/fee/reconciliation logic, settlement-file
decryption-and-parsing by the platform, or anything in
`docs/principles.md`'s existing "not this" list. This lab is a *test
harness*: mock the 3rd parties to a real-enough contract that a developer's
real code can be pointed at it unmodified.

## 1. Banking Circle: the SGA was never modeled as an account that starts empty

Source: `apps/accounts-settlement/.env.example:29-32` (`BC_SAFEGUARDING_ACCOUNT_ID_EUR`/`_GBP`),
`apps/accounts-settlement/src/settlement/mailer/safeguarding-missing-funding-notification.service.ts`,
`apps/settle-processing/src/app/services/sga-balance-check.service.ts`,
`apps/banking-circle/src/modules/webhooks/bc-webhook/dto/bc-webhook-notification.dto.ts`
(`IncomingPaymentBooked`/`IncomingPaymentProcessed` in `BcNotificationType`),
`apps/banking-circle/src/modules/internal-api/accounts/internal-account-balance.controller.ts`.

**What was wrong:** `internal/bankingcircle`'s ledger seeds a fixed-ID debtor
account (`bc_acc_worldline`) with "a large opening balance, so payouts
succeed by default." Real Banking Circle's safeguarding account starts at
whatever Worldline has actually transferred — buddy runs an hourly
`BC_ACCOUNT_BALANCE_CHECK` against it precisely because it can be
insufficient (`MissingFunding` fires a "Safeguarding Account" alert email,
`safeguarding-missing-funding-notification.service.ts:11`). Pre-funding the
mock skips the entire "did the money actually land" step the user's flow
starts with, and `IncomingPaymentBooked`/`IncomingPaymentProcessed` — real,
confirmed notification types — were never fired by anything.

**Fix:**
- Rename the seed accounts to a safeguarding-account model, one per
  currency, **starting at zero**: the EUR safeguarding account (VIBAN
  `BE00SIMSGA00000001`, holder "Infinite Safeguarding Account EUR") and the
  GBP one (VIBAN `GB00SIMSGA00000001`, holder "...GBP"). Delete
  `bc_acc_worldline`/`bc_acc_merchant` outright — clean cutover, no aliases.
  *(Their ids were `bc_acc_sga_eur`/`bc_acc_sga_gbp` when this was written;
  they are now UUIDs — see vendor-corrections addendum G, which supersedes
  every account id on this page.)*
- New `Ledger.Credit(toID string, cents int64) (*Account, error)` —
  incoming payments have no ledger "from" side in this model (the money
  arrives from outside any account this lab tracks).
- New `Engine` capability: simulate an incoming payment landing —
  synthesizes a `Payment` (FromAccountID: a fixed placeholder
  `"external_worldline"`, ToAccountID: the resolved SGA account), credits
  the ledger, and fires `IncomingPaymentBooked` immediately then
  `IncomingPaymentProcessed` after the existing `PROCESSING_DELAY`, through
  the same `notify`/subscription-fanout path outgoing payments already use.
- New endpoint `POST /internal/incoming-payments` (`INTERNAL_LISTEN`, same
  lab-only trust boundary as the B4B bridge): body
  `{"currency":"EUR","amount":"12500.00","reference":"worldline-lump-sum-1"}`
  → resolves currency to the matching SGA account (400 on an unconfigured
  currency — real BC would have the same problem, there is no account to
  credit), calls the engine, returns 202 with the created payment. This is
  the harness's "Worldline's lump sum landed" trigger — same category as
  the existing `/internal/payments/{id}/reverse` test hook.
- **Per-merchant creditor accounts auto-vivify.** `createInternalPayment`
  currently 404s unless `accountId` was already seeded — fine when B4B
  always sent the same fixed `bc_acc_merchant`, wrong once B4B (section 4
  below) derives a distinct account per beneficiary. New
  `Ledger.GetOrCreate(id, currency string) *Account`: same "seed
  deterministically on first read" philosophy as `bank`'s fake accounts and
  B4B's beneficiary auto-vivification (VIBAN/holder hash-derived from
  `id`), thread-safe, called from `createInternalPayment` instead of `Get`.
- **The outgoing debtor account is resolved by currency, not a constant.**
  `createInternalPayment` maps `req.Currency` to the matching
  safeguarding account and debits that — 400 on an unconfigured currency.
- **New internal (no-auth) balance proxy**, mirroring buddy's own
  `InternalAccountBalanceController` ("other Infinite services/Lambdas that
  need Banking Circle data without holding BC credentials themselves" —
  `internal-account-balance.controller.ts:7-10`): `GET
  /internal/accounts/{accountId}/balances` on `INTERNAL_LISTEN` →
  `{"accountId":"...","balances":[{same shape as the mTLS endpoint's
  result[]}]}`. This is what lets settlement (section 5) check SGA
  sufficiency without holding Banking Circle's mTLS+bearer credentials
  itself — exactly buddy's own reason for that endpoint's existence.
- **`POST /api/v1/notificationselfservice/clienttest/{subscriptionId}`**
  (`LISTEN`, mTLS+bearer) — real BC sandbox feature, confirmed via buddy's
  own local Banking Circle mock (`apps/banking-circle/local/tools/bc-mock-server.mjs`,
  README.md:348-350: "mirrors the real BC sandbox endpoint"). Sends one
  synthetic `PaymentStatus` notification to the subscription's registered
  URL through the same encrypted pipe — lets a developer confirm their
  webhook receiver is reachable without waiting for a real payment event.
- **`SubscriptionVersion` header** added to notification delivery (buddy's
  own local mock sends it: README.md:340-344). `Subscription` gains a
  `Version int` field (starts at 1). Buddy's actual inbound guard
  (`bc-webhook.guard.ts:57-59`) does not check this header's value — this
  is a wire-completeness addition, not something anything currently
  validates.
- `docs/ARCHITECTURE-banking-circle.md`'s ledger/VIBAN section (the one
  part Phase 2 left in effect) is superseded by this section; update its
  "See Also" cross-reference.

## 2. ACI: the card-gateway webhook was entirely unmocked

Source: `apps/settle-aci-webhook/src/webhook/{aci-webhook.controller,aci-webhook.service,aci-decryption.service}.ts`,
`apps/settle-aci-webhook/src/webhook/aci-decryption.service.spec.ts`,
`apps/settle-aci-webhook/.env.example`, `apps/settle-aci-webhook/README.md`,
`.env.example:51-54` (root).

**Business shape (user-confirmed):** ACI is the payment gateway for online
payments. A customer pays with Visa or via Shopify; ACI sends Infinite a
webhook notification; Infinite records the payment so it can calculate fees
and match it against the settlement file later — confirmed exactly by
buddy's own downstream consumers: `settle-calc`'s Shopify per-transaction
fee count (`calc.lambda.ts:72-84`, `countShopifyByAuthEntityId`, `pluginType
== 'SHOPFY'`) and the ACI↔Worldline chargeback matcher
(`worldline-chargeback-matcher.service.ts`, matches Worldline's settlement-file
`transactionRef` against ACI's `merchantTransactionId`).

**Auth is payload encryption, not a header signature or mTLS:** AES-256-GCM
over the JSON body, using a shared secret (`ACI_WEBHOOK_SECRET`, 32 bytes,
**hex**-encoded — different from Banking Circle's raw-UTF-8-bytes key
handling; each vendor's key encoding is implemented exactly as that vendor
does it, never shared/generalized between the two). IV and auth tag arrive
as **hex** (not base64, unlike Banking Circle) in headers
`X-Initialization-Vector` / `X-Authentication-Tag`
(`aci-webhook.controller.ts:16-19,38-39`). Body is the hex ciphertext,
either raw (`Content-Type: text/plain`) or JSON-wrapped
`{"encryptedBody":"<hex>"}` — the controller sniffs a leading `{`
(`aci-webhook.controller.ts:70`) to tell them apart. A GCM tag mismatch on
decrypt *is* the auth failure (`aci-decryption.service.ts:5-9`).

**Components:**
- New `internal/aci` package: `Encrypt(plaintext []byte, key []byte) (ciphertextHex, ivHex, tagHex string, err error)`
  — AES-256-GCM, random 12-byte IV, Go's combined `Seal` output split into
  ciphertext/tag (same split-not-combined wire shape as Banking Circle,
  independently required here because Node's `setAuthTag`+`.final()` also
  needs them apart), each hex-encoded separately (`encoding/hex`, not
  base64). `Notification` struct matching `AciWebhookNotificationPayload`
  verbatim (`aci-decryption.service.ts:39-101`): `Type`, and `Payload` with
  `ID, MerchantTransactionID, PaymentType, PaymentBrand,
  PresentationAmount, PresentationCurrency, PluginType, Source, ShortID,
  Timestamp, ChannelName, PaymentMethod, NDC, Result{Code,Description},
  ResultDetails{RiskOrderID,TransactionID,Action}, Card{Bin,Last4Digits,
  Holder,ExpiryMonth,ExpiryYear,Type,Country}`. Every JSON key matches
  ACI's real mixed casing exactly (e.g. `RiskOrderId`, not `riskOrderId`).
- **Known-answer test, not just a round-trip**: `aci-decryption.service.spec.ts:6-11`
  embeds ACI's own reference vector (verbatim, not a secret — it's ACI's
  published sample):
  `SAMPLE_KEY_HEX=232066076155F13C1027C662E62C0710977FA743C8D37766EF03DD94045C6981`,
  `SAMPLE_IV_HEX=C1751E8F5E9F2960CA4D6D6C`,
  `SAMPLE_TAG_HEX=AA4732C45B76FC9C2F64355808462541`, and a full sample
  ciphertext. `internal/aci`'s test decrypts that exact vector with Go's
  stdlib and asserts the known plaintext fields
  (`merchantTransactionId=="123412341235"`, `paymentType=="RX"`,
  `resultDetails.RiskOrderId=="077869000001AD320260428101938765"`) — this
  proves Go's implementation is wire-interoperable with buddy's real Node
  decryptor, not just self-consistent.
- New `cmd/aci` binary, plain HTTP, port `8087`. `POST
  /internal/simulate-payment` (lab trigger, "a card payment just
  happened"): body `{merchant_transaction_id, payment_type, payment_brand,
  presentation_amount, presentation_currency, plugin_type, source,
  result_code, result_description}` (snake_case, this lab's own trigger
  shape) → builds the real camelCase payload, encrypts it, POSTs to
  `ACI_WEBHOOK_TARGET_URL` with the headers/body-mode above (delivery is
  fire-and-retry in a goroutine, same convention as every other sender in
  this lab), returns 202 immediately with a generated id. `GET
  /internal/sent` / `GET /internal/sent/{id}` — introspection: what was
  sent, decrypted-payload echo, delivery status. `GET /health`.
- Env: `LISTEN` (default `:8087`), `ACI_WEBHOOK_SECRET` (default a
  fake-obvious *valid* 64-hex-char value — obviously fake, not
  ACI's real sample, so it can't be confused with a credential), `ACI_WEBHOOK_TARGET_URL`
  (default `http://localhost:3013/api/v1/webhook` — buddy's own documented
  default for the bare microservice, `.env.example:3-4` in
  `apps/settle-aci-webhook`, so pointing this lab at a locally-running real
  `settle-aci-webhook` is zero-config), `ACI_WEBHOOK_BODY_MODE`
  (`raw`|`json`, default `raw`), `WEBHOOK_ALLOWLIST`, `RETRY_BACKOFF`,
  `MAX_ATTEMPTS` (matching every other sender).
- **Explicitly out of scope** (documented, not missed): ACI also has (a) a
  merchant-onboarding REST client (`AciGatewayService`, divisions/merchants/
  channels) and (b) a *separate* SFTP+PGP file-exchange channel registered
  in `acquirer-config.registry.ts`'s `'aci'` case (enrolment/receipt
  documents) — both fully env-var-partitioned from the webhook
  (`ACI_WEBHOOK_SECRET` appears in neither). Neither is the flow the user
  described (a customer paying online); this lab mocks the webhook only.

## 3. Merchant verification: a real, separate service exists — mock it, always-positive

Source: `apps/verification-service/src/{application.ts,controllers/decisions.controller.ts,
controllers/data.controller.ts,dto/decisions.dto.ts,dto/data.dto.ts}`,
`libs/shared/auth-core/src/lib/guards/internal-api-key.guard.ts`,
`prisma/schema.prisma:629-644` (decision/risk enums),
`apps/gateway/src/modules/verification/verification.service.ts`.

**Confirmed not `validation-service`** (that app is generic field validation
— IBAN checksum, company-registry lookup — no merchant id, no decision).
`verification-service` is keyed on `merchantApplicationId` and is the actual
go/no-go gate: its decision is what advances a merchant to
`UNDERWRITING_APPROVED` and can later `PAUSE_SETTLEMENTS` — i.e. exactly
"verify merchants" before they're paid.

**Auth:** single header `x-internal-api-key`, constant-time compared
against `VERIFICATION_INTERNAL_API_KEY` — not mTLS, not JWT
(`internal-api-key.guard.ts:11,19,24-25`).

**Components:**
- New `internal/verify` package: `Decision` struct
  (`MerchantApplicationID int`, `OverallDecision`,
  `ManualOverrideDecision`, `AmlDecision`, `AmlCompanyDecision`,
  `RiskDecision`, `RiskCompanyDecision string`, `RiskScore int`,
  `RiskClassification string`, `CompletedAt string`), matching
  `VerifyDecisionSnapshotResponseDto` field names
  (`data.dto.ts:1138-1217`). `Engine`: `Trigger(id int) *Decision` records
  a decision after `VERIFY_PROCESSING_DELAY` (default near-instant);
  `Get(id int) (*Decision, bool)`. Decision defaults to the real "approved"
  values verbatim from `prisma/schema.prisma:629-638,640-644`:
  `overallDecision`/`amlDecision`/`amlCompanyDecision` = `"APPROVED"`,
  `riskDecision`/`riskCompanyDecision`/`riskClassification` = `"LOW"`,
  `riskScore` = a fixed low value (`5`) — unless `id` is in a configured
  force-decline set (`VERIFY_FORCE_DECLINE_IDS`, same convention as B4B's
  `B4B_FORCE_FAILURE_BENEFICIARY_IDS`), in which case `overallDecision` =
  `"DECLINE"`.
- New `cmd/verify` binary, plain HTTP (matching verification-service's own
  real default, `http://localhost:3003` — no TLS even for the real service
  in local dev), port `8088`, global path prefix `/api/v1/verification`
  matching real (`application.ts:69-70`). `GET
  /api/v1/verification/health` — public, no key required (matching
  `HealthController`'s `@Public()`). `POST
  /api/v1/verification/verify/aml-decision` — requires
  `x-internal-api-key` — body `{"merchantApplicationId": 123}` → 202,
  triggers the decision. `GET
  /api/v1/verification/verify/data/verify-decision/{merchantApplicationId}`
  — requires the header — 200 with the decision, 404 if never triggered
  for that id.
- Env: `LISTEN` (default `:8088`), `VERIFICATION_INTERNAL_API_KEY` (default
  `sim-verify-key-dev-only`), `VERIFY_PROCESSING_DELAY`,
  `VERIFY_FORCE_DECLINE_IDS`.

## 4. B4B: three wire-shape bugs found comparing the mock against the real caller

Source: `apps/accounts-settlement/src/settlement/merchants/{b4b-payments.types,
b4b-payments.service,b4b-credential.resolver}.ts` — the real caller,
more authoritative for wire shape than buddy's own tiny integration-test
stub (`b4b-mock-server.ts`, which this lab's B4B mock already deliberately
exceeds per Phase 2's own product decision).

- **`amount.amount` is a JSON number, not a string.** Real
  `B4bPaymentAmount = {amount: number, currency: string}`
  (`b4b-payments.types.ts:21-24`). This lab's wire structs
  (`cmd/b4b/main.go`'s `amountWire`, `cmd/settlement/main.go`'s
  `b4bAmount`) both typed it `string`. A real client sending a numeric
  literal would fail to unmarshal against the mock as it stands (Go's
  `encoding/json` does not coerce a JSON number into a Go `string` field) —
  this breaks "point a real client at this mock" outright, the one thing
  this lab exists to guarantee. Fix: wire-level type is `json.Number` on
  both the B4B mock (`cmd/b4b/main.go`) and settlement's own
  client-construction code (`cmd/settlement/main.go`) — `json.Number`
  unmarshals any JSON numeric literal without float precision loss and
  marshals back out unquoted, matching the real shape exactly in both
  directions. The domain-level `b4b.Amount.Amount` stays a plain decimal
  `string` internally (unchanged; `internal/money` already works in
  decimal strings) — only the two wire-struct boundaries change, via
  `string(wireAmount)` / `json.Number(domainAmount)` conversions at the
  edges.
- **`AccountRef`/`b4bAccountRef` is missing `country`.** Real
  `B4bPaymentAccount = {account, financialInstitution?, country?}`
  (`b4b-payments.types.ts:26-30`). Add `Country string` (`json:"country,omitempty"`)
  to `internal/b4b.AccountRef`, `cmd/b4b/main.go`'s `accountRefWire`, and
  `cmd/settlement/main.go`'s `b4bAccountRef`, threaded through
  `debtorViban`/`debtorAccount`/`creditorAccount` in both the request and
  the response/webhook `payload` echo. Settlement populates it from the
  same currency→country map buddy itself uses
  (`b4b-credential.resolver.ts:8-11`: `EUR:"IE", GBP:"GB"`) — reuse that
  exact mapping, do not invent a different one.
- **Beneficiary response is missing `external_ref`.** Real
  `B4bBeneficiaryResponse.external_ref?: string` is optional
  (`b4b-payments.types.ts:12-19`) and not read anywhere in the real caller
  — add the field (`json:"external_ref,omitempty"`) to
  `internal/b4b.Beneficiary`/`beneficiaryResp` for shape completeness, left
  unset (a real response can legitimately omit it too).
- **The Banking Circle bridge account was hardcoded to one merchant.**
  `approveAndBridge` always sent `accountId: "bc_acc_merchant"` — every
  payout landed on the same ledger account regardless of which merchant
  was being paid, which cannot demonstrate "settle individual merchants."
  Fix: derive it from the beneficiary id instead —
  `accountId = bankingcircle.AccountIDFor(p.BeneficiaryID)` — paired with section 1's
  ledger auto-vivification on the Banking Circle side, so each distinct
  merchant gets its own tracked balance.
- **`debtorReference` is client-supplied; real B4B generates it.** Real
  `B4bPaymentCreateRequest` has no `debtorReference` field at all — it only
  appears in the *response* payload (`debtorReference?`,
  `b4b-payments.types.ts:57`), implying B4B itself assigns it. This lab's
  mock currently accepts it as request input, which a real caller never
  sends (harmless — it would just default empty — but backwards from how
  the field is actually populated). Fix: remove it from
  `createPaymentReq`; generate one server-side at creation time
  (`"B4BREF" + shortID()`), store on `Payment.DebtorReference`, echo it in
  the create response and every webhook exactly as before.

## 5. Settlement: wire in verification and the SGA balance gate

Source: same as section 1 (SGA balance check is what actually gates payout
dispatch in the real pipeline — `sga-balance-check.service.ts`) and section
3 (verification is what gates a merchant ever being paid at all).

**Order of operations** (`submitPayout`, unchanged trigger points — the
automatic `Settled` transition and the manual `/transition` endpoint):

1. Resolve the SGA account for `rec.Currency` via
   `BC_SAFEGUARDING_ACCOUNT_ID_EUR`/`_GBP` (env vars, **same names** as
   buddy's real ones; defaults are `bankingcircle.SGAAccountEUR`/`…GBP`,
   matching section 1's seeded ids). Unconfigured currency: log, `SetPayout(rec.ID,
   "", "submission_failed")`, return — a real deployment would have the
   same gap.
2. `GET {BC_INTERNAL_URL}/internal/accounts/{sgaID}/balances` — the
   internal, no-credential proxy from section 1 (settlement never holds
   Banking Circle's mTLS/bearer credentials, matching real
   `settle-processing`, which also goes through this internal API rather
   than calling Banking Circle directly). A connectivity/parse error here
   is fail-**closed**, not fail-open like the checks below: this gates
   real money movement, so "can't confirm funds" is treated as "don't
   pay" (`SetPayout(rec.ID, "", "balance_check_failed")`). Otherwise parse
   the balance; if less than `rec.Report.SettlementNetAmount`:
   `SetPayout(rec.ID, "", "awaiting_funding")`, return. This is the
   observable "waiting for Worldline's lump sum" state the harness
   demonstrates.
3. Call the verify mock: `POST
   {VERIFY_URL}/api/v1/verification/verify/aml-decision`
   (`x-internal-api-key: {VERIFY_INTERNAL_API_KEY}`), body
   `{"merchantApplicationId": <fnv32(rec.MerchantID)>}` — a stable derived
   int, since this lab's merchant ids are strings
   (`GB00SIM...`) but the real field is an integer FK; this lab owns both
   call sites so the derivation only needs to be stable, not "real."
   Best-effort like the existing B4B beneficiary check (network/unreachable
   errors: log and continue — treated the same as verification being
   "outside of scope" and not blocking the demo); poll `GET
   {VERIFY_URL}/.../verify-decision/{id}` briefly (bounded retries, short
   sleep — `VERIFY_PROCESSING_DELAY` is near-instant) for the decision. If
   `overallDecision` is present and **not** one of `APPROVE`/`APPROVED`/
   `PRE_APPROVE`: `SetPayout(rec.ID, "", "verification_declined")`, return
   — do not call B4B.
4. Existing flow, unchanged: B4B beneficiary check (realism-only), `POST
   {B4B_URL}/oversight/v1/payments`.

**New endpoint** `POST /reports/settlement/{id}/retry-payout` — re-invokes
`submitPayout` for a record whose `SettlementState` is already `Settled`
and whose current `PayoutState` is one of `awaiting_funding` /
`verification_declined` / `submission_failed` / `balance_check_failed`
(400, clear message, otherwise — refuses to double-submit a payout
already in flight or approved). This is how the harness demonstrates the
causal fix: attempt payout against an empty SGA (`awaiting_funding`),
simulate Worldline's lump sum via section 1's endpoint, retry — now it
proceeds through verification and B4B.

**New env vars:** `VERIFY_URL` (default `http://verify:8088`),
`VERIFY_INTERNAL_API_KEY` (default `sim-verify-key-dev-only`),
`BC_INTERNAL_URL` (points at Banking Circle's plain `INTERNAL_LISTEN`,
default `http://banking-circle:8095`, for the balance check only —
settlement still never talks to Banking Circle's mTLS-credentialed
surface; deliberately **not** named `BANKING_CIRCLE_URL` — the harness
already uses that exact name for a different purpose, the mTLS `:8085`
API, and reusing it across two sibling processes with two different
meanings would be needlessly confusing even though they'd never
technically collide), `BC_SAFEGUARDING_ACCOUNT_ID_EUR`,
`BC_SAFEGUARDING_ACCOUNT_ID_GBP`.

## 6. Receiver: a raw capture endpoint closes a documented gap

Source: this lab's own `scenario_bankingcircle.go` comment (pre-existing,
Phase 2): "receiver only understands the HMAC scheme... cannot verify or
store this different wire format" — Banking Circle's (and now ACI's)
webhook deliveries currently either fail outright against `receiver`
(wrong scheme, HMAC verification fails) or have nowhere to go at all.

**Fix:** `receiver` gains `POST /raw-events` (any content-type, any body —
no signature/crypto check, explicitly unvalidated) storing
`{id, receivedAt, path, headers, bodySize}`, and `GET /raw-events` to list
them. This does not decrypt or authenticate anything — it proves *delivery
reached the configured destination with these headers and this many
bytes*, the same non-decorative bar the rest of this lab holds itself to,
without claiming to validate a scheme receiver has no keys for. Banking
Circle's `WEBHOOK_URL` moves from `/webhooks` (guaranteed HMAC-verification
failure today) to `/raw-events`; ACI's mock defaults its
`ACI_WEBHOOK_TARGET_URL` independently (section 2 — real ACI notifications
go to buddy's own `settle-aci-webhook`, not to this lab's `receiver`), but
the harness's ACI scenario may also point at `/raw-events` for an in-lab
observability check alongside the mock's own `/internal/sent` introspection.

## 7. Local domain names for demoing

No existing buddy doc covers this (confirmed: neither
`SETTLE_SETUP_GUIDE_LOCAL.md` nor any README sets up hosts-file entries or
custom TLS domains — everything there stays on `localhost`). New
`docs/local-domains.md`: `/etc/hosts` entries under a reserved-for-testing
`.test` TLD (RFC 2606 — guaranteed never publicly delegated; deliberately
**not** `.local`, which mDNS/`nss-mdns` can intercept ahead of
`/etc/hosts` on some Linux configurations), one per published port, e.g.
`banking-circle.fintechlab-simple.test`, `b4b.fintechlab-simple.test`,
`aci.fintechlab-simple.test`, `verify.fintechlab-simple.test`,
`worldline-sftp.fintechlab-simple.test`. `receiver` and `banking-circle`
certs (the only two TLS servers in this lab) gain these hostnames as
**additional** SANs in `ca/generate.sh` (existing SANs untouched). Mentions
buddy's own `NODE_EXTRA_CA_CERTS` convention (confirmed:
`apps/banking-circle/README.md:295-297,318-322`) for trusting this lab's CA
from a real buddy Node service pointed at these domains.

## Existing Patterns Followed

Same as `ARCHITECTURE-vendor-corrections.md`'s own section: Go 1.22,
`env()`/`shortID()`/`logReq()` per-binary duplication, `internal/httputilx`
responses, `internal/money`, `internal/retry`+`internal/allowlist` for any
signed-and-retried delivery, non-root `USER 65532:65532`, gofmt/vet clean,
tests matching existing tone. ACI and Verify are plain HTTP, **not** TLS —
confirmed both real vendors' own local-dev defaults are plain HTTP too
(`settle-aci-webhook` proxied over `http://localhost:3013`,
`verification-service` at `http://localhost:3003`), and B4B's own auth
model (JWT bearer, no mTLS) needs no transport-level change either — TLS in
this lab stays scoped to what actually uses it for real: Banking Circle
(mTLS) and Worldline's SFTP+PGP (SSH transport security).

## Port/env registry (new pieces only)

| Service | Port | New env vars |
| --- | --- | --- |
| `aci` (new) | 8087 | `ACI_WEBHOOK_SECRET`, `ACI_WEBHOOK_TARGET_URL`, `ACI_WEBHOOK_BODY_MODE`, `WEBHOOK_ALLOWLIST`, `RETRY_BACKOFF`, `MAX_ATTEMPTS` |
| `verify` (new) | 8088 | `VERIFICATION_INTERNAL_API_KEY`, `VERIFY_PROCESSING_DELAY`, `VERIFY_FORCE_DECLINE_IDS` |
| `banking-circle` | unchanged (8085/8095) | none new on `LISTEN`; `INTERNAL_LISTEN` gains `/internal/incoming-payments`, `/internal/accounts/{id}/balances` |
| `receiver` | unchanged (8443) | none (new routes on existing listener) |
| `settlement` | unchanged (8083) | `VERIFY_URL`, `VERIFY_INTERNAL_API_KEY`, `BC_INTERNAL_URL`, `BC_SAFEGUARDING_ACCOUNT_ID_EUR`, `BC_SAFEGUARDING_ACCOUNT_ID_GBP` |

## See Also

- `ARCHITECTURE-vendor-corrections.md` — Phase 2, unchanged except where
  this doc explicitly supersedes it (B4B wire shapes section 4 above; the
  Banking Circle ledger seed shape, section 1 above).
- `ARCHITECTURE-banking-circle.md` — its ledger/VIBAN section is superseded
  by section 1 above.
