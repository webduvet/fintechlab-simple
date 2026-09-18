package settlement

// Report is one merchant/currency/day settlement aggregate, shaped after
// Worldline's North American settlement report API (see
// docs/ARCHITECTURE-settlement-report.md).
type Report struct {
	MerchantID               string `json:"merchant_id"`
	TransactionsDate         string `json:"transactions_date"` // YYYY-MM-DD
	Currency                 string `json:"currency"`
	SettlementState          string `json:"settlement_state"` // "Scheduled" | "Settled" | "Failed" (etc, see SettlementState)
	SettlementDate           string `json:"settlement_date"`
	ApprovedTransactionCount int    `json:"approved_transaction_count"`
	DeclinedTransactionCount int    `json:"declined_transaction_count"`
	SaleAmountTotal          int64  `json:"sale_amount_total"`     // minor units
	ReturnedAmountTotal      int64  `json:"returned_amount_total"` // minor units
	ChargebacksCount         int    `json:"chargebacks_count"`
	ChargebacksAmountTotal   int64  `json:"chargebacks_amount_total"` // minor units
	DiscountRateFeeTotal     int64  `json:"discount_rate_fee_total"`  // minor units
	ChargebackFeeTotal       int64  `json:"chargeback_fee_total"`     // minor units
	ReservesHeld             int64  `json:"reserves_held"`            // minor units
	ReservesReleased         int64  `json:"reserves_released"`        // minor units
	ReservesForward          int64  `json:"reserves_forward"`         // minor units
	SettlementNetAmount      int64  `json:"settlement_net_amount"`    // minor units
}

// Entry is settlement's own view of a single completed bank-ledger
// movement. It is NOT bank's wire format: cmd/bank is `package main` (not
// importable), so cmd/settlement's HTTP adapter builds these by calling
// GET {BANK_URL}/ledger and GET {BANK_URL}/accounts and resolving each
// ledger entry's AccountID to the receiving account's IBAN.
//
// bank's /transfers handler only ever appends ledger entries on success
// (see cmd/bank/main.go's transfer(): insufficient funds and every other
// failure return 4xx before any entry is written), so the ledger
// structurally cannot contain declined/failed attempts, chargebacks, or
// returns -- there is nothing to adapt those from. Generate reflects that
// honestly: it counts only credit entries as sales and hard-codes the
// declined/chargeback/returned fields to 0 (see below).
type Entry struct {
	MerchantID  string // receiving account's IBAN, e.g. "GB00SIM0000000000003"
	Currency    string
	AmountCents int64  // positive: a credit (money in) to MerchantID
	Date        string // YYYY-MM-DD, the entry's ledger date
}

// Ledger is the minimal set of entries Generate needs for one batch run.
type Ledger struct {
	Entries []Entry
}

// Generate scans ledger for entries belonging to merchantID within
// [fromDate, toDate] (inclusive, "YYYY-MM-DD" lexical compare) and produces
// a Report. Only credit entries (AmountCents > 0) count as sales.
//
// declined_transaction_count, chargebacks_count, chargebacks_amount_total,
// and returned_amount_total are always 0: bank's ledger only records
// successful transfers (see Entry's doc comment above), so this lab has no
// data source for declines, chargebacks, or returns. That is an honest
// reflection of what the simulated bank actually does, not a stub.
func Generate(ledger *Ledger, merchantID, fromDate, toDate, settlementDate string) *Report {
	r := &Report{
		MerchantID:       merchantID,
		TransactionsDate: toDate,
		SettlementState:  string(StateScheduled),
		SettlementDate:   settlementDate,
	}
	if ledger != nil {
		for _, e := range ledger.Entries {
			if e.MerchantID != merchantID {
				continue
			}
			if e.Date < fromDate || e.Date > toDate {
				continue
			}
			if e.AmountCents <= 0 {
				continue
			}
			if r.Currency == "" {
				r.Currency = e.Currency
			}
			r.ApprovedTransactionCount++
			r.SaleAmountTotal += e.AmountCents
		}
	}
	r.SettlementNetAmount = r.SaleAmountTotal - r.DiscountRateFeeTotal - r.ChargebackFeeTotal - r.ReservesHeld + r.ReservesReleased
	return r
}
