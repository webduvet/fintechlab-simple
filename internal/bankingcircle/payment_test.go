package bankingcircle

import (
	"strings"
	"testing"
	"time"
)

func TestEngineCreateHappyPath(t *testing.T) {
	ledger := NewLedger()
	ledger.mustSeed("bc_acc_test_funded", "VBTESTFUNDED0000001", "Test Funded", "EUR", 50_000_000)
	ledger.mustSeed("bc_acc_test_merchant", "VBTESTMERCHANT00001", "Test Merchant", "EUR", 0)
	events := make(chan Payment, 8)
	engine := NewEngine(ledger, 10*time.Millisecond, func(p *Payment) { events <- *p })

	p := &Payment{
		ID:            "bcp_test1",
		FromAccountID: "bc_acc_test_funded",
		ToAccountID:   "bc_acc_test_merchant",
		Amount:        "100.00",
		Currency:      "EUR",
	}
	engine.Create(p)

	for _, want := range []NotificationType{NotificationOutgoingPaymentBooked, NotificationOutgoingPaymentProcessed} {
		select {
		case got := <-events:
			if got.State != want {
				t.Fatalf("notification = %s, want %s", got.State, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}

	got, err := engine.Get(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != NotificationOutgoingPaymentProcessed {
		t.Fatalf("final state = %s", got.State)
	}
	merchant, _ := ledger.Get("bc_acc_test_merchant")
	if merchant.Balance != "100.00" {
		t.Fatalf("merchant balance = %s", merchant.Balance)
	}
}

func TestEngineCreateMissingFunding(t *testing.T) {
	ledger := NewLedger()
	events := make(chan Payment, 8)
	engine := NewEngine(ledger, time.Millisecond, func(p *Payment) { events <- *p })

	// bc_acc_sga_eur opens at zero -- the whole point of the
	// safeguarding-account model -- so paying out of it before any
	// Worldline lump sum has landed is an insufficient-funds ledger error
	// -> MissingFunding, not a generic rejection.
	p := &Payment{
		ID:            "bcp_test2",
		FromAccountID: "bc_acc_sga_eur",
		ToAccountID:   "bc_acc_sga_gbp",
		Amount:        "50.00",
		Currency:      "EUR",
	}
	engine.Create(p)

	for _, want := range []NotificationType{NotificationOutgoingPaymentBooked, NotificationMissingFunding} {
		select {
		case got := <-events:
			if got.State != want {
				t.Fatalf("notification = %s, want %s", got.State, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}

	got, err := engine.Get(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != NotificationMissingFunding {
		t.Fatalf("final state = %s", got.State)
	}
	sga, _ := ledger.Get("bc_acc_sga_eur")
	if sga.Balance != "0.00" {
		t.Fatalf("balance mutated on missing-funding payment: %s", sga.Balance)
	}
}

func TestEngineCreateRejectedOnUnknownAccount(t *testing.T) {
	ledger := NewLedger()
	events := make(chan Payment, 8)
	engine := NewEngine(ledger, time.Millisecond, func(p *Payment) { events <- *p })

	p := &Payment{
		ID:            "bcp_test3",
		FromAccountID: "bc_acc_sga_eur",
		ToAccountID:   "bc_acc_does_not_exist",
		Amount:        "10.00",
		Currency:      "EUR",
	}
	engine.Create(p)

	for _, want := range []NotificationType{NotificationOutgoingPaymentBooked, NotificationOutgoingPaymentRejected} {
		select {
		case got := <-events:
			if got.State != want {
				t.Fatalf("notification = %s, want %s", got.State, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}

	got, err := engine.Get(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != NotificationOutgoingPaymentRejected {
		t.Fatalf("final state = %s", got.State)
	}
}

func TestEngineCreateRejectedOnSameAccount(t *testing.T) {
	ledger := NewLedger()
	events := make(chan Payment, 8)
	engine := NewEngine(ledger, time.Millisecond, func(p *Payment) { events <- *p })

	p := &Payment{
		ID:            "bcp_test4",
		FromAccountID: "bc_acc_sga_eur",
		ToAccountID:   "bc_acc_sga_eur",
		Amount:        "10.00",
		Currency:      "EUR",
	}
	engine.Create(p)

	for _, want := range []NotificationType{NotificationOutgoingPaymentBooked, NotificationOutgoingPaymentRejected} {
		select {
		case got := <-events:
			if got.State != want {
				t.Fatalf("notification = %s, want %s", got.State, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}
}

func TestEngineReverseOnlyFromProcessed(t *testing.T) {
	ledger := NewLedger()
	ledger.mustSeed("bc_acc_test_funded", "VBTESTFUNDED0000002", "Test Funded", "EUR", 50_000_000)
	ledger.mustSeed("bc_acc_test_merchant", "VBTESTMERCHANT00002", "Test Merchant", "EUR", 0)
	events := make(chan Payment, 8)
	engine := NewEngine(ledger, time.Millisecond, func(p *Payment) { events <- *p })

	p := &Payment{
		ID:            "bcp_test5",
		FromAccountID: "bc_acc_test_funded",
		ToAccountID:   "bc_acc_test_merchant",
		Amount:        "75.00",
		Currency:      "EUR",
	}

	// Reverse before Create even runs must fail: unknown payment ID.
	if _, err := engine.Reverse(p.ID, ""); err != ErrPaymentNotFound {
		t.Fatalf("err = %v, want ErrPaymentNotFound", err)
	}

	engine.Create(p)
	// Drain the Booked notification; the payment is not yet Processed, so
	// reversing now must fail with ErrInvalidState.
	<-events
	if _, err := engine.Reverse(p.ID, ""); err != ErrInvalidState {
		t.Fatalf("err = %v, want ErrInvalidState", err)
	}

	// Wait for settlement to land on Processed.
	select {
	case got := <-events:
		if got.State != NotificationOutgoingPaymentProcessed {
			t.Fatalf("state = %s, want Processed", got.State)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Processed")
	}

	reversed, err := engine.Reverse(p.ID, "test hook")
	if err != nil {
		t.Fatalf("Reverse: %v", err)
	}
	if reversed.State != NotificationReversed {
		t.Fatalf("state after Reverse = %s", reversed.State)
	}
	if !strings.Contains(reversed.Reference, "test hook") {
		t.Fatalf("reference = %q, want reason recorded", reversed.Reference)
	}

	select {
	case got := <-events:
		if got.State != NotificationReversed {
			t.Fatalf("notification = %s, want Reversed", got.State)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Reversed notification")
	}

	merchant, _ := ledger.Get("bc_acc_test_merchant")
	if merchant.Balance != "0.00" {
		t.Fatalf("merchant balance after reversal = %s, want 0.00", merchant.Balance)
	}

	// Reversing twice must fail: no longer Processed.
	if _, err := engine.Reverse(p.ID, ""); err != ErrInvalidState {
		t.Fatalf("second Reverse err = %v, want ErrInvalidState", err)
	}
}

func TestEngineGetUnknownPayment(t *testing.T) {
	engine := NewEngine(NewLedger(), time.Millisecond, nil)
	if _, err := engine.Get("nope"); err != ErrPaymentNotFound {
		t.Fatalf("err = %v, want ErrPaymentNotFound", err)
	}
}

func TestEngineCreditIncomingBookedThenProcessed(t *testing.T) {
	ledger := NewLedger()
	events := make(chan Payment, 8)
	engine := NewEngine(ledger, 10*time.Millisecond, func(p *Payment) { events <- *p })

	p, err := engine.CreditIncoming("bc_acc_sga_eur", "EUR", "12500.00", "worldline-lump-sum-1")
	if err != nil {
		t.Fatalf("CreditIncoming: %v", err)
	}
	if p.State != NotificationIncomingPaymentBooked {
		t.Fatalf("initial state = %s, want Booked", p.State)
	}
	if p.FromAccountID != "external_worldline" || p.ToAccountID != "bc_acc_sga_eur" {
		t.Fatalf("payment parties = %+v", p)
	}
	if p.Reference != "worldline-lump-sum-1" {
		t.Fatalf("reference = %q", p.Reference)
	}
	if !strings.HasPrefix(p.ID, "bcp_") {
		t.Fatalf("id = %s, want bcp_ prefix", p.ID)
	}

	// the ledger credit is synchronous -- it has already happened by the
	// time the Booked payment is returned.
	acc, err := ledger.Get("bc_acc_sga_eur")
	if err != nil {
		t.Fatal(err)
	}
	if acc.Balance != "12500.00" {
		t.Fatalf("sga balance = %s, want 12500.00", acc.Balance)
	}

	for _, want := range []NotificationType{NotificationIncomingPaymentBooked, NotificationIncomingPaymentProcessed} {
		select {
		case got := <-events:
			if got.State != want {
				t.Fatalf("notification = %s, want %s", got.State, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}

	got, err := engine.Get(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != NotificationIncomingPaymentProcessed {
		t.Fatalf("final state = %s", got.State)
	}
	// the Processed transition is notification-only -- the balance does not
	// move again.
	acc, _ = ledger.Get("bc_acc_sga_eur")
	if acc.Balance != "12500.00" {
		t.Fatalf("sga balance after Processed = %s, want unchanged 12500.00", acc.Balance)
	}
}

func TestEngineCreditIncomingUnknownAccount(t *testing.T) {
	engine := NewEngine(NewLedger(), time.Millisecond, nil)
	if _, err := engine.CreditIncoming("bc_acc_nope", "EUR", "10.00", "ref"); err != ErrAccountNotFound {
		t.Fatalf("err = %v, want ErrAccountNotFound", err)
	}
}

func TestEngineCreditIncomingInvalidAmount(t *testing.T) {
	engine := NewEngine(NewLedger(), time.Millisecond, nil)
	if _, err := engine.CreditIncoming("bc_acc_sga_eur", "EUR", "not-a-number", "ref"); err == nil {
		t.Fatal("expected error for invalid amount")
	}
}
