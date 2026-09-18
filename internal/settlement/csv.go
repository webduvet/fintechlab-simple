package settlement

import (
	"bytes"
	"encoding/csv"
	"strconv"
)

// csvHeader matches the Worldline CSV column order (snake_case, same as
// the JSON field names) documented in ARCHITECTURE-settlement-report.md.
var csvHeader = []string{
	"merchant_id", "transactions_date", "currency", "settlement_state", "settlement_date",
	"approved_transaction_count", "declined_transaction_count", "sale_amount_total",
	"returned_amount_total", "chargebacks_count", "chargebacks_amount_total",
	"discount_rate_fee_total", "chargeback_fee_total", "reserves_held", "reserves_released",
	"reserves_forward", "settlement_net_amount",
}

// csvRow renders r's fields in csvHeader order. All monetary values are
// integers (minor units), matching Worldline's SFTP file convention.
func csvRow(r *Report) []string {
	return []string{
		r.MerchantID,
		r.TransactionsDate,
		r.Currency,
		r.SettlementState,
		r.SettlementDate,
		strconv.Itoa(r.ApprovedTransactionCount),
		strconv.Itoa(r.DeclinedTransactionCount),
		strconv.FormatInt(r.SaleAmountTotal, 10),
		strconv.FormatInt(r.ReturnedAmountTotal, 10),
		strconv.Itoa(r.ChargebacksCount),
		strconv.FormatInt(r.ChargebacksAmountTotal, 10),
		strconv.FormatInt(r.DiscountRateFeeTotal, 10),
		strconv.FormatInt(r.ChargebackFeeTotal, 10),
		strconv.FormatInt(r.ReservesHeld, 10),
		strconv.FormatInt(r.ReservesReleased, 10),
		strconv.FormatInt(r.ReservesForward, 10),
		strconv.FormatInt(r.SettlementNetAmount, 10),
	}
}

// ToCSV formats a single report as UTF-8 CSV with a header row, matching
// Worldline's CSV output shape.
func ToCSV(r *Report) ([]byte, error) {
	return ToCSVAll([]*Report{r})
}

// ToCSVAll formats one header row followed by one row per report. Used by
// the multi-record GET /reports/settlement?format=csv response; ToCSV
// (single report) is the documented per-file staging entry point.
func ToCSVAll(reports []*Report) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(csvHeader); err != nil {
		return nil, err
	}
	for _, r := range reports {
		if err := w.Write(csvRow(r)); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
