package bankingcircle

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/webduvet/fintechlab-simple/internal/money"
)

// NotificationType is Banking Circle's real, closed webhook event-type enum
// (docs/ARCHITECTURE-vendor-corrections.md, section 3 — all 13 real wire
// values). This lab's Engine only ever drives the outgoing-payment subset:
// OutgoingPaymentBooked -> OutgoingPaymentProcessed | OutgoingPaymentRejected
// | MissingFunding, plus the manual Reversed test hook. The remaining
// constants exist purely for wire-shape completeness — no lifecycle in this
// lab ever fires them.
type NotificationType string

const (
	NotificationIncomingPaymentProcessed             NotificationType = "IncomingPaymentProcessed"
	NotificationIncomingPaymentBooked                NotificationType = "IncomingPaymentBooked"
	NotificationOutgoingPaymentProcessed             NotificationType = "OutgoingPaymentProcessed"
	NotificationOutgoingPaymentBooked                NotificationType = "OutgoingPaymentBooked"
	NotificationOutgoingPaymentRejected              NotificationType = "OutgoingPaymentRejected"
	NotificationMissingFunding                       NotificationType = "MissingFunding"
	NotificationReversed                             NotificationType = "Reversed"
	NotificationPaymentRouting                       NotificationType = "PaymentRouting"
	NotificationPaymentStatus                        NotificationType = "PaymentStatus"
	NotificationOutgoingDirectDebitPendingProcessing NotificationType = "OutgoingDirectDebitPendingProcessing"
	NotificationAccountHolderVerification            NotificationType = "AccountHolderVerification"
	NotificationCaseEvents                           NotificationType = "CaseEvents"
	NotificationAgencyBankingWhitelistResult         NotificationType = "AgencyBankingWhitelistResult"
)

// Payment is a single payment, outgoing or incoming, from creation through
// its terminal notification. State holds the last NotificationType the
// engine fired for it — real Banking Circle has no payment "status" field
// of its own, only the notification stream, so this lab tracks the same
// thing its webhook reports instead of inventing a parallel vocabulary.
// Both flows share this one struct and Engine deliberately (Create for
// outgoing, CreditIncoming for incoming): forking a parallel type per
// direction would duplicate the notify/onTransition plumbing for no
// benefit — real Banking Circle has no such split either.
type Payment struct {
	ID            string           `json:"id"`
	SettlementID  string           `json:"settlementId,omitempty"`
	FromAccountID string           `json:"fromAccountId"`
	ToAccountID   string           `json:"toAccountId,omitempty"`
	ToIBAN        string           `json:"toIban,omitempty"`
	Amount        string           `json:"amount"`
	Currency      string           `json:"currency"`
	Reference     string           `json:"reference,omitempty"`
	State         NotificationType `json:"state"`
	CreatedAt     string           `json:"createdAt"`
	UpdatedAt     string           `json:"updatedAt"`
}

// ErrPaymentNotFound is returned by Get and Reverse for an unknown payment ID.
var ErrPaymentNotFound = errors.New("bankingcircle: payment not found")

// ErrInvalidState is returned by Reverse when the payment is not in the
// OutgoingPaymentProcessed state.
var ErrInvalidState = errors.New("bankingcircle: payment is not in OutgoingPaymentProcessed state")

// Engine owns the payment notification lifecycle, outgoing and incoming
// alike. It is HTTP-free and fully unit-testable: onTransition (nil in
// tests) is how the caller wires in webhook delivery without the engine
// knowing anything about HTTP.
type Engine struct {
	mu           sync.Mutex
	ledger       *Ledger
	payments     map[string]*Payment
	delay        time.Duration // PROCESSING_DELAY, simulates real bank latency
	onTransition func(*Payment)
}

// NewEngine builds an Engine backed by ledger. onTransition fires (with a
// value copy, safe to read without further locking) on every notification
// the engine fires, including the initial OutgoingPaymentBooked one; pass
// nil to disable.
func NewEngine(ledger *Ledger, delay time.Duration, onTransition func(*Payment)) *Engine {
	return &Engine{
		ledger:       ledger,
		payments:     map[string]*Payment{},
		delay:        delay,
		onTransition: onTransition,
	}
}

// Create records p and immediately fires OutgoingPaymentBooked (the
// equivalent of the old lifecycle's initial Received) and, after `delay`,
// attempts the ledger move in a goroutine — the caller is never blocked
// waiting on ledger settlement. Exactly one of three notifications follows:
// OutgoingPaymentProcessed (ledger move succeeded), MissingFunding (the move
// failed specifically due to insufficient funds on FromAccountID), or
// OutgoingPaymentRejected (the move failed for any other reason, e.g.
// unknown account or same-account).
func (e *Engine) Create(p *Payment) {
	now := time.Now().UTC().Format(time.RFC3339)
	e.mu.Lock()
	p.State = NotificationOutgoingPaymentBooked
	p.CreatedAt = now
	p.UpdatedAt = now
	e.payments[p.ID] = p
	cp := *p
	e.mu.Unlock()
	e.notify(cp)
	go e.process(p)
}

