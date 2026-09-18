package settlement

import "testing"

func TestGenerateAggregation(t *testing.T) {
	ledger := &Ledger{Entries: []Entry{
		{MerchantID: "GB00SIM0000000000003", Currency: "EUR", AmountCents: 10000, Date: "2026-09-03"},
		{MerchantID: "GB00SIM0000000000003", Currency: "EUR", AmountCents: 5000, Date: "2026-09-03"},
		// debit leg (money out of the merchant account) must not count as a sale.
		{MerchantID: "GB00SIM0000000000003", Currency: "EUR", AmountCents: -3000, Date: "2026-09-03"},
		// different merchant: must not leak into this report.
		{MerchantID: "GB00SIM0000000000099", Currency: "EUR", AmountCents: 99999, Date: "2026-09-03"},
		// outside the requested date range: must not count.
		{MerchantID: "GB00SIM0000000000003", Currency: "EUR", AmountCents: 2000, Date: "2026-08-01"},
	}}

	r := Generate(ledger, "GB00SIM0000000000003", "2026-09-01", "2026-09-03", "2026-09-04")

	if r.MerchantID != "GB00SIM0000000000003" {
		t.Fatalf("MerchantID = %q", r.MerchantID)
	}
	if r.TransactionsDate != "2026-09-03" {
		t.Fatalf("TransactionsDate = %q, want the report's to_date", r.TransactionsDate)
	}
	if r.SettlementDate != "2026-09-04" {
		t.Fatalf("SettlementDate = %q", r.SettlementDate)
	}
	if r.Currency != "EUR" {
		t.Fatalf("Currency = %q", r.Currency)
	}
	if r.SettlementState != string(StateScheduled) {
		t.Fatalf("SettlementState = %q, want Scheduled", r.SettlementState)
	}
	if r.ApprovedTransactionCount != 2 {
		t.Fatalf("ApprovedTransactionCount = %d, want 2", r.ApprovedTransactionCount)
	}
	if r.SaleAmountTotal != 15000 {
		t.Fatalf("SaleAmountTotal = %d, want 15000", r.SaleAmountTotal)
	}
	// bank's ledger only ever records successful transfers (see Entry's doc
	// comment): there is no data source for declines, chargebacks, or
	// returns in this lab, so these are always 0.
	if r.DeclinedTransactionCount != 0 || r.ChargebacksCount != 0 || r.ChargebacksAmountTotal != 0 || r.ReturnedAmountTotal != 0 {
		t.Fatalf("declined/chargeback/returned fields must be 0, got %+v", r)
	}
	if r.SettlementNetAmount != 15000 {
		t.Fatalf("SettlementNetAmount = %d, want 15000 (no fees/reserves modeled)", r.SettlementNetAmount)
	}
}

func TestGenerateEmptyLedger(t *testing.T) {
	r := Generate(&Ledger{}, "GB00SIM0000000000003", "2026-09-01", "2026-09-03", "2026-09-04")
	if r.ApprovedTransactionCount != 0 || r.SaleAmountTotal != 0 || r.SettlementNetAmount != 0 {
		t.Fatalf("expected all-zero report, got %+v", r)
	}
	if r.Currency != "" {
		t.Fatalf("Currency = %q, want empty when no entries match", r.Currency)
	}
}

func TestGenerateNilLedger(t *testing.T) {
	r := Generate(nil, "GB00SIM0000000000003", "2026-09-01", "2026-09-03", "2026-09-04")
	if r.ApprovedTransactionCount != 0 {
		t.Fatalf("expected zero report for nil ledger, got %+v", r)
	}
}
