package bankingcircle

import (
	"sort"
	"strconv"
	"strings"
)

// The Connect "reports" API: the two reads a reconciliation sweep makes.
//
// These are the observation points that answer "did the money actually
// book", and they are deliberately derived from the same payment records
// the webhooks are derived from. A mock whose report disagreed with its own
// notifications would be worse than no mock: it would teach a client to
// trust a reconciliation that cannot be trusted.
//
// # What each report means
//
// The intraday reconciliation report carries no status field. A row's
// existence *is* the statement that a booking happened — so only booked and
// processed payments appear, and a reversal appears as a row with `return`
// set rather than as an absence.
//
// The rejection report is the complement: payments that did not book on the
// requested date, with an explicit status and reason. Between them every
// payment submitted on a date is accounted for exactly once, which is the
// property a sweep relies on.

// ReconciliationRow is one booking line. Field names are the wire contract;
// the pointer-ish `*string` shapes exist because the real report sends null
// for fields that do not apply, and a client distinguishing "zero" from
// "not applicable" must keep working against this.
type ReconciliationRow struct {
	PaymentID                  *string  `json:"paymentId"`
	Account                    *string  `json:"account"`
	AccountCurrency            *string  `json:"accountCurrency"`
	DebitAmount                *float64 `json:"debitAmount"`
	CreditAmount               *float64 `json:"creditAmount"`
	CreditDebitIndicator       *string  `json:"creditDebitIndicator"`
	TransactionAmount          float64  `json:"transactionAmount"`
	TransactionAmountCurrency  *string  `json:"transactionAmountCurrency"`
	ValueDate                  *string  `json:"valueDate"`
	ReportDate                 *string  `json:"reportDate"`
	Return                     *bool    `json:"return"`
	LatestStatusChangedTimestp *string  `json:"latestStatusChangedTimestamp"`
	ProcessedTimestamp         *string  `json:"processedTimestamp"`
	StatusReasonCode           *string  `json:"statusReasonCode"`
	StatusReasonDescription    *string  `json:"statusReasonDescription"`
	PaymentReferenceNumber     *string  `json:"paymentReferenceNumber"`
	UserReferenceNumber        *string  `json:"userReferenceNumber"`
	ClientOrderID              *string  `json:"clientOrderId"`
}

// RejectionRow is one payment that did not book. It carries no paymentId —
// only reference numbers — which is why this mock populates all three
// reference fields with something correlatable. See ReferenceFields.
type RejectionRow struct {
	Account                *string `json:"account"`
	AccountCurrency        *string `json:"accountCurrency"`
	ValueDate              *string `json:"valueDate"`
	ReportDate             *string `json:"reportDate"`
	PaymentAmount          float64 `json:"paymentAmount"`
	PaymentCurrency        *string `json:"paymentCurrency"`
	PaymentReferenceNumber *string `json:"paymentReferenceNumber"`
	UserReferenceNumber    *string `json:"userReferenceNumber"`
	FileReferenceNumber    *string `json:"fileReferenceNumber"`
	SourceType             *string `json:"sourceType"`
	Status                 *string `json:"status"`
	StatusReason           *string `json:"statusReason"`
}

// ReconciliationQuery is the filter the paged report accepts.
type ReconciliationQuery struct {
	FromTransactionDate string
	ToTransactionDate   string
	FromCreatedAt       string
	ToCreatedAt         string
	AccountIDs          []string
	PageNumber          int
	PageSize            int
}

// RejectionQuery is the filter the rejection report accepts.
type RejectionQuery struct {
	TransactionDate     string
	IncludeReceived     bool
	IncludeMissingFunds bool
	IncludeReversals    bool
	ExcludeBooked       bool
}

// booked reports whether a state means value moved on an account.
func booked(s NotificationType) bool {
	switch s {
	case NotificationOutgoingPaymentBooked, NotificationOutgoingPaymentProcessed,
		NotificationIncomingPaymentBooked, NotificationIncomingPaymentProcessed,
		NotificationReversed:
		return true
	}
	return false
}

// incoming reports whether the booking credits one of our accounts.
func incoming(s NotificationType) bool {
	return s == NotificationIncomingPaymentBooked || s == NotificationIncomingPaymentProcessed
}

// IntradayReconciliation returns the booking lines for a date range.
//
// Both date bounds are inclusive and compared on the date prefix, because
// that is how a caller sweeping "yesterday" reasons about them — and a
// half-open range would silently drop the last payment of the day, which is
// exactly the bug a reconciliation report exists to catch.
func IntradayReconciliation(payments []*Payment, q ReconciliationQuery) []ReconciliationRow {
	rows := make([]ReconciliationRow, 0)
	for _, p := range payments {
		if !booked(p.State) {
			continue
		}
		if !withinDates(p.UpdatedAt, q.FromTransactionDate, q.ToTransactionDate) {
			continue
		}
		if !withinDates(p.CreatedAt, q.FromCreatedAt, q.ToCreatedAt) {
			continue
		}
		account := p.FromAccountID
		if incoming(p.State) {
			account = p.ToAccountID
		}
		if len(q.AccountIDs) > 0 && !containsFold(q.AccountIDs, account) {
			continue
		}
		rows = append(rows, reconciliationRow(p, account))
	}
	// Paging over an unordered set is not paging. The caller's payment
	// store hands these back in map order, which differs between calls, so
	// without a total order page 2 can repeat a row from page 1 and drop
	// one entirely — a sweep walking pages would silently lose a payment.
	// Booking time first, id as the tie-break, so the order is total.
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		at, bt := derefStr(a.ProcessedTimestamp), derefStr(b.ProcessedTimestamp)
		if at != bt {
			return at < bt
		}
		return derefStr(a.PaymentID) < derefStr(b.PaymentID)
	})
	return page(rows, q.PageNumber, q.PageSize)
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func reconciliationRow(p *Payment, account string) ReconciliationRow {
	amount := amountFloat(p.Amount)
	date := datePart(p.UpdatedAt)
	isReturn := p.State == NotificationReversed

	row := ReconciliationRow{
		PaymentID:                  str(p.ID),
		Account:                    str(account),
		AccountCurrency:            str(p.Currency),
		TransactionAmount:          amount,
		TransactionAmountCurrency:  str(p.Currency),
		ValueDate:                  str(date),
		ReportDate:                 str(date),
		Return:                     &isReturn,
		LatestStatusChangedTimestp: str(p.UpdatedAt),
		ProcessedTimestamp:         str(p.UpdatedAt),
	}
	// A return books the opposite way round from the payment it reverses,
	// which is the whole reason a sweep has to read the indicator rather
	// than assume the sign from the amount.
	creditSide := incoming(p.State) != isReturn
	if creditSide {
		row.CreditAmount = &amount
		row.CreditDebitIndicator = str("C")
	} else {
		row.DebitAmount = &amount
		row.CreditDebitIndicator = str("D")
	}
	row.PaymentReferenceNumber, row.UserReferenceNumber, row.ClientOrderID = ReferenceFields(p)
	return row
}

