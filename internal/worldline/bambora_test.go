package worldline

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func sampleBamboraLedger() []Transaction {
	return []Transaction{
		{MID: "GB00SIM0000000000003", Currency: "EUR", AmountCents: 10000, Date: "2026-09-03"},
		{MID: "GB00SIM0000000000003", Currency: "EUR", AmountCents: 5000, Date: "2026-09-03"},
		// debit leg: must not become a TXER row.
		{MID: "GB00SIM0000000000003", Currency: "EUR", AmountCents: -3000, Date: "2026-09-03"},
		// different merchant: must not leak into this file.
		{MID: "GB00SIM0000000000099", Currency: "EUR", AmountCents: 99999, Date: "2026-09-03"},
		// outside the requested date range: must not count.
		{MID: "GB00SIM0000000000003", Currency: "EUR", AmountCents: 2000, Date: "2026-08-01"},
	}
}

func TestBamboraFilenameFormat(t *testing.T) {
	at := time.Date(2026, 9, 4, 13, 5, 9, 0, time.UTC)
	got := BamboraFilename("Worldline_Settlement", at, "ER", "eur")
	want := "20260904130509_Worldline_Settlement_ER_EUR.csv"
	if got != want {
		t.Fatalf("BamboraFilename = %q, want %q", got, want)
	}
}

func TestBamboraFilenameAfternoonFile(t *testing.T) {
	at := time.Date(2026, 9, 4, 18, 0, 0, 0, time.UTC)
	got := BamboraFilename("Worldline_Settlement", at, "AR", "USD")
	want := "20260904180000_Worldline_Settlement_AR_USD.csv"
	if got != want {
		t.Fatalf("BamboraFilename = %q, want %q", got, want)
	}
}

