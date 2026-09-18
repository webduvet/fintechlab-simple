package bankingcircle

import "testing"

func TestLedgerMoveSuccess(t *testing.T) {
	l := NewLedger()
	l.mustSeed("acc_from", "VBTEST0000000001", "From", "EUR", 50_000_000)
	l.mustSeed("acc_to", "VBTEST0000000002", "To", "EUR", 0)

	from, to, err := l.Move("acc_from", "acc_to", 12345)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if from.Balance != "499876.55" {
		t.Fatalf("from balance = %s", from.Balance)
	}
	if to.Balance != "123.45" {
		t.Fatalf("to balance = %s", to.Balance)
	}
	// confirm the ledger's own copy was updated, not just the returned copy
	got, err := l.Get("acc_to")
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance != "123.45" {
		t.Fatalf("stored balance = %s", got.Balance)
	}
}

func TestLedgerMoveInsufficientFunds(t *testing.T) {
	l := NewLedger()
	// bc_acc_sga_eur opens at zero -- the whole point of the
	// safeguarding-account model -- so paying out of it is always an
	// insufficient-funds error until it has been credited.
	_, _, err := l.Move("bc_acc_sga_eur", "bc_acc_sga_gbp", 100)
	if err != ErrInsufficientFunds {
		t.Fatalf("err = %v, want ErrInsufficientFunds", err)
	}
	// balances must be unchanged on a rejected move
	acc, _ := l.Get("bc_acc_sga_eur")
	if acc.Balance != "0.00" {
		t.Fatalf("balance mutated on failed move: %s", acc.Balance)
	}
}

func TestLedgerMoveSameAccountRejected(t *testing.T) {
	l := NewLedger()
	_, _, err := l.Move("bc_acc_sga_eur", "bc_acc_sga_eur", 100)
	if err != ErrSameAccount {
		t.Fatalf("err = %v, want ErrSameAccount", err)
	}
}

func TestLedgerMoveUnknownAccount(t *testing.T) {
	l := NewLedger()
	_, _, err := l.Move("bc_acc_sga_eur", "bc_acc_nope", 100)
	if err != ErrAccountNotFound {
		t.Fatalf("err = %v, want ErrAccountNotFound", err)
	}
}

func TestLedgerGetByVIBAN(t *testing.T) {
	l := NewLedger()
	acc, err := l.Get("BE00SIMSGA00000001")
	if err != nil {
		t.Fatal(err)
	}
	if acc.ID != "bc_acc_sga_eur" {
		t.Fatalf("id = %s", acc.ID)
	}
}

func TestLedgerSeedShape(t *testing.T) {
	l := NewLedger()
	eur, err := l.Get("bc_acc_sga_eur")
	if err != nil {
		t.Fatal(err)
	}
	if eur.VIBAN != "BE00SIMSGA00000001" || eur.Holder != "Infinite Safeguarding Account EUR" ||
		eur.Currency != "EUR" || eur.Balance != "0.00" {
		t.Fatalf("EUR SGA seed = %+v", eur)
	}
	gbp, err := l.Get("bc_acc_sga_gbp")
	if err != nil {
		t.Fatal(err)
	}
	if gbp.VIBAN != "GB00SIMSGA00000001" || gbp.Holder != "Infinite Safeguarding Account GBP" ||
		gbp.Currency != "GBP" || gbp.Balance != "0.00" {
		t.Fatalf("GBP SGA seed = %+v", gbp)
	}
	if len(l.List()) != 2 {
		t.Fatalf("seeded account count = %d, want 2", len(l.List()))
	}
}

func TestLedgerCreditSuccess(t *testing.T) {
	l := NewLedger()
	acc, err := l.Credit("bc_acc_sga_eur", 1_250_000)
	if err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if acc.Balance != "12500.00" {
		t.Fatalf("returned balance = %s", acc.Balance)
	}
	// confirm the ledger's own copy was updated, not just the returned copy
	got, err := l.Get("bc_acc_sga_eur")
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance != "12500.00" {
		t.Fatalf("stored balance = %s", got.Balance)
	}

	// a second credit accumulates rather than replacing
	acc, err = l.Credit("bc_acc_sga_eur", 100)
	if err != nil {
		t.Fatalf("second Credit: %v", err)
	}
	if acc.Balance != "12501.00" {
		t.Fatalf("accumulated balance = %s", acc.Balance)
	}
}

func TestLedgerCreditUnknownAccount(t *testing.T) {
	l := NewLedger()
	_, err := l.Credit("bc_acc_nope", 100)
	if err != ErrAccountNotFound {
		t.Fatalf("err = %v, want ErrAccountNotFound", err)
	}
}

func TestLedgerGetOrCreateVivifiesDeterministically(t *testing.T) {
	l := NewLedger()

	if _, err := l.Get("bc_acc_new_merchant"); err != ErrAccountNotFound {
		t.Fatalf("account should not exist yet: err = %v", err)
	}

	got := l.GetOrCreate("bc_acc_new_merchant", "EUR")
	if got.ID != "bc_acc_new_merchant" || got.Currency != "EUR" || got.Balance != "0.00" {
		t.Fatalf("vivified account = %+v", got)
	}
	if got.VIBAN == "" {
		t.Fatalf("vivified account missing a VIBAN")
	}

	// same id found on a second call -- not re-vivified, currency argument
	// ignored once the account already exists.
	again := l.GetOrCreate("bc_acc_new_merchant", "GBP")
	if again.VIBAN != got.VIBAN || again.Currency != "EUR" {
		t.Fatalf("GetOrCreate re-vivified an existing account: %+v", again)
	}

	// deterministic: a fresh ledger vivifying the same id gets the same VIBAN
	other := NewLedger().GetOrCreate("bc_acc_new_merchant", "EUR")
	if other.VIBAN != got.VIBAN {
		t.Fatalf("VIBAN not deterministic: %s vs %s", other.VIBAN, got.VIBAN)
	}

	// findable afterward, by ID and by its vivified VIBAN
	found, err := l.Get("bc_acc_new_merchant")
	if err != nil || found.VIBAN != got.VIBAN {
		t.Fatalf("Get by id after GetOrCreate = %+v, %v", found, err)
	}
	byVIBAN, err := l.Get(got.VIBAN)
	if err != nil || byVIBAN.ID != "bc_acc_new_merchant" {
		t.Fatalf("Get by VIBAN after GetOrCreate = %+v, %v", byVIBAN, err)
	}
}
