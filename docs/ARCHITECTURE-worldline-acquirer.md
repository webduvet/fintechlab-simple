# Worldline as acquirer: who owns the settlement file

## The problem this fixes

The lab used to generate the Worldline settlement file inside the
`settlement` service — the platform's side — and hand it to a passive file
server through a shared bind mount. Two things followed from that, both
bad:

1. **The platform manufactured the file it was supposed to be receiving.**
   Any disagreement between what Worldline really sends and what this lab
   thinks it sends was invisible, because the same code wrote and read it.
2. **The download path was never exercised.** The one code path that has to
   work when you point at real Worldline — dial SFTP, authenticate,
   list, download, decrypt, parse — was not on the critical path of any
   test. A shared directory is not a protocol.

## What it looks like now

```
   card payment at the paypoint
             │
             ▼
   ┌─────────────────────┐
   │ worldline (acquirer)│  POST /sim/transactions   ← "a card payment happened"
   │  transaction store  │
   │  settlement cycle   │  morning slot (ER)  ─┬─▶  PGP-encrypted file on its own SFTP root
   │                     │                      └─▶  lump sum → safeguarding account
   │                     │  afternoon slot (AR) ──▶  confirmation file, no money
   └──────────┬──────────┘
              │  SSH + SFTP + PGP  (the only route)
              ▼
   ┌─────────────────────┐
   │ settlement (platform│  pull → decrypt → parse → split per MID → payout
   │  stand-in)          │
   └─────────────────────┘
```

Worldline holds the transactions, cuts the file, and moves the money. The
platform's only route to any of it is over the wire.

## The two delivery slots

Worldline delivers the same settlement twice a day:

| Slot | Code | When | What the platform does |
| --- | --- | --- | --- |
| Morning file | `ER` | 08:00–10:00 window | Processes it: splits per MID, pays out |
| Afternoon file | `AR` | later the same day | Retains it for audit. Nothing else. |

The morning fire time is jittered deterministically across
`WORLDLINE_MORNING_FILE_WINDOW`, so a consumer cannot come to depend on an
exact second while a test still repeats. Only the morning slot wires a lump
sum — the afternoon file confirms a settlement that already happened, and
paying against it would pay every merchant twice. The
`worldline-confirmation-file` harness scenario asserts exactly that.

## One file per currency, not per merchant

The real filename pattern is
`{YYYYMMDDHHMMSS}_{identifier}_{ER|AR}_{CCY}.csv`. It carries a timestamp,
the contract identifier, the slot, and the currency — **and no merchant**.

That is not an omission. A settlement file is per receiving platform, so
one file covers every submerchant that traded in that currency, and the
per-outlet split lives in the rows. This is why the row-level grouping key
matters:

| Section | Merchant grouping key |
| --- | --- |
| `TXER` (transactions) | `ADDITIONAL_REF_2` |
| `CB` (chargebacks) | `SUBMERCHANT_ID` |

`BAMBORA_MID` is an acquiring-side batch id that can span several
merchants, and is **not** a merchant key in either section. Getting this
wrong pays the wrong merchant, so `ParsedFile.PayoutsByMID` is a named
function with a test rather than an inline loop at the call site.

Cutting one file per merchant — which an earlier draft of this work did —
collides on the filename and silently overwrites all but the last.

## Generated or canned

`WORLDLINE_FILE_SOURCE` picks where file content comes from:

- `generate` (default) — built from the transactions the acquirer holds.
- `fixture` — served byte-for-byte from `worldline/fixtures/`, keeping the
  fixture's own filename. Drop a real example settlement file there to test
  a parser against the genuine article rather than against this lab's
  rendering of it. The filename is preserved because a real file's name is
  part of what a consumer's filename validator has to accept.

## Swapping in the real host

The whole configuration surface for this integration is on the consumer
side:

| Variable | Meaning |
| --- | --- |
| `WORLDLINE_SFTP_HOST` / `_PORT` / `_USER` | Where and who |
| `WORLDLINE_SFTP_PASSWORD` | Password auth |
| `WORLDLINE_SFTP_PRIVATE_KEY_PATH` | Key auth — what a real account uses |
| `WORLDLINE_SFTP_KNOWN_HOST_PATH` | Pins the server's host key |
| `WORLDLINE_PGP_PRIVATE_KEY_PATH` | Decrypts what the acquirer sends |
| `WORLDLINE_PULL_INTERVAL` | How often to look |
| `WORLDLINE_ARCHIVE_DIR` | Where delivered files are retained |

The client offers a key first and falls back to a password, so one
configuration works against both a key-only real host and this lab. The
server likewise runs both methods at once — setting a password used to
switch key auth off entirely, which meant the lab could not exercise the
method production actually uses.

Host-key pinning is off when `WORLDLINE_SFTP_KNOWN_HOST_PATH` is unset, and
the code says so explicitly rather than defaulting quietly. A real
deployment must set it; the lab writes the simulator's own public host key
to `wlsftp-keys/host_key.pub` at startup so it can be pinned locally too.

## Not modelled

- No Worldline Acquiring REST API. The settlement file over SFTP is the
  whole integration today; the REST surface is a documented seam, not a
  stub pretending to be one.
- No chargebacks. The `CB` section is always emitted and always empty:
  this lab has no dispute source, and inventing one would be theatre.
- No fee schedules. What each merchant is *owed* comes from the file; what
  the platform *keeps* is product configuration that lives on the
  platform, deliberately outside this repo.
