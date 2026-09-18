# Architecture: Banking Circle Payout Rail (superseded)

This document's original design (a generic `Received → Processing →
Processed → Rejected → Returned` payout lifecycle behind `POST /payments`,
seeded as `bc_acc_worldline`/`bc_acc_merchant`) was written before buddy's
actual Banking Circle integration was read. It was superseded in two
passes, both grounded in buddy's real code (paths cited):

1. **`ARCHITECTURE-vendor-corrections.md` section 3** — real Banking
   Circle has no payment-creation endpoint at all. Its actual surface,
   from buddy's side, is mTLS + Basic→Bearer auth, a balance read, webhook
   subscription management, and AES-256-GCM encrypted webhook receipt.
   Buddy's own payout path goes through B4B; Banking Circle is B4B's
   downstream settlement bank, told a payment exists via an internal
   bridge call once B4B approves it.
2. **`ARCHITECTURE-phase3-corrections.md` section 1** — the ledger/VIBAN
   seed shape this doc originally described (the one part the first pass
   left in effect) is itself superseded: accounts model a real
   safeguarding account (SGA) per currency that starts at **zero** and
   must be credited by an explicit "Worldline lump sum landed"
   simulation before any merchant payout can proceed, and per-merchant
   creditor accounts auto-vivify rather than being fixed/shared.

Every code sample, endpoint table, and env var list below this point in
the original document is inaccurate against the current implementation —
removed rather than left to mislead. `internal/bankingcircle` and
`cmd/bankingcircle` are the ground truth; `docs/ARCHITECTURE-vendor-corrections.md`
section 3 and `docs/ARCHITECTURE-phase3-corrections.md` section 1 are the
design docs that actually describe them.

## What's still true

- Banking Circle answers "did the money actually move", a separate
  concern from Worldline's "how much did the merchant sell" — that
  framing motivated the split and still holds.
- It is a real transaction bank (Luxembourg-licensed), a B2B rail for
  regulated financial institutions, not consumer-facing — the reference
  point this lab is shaped after, unchanged.

See `docs/catalogue.md` for the current one-line summary and links to both
superseding documents.
