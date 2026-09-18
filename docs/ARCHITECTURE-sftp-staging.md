# Architecture: SFTP Staging

## Goal

Provide a simulated secure file transfer (SFTP) environment where settlement report files are deposited — matching Worldline's SFT (Secure File Transfer) model for daily WX files and financial reports.

## Worldline Reference Model

Worldline delivers all transactional reports via secure file transfer (SFT). Setup details are provided during technical onboarding. The "out" directory under "Files" in the Web Client is where reports land. WX files, collection reports, and financial reports can be delivered to separate SFT directories (up to 3 distinct directories per account configuration). Reports are placed in the merchant's own directory on the SFTP server.

North American Worldline infrastructure uses `api.na.bambora.com` for the REST API and SFTP for file delivery.

This architecture simulates the SFTP staging concept using an in-process file writer, since a full SFTP server would add operational complexity beyond the lab's scope. The output format and directory structure mirror what a real SFTP provider would deliver.

## Scope

- New `sftp` package (`internal/sftp/`) — file staging logic
- SFTP output directory mounted into the settlement container via Docker volume
- Configuration-driven directory layout (merchant subdirectories, report types)
- File naming convention matching Worldline conventions

## What This Is Not

- Not a real SFTP server (no SSH/SFTP protocol). A file writer that deposits files into a structured directory tree.
- Not a production-grade secure file transfer. This is a simulation — the directory structure and file formats are what matter for the lab.
- No authentication or authorization for file access. The directory is container-local.

## Components

### 1. `internal/sftp/staging.go`

Core staging logic. Writes report files to the configured output directory.

**Types:**

```go
// Stager writes settlement report files to a structured directory tree.
type Stager struct {
    OutDir string // base directory for all staged files
}

// Stage writes a report file for a given merchant and format.
// Returns the relative path within the SFTP directory structure.
func (s *Stager) Stage(report *settlement.Report, format string) (relativePath string, err error)

// ListOut returns all files in the staging directory, sorted by modified time descending.
func (s *Stager) ListOut() ([]os.FileInfo, error)

// ReadOut reads and returns the contents of a file by relative path.
func (s *Stager) ReadOut(relativePath string) ([]byte, error)
```

**Directory layout:**

```
{sftp_out_dir}/
├── out/
│   └── {merchant_id}/
│       ├── {merchant_id}_{transactions_date}_{currency}_WX.csv
│       ├── {merchant_id}_{transactions_date}_{currency}_WX.xml
│       └── {merchant_id}_{transactions_date}_{currency}_WX.json  (API response mirror)
└── staging/
    └── {merchant_id}/
        └── pending/    # files being generated but not yet finalized
```

**File naming convention:**

```
{merchant_id}_{transactions_date}_{currency}_WX.{format}
```

Examples:
- `GB00SIM0000000000003_2026-09-03_EUR_WX.csv`
- `GB00SIM0000000000003_2026-09-03_EUR_WX.xml`

**Environment variables:**

| Variable | Default | Description |
| --- | --- | --- |
| `SFTP_OUT_DIR` | `/sftp/out` | Base directory for staged settlement files |
| `SFTP_STAGING_DIR` | `/sftp/staging` | Directory for files in progress |

### 2. `internal/sftp/format.go`

Format detection and validation for incoming/outgoing files. Validates that a staged file has the correct naming convention and can be parsed.

```go
// ParseFilename extracts merchantID, date, currency, and format from a Worldline-style filename.
func ParseFilename(name string) (merchantID, date, currency, format string, err error)

// ValidateFilename checks that a filename matches the expected pattern.
func ValidateFilename(name string) error
```

### 3. Volume mount in `compose.yml`

Add a new `sftp` service or add a volume mount to the existing settlement service:

```yaml
# Option A: standalone sftp staging directory (simplest)
volumes:
  - ./sftp/out:/sftp/out:rw
  - ./sftp/staging:/sftp/staging:rw
```

Or integrate into the settlement service:

