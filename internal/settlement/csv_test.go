package settlement

import (
	"strings"
	"testing"
)

func sampleReport() *Report {
	return &Report{
		MerchantID:               "GB00SIM0000000000003",
		TransactionsDate:         "2026-09-03",
		Currency:                 "EUR",
		SettlementState:          "Scheduled",
		SettlementDate:           "2026-09-04",
		ApprovedTransactionCount: 42,
		DeclinedTransactionCount: 3,
		SaleAmountTotal:          150000,
		ReturnedAmountTotal:      5000,
		ChargebacksCount:         1,
		ChargebacksAmountTotal:   5000,
		DiscountRateFeeTotal:     2250,
		ChargebackFeeTotal:       500,
		ReservesHeld:             10000,
		ReservesReleased:         0,
		ReservesForward:          0,
		SettlementNetAmount:      133250,
	}
}

func TestToCSVHeaderAndRow(t *testing.T) {
	data, err := ToCSV(sampleReport())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected header + 1 row, got %d lines: %q", len(lines), data)
	}
	wantHeader := "merchant_id,transactions_date,currency,settlement_state,settlement_date,approved_transaction_count,declined_transaction_count,sale_amount_total,returned_amount_total,chargebacks_count,chargebacks_amount_total,discount_rate_fee_total,chargeback_fee_total,reserves_held,reserves_released,reserves_forward,settlement_net_amount"
	if lines[0] != wantHeader {
		t.Fatalf("header = %q, want %q", lines[0], wantHeader)
	}
	wantRow := "GB00SIM0000000000003,2026-09-03,EUR,Scheduled,2026-09-04,42,3,150000,5000,1,5000,2250,500,10000,0,0,133250"
	if lines[1] != wantRow {
		t.Fatalf("row = %q, want %q", lines[1], wantRow)
	}
}

func TestToCSVAllMultipleReports(t *testing.T) {
	r2 := sampleReport()
	r2.MerchantID = "GB00SIM0000000000004"
	data, err := ToCSVAll([]*Report{sampleReport(), r2})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header + 2 rows, got %d: %q", len(lines), data)
	}
	if !strings.HasPrefix(lines[1], "GB00SIM0000000000003,") || !strings.HasPrefix(lines[2], "GB00SIM0000000000004,") {
		t.Fatalf("rows out of order or wrong: %v", lines[1:])
	}
}
