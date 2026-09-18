# Architecture: Worldline SFTP Channel — Onboarding & Settlement File Sharing

> **Historical record.** This describes the `sftp-gateway` service as
> originally built. That service is now `worldline` and owns the
> settlement file itself rather than serving one the platform wrote —
> see [ARCHITECTURE-worldline-acquirer.md](ARCHITECTURE-worldline-acquirer.md).
> The SFT directory conventions below still hold.

## Goal

Model the Worldline SFTP (Secure File Transfer) channel that serves two distinct purposes: (1) sharing onboarding and configuration documents between Worldline and merchants, and (2) delivering daily settlement files (WX files, Financial Reports) from Worldline to merchants. This is the channel through which merchants receive their daily operational and financial data.

## Worldline Reference Model

Worldline uses Secure File Transfer (SFT) for all transactional report delivery. Setup details are provided during technical onboarding. Multiple SFT directories can be configured per account (up to 3 distinct directories for different report types — daily reports, collection reports, financial reports).

The Web Client provides a "Files" section with an "out" directory where reports land. Historical daily reports have been decommissioned from the Payment Console; only WX (Operational) and Financial Reports remain available via the console and SFTP.

Key facts from Worldline documentation:
- WX file: daily operational report, per merchant/account/contract ID, XML/CSV/ASCII, uploaded before 00:00 CET
- Financial Report: daily (Mon-Fri), PDF/CSV/XML UTF8, configurable remittance frequency (daily or weekly)
- SFT directories are merchant-configurable; different report types can go to different directories
- Collection reports have been phased out; reconciliation occurs between acquirer, user, and Worldline
- Settlement reports via API: `GET /reports/settlement` at `api.na.bambora.com/v1`, 3-month range, Pacific Time cutoff
- Reports are placed in the merchant's own directory on the SFTP server

## Scope

- New `cmd/sftp-gateway/` service — simulates the Worldline SFTP file exchange channel
- Onboarding document sharing directory structure
- Settlement file ingestion and verification
- Dual-purpose channel: onboarding docs in → settlement files out (simulated bidirectional exchange)

## What This Is Not

- Not a real SFTP server (no SSH protocol). A file-based simulation.
- Not a real Worldline onboarding flow. No vendor contracts, no real credentials.
- Not a production file exchange. No TLS client authentication, no key-based auth.
- Not a replacement for the settlement service — this is the "channel" layer, the settlement service is the "generation" layer.

## Components

### 1. Directory Structure

The SFTP gateway simulates the Worldline SFTP directory layout:

```
/sftp/
├── inbound/              # Files coming IN to the sim (from merchant → Worldline)
│   ├── onboarding/       # Onboarding documents submitted by merchant
│   │   └── {merchant_id}/
│   │       └── *.pdf|*.csv|*.xml
│   └── corrections/      # Correction files for superseded settlements
│       └── {merchant_id}/
│           └── *.csv|*.xml
├── outbound/             # Files going OUT from the sim (from Worldline → merchant)
│   ├── {merchant_id}/    # Merchant-specific directory
│   │   ├── daily/        # Daily WX files
│   │   │   └── {merchant_id}_{date}_{currency}_WX.*
│   │   └── financial/    # Financial reports
│   │       └── {merchant_id}_{date}_financial.*
│   └── shared/           # Shared/configuration files
│       └── *.pdf|*.xml
├── config/               # Channel configuration
│   ├── sftp-config.json  # Directory mappings, report types, merchant settings
│   └── merchant-onboarding.json  # Merchant onboarding metadata
└── archive/              # Archived/completed files
    └── {year}/{month}/
```

### 2. `internal/sftp-gateway/channel.go`

Core channel logic. Manages file exchange between inbound and outbound directories.

**Types:**

