// Package bankingcircle models Banking Circle as a settlement bank: an
// account ledger with virtual IBANs (VIBANs), a payout lifecycle state
// machine, and a reconciliation query. See
// docs/ARCHITECTURE-banking-circle.md for the reference model this is
// grounded in and what is deliberately out of scope, and
// docs/ARCHITECTURE-phase3-corrections.md section 1 for the
// safeguarding-account (SGA) correction this file implements.
package bankingcircle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"

	"github.com/webduvet/fintechlab-simple/internal/money"
)

// Account is a Banking Circle client sub-account, identified by a virtual
// IBAN (VIBAN) — a distinct identifier space from the fake bank's IBANs, so
// the two ledgers are never mistaken for one another.
type Account struct {
	ID       string `json:"id"`
	VIBAN    string `json:"viban"`
	Holder   string `json:"holder"`
	Currency string `json:"currency"`
	Balance  string `json:"balance"` // decimal string, see internal/money
	cents    int64
}

// Ledger is a tiny in-memory account store, same shape as cmd/bank's store.
type Ledger struct {
	mu      sync.Mutex
	accts   map[string]*Account
	byVIBAN map[string]*Account
}

var (
	ErrAccountNotFound   = errors.New("bankingcircle: account not found")
	ErrInsufficientFunds = errors.New("bankingcircle: insufficient funds")
	ErrSameAccount       = errors.New("bankingcircle: cannot move to the same account")
)

// NewLedger seeds the two safeguarding accounts (SGA) this lab needs, one
// per currency, both starting at zero. Real Banking Circle's SGA starts at
// whatever Worldline has actually transferred — buddy runs an hourly
// balance check against it precisely because it can be insufficient
// (docs/ARCHITECTURE-phase3-corrections.md section 1). Pre-funding it, as
// this ledger's predecessor did, would skip the "did the money actually
// land" step the real flow starts with. Credited only via Credit, driven by
// the Engine.CreditIncoming "Worldline lump sum landed" simulation.
func NewLedger() *Ledger {
	l := &Ledger{accts: map[string]*Account{}, byVIBAN: map[string]*Account{}}
	l.mustSeed("bc_acc_sga_eur", "BE00SIMSGA00000001", "Infinite Safeguarding Account EUR", "EUR", 0)
	l.mustSeed("bc_acc_sga_gbp", "GB00SIMSGA00000001", "Infinite Safeguarding Account GBP", "GBP", 0)
	return l
}

func (l *Ledger) mustSeed(id, viban, holder, ccy string, cents int64) {
	a := &Account{ID: id, VIBAN: viban, Holder: holder, Currency: ccy, cents: cents, Balance: money.Format(cents)}
	l.accts[id] = a
	l.byVIBAN[viban] = a
}

// Get looks an account up by ID or VIBAN.
func (l *Ledger) Get(id string) (*Account, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.accts[id]
	if !ok {
		a, ok = l.byVIBAN[id]
	}
	if !ok {
		return nil, ErrAccountNotFound
	}
	cp := *a
	return &cp, nil
}

// GetOrCreate returns the account with the given id (looked up by ID only,
// not VIBAN), auto-vivifying a deterministic synthetic one on first read if
// it doesn't exist yet — same "seed on first read" philosophy as
// internal/b4b's beneficiary auto-vivification (LookupBeneficiary),
// hash-derived (not random) so the same id always yields the same account.
// This never fails: any string id is a valid creditor account id in this
// model, since B4B (docs/ARCHITECTURE-phase3-corrections.md section 1)
// derives a distinct account per beneficiary rather than sending a single
// known one.
func (l *Ledger) GetOrCreate(id, currency string) *Account {
	l.mu.Lock()
	defer l.mu.Unlock()
	if a, ok := l.accts[id]; ok {
		cp := *a
		return &cp
	}
	sum := sha256.Sum256([]byte(id))
	seed := strings.ToUpper(hex.EncodeToString(sum[:]))
	a := &Account{
		ID:       id,
		VIBAN:    "XX00SIMGEN" + seed[:16],
		Holder:   "Auto-Vivified Creditor " + id,
		Currency: currency,
		cents:    0,
		Balance:  money.Format(0),
	}
	l.accts[id] = a
	l.byVIBAN[a.VIBAN] = a
	cp := *a
	return &cp
}

// List returns every account.
func (l *Ledger) List() []*Account {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]*Account, 0, len(l.accts))
	for _, a := range l.accts {
		cp := *a
		out = append(out, &cp)
	}
	return out
}

// Move debits fromID and credits toID by cents. Both must already exist as
// account IDs (not VIBANs — callers resolve VIBANs to IDs via Get first).
// Insufficient funds and same-account moves are typed errors the HTTP layer
// turns into 409/400, exactly like bank's /transfers.
func (l *Ledger) Move(fromID, toID string, cents int64) (from, to *Account, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if fromID == toID {
		return nil, nil, ErrSameAccount
	}
	f, ok := l.accts[fromID]
	if !ok {
		return nil, nil, ErrAccountNotFound
	}
	t, ok := l.accts[toID]
	if !ok {
		return nil, nil, ErrAccountNotFound
	}
	if f.cents < cents {
		return nil, nil, ErrInsufficientFunds
	}
	f.cents -= cents
	t.cents += cents
	f.Balance = money.Format(f.cents)
	t.Balance = money.Format(t.cents)
	fcp, tcp := *f, *t
	return &fcp, &tcp, nil
}

// Credit adds cents to toID's balance. Incoming payments (the SGA lump-sum
// landing) have no ledger "from" side in this model — the money arrives
// from outside any account this lab tracks — so unlike Move there is no
// debtor to check or debit.
func (l *Ledger) Credit(toID string, cents int64) (*Account, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.accts[toID]
	if !ok {
		return nil, ErrAccountNotFound
	}
	t.cents += cents
	t.Balance = money.Format(t.cents)
	cp := *t
	return &cp, nil
}