func TestGenerateBamboraMultiSectionStructure(t *testing.T) {
	f := GenerateBambora(sampleBamboraLedger(), "GB00SIM0000000000003", "2026-09-01", "2026-09-03", BamboraOptions{})

	if f.Meta.NumberOfItems != 2 {
		t.Fatalf("Meta.NumberOfItems = %d, want 2", f.Meta.NumberOfItems)
	}
	if f.Meta.SettlementAmount != 15000 {
		t.Fatalf("Meta.SettlementAmount = %d, want 15000", f.Meta.SettlementAmount)
	}
	if f.Meta.SettlementCurrency != "EUR" {
		t.Fatalf("Meta.SettlementCurrency = %q, want EUR", f.Meta.SettlementCurrency)
	}
	if f.Meta.ToAccount != "GB00SIM0000000000003" {
		t.Fatalf("Meta.ToAccount = %q, want the merchant IBAN", f.Meta.ToAccount)
	}
	if len(f.Batches) != 1 {
		t.Fatalf("expected exactly 1 batch, got %d", len(f.Batches))
	}
	batch := f.Batches[0]
	if batch.NumberOfTrans != 2 || len(batch.Transactions) != 2 {
		t.Fatalf("expected 2 transactions in the batch, got NumberOfTrans=%d len=%d", batch.NumberOfTrans, len(batch.Transactions))
	}
	if batch.NetAmount != 15000 || batch.SettlementAmount != 15000 {
		t.Fatalf("batch amounts = net:%d settlement:%d, want 15000 each", batch.NetAmount, batch.SettlementAmount)
	}
	if len(f.Chargebacks) != 0 {
		t.Fatalf("expected no chargebacks (no data source in this lab), got %d", len(f.Chargebacks))
	}

	var buf bytes.Buffer
	if err := f.WriteCSV(&buf); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	// Settlement header + row (2), Batch header + row (2), TXER header + 2
	// rows (3), CB header only, no rows (1) = 8 lines.
	if len(lines) != 8 {
		t.Fatalf("expected 8 lines (settlement x2, batch x2, txer x3, cb header x1), got %d:\n%s", len(lines), buf.String())
	}
	wantMetaHeader := `"VERSION_NUMBER","RECORD_TYPE","SETTLEMENT_AMOUNT","SETTLEMENT_CURRENCY","VALUE_DATE","NUMBER_OF_ITEMS","TO_ACCOUNT","PAYMENT_REFERENCE"`
	if lines[0] != wantMetaHeader {
		t.Fatalf("line 0 = %q, want %q", lines[0], wantMetaHeader)
	}
	if !strings.HasPrefix(lines[1], `"1","ST","15000","EUR",`) {
		t.Fatalf("line 1 (settlement row) = %q", lines[1])
	}
	wantBatchHeader := `"RECORD_TYPE","BAMBORA_MID","BATCH_REF","PAYREF_EXTENDED","NET_AMOUNT","BATCH_CURRENCY","SETTLEMENT_AMOUNT","SETTLEMENT_CURRENCY","NUMBER_OF_TRANS"`
	if lines[2] != wantBatchHeader {
		t.Fatalf("line 2 = %q, want %q", lines[2], wantBatchHeader)
	}
	if !strings.HasPrefix(lines[3], `"BT",`) {
		t.Fatalf("line 3 (batch row) = %q, want RECORD_TYPE BT", lines[3])
	}
	wantTXERHeader := `"RECORD_TYPE","BAMBORA_MID","SUBMERCHANT_ID","BATCH_REF","TRANSACTION_REF","BAMBORA_REF","ADDITIONAL_REF_1","ADDITIONAL_REF_2","TRANSACTION_TYPE","TRANSACTION_AMOUNT","TRANSACTION_CURRENCY","FX_RATE","SETTLEMENT_AMOUNT","SETTLEMENT_CURRENCY","CARD_SCHEME_NAME","CARD_USAGE","CARD_CATEGORY","INTERCHANGE_DOMAIN","COUNTRY_MERCHANT","COUNTRY_ISSUER","MCC","ECOM_SECURITY_LEVEL","ADDITIONAL_REF_3","CARD_NUMBER_TRUNCATED","TRANSACTION_DATE","TRANSACTION_TIME","CASHBACK_AMOUNT","PAYREF_EXTENDED","TERMINALID"`
	if lines[4] != wantTXERHeader {
		t.Fatalf("line 4 = %q, want %q", lines[4], wantTXERHeader)
	}
	if !strings.HasPrefix(lines[5], `"TXER",`) || !strings.HasPrefix(lines[6], `"TXER",`) {
		t.Fatalf("lines 5-6 (txer rows) = %q, %q", lines[5], lines[6])
	}
	wantCBHeader := `"RECORD_TYPE","BAMBORA_MID","SUBMERCHANT_ID","ORIGINAL_TRANSACTION_REF","BAMBORA_REF","DISPUTE_TRANSACTION_AMOUNT","DISPUTE_CURRENCY","DISPUTE_SETTLEMENT_AMOUNT","DISPUTE_SETTLEMENT_CURRENCY","DISPUTE_REASON_CODE","DISPUTE_REGISTRATION_DATE","DISPUTE_FEE","DISPUTE_FEE_CURRENCY","ORIGINAL_TRANSACTION_ADDITIONAL_REF_1"`
	if lines[7] != wantCBHeader {
		t.Fatalf("line 7 = %q, want %q", lines[7], wantCBHeader)
	}
}

