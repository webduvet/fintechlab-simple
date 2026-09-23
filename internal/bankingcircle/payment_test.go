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

	// The EUR safeguarding account opens at zero -- the whole point of the
	// safeguarding-account model -- so paying out of it before any
	// Worldline lump sum has landed is an insufficient-funds ledger error
	// -> MissingFunding, not a generic rejection.
	p := &Payment{
		ID:            "bcp_test2",
		FromAccountID: SGAAccountEUR,
		ToAccountID:   SGAAccountGBP,
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
	sga, _ := ledger.Get(SGAAccountEUR)
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
		FromAccountID: SGAAccountEUR,
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
		FromAccountID: SGAAccountEUR,
		ToAccountID:   SGAAccountEUR,
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
	if reversed.ReversalReason != "test hook" {
		t.Fatalf("reversal reason = %q, want the reason recorded", reversed.ReversalReason)
	}
	if reversed.Reference != "" {
		t.Fatalf("reference = %q, want it untouched by the reversal", reversed.Reference)
	}
	if reversed.ProcessedAt == "" || reversed.ReversedAt == "" {
		t.Fatalf("processedAt = %q, reversedAt = %q; want both kept", reversed.ProcessedAt, reversed.ReversedAt)
	}

	// The reversal is booked (a second OutgoingPaymentBooked, same payment
	// and reference) and then reported as Reversed.
	for _, want := range []NotificationType{NotificationOutgoingPaymentBooked, NotificationReversed} {
		select {
		case got := <-events:
			if got.State != want || got.ID != p.ID || got.ReferenceNumber != reversed.ReferenceNumber {
				t.Fatalf("notification = %s for %s (%s), want %s for %s (%s)",
					got.State, got.ID, got.ReferenceNumber, want, p.ID, reversed.ReferenceNumber)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s notification", want)
		}
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

	p, err := engine.CreditIncoming(SGAAccountEUR, "EUR", "12500.00", "worldline-lump-sum-1")
	if err != nil {
		t.Fatalf("CreditIncoming: %v", err)
	}
	if p.State != NotificationIncomingPaymentBooked {
		t.Fatalf("initial state = %s, want Booked", p.State)
	}
	if p.FromAccountID != "external_worldline" || p.ToAccountID != SGAAccountEUR {
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
	acc, err := ledger.Get(SGAAccountEUR)
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
	acc, _ = ledger.Get(SGAAccountEUR)
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
	if _, err := engine.CreditIncoming(SGAAccountEUR, "EUR", "not-a-number", "ref"); err == nil {
		t.Fatal("expected error for invalid amount")
	}
}

// TestEngineForcedOutcomes: queued outcomes are taken by the next outgoing
// payments in order — a rejection moves no money, a pending one never
// leaves Booked — and payments past the queue take the normal path.
func TestEngineForcedOutcomes(t *testing.T) {
	ledger := NewLedger()
	ledger.mustSeed("bc_acc_test_funded", "VBTESTFUNDED0000001", "Test Funded", "EUR", 50_000_000)
	ledger.mustSeed("bc_acc_test_merchant", "VBTESTMERCHANT00001", "Test Merchant", "EUR", 0)
	engine := NewEngine(ledger, time.Millisecond, nil)

	if _, err := engine.ForceNext([]Outcome{"Lost"}); err == nil {
		t.Fatal("an unknown outcome must be refused")
	}
	queued, err := engine.ForceNext([]Outcome{OutcomeRejected, OutcomePending})
	if err != nil || len(queued) != 2 {
		t.Fatalf("ForceNext = %v, %v", queued, err)
	}

	for _, id := range []string{"bcp_rejected", "bcp_pending", "bcp_normal"} {
		engine.Create(&Payment{ID: id, FromAccountID: "bc_acc_test_funded",
			ToAccountID: "bc_acc_test_merchant", Amount: "10.00", Currency: "EUR"})
	}
	if left := engine.ForcedOutcomes(); len(left) != 0 {
		t.Fatalf("queue should be drained, still has %v", left)
	}

	want := map[string]NotificationType{
		"bcp_rejected": NotificationOutgoingPaymentRejected,
		"bcp_pending":  NotificationOutgoingPaymentBooked,
		"bcp_normal":   NotificationOutgoingPaymentProcessed,
	}
	deadline := time.Now().Add(time.Second)
	for id, state := range want {
		for {
			got, _ := engine.Get(id)
			if got.State == state {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s state = %s, want %s", id, got.State, state)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	time.Sleep(20 * time.Millisecond)
	if got, _ := engine.Get("bcp_pending"); got.State != NotificationOutgoingPaymentBooked {
		t.Fatalf("a pending payment must stay booked, got %s", got.State)
	}
	if merchant, _ := ledger.Get("bc_acc_test_merchant"); merchant.Balance != "10.00" {
		t.Fatalf("only the normal payment moves money, merchant balance = %s", merchant.Balance)
	}
}

// TestEngineAssignsBankReferenceNumbers: every payment the bank accepts,
// outgoing or incoming, gets its own 010F10-prefixed reference.
func TestEngineAssignsBankReferenceNumbers(t *testing.T) {
	ledger := NewLedger()
	ledger.mustSeed("bc_acc_test_funded", "VBTESTFUNDED0000009", "Test Funded", "EUR", 50_000_000)
	ledger.mustSeed("bc_acc_test_merchant", "VBTESTMERCHANT00009", "Test Merchant", "EUR", 0)
	engine := NewEngine(ledger, time.Millisecond, nil)

	out := &Payment{ID: "bcp_ref1", FromAccountID: "bc_acc_test_funded",
		ToAccountID: "bc_acc_test_merchant", Amount: "1.00", Currency: "EUR"}
	engine.Create(out)
	in, err := engine.CreditIncoming("bc_acc_test_merchant", "EUR", "2.00", "lump")
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for _, p := range []*Payment{out, in} {
		ref := p.ReferenceNumber
		if len(ref) != 16 || ref[:6] != "010F10" {
			t.Errorf("%s: reference %q, want 16 characters under 010F10", p.ID, ref)
		}
		if seen[ref] {
			t.Errorf("%s: reference %q handed out twice", p.ID, ref)
		}
		seen[ref] = true
	}
}

// TestEngineReturnIsANewIncomingPayment: a return is not a status. The
// payout stays Processed, and the money comes back as a payment of its own
// — own id, own reference, `return` set — that is booked and then
// processed like any incoming payment. A payout comes back at most once,
// and a returned payout can no longer be reversed.
func TestEngineReturnIsANewIncomingPayment(t *testing.T) {
	ledger := NewLedger()
	ledger.mustSeed("bc_acc_test_funded", "VBTESTFUNDED0000010", "Test Funded", "EUR", 50_000_000)
	ledger.mustSeed("bc_acc_test_merchant", "VBTESTMERCHANT00010", "Test Merchant", "EUR", 0)
	events := make(chan Payment, 8)
	engine := NewEngine(ledger, time.Millisecond, func(p *Payment) { events <- *p })
	next := func(want NotificationType) Payment {
		t.Helper()
		select {
		case got := <-events:
			if got.State != want {
				t.Fatalf("notification = %s, want %s", got.State, want)
			}
			return got
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
		return Payment{}
	}

	if _, err := engine.Return("bcp_nope", "", ""); err != ErrPaymentNotFound {
		t.Fatalf("unknown payment: err = %v, want ErrPaymentNotFound", err)
	}
	payout := &Payment{ID: "bcp_ret1", FromAccountID: "bc_acc_test_funded",
		ToAccountID: "bc_acc_test_merchant", Amount: "75.00", Currency: "EUR"}
	engine.Create(payout)
	next(NotificationOutgoingPaymentBooked)
	if _, err := engine.Return(payout.ID, "", ""); err != ErrInvalidState {
		t.Fatalf("booked payout: err = %v, want ErrInvalidState", err)
	}
	next(NotificationOutgoingPaymentProcessed)

	ret, err := engine.Return(payout.ID, "AC04", "Closed account number")
	if err != nil {
		t.Fatalf("Return: %v", err)
	}
	if ret.ID == payout.ID || !ret.Return || ret.ReturnOf != payout.ID {
		t.Fatalf("return payment = %+v, want a new payment flagged as the payout's return", ret)
	}
	if ret.ReferenceNumber == "" || ret.ReferenceNumber == payout.ReferenceNumber {
		t.Errorf("return reference = %q, want its own (payout's is %q)", ret.ReferenceNumber, payout.ReferenceNumber)
	}
	if ret.FromAccountID != "bc_acc_test_merchant" || ret.ToAccountID != "bc_acc_test_funded" {
		t.Errorf("return moves %s -> %s, want merchant back to the funded account", ret.FromAccountID, ret.ToAccountID)
	}
	wantLines := []string{"RETURN OF PAYMENT", payout.ReferenceNumber, "AC04 Closed account number"}
	if strings.Join(ret.Remittance, "|") != strings.Join(wantLines, "|") {
		t.Errorf("remittance = %q, want %q", ret.Remittance, wantLines)
	}
	next(NotificationIncomingPaymentBooked)
	if got := next(NotificationIncomingPaymentProcessed); got.ID != ret.ID || !got.Return {
		t.Errorf("processed notification for %s (return %v), want the return payment", got.ID, got.Return)
	}

	original, _ := engine.Get(payout.ID)
	if original.State != NotificationOutgoingPaymentProcessed || original.ReturnedBy != ret.ID {
		t.Errorf("payout state = %s, returnedBy = %q; want still Processed, returned by %s",
			original.State, original.ReturnedBy, ret.ID)
	}
	if merchant, _ := ledger.Get("bc_acc_test_merchant"); merchant.Balance != "0.00" {
		t.Errorf("merchant balance after return = %s, want 0.00", merchant.Balance)
	}
	if _, err := engine.Return(payout.ID, "", ""); err != ErrAlreadyReturned {
		t.Errorf("second return: err = %v, want ErrAlreadyReturned", err)
	}
	if _, err := engine.Reverse(payout.ID, ""); err != ErrAlreadyReturned {
		t.Errorf("reverse after return: err = %v, want ErrAlreadyReturned", err)
	}
}
