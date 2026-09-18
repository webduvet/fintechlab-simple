# Architecture: Settlement Report Generation

## Goal

Produce a daily settlement report file (CSV or XML) from the ledger — matching the Worldline WX file pattern. A separate report is generated per merchant/account ID, covering all completed payments for that day, with totals per currency.

## Worldline Reference Model

Worldline generates daily WX (Operational) files uploaded to SFTP before 00:00 CET. Per merchant/account/contract ID. Available in XML, CSV, ASCII. Covers all processed orders and transactions with total amounts per currency.

North American settlement API (`GET /reports/settlement`) returns JSON with fields: `merchant_id`, `transactions_date`, `currency`, `settlement_net_amount`, `settlement_state`, `approved_transaction_count`, `declined_transaction_count`, `sale_amount_total`, `returned_amount_total`, `chargebacks_count`, `chargebacks_amount_total`, `card_transaction_approved_rate`, `card_transaction_declined_rate`, `card_discount_rate`, `approved_transaction_fee_total`, `declined_transaction_fee_total`, `discount_rate_fee_total`, `chargeback_fee_total`, `reserves_held`, `reserves_released`, `reserves_forward`.

This architecture targets the core of that model using only the existing sim-lab ledger and Go standard library.

## Scope

- New `settlement` package (`internal/settlement/`) — report generation logic
- New `settlement` HTTP service (`cmd/settlement/`) — serves `GET /reports/settlement` and triggers batch generation
- Report output written to the SFTP staging directory (see `ARCHITECTURE-sftp-staging.md`)
- Settlement state machine (see `ARCHITECTURE-settlement-state-machine.md`) drives which payments are included

## What This Is Not

- Not a real clearing system. No FX, no reserved funds, no actual banking rails.
- Not a replica of Worldline's exact report format. The field names and structure are inspired by the documented API shape.
- No external dependencies beyond Go standard library and existing internal packages.

## Components

### 1. `internal/settlement/report.go`

Core report generation. Reads completed payments from the ledger for a given date range, aggregates them, and produces a report.

**Types:**

```go
type Report struct {
    MerchantID              string    `json:"merchant_id"`
    TransactionsDate        string    `json:"transactions_date"`  // YYYY-MM-DD
    Currency                string    `json:"currency"`
    SettlementState         string    `json:"settlement_state"`   // "Scheduled" | "Settled" | "Failed"
    SettlementDate          string    `json:"settlement_date"`
    ApprovedTransactionCount int      `json:"approved_transaction_count"`
    DeclinedTransactionCount int      `json:"declined_transaction_count"`
    SaleAmountTotal         int64     `json:"sale_amount_total"`          // minor units
    ReturnedAmountTotal     int64     `json:"returned_amount_total"`      // minor units
    ChargebacksCount        int       `json:"chargebacks_count"`
    ChargebacksAmountTotal  int64     `json:"chargebacks_amount_total"`   // minor units
    DiscountRateFeeTotal    int64     `json:"discount_rate_fee_total"`    // minor units
    ChargebackFeeTotal      int64     `json:"chargeback_fee_total"`       // minor units
    ReservesHeld            int64     `json:"reserves_held"`              // minor units
    ReservesReleased        int64     `json:"reserves_released"`          // minor units
    ReservesForward         int64     `json:"reserves_forward"`           // minor units
    SettlementNetAmount     int64     `json:"settlement_net_amount"`      // minor units
}
```

**Key function:**

```go
// Generate scans the ledger for payments in [fromDate, toDate] and produces a Report.
// Only payments with status "settled" are included in the sale_amount_total.
// Failed/declined payments contribute to declined_transaction_count.
func Generate(ledger *bank.Ledger, merchantID, fromDate, toDate, settlementDate string) *Report
```

**Aggregation rules:**
- `approved_transaction_count` = count of payments where `payment.status == "settled"`
- `declined_transaction_count` = count of payments where `payment.status == "rejected"` or `"failed"`
- `sale_amount_total` = sum of all settled payment amounts (minor units)
- `returned_amount_total` = sum of amounts for any refunded/credited payments (minor units)
- `chargebacks_count` = count of payments with chargeback flag set
- `settlement_net_amount` = `sale_amount_total - discount_rate_fee_total - chargeback_fee_total - reserves_held + reserves_released`
- `settlement_state` starts as `"Scheduled"` when first generated; transitions via state machine

