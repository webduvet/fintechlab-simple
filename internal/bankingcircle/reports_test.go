package bankingcircle

import (
	"testing"
)

// samplePayments is one day's activity: a lump sum in, three payouts that
// booked, one rejected, one short of funds, and one returned after booking.
func samplePayments() []*Payment {
	return []*Payment{
		{ID: "bc_in_1", FromAccountID: "external", ToAccountID: SGAAccountEUR,
			Amount: "4120.00", Currency: "EUR", Reference: "wl-lump-1",
			State:     NotificationIncomingPaymentProcessed,
			CreatedAt: "2026-09-18T06:00:00Z", UpdatedAt: "2026-09-18T06:00:05Z"},

		{ID: "bc_p_1", FromAccountID: SGAAccountEUR, ToAccountID: "acc_m1",
			Amount: "125.00", Currency: "EUR", Reference: "stl-1", SettlementID: "SETL-1",
			State:     NotificationOutgoingPaymentProcessed,
			CreatedAt: "2026-09-18T09:00:00Z", UpdatedAt: "2026-09-18T09:00:10Z"},
		{ID: "bc_p_2", FromAccountID: SGAAccountEUR, ToAccountID: "acc_m2",
			Amount: "80.50", Currency: "EUR", Reference: "stl-2", SettlementID: "SETL-1",
			State:     NotificationOutgoingPaymentBooked,
			CreatedAt: "2026-09-18T09:00:01Z", UpdatedAt: "2026-09-18T09:00:11Z"},
		{ID: "bc_p_3", FromAccountID: SGAAccountGBP, ToAccountID: "acc_m3",
			Amount: "40.00", Currency: "GBP", Reference: "stl-3", SettlementID: "SETL-2",
			State:     NotificationOutgoingPaymentProcessed,
			CreatedAt: "2026-09-18T09:00:02Z", UpdatedAt: "2026-09-18T09:00:12Z"},

		{ID: "bc_r_1", FromAccountID: SGAAccountEUR, ToAccountID: "acc_m4",
			Amount: "10.00", Currency: "EUR", Reference: "stl-4", SettlementID: "SETL-1",
			State:     NotificationOutgoingPaymentRejected,
			CreatedAt: "2026-09-18T09:00:03Z", UpdatedAt: "2026-09-18T09:00:13Z"},
		{ID: "bc_r_2", FromAccountID: SGAAccountEUR, ToAccountID: "acc_m5",
			Amount: "999.00", Currency: "EUR", Reference: "stl-5", SettlementID: "SETL-1",
			State:     NotificationMissingFunding,
			CreatedAt: "2026-09-18T09:00:04Z", UpdatedAt: "2026-09-18T09:00:14Z"},
		{ID: "bc_rev_1", FromAccountID: SGAAccountEUR, ToAccountID: "acc_m6",
			Amount: "55.00", Currency: "EUR", Reference: "stl-6", SettlementID: "SETL-1",
			State:     NotificationReversed,
			CreatedAt: "2026-09-18T09:00:05Z", UpdatedAt: "2026-09-18T09:00:15Z"},

		// Yesterday. Must not appear in today's reports.
		{ID: "bc_old", FromAccountID: SGAAccountEUR, ToAccountID: "acc_m1",
			Amount: "1.00", Currency: "EUR", Reference: "stl-old",
			State:     NotificationOutgoingPaymentProcessed,
			CreatedAt: "2026-09-17T09:00:00Z", UpdatedAt: "2026-09-17T09:00:10Z"},
	}
}

func today() ReconciliationQuery {
	return ReconciliationQuery{
		FromTransactionDate: "2026-09-18", ToTransactionDate: "2026-09-18",
		PageNumber: 1, PageSize: 100,
	}
}

func ids(rows []ReconciliationRow) map[string]ReconciliationRow {
	out := map[string]ReconciliationRow{}
	for _, r := range rows {
		if r.PaymentID != nil {
			out[*r.PaymentID] = r
		}
	}
	return out
}

// TestReconciliationCarriesOnlyBookings: the report has no status field, so
// a row's existence is the booking signal. A payment that did not book must
// not appear at all, or a sweep would count money that never moved.
func TestReconciliationCarriesOnlyBookings(t *testing.T) {
	rows := IntradayReconciliation(samplePayments(), today())
	got := ids(rows)

	for _, want := range []string{"bc_in_1", "bc_p_1", "bc_p_2", "bc_p_3", "bc_rev_1"} {
		if _, ok := got[want]; !ok {
			t.Errorf("%s booked but is missing from the reconciliation report", want)
		}
	}
	for _, unwanted := range []string{"bc_r_1", "bc_r_2"} {
		if _, ok := got[unwanted]; ok {
			t.Errorf("%s did not book but appears in the reconciliation report", unwanted)
		}
	}
	if _, ok := got["bc_old"]; ok {
		t.Error("yesterday's payment appears in today's report")
	}
}