```go
// Channel represents the SFTP file exchange channel.
type Channel struct {
    BaseDir string // /sftp
}

// DropInbound places a file in the onboarding or corrections inbound directory.
func (c *Channel) DropInbound(category, merchantID string, filename string, content []byte) (path string, err error)

// ListOutbound lists settlement files available for a merchant.
func (c *Channel) ListOutbound(merchantID string) ([]os.FileInfo, error)

// ReadOutbound reads a specific outbound file for a merchant.
func (c *Channel) ReadOutbound(merchantID, relativePath string) ([]byte, error)

// ListInbound lists inbound files for a category and merchant.
func (c *Channel) ListInbound(category, merchantID string) ([]os.FileInfo, error)

// GetConfig returns the channel configuration.
func (c *Channel) GetConfig() (*Config, error)

// UpdateConfig updates the channel configuration.
func (c *Channel) UpdateConfig(cfg *Config) error
```

**Config types:**

```go
type Config struct {
    MerchantID        string   `json:"merchant_id"`
    SFTPDirectory     string   `json:"sftp_directory"`
    ReportTypes       []string `json:"report_types"`     // ["WX", "Financial"]
    ReportFormats     []string `json:"report_formats"`   // ["CSV", "XML", "ASCII"]
    CutOffTime        string   `json:"cut_off_time"`     // "00:00 CET"
    RemittanceFrequency string `json:"remittance_frequency"` // "daily" | "weekly"
    SFTPDirectoryCount int     `json:"sftp_directory_count"` // max 3
}
```

### 3. `cmd/sftp-gateway/main.go`

New Docker service exposing the SFTP channel API.

| Endpoint | Method | Purpose |
| --- | --- | --- |
| `/health` | GET | Health check |
| `/inbound/onboarding/{merchant_id}` | POST | Upload an onboarding document |
| `/inbound/corrections/{merchant_id}` | POST | Upload a correction file |
| `/inbound/onboarding/{merchant_id}` | GET | List onboarding documents |
| `/inbound/corrections/{merchant_id}` | GET | List correction files |
| `/outbound/{merchant_id}/daily` | GET | List daily WX files |
| `/outbound/{merchant_id}/financial` | GET | List financial reports |
| `/outbound/{merchant_id}/{category}/{filename}` | GET | Read a specific outbound file |
| `/config` | GET | Get channel configuration |
| `/config` | PUT | Update channel configuration |
| `/archive/{year}/{month}` | GET | List archived files |

**Environment variables:**

| Variable | Default | Description |
| --- | --- | --- |
| `LISTEN` | `:8084` | HTTP listen address |
| `SFTP_BASE_DIR` | `/sftp` | Base directory for all SFTP file operations |
| `MERCHANT_ID` | `GB00SIM0000000000003` | Default merchant ID for this channel |

### 4. Onboarding Document Flow

The onboarding channel simulates how merchants submit configuration and onboarding documents to Worldline via SFTP:

```
Merchant → POST /inbound/onboarding/{merchant_id}
  → File dropped in /sftp/inbound/onboarding/{merchant_id}/
  → File validated (extension, size)
  → File available for review via GET /inbound/onboarding/{merchant_id}

This mirrors Worldline's technical onboarding process where SFT directory
setup details and configuration files are exchanged.
```

**Supported onboarding document types:**
- PDF (contracts, terms)
- CSV (account mappings, merchant lists)
- XML (configuration, certificate references)

### 5. Settlement File Delivery Flow

The settlement delivery channel connects the settlement service to the SFTP gateway:

```
Settlement Service (POST /reports/settlement/generate)
  → Generates Report
  → Stages files to /sftp/outbound/{merchant_id}/daily/
  → SFTP Gateway serves files via GET /outbound/{merchant_id}/daily
  → Merchant polls SFTP Gateway for new files
  → Files moved to /sftp/archive/{year}/{month}/ after retrieval
```

**Docker compose integration:**

```yaml
sftp-gateway:
  build:
    context: .
    dockerfile: docker/Dockerfile.sftp-gateway
  ports:
    - "8084:8084"
  volumes:
    - ./sftp/inbound:/sftp/inbound:rw
    - ./sftp/outbound:/sftp/outbound:rw
    - ./sftp/config:/sftp/config:rw
    - ./sftp/archive:/sftp/archive:rw
  environment:
    LISTEN: ":8084"
    SFTP_BASE_DIR: /sftp
    MERCHANT_ID: "GB00SIM0000000000003"
  depends_on:
    bank:
      condition: service_started
```

