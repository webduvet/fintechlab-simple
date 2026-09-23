package bankingcircle

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// samplePayments is one day's activity: a lump sum in, three payouts that
// booked, one rejected, one short of funds, and one reversed after processing.
func samplePayments() []*Payment {
	payments := []*Payment{
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
	// Each has the bank's own reference, as every accepted payment does.
	for i, p := range payments {
		p.ReferenceNumber = fmt.Sprintf("010F10%010d", i+1)
	}
	return payments
}

func today() ReconciliationQuery {
	return ReconciliationQuery{
		FromTransactionDate: "2026-09-18", ToTransactionDate: "2026-09-18",
		PageNumber: 1, PageSize: 100, IBAN: testIBAN,
	}
}

// testIBAN is the safeguarding accounts' IBANs, as the ledger seeds them.
func testIBAN(accountID string) string {
	return map[string]string{
		SGAAccountEUR: "BE00SIMSGA00000001",
		SGAAccountGBP: "GB00SIMSGA00000001",
	}[accountID]
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
// know which way value moved.
func TestCreditDebitIndicatorMatchesTheSide(t *testing.T) {
	got := ids(IntradayReconciliation(samplePayments(), today()))

	in := got["bc_in_1"]
	if in.CreditDebitIndicator == nil || *in.CreditDebitIndicator != "CRDT" {
		t.Errorf("money arriving should be a credit, got %v", deref(in.CreditDebitIndicator))
	}
	if in.CreditAmount == nil || in.DebitAmount != nil {
		t.Errorf("a credit fills creditAmount only: credit=%v debit=%v",
			derefF(in.CreditAmount), derefF(in.DebitAmount))
	}

	out := got["bc_p_1"]
	if out.CreditDebitIndicator == nil || *out.CreditDebitIndicator != "DBIT" {
		t.Errorf("a payout should be a debit, got %v", deref(out.CreditDebitIndicator))
	}
	if out.DebitAmount == nil || out.CreditAmount != nil {
		t.Errorf("a debit fills debitAmount only: credit=%v debit=%v",
			derefF(out.CreditAmount), derefF(out.DebitAmount))
	}

	if in.Return != nil || out.Return != nil {
		t.Errorf("`return` is true or null, never false: in=%v out=%v", in.Return, out.Return)
	}
}

// TestReversalIsASecondNegativeBooking: a scheme reversal does not rewrite
// the payment's booking. The payout keeps its DBIT row, and the reversal is
// a second DBIT row on the same payment with the amount negated — the
// docs' "negative equivalent of the original amount debited". It is not a
// return: `return` marks an incoming return payment, and stays null here.
func TestReversalIsASecondNegativeBooking(t *testing.T) {
	payments := samplePayments()
	rev := payments[6]
	rev.ProcessedAt = "2026-09-18T09:00:10Z"
	rev.ReversedAt = rev.UpdatedAt
	rev.ReversalReason = "Rejected by the scheme"

	var rows []ReconciliationRow
	for _, r := range IntradayReconciliation(payments, today()) {
		if deref(r.PaymentID) == "bc_rev_1" {
			rows = append(rows, r)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("reversed payment has %d rows, want its booking and the reversal", len(rows))
	}
	booking, reversal := rows[0], rows[1]
	for name, r := range map[string]ReconciliationRow{"booking": booking, "reversal": reversal} {
		if deref(r.CreditDebitIndicator) != "DBIT" || r.CreditAmount != nil {
			t.Errorf("%s: indicator %s, credit %v; want a DBIT line", name, deref(r.CreditDebitIndicator), derefF(r.CreditAmount))
		}
		if r.Return != nil {
			t.Errorf("%s: return = %v, want null: a reversal is not an incoming return", name, *r.Return)
		}
	}
	if derefF(booking.DebitAmount) != 55.0 || derefF(reversal.DebitAmount) != -55.0 {
		t.Errorf("debitAmount booking=%v reversal=%v, want 55 and -55", derefF(booking.DebitAmount), derefF(reversal.DebitAmount))
	}
	if deref(booking.ProcessedTimestamp) != rev.ProcessedAt || deref(reversal.ProcessedTimestamp) != rev.ReversedAt {
		t.Errorf("processedTimestamp booking=%s reversal=%s, want the processing and the reversal time",
			deref(booking.ProcessedTimestamp), deref(reversal.ProcessedTimestamp))
	}
	if booking.StatusReasonDescription != nil || deref(reversal.StatusReasonDescription) != "Rejected by the scheme" {
		t.Errorf("statusReasonDescription booking=%v reversal=%q, want null and the reason",
			booking.StatusReasonDescription, deref(reversal.StatusReasonDescription))
	}
}

// TestReversalBooksOnItsOwnDate: the booking stays on the day it was
// processed, and the reversal lands on the day it happened.
func TestReversalBooksOnItsOwnDate(t *testing.T) {
	payments := samplePayments()
	rev := payments[6]
	rev.ProcessedAt = "2026-09-17T09:00:10Z"
	rev.CreatedAt = "2026-09-17T09:00:05Z"
	rev.ReversedAt = "2026-09-18T09:00:15Z"

	q := today()
	q.FromTransactionDate, q.FromCreatedAt = "2026-09-17", "2026-09-17"
	q.ToTransactionDate = "2026-09-17"
	var day1, day2 []ReconciliationRow
	for _, r := range IntradayReconciliation(payments, q) {
		if deref(r.PaymentID) == "bc_rev_1" {
			day1 = append(day1, r)
		}
	}
	q.FromTransactionDate, q.ToTransactionDate = "2026-09-18", "2026-09-18"
	for _, r := range IntradayReconciliation(payments, q) {
		if deref(r.PaymentID) == "bc_rev_1" {
			day2 = append(day2, r)
		}
	}
	if len(day1) != 1 || isReversalRow(day1[0]) {
		t.Errorf("processing day has %d rows for the payment, want only its booking", len(day1))
	}
	if len(day2) != 1 || !isReversalRow(day2[0]) {
		t.Errorf("reversal day has %d rows for the payment, want only the reversal", len(day2))
	}
}

// rowKey names a report line: a payment's booking, or its reversal.
func rowKey(r ReconciliationRow) string {
	if isReversalRow(r) {
		return deref(r.PaymentID) + "/reversal"
	}
	return deref(r.PaymentID)
}

// TestRejectionReportIsTheComplement is the property the pair exists for:
// every payment instructed on the date appears in at least one of the two
// reports, and only a pending payment that has already booked appears in
// both. If both could omit a payment, a sweep would silently lose money.
func TestRejectionReportIsTheComplement(t *testing.T) {
	payments := samplePayments()
	booked := ids(IntradayReconciliation(payments, today()))
	rejected := map[string]RejectionRow{}
	for _, r := range Rejections(payments, defaultRejections()) {
		rejected[deref(r.PaymentReferenceNumber)] = r
	}

	for _, p := range payments {
		if datePart(p.CreatedAt) != "2026-09-18" || incoming(p.State) {
			continue
		}
		_, inRecon := booked[p.ID]
		_, inRejected := rejected[p.ReferenceNumber]
		switch {
		case !inRecon && !inRejected:
			t.Errorf("%s (%s) is in neither report — a sweep would lose it", p.ID, p.State)
		case inRecon && inRejected && p.State != NotificationOutgoingPaymentBooked:
			t.Errorf("%s (%s) is in both reports", p.ID, p.State)
		}
	}

	// The statuses are the documented ones, and only a failure has a reason.
	for id, want := range map[string][2]string{
		"bc_r_1": {"Rejected", "reason"},
		"bc_r_2": {"Insufficient Funds", "reason"},
		"bc_p_2": {"Received", ""},
	} {
		var ref string
		for _, p := range payments {
			if p.ID == id {
				ref = p.ReferenceNumber
			}
		}
		r, ok := rejected[ref]
		if !ok {
			t.Errorf("%s is not on the rejection report", id)
			continue
		}
		if deref(r.Status) != want[0] {
			t.Errorf("%s: status = %q, want %q", id, deref(r.Status), want[0])
		}
		if hasReason := deref(r.StatusReason) != ""; hasReason != (want[1] != "") {
			t.Errorf("%s: statusReason = %q", id, deref(r.StatusReason))
		}
	}
	if _, ok := rejected["010F100000000007"]; ok {
		t.Error("the reversed payout is on the rejection report; IncludeReversals covers direct debits only")
	}
}

// defaultRejections is a request for 2026-09-18 with the defaults the
// handler applies.
func defaultRejections() RejectionQuery {
	return RejectionQuery{TransactionDate: "2026-09-18", IncludeReceived: true, IncludeMissingFunds: true, ReportDate: "2026-09-19", IBAN: testIBAN}
}

// TestRejectionFiltersAreHonoured: IncludeMissingFunds and IncludeReceived
// drop their kind, and ExcludeBooked drops pending payments that have
// already booked.
func TestRejectionFiltersAreHonoured(t *testing.T) {
	payments := samplePayments()
	all := Rejections(payments, defaultRejections())
	if len(all) != 3 {
		t.Fatalf("default report has %d rows, want the rejected, missing-funds and pending payouts", len(all))
	}
	noFunds := defaultRejections()
	noFunds.IncludeMissingFunds = false
	noReceived := defaultRejections()
	noReceived.IncludeReceived = false
	noBooked := defaultRejections()
	noBooked.ExcludeBooked = true

	for name, q := range map[string]RejectionQuery{
		"IncludeMissingFunds=false": noFunds,
		"IncludeReceived=false":     noReceived,
		"ExcludeBooked=true":        noBooked,
	} {
		if got := len(Rejections(payments, q)); got != len(all)-1 {
			t.Errorf("%s: %d rows, want exactly one fewer than %d", name, got, len(all))
		}
	}
}

// TestRejectionRowMatchesTheReference: the fields a row carries and their
// shapes are the reference's, spelling included.
func TestRejectionRowMatchesTheReference(t *testing.T) {
	payments := samplePayments()
	payments[4].ToIBAN = "BE10001001111111"
	var row RejectionRow
	for _, r := range Rejections(payments, defaultRejections()) {
		if deref(r.PaymentReferenceNumber) == payments[4].ReferenceNumber {
			row = r
		}
	}
	for name, pair := range map[string][2]string{
		"pTxndate":         {deref(row.PTxndate), "2026-09-18T00:00:00+00:00"},
		"valueDate":        {deref(row.ValueDate), "2026-09-18T00:00:00+00:00"},
		"reportDate":       {deref(row.ReportDate), "2026-09-19T00:00:00+00:00"},
		"transferCurrency": {deref(row.TransferCurrency), "EUR"},
		"destinationIban":  {deref(row.DestinationIban), "BE10001001111111"},
		"sourceType":       {deref(row.SourceType), "Single payment"},
		"status":           {deref(row.Status), "Rejected"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %q, want %q", name, pair[0], pair[1])
		}
	}
	if row.FileReferenceNumber == nil || *row.FileReferenceNumber != "" {
		t.Errorf("fileReferenceNumber = %v, want \"\" for a single payment", row.FileReferenceNumber)
	}
	if row.PIdChanneluser != nil || row.CustomerID != nil {
		t.Error("pIdChanneluser and customerId must be null: the lab has neither")
	}

	raw, _ := json.Marshal(row)
	for _, key := range []string{`"pIdChanneluser"`, `"pTxndate"`, `"customerId"`, `"transferCurrency"`, `"destinationIban"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("row JSON has no %s property", key)
		}
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
			if seen[rowKey(r)] {
				t.Fatalf("%s returned on more than one page", rowKey(r))
			}
			seen[rowKey(r)] = true
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
	for _, r := range Rejections(payments, defaultRejections()) {
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
				got = append(got, rowKey(r))
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
	q := defaultRejections()
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

// TestProcessedTimestampOnlyOnceProcessed: the report carries booked and
// processed payments alike, and processedTimestamp is what tells them
// apart. A booked row with a timestamp would read as settled to a sweep.
func TestProcessedTimestampOnlyOnceProcessed(t *testing.T) {
	got := ids(IntradayReconciliation(samplePayments(), today()))

	if got["bc_p_2"].ProcessedTimestamp != nil {
		t.Errorf("booked-only payment has processedTimestamp %v", deref(got["bc_p_2"].ProcessedTimestamp))
	}
	for _, id := range []string{"bc_in_1", "bc_p_1", "bc_rev_1"} {
		if got[id].ProcessedTimestamp == nil {
			t.Errorf("%s is processed but has no processedTimestamp", id)
		}
	}
}

func TestPaymentStatusMapsEveryState(t *testing.T) {
	for state, want := range map[NotificationType]string{
		NotificationOutgoingPaymentBooked:    "PendingProcessing",
		NotificationPaymentRouting:           "PendingProcessing",
		NotificationOutgoingPaymentProcessed: "Processed",
		NotificationOutgoingPaymentRejected:  "Rejected",
		NotificationMissingFunding:           "MissingFunding",
		NotificationReversed:                 "Reversed",
	} {
		if got := PaymentStatus(state); got != want {
			t.Errorf("PaymentStatus(%s) = %s, want %s", state, got, want)
		}
	}
}

// TestReferenceFieldsAreTheBanks: paymentReferenceNumber is the bank's own
// reference, never something we sent, and clientOrderId is only for FX
// trades, which the lab has none of.
func TestReferenceFieldsAreTheBanks(t *testing.T) {
	payments := samplePayments()
	payments[1].ReferenceNumber = "010F100000000042"
	payments[2].ReferenceNumber = ""
	got := ids(IntradayReconciliation(payments, today()))

	if ref := deref(got["bc_p_1"].PaymentReferenceNumber); ref != "010F100000000042" {
		t.Errorf("paymentReferenceNumber = %q, want the bank's reference", ref)
	}
	if ref := got["bc_p_2"].PaymentReferenceNumber; ref != nil {
		t.Errorf("paymentReferenceNumber = %q for a payment with no bank reference, want null", *ref)
	}
	for id, row := range got {
		if row.ClientOrderID != nil {
			t.Errorf("%s: clientOrderId = %q, want null outside FX trades", id, *row.ClientOrderID)
		}
	}
}

// TestReturnIsItsOwnIncomingRow: the returned payout's DBIT row is left
// alone, and the return is a CRDT row of its own with `return: true`, the
// remittance lines in paymentDetails, and the docs' return indicators in
// additionalRemittanceInformation.
func TestReturnIsItsOwnIncomingRow(t *testing.T) {
	payments := samplePayments()
	payout := payments[1]
	payout.ReferenceNumber = "010F100000000001"
	payout.ReturnedBy = "bc_ret_1"
	payments = append(payments, &Payment{
		ID: "bc_ret_1", FromAccountID: "acc_m1", ToAccountID: SGAAccountEUR,
		Amount: "125.00", Currency: "EUR", ReferenceNumber: "010F100000000009",
		State:     NotificationIncomingPaymentProcessed,
		CreatedAt: "2026-09-18T11:00:00Z", UpdatedAt: "2026-09-18T11:00:05Z",
		Return: true, ReturnOf: payout.ID, ReturnedReference: payout.ReferenceNumber,
		ReturnReasonCode: "AC04", ReturnReasonDescription: "Closed account number",
		Remittance: []string{"RETURN OF PAYMENT", payout.ReferenceNumber, "AC04 Closed account number"},
	})
	got := ids(IntradayReconciliation(payments, today()))

	if orig := got["bc_p_1"]; deref(orig.CreditDebitIndicator) != "DBIT" || orig.Return != nil {
		t.Errorf("payout row: indicator %s, return %v; want DBIT and null", deref(orig.CreditDebitIndicator), orig.Return)
	}
	ret, ok := got["bc_ret_1"]
	if !ok {
		t.Fatal("the return payment is not on the report")
	}
	if deref(ret.CreditDebitIndicator) != "CRDT" || derefF(ret.CreditAmount) != 125.0 || deref(ret.Account) != "BE00SIMSGA00000001" {
		t.Errorf("return row: %s %v on %s, want CRDT 125 on the safeguarding account",
			deref(ret.CreditDebitIndicator), derefF(ret.CreditAmount), deref(ret.Account))
	}
	if ret.Return == nil || !*ret.Return {
		t.Error("return row: `return` must be true")
	}
	for name, pair := range map[string][2]string{
		"paymentDetails1":                  {deref(ret.PaymentDetails1), "RETURN OF PAYMENT"},
		"paymentDetails2":                  {deref(ret.PaymentDetails2), "010F100000000001"},
		"additionalRemittanceInformation1": {deref(ret.AdditionalRemittanceInfo1), "/RETN/"},
		"additionalRemittanceInformation2": {deref(ret.AdditionalRemittanceInfo2), "/AC04/Closed account number"},
		"additionalRemittanceInformation3": {deref(ret.AdditionalRemittanceInfo3), "/MREF/010F100000000001"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %q, want %q", name, pair[0], pair[1])
		}
	}
}

// TestAccountIsTheIBAN: both reports name an account by its IBAN ("IBAN of
// your account"), never by the id the AccountId filter matches on, and an
// account with no known IBAN is null rather than its id.
func TestAccountIsTheIBAN(t *testing.T) {
	payments := samplePayments()

	q := today()
	q.AccountIDs = []string{SGAAccountGBP}
	gbp := IntradayReconciliation(payments, q)
	if len(gbp) != 1 || deref(gbp[0].Account) != "GB00SIMSGA00000001" {
		t.Fatalf("GBP rows = %d, account %q; want one, on the GBP IBAN", len(gbp), deref(gbp[0].Account))
	}
	for _, r := range Rejections(payments, defaultRejections()) {
		if deref(r.Account) != "BE00SIMSGA00000001" {
			t.Errorf("rejection row %s: account = %q, want the EUR IBAN", deref(r.PaymentReferenceNumber), deref(r.Account))
		}
	}

	q = today()
	q.IBAN = func(string) string { return "" }
	for _, r := range IntradayReconciliation(payments, q) {
		if r.Account != nil {
			t.Errorf("%s: account = %q with no IBAN known, want null", deref(r.PaymentID), *r.Account)
		}
	}
}

// TestReconciliationDatesUseTheExampleFormat: reportDate and valueDate are
// sent the way the reference example sends them, midnight UTC with an
// offset, and still filter on the transaction date.
func TestReconciliationDatesUseTheExampleFormat(t *testing.T) {
	got := ids(IntradayReconciliation(samplePayments(), today()))
	row := got["bc_p_1"]
	if deref(row.ReportDate) != "2026-09-18T00:00:00+00:00" || deref(row.ValueDate) != "2026-09-18T00:00:00+00:00" {
		t.Errorf("reportDate = %q, valueDate = %q; want 2026-09-18T00:00:00+00:00", deref(row.ReportDate), deref(row.ValueDate))
	}
	if _, ok := got["bc_old"]; ok {
		t.Error("yesterday's payment is on today's report")
	}
}