// TestCreditDebitIndicatorMatchesTheSide: a sweep reads the indicator to
// know which way value moved, and a return books the opposite way from the
// payment it reverses.
func TestCreditDebitIndicatorMatchesTheSide(t *testing.T) {
	got := ids(IntradayReconciliation(samplePayments(), today()))

	in := got["bc_in_1"]
	if in.CreditDebitIndicator == nil || *in.CreditDebitIndicator != "C" {
		t.Errorf("money arriving should be a credit, got %v", deref(in.CreditDebitIndicator))
	}
	if in.CreditAmount == nil || in.DebitAmount != nil {
		t.Errorf("a credit fills creditAmount only: credit=%v debit=%v",
			derefF(in.CreditAmount), derefF(in.DebitAmount))
	}

	out := got["bc_p_1"]
	if out.CreditDebitIndicator == nil || *out.CreditDebitIndicator != "D" {
		t.Errorf("a payout should be a debit, got %v", deref(out.CreditDebitIndicator))
	}
	if out.DebitAmount == nil || out.CreditAmount != nil {
		t.Errorf("a debit fills debitAmount only: credit=%v debit=%v",
			derefF(out.CreditAmount), derefF(out.DebitAmount))
	}

	rev := got["bc_rev_1"]
	if rev.Return == nil || !*rev.Return {
		t.Error("a reversal must set `return`, or a sweep counts it as a second payout")
	}
	if rev.CreditDebitIndicator == nil || *rev.CreditDebitIndicator != "C" {
		t.Errorf("a returned payout credits the account back, got %v", deref(rev.CreditDebitIndicator))
	}
}

// TestRejectionReportIsTheComplement is the property the whole pair exists
// for: every payment on the date appears in exactly one of the two reports.
// If both could omit a payment, a sweep would silently lose money.
func TestRejectionReportIsTheComplement(t *testing.T) {
	payments := samplePayments()
	booked := ids(IntradayReconciliation(payments, today()))
	rejected := Rejections(payments, RejectionQuery{
		TransactionDate:     "2026-09-18",
		IncludeMissingFunds: true, IncludeReversals: true, IncludeReceived: true,
	})

	rejectedRefs := map[string]bool{}
	for _, r := range rejected {
		if r.PaymentReferenceNumber != nil {
			rejectedRefs[*r.PaymentReferenceNumber] = true
		}
	}

	for _, p := range payments {
		if datePart(p.UpdatedAt) != "2026-09-18" {
			continue
		}
		_, inRecon := booked[p.ID]
		inRejected := rejectedRefs[p.Reference]
		switch {
		case !inRecon && !inRejected:
			t.Errorf("%s (%s) is in neither report — a sweep would lose it", p.ID, p.State)
		case inRecon && inRejected && p.State != NotificationReversed:
			t.Errorf("%s (%s) is in both reports", p.ID, p.State)
		}
	}

	// And the rejection rows say why, since that report does carry a status.
	for _, r := range rejected {
		if r.Status == nil || *r.Status == "" {
			t.Errorf("rejection row %v has no status", deref(r.PaymentReferenceNumber))
		}
		if r.StatusReason == nil || *r.StatusReason == "" {
			t.Errorf("rejection row %v has no reason", deref(r.PaymentReferenceNumber))
		}
	}
}

// TestRejectionFiltersAreHonoured: the sweep turns these on and off, and a
// mock that ignored them would report a clean day as a broken one.
func TestRejectionFiltersAreHonoured(t *testing.T) {
	payments := samplePayments()
	base := RejectionQuery{TransactionDate: "2026-09-18", IncludeMissingFunds: true, IncludeReversals: true}

	all := Rejections(payments, base)
	noFunds := base
	noFunds.IncludeMissingFunds = false
	noReversals := base
	noReversals.IncludeReversals = false

	if len(Rejections(payments, noFunds)) != len(all)-1 {
		t.Error("IncludeMissingFunds=false should drop exactly the missing-funds row")
	}
	if len(Rejections(payments, noReversals)) != len(all)-1 {
		t.Error("IncludeReversals=false should drop exactly the reversal row")
	}
}