func (e *Engine) process(p *Payment) {
	if e.delay > 0 {
		time.Sleep(e.delay)
	}
	cents, err := money.Parse(p.Amount)
	if err == nil {
		_, _, err = e.ledger.Move(p.FromAccountID, p.ToAccountID, cents)
	}

	e.mu.Lock()
	switch {
	case err == nil:
		p.State = NotificationOutgoingPaymentProcessed
	case errors.Is(err, ErrInsufficientFunds):
		p.State = NotificationMissingFunding
	default:
		p.State = NotificationOutgoingPaymentRejected
	}
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	cp := *p
	e.mu.Unlock()
	e.notify(cp)
}

// Get returns a copy of the payment by ID.
func (e *Engine) Get(id string) (*Payment, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.payments[id]
	if !ok {
		return nil, ErrPaymentNotFound
	}
	cp := *p
	return &cp, nil
}

// List returns a copy of every payment.
func (e *Engine) List() []*Payment {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Payment, 0, len(e.payments))
	for _, p := range e.payments {
		cp := *p
		out = append(out, &cp)
	}
	return out
}

// Reverse manually transitions an OutgoingPaymentProcessed payment to
// Reversed, reversing the ledger credit (toAccount -> fromAccount) and
// firing the Reversed notification. Lab-only test hook, reachable via
// POST /internal/payments/{id}/reverse — real reversals are bank-driven and
// asynchronous; this lets the harness exercise that path deterministically,
// the same purpose the old Return() served. reason is recorded on the
// payment's Reference for audit, when non-empty.
func (e *Engine) Reverse(id, reason string) (*Payment, error) {
	e.mu.Lock()
	p, ok := e.payments[id]
	if !ok {
		e.mu.Unlock()
		return nil, ErrPaymentNotFound
	}
	if p.State != NotificationOutgoingPaymentProcessed {
		e.mu.Unlock()
		return nil, ErrInvalidState
	}
	cents, err := money.Parse(p.Amount)
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}
	if _, _, err := e.ledger.Move(p.ToAccountID, p.FromAccountID, cents); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	p.State = NotificationReversed
	if reason != "" {
		if p.Reference != "" {
			p.Reference += " | reversed: " + reason
		} else {
			p.Reference = "reversed: " + reason
		}
	}
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	cp := *p
	e.mu.Unlock()
	e.notify(cp)
	return &cp, nil
}

func (e *Engine) notify(p Payment) {
	if e.onTransition != nil {
		e.onTransition(&p)
	}
}

// CreditIncoming simulates Worldline's lump sum landing in the
// safeguarding account (docs/ARCHITECTURE-phase3-corrections.md section
// 1): parses amount, credits the ledger, and immediately fires
// IncomingPaymentBooked, then — after the same `delay` the outgoing engine
// uses, in a goroutine, so the caller is never blocked — flips to
// IncomingPaymentProcessed and fires again. Both notifications go through
// the same notify/onTransition plumbing Create uses; there is no separate
// callback. Unlike Create, the ledger credit happens synchronously before
// the initial notification: Credit's only failure mode is an unknown
// toAccountID, which callers must resolve (or auto-vivify) before calling
// this, so there is no MissingFunding-equivalent second outcome to report.
func (e *Engine) CreditIncoming(toAccountID, currency, amount, reference string) (*Payment, error) {
	cents, err := money.Parse(amount)
	if err != nil {
		return nil, err
	}
	if _, err := e.ledger.Credit(toAccountID, cents); err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	p := &Payment{
		ID:            "bcp_" + randomHex(6),
		FromAccountID: "external_worldline",
		ToAccountID:   toAccountID,
		Amount:        amount,
		Currency:      currency,
		Reference:     reference,
		State:         NotificationIncomingPaymentBooked,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	e.mu.Lock()
	e.payments[p.ID] = p
	cp := *p
	e.mu.Unlock()
	e.notify(cp)

	go func() {
		if e.delay > 0 {
			time.Sleep(e.delay)
		}
		e.mu.Lock()
		p.State = NotificationIncomingPaymentProcessed
		p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		cp := *p
		e.mu.Unlock()
		e.notify(cp)
	}()

	return &cp, nil
}

// randomHex generates a random hex id, local to this package — same tiny
// per-binary/per-package helper shape as cmd/bankingcircle's shortID, not
// shared across packages by convention in this repo.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