### 2. `internal/settlement/csv.go`

CSV formatter matching Worldline CSV output shape.

```go
func ToCSV(r *Report) ([]byte, error)
```

Output columns (matching the JSON field names in snake_case):
```
merchant_id,transactions_date,currency,settlement_state,settlement_date,approved_transaction_count,declined_transaction_count,sale_amount_total,returned_amount_total,chargebacks_count,chargebacks_amount_total,discount_rate_fee_total,chargeback_fee_total,reserves_held,reserves_released,reserves_forward,settlement_net_amount
```

All monetary values are formatted as integers (minor units) to match Worldline's SFTP file convention.

### 3. `internal/settlement/xml.go`

XML formatter matching Worldline XML output shape.

```go
func ToXML(r *Report) ([]byte, error)
```

Output root element `<settlementReport>` with a `<report>` child for each merchant. Structure:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<settlementReport>
  <report>
    <merchantId>123</merchantId>
    <transactionsDate>2026-09-03</transactionsDate>
    <currency>EUR</currency>
    <settlementState>Scheduled</settlementState>
    <settlementDate>2026-09-04</settlementDate>
    ...
  </report>
</settlementReport>
```

### 4. `cmd/settlement/main.go`

New Docker service exposing:

| Endpoint | Method | Purpose |
| --- | --- | --- |
| `/health` | GET | Health check |
| `/reports/settlement` | GET | Query settlement report by date range |
| `/reports/settlement/generate` | POST | Trigger batch generation for yesterday |
| `/reports/settlement/{id}` | GET | Get a specific report by ID |

**Query parameters for `GET /reports/settlement`:**

| Param | Type | Default | Description |
| --- | --- | --- | --- |
| `from_date` | string | 7 days ago | Start date (YYYY-MM-DD) |
| `to_date` | string | today | End date (YYYY-MM-DD) |
| `merchant_id` | string | all | Filter by merchant ID |
| `format` | string | `json` | Output format: `json`, `csv`, `xml` |

**Environment variables:**

| Variable | Default | Description |
| --- | --- | --- |
| `LISTEN` | `:8083` | HTTP listen address |
| `BANK_URL` | `http://bank:8081` | Bank ledger API URL |
| `SFTP_OUT_DIR` | `/sftp/out` | Directory where report files are written |
| `SETTLEMENT_STATE_FILE` | `/data/settlements.json` | Persisted settlement state file |
| `CUTOFF_TIME` | `00:00` | Daily cut-off time in UTC for daily batch generation |

## File Naming Convention

Settlement report files follow Worldline's naming pattern:

```
{merchant_id}_{transactions_date}_{currency}_WX.{format}
```

Examples:
- `GB00SIM0000000000003_2026-09-03_EUR_WX.csv`
- `GB00SIM0000000000003_2026-09-03_EUR_WX.xml`

## Data Flow

```
Payment API (POST /payments)
  → Bank ledger (POST /transfers, status stored)
    → Notifier (webhook enqueue)
      → [NEW] Settlement batch job runs at cut-off time
        → Reads ledger for completed payments since last cut-off
        → Aggregates into Report per merchant
        → Formats as CSV and XML
        → Writes to SFTP staging directory
        → Persists settlement state (Scheduled)
```

## Testing

- Unit tests for `Generate()` with known ledger data
- Unit tests for `ToCSV()` and `ToXML()` output format
- Integration test: create payments → run batch → verify report contents
- Verify file naming convention
- Verify `GET /reports/settlement` returns correct JSON with date range filtering

## Existing Patterns Followed

- Go 1.22, standard library only (no external dependencies)
- `sync.Mutex` for in-memory state (matching bank/notifier pattern)
- `env()` function for configuration with defaults
- `httputilx.WriteJSON` / `httputilx.Error` for HTTP responses
- Same Docker compose integration pattern as existing services
- Same `shortID()` pattern for ID generation

## See Also

- `ARCHITECTURE-settlement-state-machine.md` — state transitions
- `ARCHITECTURE-sftp-staging.md` — file staging and delivery
- `ARCHITECTURE-reconciliation-api.md` — HTTP query endpoint