// TestAccountFilterAndPaging: a sweep walks pages until a short page, and
// filters by the accounts it cares about.
func TestAccountFilterAndPaging(t *testing.T) {
	payments := samplePayments()

	q := today()
	q.AccountIDs = []string{SGAAccountGBP}
	gbp := IntradayReconciliation(payments, q)
	if len(gbp) != 1 || gbp[0].PaymentID == nil || *gbp[0].PaymentID != "bc_p_3" {
		t.Fatalf("account filter returned %d rows, want just the GBP payout", len(gbp))
	}

	// Paging must partition the same set: no row seen twice, none lost.
	full := IntradayReconciliation(payments, today())
	seen := map[string]bool{}
	for pageNo := 1; ; pageNo++ {
		p := today()
		p.PageSize = 2
		p.PageNumber = pageNo
		rows := IntradayReconciliation(payments, p)
		for _, r := range rows {
			if seen[*r.PaymentID] {
				t.Fatalf("%s returned on more than one page", *r.PaymentID)
			}
			seen[*r.PaymentID] = true
		}
		if len(rows) < 2 {
			break
		}
		if pageNo > 10 {
			t.Fatal("paging did not terminate")
		}
	}
	if len(seen) != len(full) {
		t.Errorf("paging returned %d rows, the unpaged report has %d", len(seen), len(full))
	}
}

// TestEveryRowCarriesACorrelationHandle: a rejection row has no paymentId,
// so if all three reference fields were empty there would be no way back to
// our record at all.
func TestEveryRowCarriesACorrelationHandle(t *testing.T) {
	payments := samplePayments()
	for _, r := range Rejections(payments, RejectionQuery{TransactionDate: "2026-09-18", IncludeMissingFunds: true, IncludeReversals: true}) {
		if r.PaymentReferenceNumber == nil && r.UserReferenceNumber == nil && r.FileReferenceNumber == nil {
			t.Error("a rejection row with no reference of any kind cannot be correlated")
		}
	}
	for _, r := range IntradayReconciliation(payments, today()) {
		if r.PaymentID == nil {
			t.Error("a reconciliation row with no paymentId cannot be correlated")
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func derefF(f *float64) any {
	if f == nil {
		return "<nil>"
	}
	return *f
}

// TestPagingIsStableAcrossShuffledInput is the regression for a bug that
// only showed up against a running server: the payment store hands its
// records back in map order, so without a total order page 2 repeated a row
// from page 1 and dropped another entirely. A sweep walking pages would
// have silently lost a payment — the exact failure a reconciliation report
// exists to make impossible.
func TestPagingIsStableAcrossShuffledInput(t *testing.T) {
	base := samplePayments()

	collect := func(payments []*Payment) []string {
		var got []string
		for pageNo := 1; pageNo <= 10; pageNo++ {
			q := today()
			q.PageSize = 2
			q.PageNumber = pageNo
			rows := IntradayReconciliation(payments, q)
			for _, r := range rows {
				got = append(got, *r.PaymentID)
			}
			if len(rows) < 2 {
				break
			}
		}
		return got
	}

	want := collect(base)
	if len(want) == 0 {
		t.Fatal("no rows to page over")
	}
	seen := map[string]int{}
	for _, id := range want {
		seen[id]++
		if seen[id] > 1 {
			t.Fatalf("%s appears on more than one page", id)
		}
	}

	// Every rotation of the same set must produce the same pages.
	for shift := 1; shift < len(base); shift++ {
		rotated := append(append([]*Payment{}, base[shift:]...), base[:shift]...)
		got := collect(rotated)
		if len(got) != len(want) {
			t.Fatalf("rotation %d returned %d rows, want %d", shift, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("rotation %d changed the paging: position %d is %s, want %s",
					shift, i, got[i], want[i])
			}
		}
	}

	// And the rejection report is stable too, even though it is not paged.
	q := RejectionQuery{TransactionDate: "2026-09-18", IncludeMissingFunds: true, IncludeReversals: true}
	first := Rejections(base, q)
	for shift := 1; shift < len(base); shift++ {
		rotated := append(append([]*Payment{}, base[shift:]...), base[:shift]...)
		got := Rejections(rotated, q)
		if len(got) != len(first) {
			t.Fatalf("rejection rotation %d returned %d rows, want %d", shift, len(got), len(first))
		}
		for i := range first {
			if deref(got[i].PaymentReferenceNumber) != deref(first[i].PaymentReferenceNumber) {
				t.Fatalf("rejection rotation %d reordered row %d", shift, i)
			}
		}
	}
}
