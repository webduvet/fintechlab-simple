package bankingcircle

import "testing"

// TestBusinessDate: the day ends at 19:00 Central European time, summer and
// winter alike, and weekends book on the Monday after.
func TestBusinessDate(t *testing.T) {
	for _, c := range []struct{ at, want, why string }{
		{"2026-09-17T16:59:59Z", "2026-09-17", "18:59 CEST, Thursday: still today"},
		{"2026-09-17T17:00:00Z", "2026-09-18", "19:00 CEST: the next business day"},
		{"2026-09-17T22:30:00Z", "2026-09-18", "00:30 CEST on the 18th: that day, though UTC says the 17th"},
		{"2026-09-18T17:30:00Z", "2026-09-21", "19:30 CEST on a Friday: Monday"},
		{"2026-09-19T10:00:00Z", "2026-09-21", "Saturday: Monday"},
		{"2026-09-20T23:00:00Z", "2026-09-21", "01:00 CEST on Monday: Monday"},
		{"2026-12-01T17:59:00Z", "2026-12-01", "18:59 CET in winter: still today"},
		{"2026-12-01T18:00:00Z", "2026-12-02", "19:00 CET in winter: the next business day"},
		{"2026-09-18", "2026-09-18", "a bare date is that day"},
		{"not a time", "", "unparseable"},
	} {
		if got := BusinessDate(c.at); got != c.want {
			t.Errorf("%s (%s): got %q, want %q", c.at, c.why, got, c.want)
		}
	}
}

// TestLateBookingIsOnTheNextBusinessDaysReport: a payout processed at 19:30
// CEST on a Friday is on Monday's intraday report, not Friday's — the case
// the bank's docs warn about.
func TestLateBookingIsOnTheNextBusinessDaysReport(t *testing.T) {
	late := &Payment{ID: "bc_late", FromAccountID: SGAAccountEUR, ToAccountID: "acc_m9",
		Amount: "9.00", Currency: "EUR", State: NotificationOutgoingPaymentProcessed,
		CreatedAt: "2026-09-18T17:29:00Z", UpdatedAt: "2026-09-18T17:30:00Z", ProcessedAt: "2026-09-18T17:30:00Z"}

	friday := today()
	if _, ok := ids(IntradayReconciliation([]*Payment{late}, friday))["bc_late"]; ok {
		t.Error("a booking after 19:00 CEST is on Friday's report")
	}
	monday := today()
	monday.FromTransactionDate, monday.ToTransactionDate = "2026-09-21", "2026-09-21"
	row, ok := ids(IntradayReconciliation([]*Payment{late}, monday))["bc_late"]
	if !ok {
		t.Fatal("a booking after 19:00 CEST on Friday is not on Monday's report")
	}
	if deref(row.ReportDate) != "2026-09-21T00:00:00+00:00" {
		t.Errorf("reportDate = %q, want Monday", deref(row.ReportDate))
	}
}
