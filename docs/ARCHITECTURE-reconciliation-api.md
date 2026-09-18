# Architecture: Reconciliation API

## Goal

Expose a `GET /reports/settlement` HTTP endpoint on the settlement service that allows querying settlement reports by date range, merchant ID, and format. This mirrors Worldline's settlement report API (`GET /reports/settlement` at `api.na.bambora.com/v1`) and the North American settlement report endpoint that returns credit card settlement and fee information with up to a three-month range.

## Worldline Reference Model

Worldline's settlement report API (`GET /reports/settlement`) returns a JSON array with settlement data including `merchant_id`, `transactions_date`, `currency`, `settlement_net_amount`, `settlement_state`, `approved_transaction_count`, `declined_transaction_count`, `sale_amount_total`, `returned_amount_total`, `chargebacks_count`, `chargebacks_amount_total`, `card_transaction_approved_rate`, `card_transaction_declined_rate`, `card_discount_rate`, `gst_tax_rate`, `approved_transaction_fee_total`, `declined_transaction_fee_total`, `discount_rate_fee_total`, `chargeback_fee_total`, `gst_tax_fee_total`, `reserves_held`, `reserves_released`, `reserves_forward`.

The API supports `from_date`, `to_date`, and `report_days` query parameters. Settlement is calculated in Pacific Time with midnight cutoff. Up to 3-month range retrievable.

Also available: `GET /reports/commissions` with similar date filtering (`from_date`, `to_date`, `report_days`, `filter_by`, `filter_value`, `start_row`, `end_row`).

This architecture targets the core settlement query pattern with the sim-lab's existing data model.

## Scope

- Extend the settlement service (`cmd/settlement/`) with the reconciliation query endpoint
- New `internal/settlement/store.go` — persisted settlement state store
- Query parameter handling: `from_date`, `to_date`, `merchant_id`, `format`
- Response serialization in JSON, CSV, or XML format

## What This Is Not

- Not the real Bambora/Worldline API endpoint. Different field names and data model.
- Not a three-month system (sim-lab data is limited to what has been generated).
- No authentication — this is a local lab.
- No PST cutoff enforcement (sim-lab uses UTC).

## Components

### 1. `internal/settlement/store.go`

Persisted in-memory store for settlement reports, surviving across HTTP requests.

**Types:**

```go
type SettlementState string

const (
    StateScheduled SettlementState = "Scheduled"
    StateProcessing SettlementState = "Processing"
    StateSettled SettlementState = "Settled"
    StateFailed SettlementState = "Failed"
)

type SettlementRecord struct {
    ID              string        `json:"id"`
    MerchantID      string        `json:"merchant_id"`
    TransactionsDate string       `json:"transactions_date"`
    Currency        string        `json:"currency"`
    State           SettlementState `json:"settlement_state"`
    SettlementDate  string        `json:"settlement_date"`
    Report          *Report       `json:"report,omitempty"`  // populated when settled
    CreatedAt       string        `json:"created_at"`
    UpdatedAt       string        `json:"updated_at"`
}

type Store struct {
    mu    sync.Mutex
    records map[string]*SettlementRecord  // keyed by record ID
    byMerchant map[string][]string         // merchantID -> []recordIDs
    byDate     map[string][]string          // transactionsDate -> []recordIDs
}
```

**Key methods:**

```go
func NewStore() *Store
func (s *Store) Create(r *SettlementRecord) error
func (s *Store) UpdateState(id string, state SettlementState) error
func (s *Store) SetReport(id string, r *Report) error
func (s *Store) Query(fromDate, toDate, merchantID string) []*SettlementRecord
func (s *Store) Get(id string) (*SettlementRecord, error)
func (s *Store) List() []*SettlementRecord
func (s *Store) Save(path string) error    // persist to JSON file
func (s *Store) Load(path string) error    // load from JSON file
```

**Persistence:** The store loads from `SETTLEMENT_STATE_FILE` (default `/data/settlements.json`) on startup and saves on each mutation. This ensures settlement records survive container restarts.

### 2. `cmd/settlement/main.go` — new endpoint