// TestGenerateBamboraMerchantKeyIsAdditionalRef2 is a regression test for
// the exact mistake docs/ARCHITECTURE-vendor-corrections.md section 1
// calls out: the real merchant grouping key for TXER rows is
// ADDITIONAL_REF_2, not BAMBORA_MID (one BAMBORA_MID can span multiple
// merchants in the real file) and not SUBMERCHANT_ID either (that column
// keys CB rows, not TXER). A naive implementation might alias
// ADDITIONAL_REF_2 to the synthesized BAMBORA_MID/SUBMERCHANT_ID values,
// or vice versa -- this asserts that never happens.
func TestGenerateBamboraMerchantKeyIsAdditionalRef2(t *testing.T) {
	const merchantID = "GB00SIM0000000000003"
	f := GenerateBambora(sampleBamboraLedger(), merchantID, "2026-09-01", "2026-09-03", BamboraOptions{})

	if len(f.Batches) != 1 || len(f.Batches[0].Transactions) == 0 {
		t.Fatalf("expected at least one TXER row, got %+v", f.Batches)
	}
	batch := f.Batches[0]
	for _, txn := range batch.Transactions {
		if txn.AdditionalRef2 != merchantID {
			t.Fatalf("TXER.AdditionalRef2 = %q, want the real merchant id %q (this is the grouping key, not BAMBORA_MID)", txn.AdditionalRef2, merchantID)
		}
		if txn.BamboraMID == merchantID {
			t.Fatalf("TXER.BamboraMID must NOT equal the merchant id %q -- BAMBORA_MID only keys batches and can span multiple merchants, it must never be used as (or confused with) the merchant key", merchantID)
		}
		if txn.SubmerchantID == merchantID {
			t.Fatalf("TXER.SubmerchantID must NOT equal the merchant id %q -- SUBMERCHANT_ID is the CB rows' merchant key, not TXER's", merchantID)
		}
	}
	// The batch's own BAMBORA_MID must match every one of its TXER rows'
	// BAMBORA_MID (it's a real, consistent batch identifier) while still
	// staying distinct from the merchant id.
	if batch.BamboraMID == "" {
		t.Fatal("batch.BamboraMID must not be empty")
	}
	for _, txn := range batch.Transactions {
		if txn.BamboraMID != batch.BamboraMID {
			t.Fatalf("TXER.BamboraMID = %q, want it to match its batch's BamboraMID %q", txn.BamboraMID, batch.BamboraMID)
		}
	}
}

func TestGenerateBamboraDeterministicOutput(t *testing.T) {
	f1 := GenerateBambora(sampleBamboraLedger(), "GB00SIM0000000000003", "2026-09-01", "2026-09-03", BamboraOptions{})
	f2 := GenerateBambora(sampleBamboraLedger(), "GB00SIM0000000000003", "2026-09-01", "2026-09-03", BamboraOptions{})

	var buf1, buf2 bytes.Buffer
	if err := f1.WriteCSV(&buf1); err != nil {
		t.Fatal(err)
	}
	if err := f2.WriteCSV(&buf2); err != nil {
		t.Fatal(err)
	}
	if buf1.String() != buf2.String() {
		t.Fatalf("GenerateBambora is not deterministic: two runs on identical input produced different output:\n--- run 1 ---\n%s\n--- run 2 ---\n%s", buf1.String(), buf2.String())
	}
	if buf1.Len() == 0 {
		t.Fatal("expected non-empty output")
	}
}

func TestGenerateBamboraEmptyLedgerStillWritesHeaders(t *testing.T) {
	f := GenerateBambora(nil, "GB00SIM0000000000003", "2026-09-01", "2026-09-03", BamboraOptions{})
	if len(f.Batches) != 0 {
		t.Fatalf("expected no batches for an empty ledger, got %d", len(f.Batches))
	}
	if f.Meta.NumberOfItems != 0 {
		t.Fatalf("Meta.NumberOfItems = %d, want 0", f.Meta.NumberOfItems)
	}

	var buf bytes.Buffer
	if err := f.WriteCSV(&buf); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	// Settlement header + row, CB header only: no batches at all.
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (settlement header+row, cb header), got %d:\n%s", len(lines), buf.String())
	}
}

func TestBamboraOptionsDefaults(t *testing.T) {
	f := GenerateBambora(sampleBamboraLedger(), "GB00SIM0000000000003", "2026-09-01", "2026-09-03", BamboraOptions{})
	if f.Meta.VersionNumber != "1" {
		t.Fatalf("VersionNumber default = %q, want %q", f.Meta.VersionNumber, "1")
	}
	if f.Meta.ValueDate != "2026-09-03" {
		t.Fatalf("ValueDate default = %q, want toDate", f.Meta.ValueDate)
	}
	if f.Meta.PaymentReference == "" {
		t.Fatal("expected a non-empty default PaymentReference")
	}

	f2 := GenerateBambora(sampleBamboraLedger(), "GB00SIM0000000000003", "2026-09-01", "2026-09-03", BamboraOptions{
		ValueDate:        "2026-09-05",
		PaymentReference: "custom-ref",
		VersionNumber:    "2",
	})
	if f2.Meta.ValueDate != "2026-09-05" || f2.Meta.PaymentReference != "custom-ref" || f2.Meta.VersionNumber != "2" {
		t.Fatalf("options overrides not applied: %+v", f2.Meta)
	}
}