```yaml
settlement:
  build:
    context: .
    dockerfile: docker/Dockerfile.settlement
  ports:
    - "8083:8083"
  volumes:
    - ./sftp/out:/sftp/out:rw
    - ./sftp/staging:/sftp/staging:rw
  environment:
    SFTP_OUT_DIR: /sftp/out
    SFTP_STAGING_DIR: /sftp/staging
  depends_on:
    bank:
      condition: service_started
```

### 4. `cmd/settlement/main.go` (staging integration)

The settlement service imports `internal/sftp` and calls `Stager.Stage()` after generating each report.

**Flow:**

```
1. Generate() produces a Report
2. Stager.Stage(report, "csv") writes CSV to {out_dir}/{merchant_id}/...
3. Stager.Stage(report, "xml") writes XML to {out_dir}/{merchant_id}/...
4. Stager.Stage(report, "json") writes JSON to {out_dir}/{merchant_id}/...
5. File is now available in the SFTP staging directory
6. GET /sftp/out/{relative_path} returns the file contents (for testing)
```

### 5. SFTP API endpoint on settlement service

| Endpoint | Method | Purpose |
| --- | --- | --- |
| `/sftp/out` | GET | List all files in the staging directory |
| `/sftp/out/{merchant_id}` | GET | List files for a specific merchant |
| `/sftp/out/{relative_path}` | GET | Read a specific staged file |
| `/sftp/staging` | GET | List files currently being staged |

## File Format Details

### CSV Format

UTF-8, comma-separated, header row included. All monetary values as integers (minor units). Matches the Worldline CSV shape:

```csv
merchant_id,transactions_date,currency,settlement_state,settlement_date,approved_transaction_count,declined_transaction_count,sale_amount_total,returned_amount_total,chargebacks_count,chargebacks_amount_total,discount_rate_fee_total,chargeback_fee_total,reserves_held,reserves_released,reserves_forward,settlement_net_amount
GB00SIM0000000000003,2026-09-03,EUR,Scheduled,2026-09-04,42,3,150000,5000,1,5000,2250,500,10000,0,0,133250
```

### XML Format

UTF-8 encoded, XML declaration included. Root element `<settlementReport>`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<settlementReport>
  <report>
    <merchantId>GB00SIM0000000000003</merchantId>
    <transactionsDate>2026-09-03</transactionsDate>
    <currency>EUR</currency>
    <settlementState>Scheduled</settlementState>
    <settlementDate>2026-09-04</settlementDate>
    <approvedTransactionCount>42</approvedTransactionCount>
    <declinedTransactionCount>3</declinedTransactionCount>
    <saleAmountTotal>150000</saleAmountTotal>
    <returnedAmountTotal>5000</returnedAmountTotal>
    <chargebacksCount>1</chargebacksCount>
    <chargebacksAmountTotal>5000</chargebacksAmountTotal>
    <discountRateFeeTotal>2250</discountRateFeeTotal>
    <chargebackFeeTotal>500</chargebackFeeTotal>
    <reservesHeld>10000</reservesHeld>
    <reservesReleased>0</reservesReleased>
    <reservesForward>0</reservesForward>
    <settlementNetAmount>133250</settlementNetAmount>
  </report>
</settlementReport>
```

## Testing

- Unit test `ParseFilename()` with valid and invalid filenames
- Unit test `Stage()` writes correct directory structure and file contents
- Unit test `ReadOut()` retrieves staged files
- Integration test: generate report → verify file exists in staging → verify file contents
- Verify file naming convention matches Worldline pattern
- Verify CSV header and XML structure

## Existing Patterns Followed

- Go 1.22, standard library only
- `os.MkdirAll` for directory creation
- `os.WriteFile` for file writing
- `filepath.Join` for path construction
- `encoding/csv` for CSV output
- `encoding/xml` for XML output (or `strings.Builder` if simpler XML is preferred)
- Same `env()` pattern for configuration
- Same Docker volume mount pattern as existing services

## See Also

- `ARCHITECTURE-settlement-report.md` — report generation
- `ARCHITECTURE-settlement-state-machine.md` — state transitions
- `ARCHITECTURE-reconciliation-api.md` — HTTP query endpoint
