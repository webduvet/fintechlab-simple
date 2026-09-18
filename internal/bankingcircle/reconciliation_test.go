package bankingcircle

import "testing"

func TestReconcileDateAndAccountFiltering(t *testing.T) {
	payments := []*Payment{
		{ID: "p1", FromAccountID: "a", ToAccountID: "b", UpdatedAt: "2026-01-01T10:00:00Z"},
		{ID: "p2", FromAccountID: "a", ToAccountID: "c", UpdatedAt: "2026-01-01T11:00:00Z"},
		{ID: "p3", FromAccountID: "x", ToAccountID: "b", UpdatedAt: "2026-01-02T10:00:00Z"},
	}

	byDate := Reconcile(payments, "2026-01-01", "")
	if len(byDate) != 2 {
		t.Fatalf("byDate = %d, want 2", len(byDate))
	}

	byDateAndAcct := Reconcile(payments, "2026-01-01", "b")
	if len(byDateAndAcct) != 1 || byDateAndAcct[0].ID != "p1" {
		t.Fatalf("byDateAndAcct = %+v", byDateAndAcct)
	}

	byAcctOnly := Reconcile(payments, "", "b")
	if len(byAcctOnly) != 2 {
		t.Fatalf("byAcctOnly = %d, want 2", len(byAcctOnly))
	}

	empty := Reconcile(payments, "2099-01-01", "")
	if len(empty) != 0 {
		t.Fatalf("empty = %d, want 0", len(empty))
	}
}
