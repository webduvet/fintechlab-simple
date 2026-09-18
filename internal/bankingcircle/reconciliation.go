package bankingcircle

import "strings"

// Reconcile returns every payment whose UpdatedAt falls on date (YYYY-MM-DD,
// UTC) for the given account, or every account if accountID is "". Matches
// the confirmed-real semantics: "relevant information of payments on all of
// your accounts at the given transaction date", used "to perform financial
// reconciliation of bookings".
func Reconcile(payments []*Payment, date, accountID string) []*Payment {
	out := make([]*Payment, 0)
	for _, p := range payments {
		if date != "" && !strings.HasPrefix(p.UpdatedAt, date) {
			continue
		}
		if accountID != "" && p.FromAccountID != accountID && p.ToAccountID != accountID {
			continue
		}
		out = append(out, p)
	}
	return out
}
