// Bank is a tiny in-memory ledger with obviously fake IBANs (GB00SIM...).
package main

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/webduvet/fintechlab-simple/internal/httputilx"
	"github.com/webduvet/fintechlab-simple/internal/money"
)

type account struct {
	ID       string `json:"id"`
	IBAN     string `json:"iban"`
	Holder   string `json:"holder"`
	Currency string `json:"currency"`
	Balance  string `json:"balance"`
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

func newStore() *store {
	s := &store{accts: map[string]*account{}, byIBAN: map[string]*account{}}
	s.mustSeed("acc_alice", "GB00SIM0000000000001", "Alice Simulator", "EUR", 1_000_000)
	s.mustSeed("acc_bob", "GB00SIM0000000000002", "Bob Simulator", "EUR", 50_000)
	s.mustSeed("acc_merchant", "GB00SIM0000000000003", "Merchant Simulator", "EUR", 0)
	return s
}

func (s *store) mustSeed(id, iban, holder, ccy string, cents int64) {
	a := &account{ID: id, IBAN: iban, Holder: holder, Currency: ccy, cents: cents, Balance: money.Format(cents)}
	s.accts[id] = a
	s.byIBAN[iban] = a
}

func main() {
	addr := env("LISTEN", ":8081")
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
	now := time.Now().UTC().Format(time.RFC3339)
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
