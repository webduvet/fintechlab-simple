// Bank is a tiny in-memory ledger with obviously fake IBANs (GB00SIM...).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/webduvet/fintechlab-simple/internal/httputilx"
	"github.com/webduvet/fintechlab-simple/internal/money"
	"github.com/webduvet/fintechlab-simple/internal/runnerclock"
)

type account struct {
	ID       string `json:"id"`
	IBAN     string `json:"iban"`
	Holder   string `json:"holder"`
	Currency string `json:"currency"`
	Balance  string `json:"balance"`
	// Kind says whose money this is: "infinite" for the programme's own
	// account, "merchant" for a settled outlet, "demo" for the seeded
	// examples. The console groups by it, and a payout arriving into an
	// account nobody can name is a thing worth being able to see.
	Kind string `json:"kind"`
	// OpenedBy records how the account came to exist — "seed" or the
	// reference of the credit that auto-opened it. A beneficiary bank
	// opens an account the first time money arrives for an IBAN, and
	// which credit did that is the question asked afterwards.
	OpenedBy string `json:"opened_by,omitempty"`
	cents    int64
}

type entry struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	PaymentID string `json:"paymentId,omitempty"`
	Amount    string `json:"amount"`
	Currency  string `json:"currency"`
	Ref       string `json:"reference,omitempty"`
	At        string `json:"at"`
}

type store struct {
	mu      sync.Mutex
	accts   map[string]*account
	byIBAN  map[string]*account
	entries []entry
	seq     int
}

// newStore seeds the two accounts the settle path ends in, and nothing
// else that matters.
//
// The programme has one account per currency — that is where
// INFINITE_SETTLEMENT lands, and it is separate from every merchant's, so
// "did the platform get its share" is a balance rather than an inference.
// Merchant accounts are not seeded: a beneficiary bank does not know an
// IBAN until money arrives for it, and opening them on demand is both more
// honest and less coordination between this service and the console.
func newStore() *store {
	s := &store{accts: map[string]*account{}, byIBAN: map[string]*account{}}
	s.mustSeed("acc_infinite_eur", "GB00SIMINF00000001", "InfinitePay Ltd", "EUR", 0, kindInfinite)
	s.mustSeed("acc_infinite_gbp", "GB00SIMINF00000002", "InfinitePay Ltd", "GBP", 0, kindInfinite)

	// Two funded demo accounts, so `make demo-payment` and the payment-api
	// scenario have somewhere to move money from without a settlement run.
	s.mustSeed("acc_alice", "GB00SIM0000000000001", "Alice Simulator", "EUR", 1_000_000, kindDemo)
	s.mustSeed("acc_bob", "GB00SIM0000000000002", "Bob Simulator", "EUR", 50_000, kindDemo)
	s.mustSeed("acc_merchant", "GB00SIM0000000000003", "Merchant Simulator", "EUR", 0, kindDemo)
	return s
}

const (
	kindInfinite = "infinite"
	kindMerchant = "merchant"
	kindDemo     = "demo"
)

func (s *store) mustSeed(id, iban, holder, ccy string, cents int64, kind string) {
	a := &account{ID: id, IBAN: iban, Holder: holder, Currency: ccy, cents: cents,
		Balance: money.Format(cents), Kind: kind, OpenedBy: "seed"}
	s.accts[id] = a
	s.byIBAN[iban] = a
}

// creditReq is a payment arriving over a rail. The sending bank names the
// beneficiary by IBAN, because that is all it has — it does not know this
// bank's internal account ids, and a real one would not.
type creditReq struct {
	IBAN      string `json:"iban"`
	Holder    string `json:"holder"`
	Amount    string `json:"amount"`
	Currency  string `json:"currency"`
	Reference string `json:"reference"`
	Kind      string `json:"kind"`
}

// credit implements POST /internal/credit: money arriving from outside.
//
// It auto-opens on first sight of an IBAN. That is what a beneficiary bank
// does, and it means no coordination is needed between whoever creates a
// merchant and this service — the account exists because a payout arrived,
// which is also the only moment it is true.
func (s *store) credit(w http.ResponseWriter, r *http.Request) {
	var req creditReq
	if err := httputilx.ReadJSON(r, &req); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	if req.IBAN == "" {
		httputilx.Error(w, 400, "iban required: a rail names the beneficiary by IBAN")
		return
	}
	cents, err := money.Parse(req.Amount)
	if err != nil {
		httputilx.Error(w, 400, "amount: "+err.Error())
		return
	}
	if cents <= 0 {
		httputilx.Error(w, 400, "amount must be positive")
		return
	}
	ccy := req.Currency
	if ccy == "" {
		ccy = "EUR"
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	a, existed := s.byIBAN[req.IBAN]
	if !existed {
		kind := req.Kind
		if kind == "" {
			kind = kindMerchant
		}
		holder := req.Holder
		if holder == "" {
			holder = "Beneficiary " + req.IBAN
		}
		a = &account{
			ID: "acc_" + strings.ToLower(req.IBAN), IBAN: req.IBAN, Holder: holder,
			Currency: ccy, Kind: kind, OpenedBy: req.Reference,
		}
		s.accts[a.ID] = a
		s.byIBAN[a.IBAN] = a
	}
	if a.Currency != ccy {
		httputilx.Error(w, 422, fmt.Sprintf("account %s is %s, credit is %s", a.IBAN, a.Currency, ccy))
		return
	}
	a.cents += cents
	a.Balance = money.Format(a.cents)
	s.seq++
	s.entries = append(s.entries, entry{
		ID: fmt.Sprintf("ent_%d", s.seq), AccountID: a.ID, Amount: money.Format(cents),
		Currency: ccy, Ref: req.Reference, At: runnerclock.Now().Format(time.RFC3339),
	})
	httputilx.WriteJSON(w, 200, map[string]any{
		"account": a, "opened": !existed, "credited": money.Format(cents),
	})
}

func main() {
	addr := env("LISTEN", ":8081")
	runnerclock.FollowEnv(context.Background(), "bank")
	s := newStore()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		httputilx.WriteJSON(w, 200, map[string]string{"status": "ok", "service": "bank"})
	})
	mux.HandleFunc("GET /accounts", s.listAccounts)
	mux.HandleFunc("GET /accounts/{id}", s.getAccount)
	mux.HandleFunc("POST /accounts", s.createAccount)
	mux.HandleFunc("POST /transfers", s.transfer)
	mux.HandleFunc("GET /ledger", s.listLedger)

	// The rail hop: a payout leaving the settlement bank has to arrive
	// somewhere, and this is where. No auth — it is a lab seam, and the
	// allowlist on the sending side is what governs who may call it.
	mux.HandleFunc("POST /internal/credit", s.credit)
	log.Printf("bank listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, logReq(mux)))
}