// Rejections returns the payments that did not book on a date.
func Rejections(payments []*Payment, q RejectionQuery) []RejectionRow {
	rows := make([]RejectionRow, 0)
	for _, p := range payments {
		if q.TransactionDate != "" && datePart(p.UpdatedAt) != q.TransactionDate {
			continue
		}
		status, reason, ok := rejectionStatus(p.State)
		if !ok {
			continue
		}
		if p.State == NotificationMissingFunding && !q.IncludeMissingFunds {
			continue
		}
		if p.State == NotificationReversed && !q.IncludeReversals {
			continue
		}
		account := p.FromAccountID
		if incoming(p.State) {
			account = p.ToAccountID
		}
		date := datePart(p.UpdatedAt)
		ref, user, order := ReferenceFields(p)
		rows = append(rows, RejectionRow{
			Account:                str(account),
			AccountCurrency:        str(p.Currency),
			ValueDate:              str(date),
			ReportDate:             str(date),
			PaymentAmount:          amountFloat(p.Amount),
			PaymentCurrency:        str(p.Currency),
			PaymentReferenceNumber: ref,
			UserReferenceNumber:    user,
			FileReferenceNumber:    order,
			SourceType:             str("Api"),
			Status:                 str(status),
			StatusReason:           str(reason),
		})
	}
	// Same reason as the reconciliation report, even though this one is
	// not paged: a report whose row order changes between identical calls
	// is one nobody can diff.
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if derefStr(a.ValueDate) != derefStr(b.ValueDate) {
			return derefStr(a.ValueDate) < derefStr(b.ValueDate)
		}
		return derefStr(a.PaymentReferenceNumber) < derefStr(b.PaymentReferenceNumber)
	})
	return rows
}

// rejectionStatus maps a state onto the report's status/reason pair, and
// reports whether the payment belongs in this report at all.
func rejectionStatus(s NotificationType) (status, reason string, ok bool) {
	switch s {
	case NotificationOutgoingPaymentRejected:
		return "Rejected", "Payment rejected by the beneficiary bank (simulated)", true
	case NotificationMissingFunding:
		return "MissingFunding", "Insufficient funds on the debtor account (simulated)", true
	case NotificationReversed:
		return "Reversed", "Payment returned after booking (simulated)", true
	case NotificationPaymentRouting, NotificationOutgoingDirectDebitPendingProcessing:
		return "Pending", "Still processing at the requested date (simulated)", true
	}
	return "", "", false
}

// ReferenceFields is the one correlation seam a caller has to a rejection
// row, which carries no paymentId.
//
// The mapping from an outbound external reference to one of Banking
// Circle's three reference fields is not something this mock can confirm,
// so it does not guess: all three are populated with something the caller
// sent, and whichever one a client matches on will work. If a real response
// turns out to use only one, narrow this and the tests that assert it.
func ReferenceFields(p *Payment) (paymentRef, userRef, clientOrderID *string) {
	ref := p.Reference
	if ref == "" {
		ref = p.SettlementID
	}
	order := p.SettlementID
	if order == "" {
		order = p.Reference
	}
	return str(ref), str(ref), str(order)
}

// page applies PageNumber/PageSize. Page numbers are 1-based; a page past
// the end is empty rather than an error, because a sweep walking pages
// until it sees fewer rows than it asked for must be able to stop.
func page(rows []ReconciliationRow, number, size int) []ReconciliationRow {
	if size <= 0 {
		return rows
	}
	if number <= 0 {
		number = 1
	}
	start := (number - 1) * size
	if start >= len(rows) {
		return []ReconciliationRow{}
	}
	end := start + size
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end]
}

// withinDates compares on the date prefix, inclusive at both ends. An empty
// bound does not filter.
func withinDates(timestamp, from, to string) bool {
	d := datePart(timestamp)
	if d == "" {
		return from == "" && to == ""
	}
	if from != "" && d < datePart(from) {
		return false
	}
	if to != "" && d > datePart(to) {
		return false
	}
	return true
}

func datePart(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

// amountFloat parses the decimal string these records carry. A malformed
// amount reports zero rather than failing the whole report: one bad row
// must not hide every good one from a sweep.
func amountFloat(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), v) {
			return true
		}
	}
	return false
}

// str returns a pointer to s, or nil when s is empty — the report sends
// null for a field that does not apply, and a client may be reading that
// distinction.
func str(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