### 6. Configuration file (`sftp/config/merchant-onboarding.json`)

Initial configuration seeded into the container:

```json
{
  "merchant_id": "GB00SIM0000000000003",
  "sftp_directory": "/sftp/outbound/GB00SIM0000000000003",
  "report_types": ["WX", "Financial"],
  "report_formats": ["CSV", "XML", "ASCII"],
  "cut_off_time": "00:00 CET",
  "remittance_frequency": "daily",
  "sftp_directory_count": 3,
  "directories": {
    "daily": "/sftp/outbound/{merchant_id}/daily",
    "financial": "/sftp/outbound/{merchant_id}/financial",
    "onboarding": "/sftp/inbound/onboarding/{merchant_id}",
    "corrections": "/sftp/inbound/corrections/{merchant_id}",
    "archive": "/sftp/archive/{year}/{month}"
  }
}
```

### 7. File Lifecycle

```
1. GENERATION: Settlement report generated → written to /sftp/outbound/{merchant_id}/daily/
2. PUBLISHED: File appears in outbound directory, available for download
3. RETRIEVAL: Merchant downloads file via GET /outbound/{merchant_id}/daily/{filename}
4. ARCHIVING: After retrieval, file is moved to /sftp/archive/{year}/{month}/
5. CORRECTION: If a settlement is superseded, correction file placed in /sftp/inbound/corrections/{merchant_id}/
```

## Worldline SFTP Channel Details

### What Worldline Actually Does

1. **Technical onboarding** provides SFT directory setup details
2. **Multiple SFT directories** can be configured (up to 3 per account)
3. **Different report types** can be delivered to different SFT directories
4. **WX files** are delivered daily before 00:00 CET
5. **Financial reports** are delivered Monday-Friday (daily or weekly remittance)
6. **Reports are placed in the merchant's own directory** on the SFTP server
7. **Historical daily reports** have been decommissioned; only WX and Financial Reports remain
8. **Collection reports** have been phased out
9. **Manual download** is available via the Web Client Files → "out" directory

### How This Simulation Maps

| Worldline Concept | Sim-Lab Implementation |
| --- | --- |
| SFT directory setup | `sftp/config/merchant-onboarding.json` |
| Multiple SFT directories | `directories.daily`, `directories.financial`, `directories.onboarding` |
| WX file delivery | `/sftp/outbound/{merchant_id}/daily/{merchant_id}_{date}_EUR_WX.csv` |
| Financial report delivery | `/sftp/outbound/{merchant_id}/financial/{merchant_id}_{date}_financial.csv` |
| Merchant's own directory | `/sftp/outbound/{merchant_id}/` |
| Web Client Files → out | `GET /outbound/{merchant_id}/daily` |
| Technical onboarding docs | `POST /inbound/onboarding/{merchant_id}` |
| Archive of old reports | `/sftp/archive/{year}/{month}/` |
| Cut-off before 00:00 CET | `CUTOFF_TIME` env var (default `00:00`) |

## Testing

- Unit test `Channel.DropInbound()` writes correct path and content
- Unit test `Channel.ListOutbound()` returns correct files for merchant
- Unit test `Channel.ReadOutbound()` retrieves file contents
- Unit test config load/update round-trip
- Integration test: generate settlement → verify file in outbound → read via API → verify archive
- Verify directory structure matches Worldline convention
- Verify file naming convention
- Verify onboarding document upload and listing
- Verify config endpoint returns correct directory mappings

## Existing Patterns Followed

- Go 1.22, standard library only
- `os.MkdirAll`, `os.WriteFile`, `os.ReadFile` for file operations
- `encoding/json` for config files
- Same `env()` pattern
- Same Docker compose volume mount pattern
- `httputilx.WriteJSON` / `httputilx.Error` for HTTP responses
- `shortID()` for ID generation

## See Also

- `ARCHITECTURE-settlement-report.md` — report generation
- `ARCHITECTURE-sftp-staging.md` — file staging details
- `ARCHITECTURE-reconciliation-api.md` — reconciliation query endpoint
- `ARCHITECTURE-settlement-state-machine.md` — state transitions