func (s *store) listAccounts(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*account, 0, len(s.accts))
	for _, a := range s.accts {
		out = append(out, a)
	}
	httputilx.WriteJSON(w, 200, map[string]any{"accounts": out})
}

func (s *store) getAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accts[id]
	if !ok {
		if x, ok2 := s.byIBAN[id]; ok2 {
			a, ok = x, true
		}
	}
	if !ok {
		httputilx.Error(w, 404, "account not found")
		return
	}
	httputilx.WriteJSON(w, 200, a)
}

type createReq struct {
	Holder         string `json:"holder"`
	Currency       string `json:"currency"`
	OpeningBalance string `json:"openingBalance"`
}

func (s *store) createAccount(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if err := httputilx.ReadJSON(r, &req); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	if req.Holder == "" {
		httputilx.Error(w, 400, "holder required")
		return
	}
	if req.Currency == "" {
		req.Currency = "EUR"
	}
	cents := int64(0)
	if req.OpeningBalance != "" {
		var err error
		cents, err = money.Parse(req.OpeningBalance)
		if err != nil {
			httputilx.Error(w, 400, err.Error())
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	iban := fakeIBAN(4 + s.seq)
	id := "acc_" + shortID()
	a := &account{ID: id, IBAN: iban, Holder: req.Holder, Currency: req.Currency, cents: cents, Balance: money.Format(cents)}
	s.accts[id] = a
	s.byIBAN[iban] = a
	httputilx.WriteJSON(w, 201, a)
}

type transferReq struct {
	FromAccountID string `json:"fromAccountId"`
	ToAccountID   string `json:"toAccountId"`
	ToIBAN        string `json:"toIban"`
	Amount        string `json:"amount"`
	Currency      string `json:"currency"`
	Reference     string `json:"reference"`
	PaymentID     string `json:"paymentId"`
}

func (s *store) transfer(w http.ResponseWriter, r *http.Request) {
	var req transferReq
	if err := httputilx.ReadJSON(r, &req); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	cents, err := money.Parse(req.Amount)
	if err != nil || cents <= 0 {
		httputilx.Error(w, 400, "amount must be a positive decimal")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	from, ok := s.accts[req.FromAccountID]
	if !ok {
		httputilx.Error(w, 404, "debtor account not found")
		return
	}
	var to *account
	if req.ToAccountID != "" {
		to = s.accts[req.ToAccountID]
	} else if req.ToIBAN != "" {
		to = s.byIBAN[req.ToIBAN]
	}
	if to == nil {
		httputilx.Error(w, 404, "creditor account not found")
		return
	}
	if from.ID == to.ID {
		httputilx.Error(w, 400, "cannot transfer to the same account")
		return
	}
	ccy := req.Currency
	if ccy == "" {
		ccy = from.Currency
	}
	if from.Currency != ccy || to.Currency != ccy {
		httputilx.Error(w, 409, "currency mismatch (sim is single-ccy per account)")
		return
	}
	if from.cents < cents {
		httputilx.Error(w, 409, "insufficient funds")
		return
	}
	from.cents -= cents
	to.cents += cents
	from.Balance = money.Format(from.cents)
	to.Balance = money.Format(to.cents)
	now := runnerclock.Now().Format(time.RFC3339)
	debit := entry{ID: "le_" + shortID(), AccountID: from.ID, PaymentID: req.PaymentID, Amount: money.Format(-cents), Currency: ccy, Ref: req.Reference, At: now}
	credit := entry{ID: "le_" + shortID(), AccountID: to.ID, PaymentID: req.PaymentID, Amount: money.Format(cents), Currency: ccy, Ref: req.Reference, At: now}
	s.entries = append(s.entries, debit, credit)
	httputilx.WriteJSON(w, 201, map[string]any{
		"from":    from,
		"to":      to,
		"entries": []entry{debit, credit},
	})
}

func (s *store) listLedger(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	httputilx.WriteJSON(w, 200, map[string]any{"entries": s.entries})
}

func fakeIBAN(n int) string {
	return "GB00SIM" + strings.Repeat("0", 12-len(itoa(n))) + itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func shortID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func logReq(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