**`GET /reports/settlement`**

Query parameters:

| Param | Type | Default | Description |
| --- | --- | --- | --- |
| `from_date` | string | 7 days ago | Start date (YYYY-MM-DD) |
| `to_date` | string | today | End date (YYYY-MM-DD) |
| `merchant_id` | string | all | Filter by merchant/account ID |
| `format` | string | `json` | Response format: `json`, `csv`, `xml` |

**Response (JSON):**

```json
{
  "data": [
    {
      "merchant_id": "GB00SIM0000000000003",
      "transactions_date": "2026-09-03",
      "currency": "EUR",
      "settlement_state": "Scheduled",
      "settlement_date": "2026-09-04",
      "approved_transaction_count": 42,
      "declined_transaction_count": 3,
      "sale_amount_total": 150000,
      "returned_amount_total": 5000,
      "chargebacks_count": 1,
      "chargebacks_amount_total": 5000,
      "discount_rate_fee_total": 2250,
      "chargeback_fee_total": 500,
      "reserves_held": 10000,
      "reserves_released": 0,
      "reserves_forward": 0,
      "settlement_net_amount": 133250
    }
  ],
  "total": 1,
  "from_date": "2026-09-01",
  "to_date": "2026-09-03"
}
```

**Response (CSV):** Returns `text/csv` Content-Type with the same columns as the file format.

**Response (XML):** Returns `application/xml` Content-Type.

**`GET /reports/settlement/{id}`**

Returns a single settlement record by ID. Same format options.

**`POST /reports/settlement/generate`**

Triggers batch generation. Body:

```json
{
  "from_date": "2026-09-01",
  "to_date": "2026-09-03",
  "merchant_id": "GB00SIM0000000000003"
}
```

Response:

```json
{
  "message": "generation started",
  "records_created": 1,
  "record_ids": ["set_abc123"]
}
```

### 3. Environment variables

| Variable | Default | Description |
| --- | --- | --- |
| `SETTLEMENT_STATE_FILE` | `/data/settlements.json` | Path to persisted settlement state |
| `LISTEN` | `:8083` | HTTP listen address |
| `BANK_URL` | `http://bank:8081` | Bank ledger API URL |

### 4. Date range handling

```go
// Default from_date is 7 days ago if not provided.
// Default to_date is today if not provided.
// Maximum range is 90 days (matching Worldline's 3-month limit).
func parseDateRange(from, to string) (fromDate, toDate time.Time, err error)
```

## Data Flow

```
1. Client calls GET /reports/settlement?from_date=2026-09-01&to_date=2026-09-03
2. Store.Query() filters records by date range and optional merchant_id
3. Each SettlementRecord is converted to the response format
4. If format=csv, response is CSV with Content-Type: text/csv
5. If format=xml, response is XML with Content-Type: application/xml
6. If format=json (default), response is JSON with Content-Type: application/json
```

## Error Handling

| Status | Condition |
| --- | --- |
| 400 | Invalid date format or date range > 90 days |
| 404 | Settlement record ID not found |
| 500 | Internal error reading/writing store |

## Testing

- Unit test `Store.Query()` with various date ranges and filters
- Unit test `Store.Save()` / `Store.Load()` round-trip
- Unit test `parseDateRange()` with default values and edge cases
- Integration test: create payments → generate settlement → query endpoint → verify response
- Verify CSV output format with correct headers
- Verify XML output format with correct structure
- Verify 90-day range limit enforcement
- Verify persistence across simulated restarts

## Existing Patterns Followed

- `httputilx.WriteJSON` / `httputilx.Error` for HTTP responses
- `sync.Mutex` for concurrent access
- `env()` function for configuration
- Standard library `encoding/csv`, `encoding/xml`, `encoding/json`
- Same Docker compose integration pattern
- Same `shortID()` pattern for ID generation (`set_` prefix)

## See Also

- `ARCHITECTURE-settlement-report.md` — report generation
- `ARCHITECTURE-sftp-staging.md` — file staging
- `ARCHITECTURE-settlement-state-machine.md` — state transitions
