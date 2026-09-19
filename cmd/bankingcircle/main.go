// Banking Circle models the real Banking Circle contract as buddy consumes
// it: auth (mTLS + OAuth-shaped bearer), a balance read, webhook
// subscription CRUD, and the outgoing-payment notification lifecycle it
// drives once told a payment exists by the B4B mock. See
// docs/ARCHITECTURE-vendor-corrections.md section 3 and its Addendum
// (sections B/C), which supersede docs/ARCHITECTURE-banking-circle.md's
// lifecycle/auth/HTTP-surface model — that doc's ledger/VIBAN seed shape is
// the only part of it still in effect.
//
// Two listeners run in one process (Addendum section C): mTLS applies to an
// entire http.Server, not per-route, so the real-shaped, credentialed API
// and the lab-only B4B bridge cannot share a listener.
//   - LISTEN (default :8085, mTLS + bearer): every real-shaped endpoint,
//     plus a few read-only debug endpoints (accounts/payments/reconciliation
//     listings) this lab keeps for harness introspection — not part of the
//     real contract, but still behind the same credentials as everything
//     else on this listener.
//   - INTERNAL_LISTEN (default :8095, plain HTTP, no auth): the B4B bridge
//     (POST /internal/payments), the manual reversal test hook (POST
//     /internal/payments/{id}/reverse), the "Worldline lump sum landed"
//     trigger (POST /internal/incoming-payments), and a no-auth balance
//     proxy (GET /internal/accounts/{accountId}/balances) — lab-only,
//     same-network trust.
package main

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/webduvet/fintechlab-simple/internal/activity"
	"github.com/webduvet/fintechlab-simple/internal/allowlist"
	"github.com/webduvet/fintechlab-simple/internal/bankingcircle"
	"github.com/webduvet/fintechlab-simple/internal/httputilx"
	"github.com/webduvet/fintechlab-simple/internal/money"
	"github.com/webduvet/fintechlab-simple/internal/waitfor"
)

type app struct {
	ledger   *bankingcircle.Ledger
	engine   *bankingcircle.Engine
	client   *http.Client
	list     *allowlist.List
	notifKey []byte
	tokens   *tokenStore
	subs     *bankingcircle.SubscriptionStore
	dispatch *bankingcircle.Dispatcher
	mail     *bankingcircle.MailBox
	tokenTTL time.Duration
	// bankCreditURL is the beneficiary bank's rail endpoint. Empty
	// disables the hop entirely, so a stack without one is unchanged.
	bankCreditURL string
	// delivery is the *effective* config after BC_TIME_SCALE is applied,
	// kept so it can be reported. A reader of the config file alone sees
	// the file's scale, not the one this process is running.
	delivery bankingcircle.DeliveryConfig
	// The two halves of this vendor's conversation, kept apart because
	// they answer different questions. "Did the payout reach the bank?"
	// is the payments log; "did the bank tell us it had?" is the
	// notifications log, and a run where the first is full and the second
	// empty is a specific, common and otherwise invisible failure.
	payLog   *activity.Log
	notifLog *activity.Log
}

func main() {
	addr := env("LISTEN", ":8085")
	internalAddr := env("INTERNAL_LISTEN", ":8095")
	delay := envDuration("PROCESSING_DELAY", 2*time.Second)
	dest := env("WEBHOOK_URL", "https://receiver:8443/webhooks")
	spec := env("WEBHOOK_ALLOWLIST", "receiver,receiver:8443,localhost,127.0.0.1")
	bankCreditURL := strings.TrimSpace(env("BANK_CREDIT_URL", ""))
	list, err := allowlist.Parse(spec)
	if err != nil {
		log.Fatalf("allowlist: %v", err)
	}
	if err := list.Allowed(dest); err != nil {
		log.Fatalf("default WEBHOOK_URL rejected by allowlist: %v", err)
	}
	// Delivery behaviour -- the retry schedule, batch sizing and the time
	// scale that makes a two-day schedule watchable -- comes from a config
	// file, because the interesting part of it is a table. A missing file
	// means the real, unscaled schedule.
	deliveryCfg, err := bankingcircle.LoadDeliveryConfig(env("BC_DELIVERY_CONFIG", "/config/banking-circle.json"))
	if err != nil {
		log.Printf("banking-circle: %v; falling back to the documented default schedule", err)
	}
	// The one knob worth having outside the file: the real schedule takes
	// two days to reach deactivation, and nobody is going to wait. The
	// file keeps the real table so it stays readable as documentation;
	// this divides every delay in it. The step count and the shape --
	// which is what a client is actually being tested against -- do not
	// change.
	if v := env("BC_TIME_SCALE", ""); v != "" {
		scale, perr := strconv.ParseFloat(v, 64)
		if perr != nil || scale <= 0 {
			log.Printf("banking-circle: BC_TIME_SCALE=%q is not a positive number, keeping %g", v, deliveryCfg.TimeScale)
		} else {
			deliveryCfg.TimeScale = scale
		}
	}
	// Real Banking Circle encrypts webhook notifications with the raw UTF-8
	// bytes of a 32-character key — NOT base64-decoded, confirmed against
	// buddy's own decrypt code even though this looks like a bug. This is a
	// fresh env var (HMAC_SECRET no longer applies: this isn't an HMAC
	// scheme). Default is a fake-obvious 32-char dev key, never a real one.
	notifKey := []byte(env("BC_NOTIFICATION_KEY", "sim-bc-notification-key-32-chars"))
	if len(notifKey) != 32 {
		log.Fatalf("BC_NOTIFICATION_KEY must be exactly 32 characters (raw UTF-8 bytes = AES-256 key), got %d", len(notifKey))
	}
	caFile := env("CA_FILE", "/certs/ca.pem")
	certFile := env("TLS_CERT", "/certs/banking-circle.pem")
	keyFile := env("TLS_KEY", "/certs/banking-circle-key.pem")
	// receiver's and our own certs are signed by this CA; the ca service and
	// this one start concurrently under compose, so wait rather than trust
	// orchestrator ordering (podman-compose does not block on it — see
	// docs/security/ca-and-tls.md).
	if err := waitfor.Files(30*time.Second, caFile, certFile, keyFile); err != nil {
		log.Printf("banking-circle: %v; continuing, mTLS listener and HTTPS to receiver will fail until certs exist", err)
	}
	client, err := httpClient(caFile)
	if err != nil {
		log.Fatalf("tls client: %v", err)
	}

	a := &app{
		ledger:   bankingcircle.NewLedger(),
		client:   client,
		list:     list,
		notifKey: notifKey,
		tokens:   newTokenStore(),
		subs:     bankingcircle.NewSubscriptionStore(func(prefix string) string { return prefix + "_" + shortID() }),
		mail:     &bankingcircle.MailBox{},
		tokenTTL: envDuration("TOKEN_TTL", time.Hour),
		delivery: deliveryCfg,

		bankCreditURL: bankCreditURL,
		payLog: activity.New("payments", "Payments received",
			"What arrived on the lab bridge: payouts from B4B, and money landing on the safeguarding accounts."),
		notifLog: activity.New("notifications", "Notifications sent",
			"Each encrypted batch this vendor posted to a subscription's endpoint, and what that endpoint answered."),
	}
	a.dispatch = bankingcircle.NewDispatcher(deliveryCfg, a.subs, a.mail, log.Printf)
	a.dispatch.Send = a.sendEncrypted
	a.engine = bankingcircle.NewEngine(a.ledger, delay, a.onTransition)

	// Seed one default subscription, subscribed to every event type this
	// lab's engine can fire, so a demo works without an explicit subscribe
	// call first. A real client creates its own and picks its own events;
	// this is a convenience, and every field on it is one the real API
	// would have required.
	seeded, err := a.subs.Create(bankingcircle.CreateParams{
		Endpoint:                   dest,
		EncryptionKey:              string(notifKey),
		Status:                     bankingcircle.StatusActive,
		Email:                      env("BC_SEED_SUBSCRIPTION_EMAIL", "alerts@fintechlab-simple.local"),
		MaxNotificationsPerMessage: envInt("BC_SEED_BATCH_SIZE", bankingcircle.MinNotificationsPerMessage),
	})
	if err != nil {
		log.Fatalf("seed default subscription: %v", err)
	}
	for _, et := range []bankingcircle.NotificationType{
		bankingcircle.NotificationOutgoingPaymentBooked,
		bankingcircle.NotificationOutgoingPaymentProcessed,
		bankingcircle.NotificationOutgoingPaymentRejected,
		bankingcircle.NotificationMissingFunding,
		bankingcircle.NotificationReversed,
		bankingcircle.NotificationIncomingPaymentProcessed,
		bankingcircle.NotificationIncomingPaymentBooked,
		bankingcircle.NotificationPaymentStatus,
	} {
		// No targetIds: subscription-wide, which is what a seed should be.
		if _, err := a.subs.AddEvent(seeded.ID, string(et), bankingcircle.TargetCompany, nil); err != nil {
			log.Fatalf("seed subscription event %s: %v", et, err)
		}
	}
	log.Printf("banking-circle: seeded subscription %s -> %s (batch=%d, retries=%d, time_scale=%g)",
		seeded.ID, dest, seeded.MaxNotificationsPerMessage,
		deliveryCfg.DeactivateAfterRetries, deliveryCfg.TimeScale)

	go func() {
		log.Printf("banking-circle internal listening on %s (plain, no auth) — B4B bridge only", internalAddr)
		log.Fatal(http.ListenAndServe(internalAddr, logReq(a.internalMux())))
	}()

	// mTLS is optional. Real integrations vary -- some present a client
	// certificate, some do not -- and a simulator that forces it on makes
	// the lab unusable for the ones that do not, while a simulator that
	// cannot do it at all makes it untestable for the ones that do.
	//
	//   off      plain HTTPS, no client certificate asked for
	//   optional a client certificate is requested and verified if given
	//   require  a valid client certificate is mandatory (the old behaviour)
	mode := strings.ToLower(env("BC_MTLS", "require"))
	ln, err := tlsListener(addr, certFile, keyFile, caFile, mode)
	if err != nil {
		log.Fatalf("tls listener: %v", err)
	}
	log.Printf("banking-circle listening on %s mtls=%s dest=%s allowlist=%s delay=%s", addr, mode, dest, spec, delay)
	log.Fatal(http.Serve(ln, logReq(a.mux())))
}

// tlsListener builds the credentialed API's TLS listener. mode selects how
// hard a client certificate is enforced: "require" (the default, and what
// the previous implementation always did), "optional", or "off".
//
// "off" still serves HTTPS -- it drops the client-certificate requirement,
// not the transport security. Bearer auth applies in every mode, so
// turning mTLS off never makes the API unauthenticated.
func tlsListener(addr, certFile, keyFile, caFile, mode string) (net.Listener, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("server cert/key: %w", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	switch mode {
	case "off", "false", "none":
		cfg.ClientAuth = tls.NoClientCert
	case "optional", "verify-if-given":
		cfg.ClientAuth = tls.VerifyClientCertIfGiven
	case "require", "required", "true", "":
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	default:
		return nil, fmt.Errorf("BC_MTLS=%q, want off, optional or require", mode)
	}
	if cfg.ClientAuth != tls.NoClientCert {
		caPEM, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("ca file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("ca file %s had no certs", caFile)
		}
		cfg.ClientCAs = pool
	}
	return tls.Listen("tcp", addr, cfg)
}

// mux is the real-shaped, mTLS + bearer credentialed API (LISTEN).
func (a *app) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		httputilx.WriteJSON(w, 200, map[string]string{"status": "ok", "service": "banking-circle"})
	})
	mux.HandleFunc("GET /api/v1/authorizations/authorize", a.authorize)
	mux.HandleFunc("GET /api/v1/accounts/{accountId}/balances", a.requireBearer(a.balances))
	mux.HandleFunc("GET /api/v1/notificationselfservice/subscription", a.requireBearer(a.listSubscriptions))
	mux.HandleFunc("POST /api/v1/notificationselfservice/subscription", a.requireBearer(a.createSubscription))
	mux.HandleFunc("GET /api/v1/notificationselfservice/subscription/{id}", a.requireBearer(a.getSubscription))
	mux.HandleFunc("PUT /api/v1/notificationselfservice/subscription/{id}", a.requireBearer(a.updateSubscription))
	mux.HandleFunc("PUT /api/v1/notificationselfservice/subscription/{id}/activate", a.requireBearer(a.activateSubscription))
	mux.HandleFunc("PUT /api/v1/notificationselfservice/subscription/{id}/deactivate", a.requireBearer(a.deactivateSubscription))
	mux.HandleFunc("DELETE /api/v1/notificationselfservice/subscription/{id}", a.requireBearer(a.deleteSubscription))
	mux.HandleFunc("POST /api/v1/notificationselfservice/subscriptionEvent", a.requireBearer(a.createSubscriptionEvent))
	mux.HandleFunc("PUT /api/v1/notificationselfservice/subscriptionEvent/{id}/targets", a.requireBearer(a.replaceSubscriptionEventTargets))
	mux.HandleFunc("DELETE /api/v1/notificationselfservice/subscriptionEvent/{id}", a.requireBearer(a.deleteSubscriptionEvent))
	mux.HandleFunc("POST /api/v1/notificationselfservice/clienttest/{subscriptionId}", a.requireBearer(a.clientTest))
	// Lab-only: the warning and deactivation emails the retry schedule
	// emits, so a failing subscription is observable rather than just
	// eventually silent.
	mux.HandleFunc("GET /sim/emails", a.requireBearer(a.listEmails))
	mux.HandleFunc("POST /sim/subscription/{subscriptionId}/notifications", a.requireBearer(a.enqueueSyntheticNotifications))
	mux.HandleFunc("GET /sim/subscription/{subscriptionId}/pending", a.requireBearer(a.pendingNotifications))
	// The delivery table this process is actually running, which is the
	// file's plus whatever BC_TIME_SCALE overrode. Reading the file alone
	// reports a schedule nobody is on.
	mux.HandleFunc("GET /sim/delivery-config", a.requireBearer(a.deliveryConfig))
	// The console reads this one. It sits on the credentialed API
	// rather than the bridge because that is the surface the console
	// already holds a token for, and an observability endpoint is not
	// a reason to open a second unauthenticated door.
	mux.HandleFunc("GET /sim/activity", a.requireBearer(activity.Handler(a.payLog, a.notifLog)))
	// Debug/introspection endpoints kept from the old surface — not part of
	// the real Banking Circle contract, but useful for the harness and kept
	// behind the same credentials as everything else on this listener.
	mux.HandleFunc("GET /accounts", a.requireBearer(a.listAccounts))
	mux.HandleFunc("GET /accounts/{id}", a.requireBearer(a.getAccount))
	mux.HandleFunc("GET /payments", a.requireBearer(a.listPayments))
	mux.HandleFunc("GET /payments/{id}", a.requireBearer(a.getPayment))
	mux.HandleFunc("GET /reconciliation", a.requireBearer(a.reconcile))

	// The Connect reports surface. These are what a reconciliation sweep
	// reads, so they carry the vendor's own paths and body shapes rather
	// than this mock's convenience ones above.
	mux.HandleFunc("GET /api/v1/reports/intraday-reconciliation-paged-report",
		a.requireBearer(a.intradayReconciliationReport))
	mux.HandleFunc("GET /api/v1/reports/rejection-report",
		a.requireBearer(a.rejectionReport))
	return mux
}

func (a *app) deliveryConfig(w http.ResponseWriter, r *http.Request) {
	httputilx.WriteJSON(w, 200, a.delivery)
}

// internalMux is the lab-only, unauthenticated B4B<->Banking-Circle bridge
// (INTERNAL_LISTEN). Nothing else is served here.
func (a *app) internalMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/payments", a.payLog.Watch("payment.create", summarizeBridgePayment, a.createInternalPayment))
	mux.HandleFunc("POST /internal/payments/{id}/reverse", a.reverseInternalPayment)
	mux.HandleFunc("POST /internal/incoming-payments", a.payLog.Watch("payment.incoming", summarizeIncoming, a.createIncomingPayment))
	mux.HandleFunc("GET /internal/accounts/{accountId}/balances", a.internalAccountBalances)
	return mux
}

// --- auth ---------------------------------------------------------------

type tokenStore struct {
	mu     sync.Mutex
	tokens map[string]time.Time
}

func newTokenStore() *tokenStore {
	return &tokenStore{tokens: map[string]time.Time{}}
}

func (s *tokenStore) issue(ttl time.Duration) string {
	tok := "bcat_" + randomHex(24)
	s.mu.Lock()
	s.tokens[tok] = time.Now().Add(ttl)
	s.mu.Unlock()
	return tok
}

func (s *tokenStore) valid(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.tokens[tok]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.tokens, tok)
		return false
	}
	return true
}

// authorize issues a bearer token for any non-empty Basic credential — this
// lab has no real credential store, matching its existing "obviously fake,
// deliberately simple" auth posture elsewhere (e.g. bank's PINs).
func (a *app) authorize(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	if !ok || user == "" || pass == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="banking-circle"`)
		httputilx.Error(w, 401, "basic auth required")
		return
	}
	token := a.tokens.issue(a.tokenTTL)
	httputilx.WriteJSON(w, 200, map[string]any{
		"access_token": token,
		"expires_in":   int(a.tokenTTL.Seconds()),
		"token_type":   "bearer",
	})
}

// requireBearer guards every real-shaped endpoint except authorize itself.
func (a *app) requireBearer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authz := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(authz, prefix) || !a.tokens.valid(strings.TrimPrefix(authz, prefix)) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="banking-circle"`)
			httputilx.Error(w, 401, "bearer token required")
			return
		}
		next(w, r)
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// --- balances -------------------------------------------------------------

type balanceEntry struct {
	Type                     string `json:"type"`
	Currency                 string `json:"currency"`
	BeginOfDayAmount         string `json:"beginOfDayAmount"`
	FinancialDate            string `json:"financialDate"`
	IntraDayAmount           string `json:"intraDayAmount"`
	LastTransactionTimestamp string `json:"lastTransactionTimestamp"`
}

type pageInfo struct {
	CurrentPage int `json:"currentPage"`
	PageSize    int `json:"pageSize"`
	RowCount    int `json:"rowCount,omitempty"`
}

// balanceEntries builds the balance-entry list shared by both the
// mTLS-credentialed balances endpoint and the no-auth internal balance
// proxy below — identical shape for both, since they're reading the exact
// same underlying account (docs/ARCHITECTURE-phase3-corrections.md section
// 1).
func (a *app) balanceEntries(acct *bankingcircle.Account) []balanceEntry {
	now := time.Now().UTC()
	return []balanceEntry{{
		Type:                     "CurrentBalance",
		Currency:                 acct.Currency,
		BeginOfDayAmount:         acct.Balance,
		FinancialDate:            now.Format("2006-01-02"),
		IntraDayAmount:           acct.Balance,
		LastTransactionTimestamp: now.Format(time.RFC3339),
	}}
}

// balances implements GET /api/v1/accounts/{accountId}/balances. This lab's
// ledger has no intraday/beginOfDay distinction, so both amounts reflect the
// current balance — a documented simplification, not a hidden bug.
func (a *app) balances(w http.ResponseWriter, r *http.Request) {
	acc, err := a.ledger.Get(r.PathValue("accountId"))
	if err != nil {
		httputilx.Error(w, 404, "account not found")
		return
	}
	page := queryInt(r, "pageNumber", 1)
	size := queryInt(r, "pageSize", 20)
	entries := a.balanceEntries(acc)
	httputilx.WriteJSON(w, 200, map[string]any{
		"result":   entries,
		"pageInfo": pageInfo{CurrentPage: page, PageSize: size, RowCount: len(entries)},
	})
}

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func (a *app) listAccounts(w http.ResponseWriter, r *http.Request) {
	httputilx.WriteJSON(w, 200, map[string]any{"accounts": a.ledger.List()})
}

func (a *app) getAccount(w http.ResponseWriter, r *http.Request) {
	acc, err := a.ledger.Get(r.PathValue("id"))
	if err != nil {
		httputilx.Error(w, 404, "account not found")
		return
	}
	httputilx.WriteJSON(w, 200, acc)
}

func (a *app) listPayments(w http.ResponseWriter, r *http.Request) {
	httputilx.WriteJSON(w, 200, map[string]any{"payments": a.engine.List()})
}

func (a *app) getPayment(w http.ResponseWriter, r *http.Request) {
	p, err := a.engine.Get(r.PathValue("id"))
	if err != nil {
		httputilx.Error(w, 404, "payment not found")
		return
	}
	httputilx.WriteJSON(w, 200, p)
}

func (a *app) reconcile(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if date == "" {
		httputilx.Error(w, 400, "date query parameter required (YYYY-MM-DD)")
		return
	}
	payments := bankingcircle.Reconcile(a.engine.List(), date, r.URL.Query().Get("account_id"))
	httputilx.WriteJSON(w, 200, map[string]any{"payments": payments})
}

// intradayReconciliationReport implements
// GET /api/v1/reports/intraday-reconciliation-paged-report.
//
// A row's existence is the booking signal — the report carries no status —
// so this answers from the same payment records the notifications are built
// from. If the two ever disagreed, a client would be reconciling against a
// story rather than against the money.
func (a *app) intradayReconciliationReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := bankingcircle.ReconciliationQuery{
		FromTransactionDate: q.Get("FromTransactionDate"),
		ToTransactionDate:   q.Get("ToTransactionDate"),
		FromCreatedAt:       q.Get("FromCreatedAt"),
		ToCreatedAt:         q.Get("ToCreatedAt"),
		PageNumber:          atoiOr(q.Get("PageNumber"), 1),
		PageSize:            atoiOr(q.Get("PageSize"), 100),
	}
	if ids := q.Get("AccountId"); ids != "" {
		query.AccountIDs = strings.Split(ids, ",")
	}
	rows := bankingcircle.IntradayReconciliation(a.engine.List(), query)
	httputilx.WriteJSON(w, 200, map[string]any{"reconciliations": rows})
}

// rejectionReport implements GET /api/v1/reports/rejection-report: the
// complement of the reconciliation report, carrying the payments that did
// not book and why.
func (a *app) rejectionReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows := bankingcircle.Rejections(a.engine.List(), bankingcircle.RejectionQuery{
		TransactionDate:     q.Get("TransactionDate"),
		IncludeReceived:     boolOr(q.Get("IncludeReceived"), true),
		IncludeMissingFunds: boolOr(q.Get("IncludeMissingFunds"), true),
		IncludeReversals:    boolOr(q.Get("IncludeReversals"), true),
		ExcludeBooked:       boolOr(q.Get("ExcludeBooked"), false),
	})
	httputilx.WriteJSON(w, 200, map[string]any{"rejections": rows})
}

// atoiOr is lenient on purpose: a malformed page number should produce the
// first page, not a 400 that hides the report from a sweep.
func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func boolOr(s string, def bool) bool {
	if s == "" {
		return def
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return def
	}
	return v
}

// --- the B4B<->Banking-Circle bridge (INTERNAL_LISTEN, no auth) --------

// sgaAccountID maps a currency to the safeguarding account (SGA) this mock
// debits outgoing payouts from and credits incoming Worldline lump sums
// into — resolved by currency, not a fixed constant, now that there is one
// SGA per currency instead of a single pre-funded funding account
// (docs/ARCHITECTURE-phase3-corrections.md section 1).
func sgaAccountID(currency string) (string, error) {
	switch currency {
	case "EUR":
		return "bc_acc_sga_eur", nil
	case "GBP":
		return "bc_acc_sga_gbp", nil
	default:
		return "", fmt.Errorf("no safeguarding account configured for currency %q", currency)
	}
}

type internalPaymentReq struct {
	PaymentID string `json:"paymentId"`
	AccountID string `json:"accountId"`
	// IBAN is the beneficiary's account at their own bank. Optional, and
	// carried rather than derived: the settlement bank knows where it is
	// sending money, and without it the receiving bank has nothing to open
	// an account against.
	IBAN        string `json:"iban"`
	Holder      string `json:"holder"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	ExternalRef string `json:"externalRef"`
}

// createInternalPayment implements POST /internal/payments — the seam the
// B4B mock calls, once its own payment reaches B4BTMApproved, carrying the
// real BC paymentId it's about to reference in its own webhook. paymentId
// is used as-is (generated by B4B, not by us); accountId is the creditor,
// auto-vivified via Ledger.GetOrCreate if this is the first payment to it
// (docs/ARCHITECTURE-phase3-corrections.md section 1 — B4B derives a
// distinct creditor account per beneficiary, not a single fixed known one,
// so this can no longer 404); the debtor is the safeguarding account
// matching the payment's currency; externalRef lands on the existing
// Payment.SettlementID field verbatim (Addendum section B).
func (a *app) createInternalPayment(w http.ResponseWriter, r *http.Request) {
	var req internalPaymentReq
	if err := httputilx.ReadJSON(r, &req); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	if req.PaymentID == "" {
		httputilx.Error(w, 400, "paymentId required")
		return
	}
	if req.AccountID == "" {
		httputilx.Error(w, 400, "accountId required")
		return
	}
	cents, err := money.Parse(req.Amount)
	if err != nil || cents <= 0 {
		httputilx.Error(w, 400, "amount must be a positive decimal")
		return
	}
	ccy := req.Currency
	if ccy == "" {
		ccy = "EUR"
	}
	from, err := sgaAccountID(ccy)
	if err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	to := a.ledger.GetOrCreate(req.AccountID, ccy)
	now := time.Now().UTC().Format(time.RFC3339)
	p := &bankingcircle.Payment{
		ID:            req.PaymentID,
		SettlementID:  req.ExternalRef,
		FromAccountID: from,
		ToAccountID:   to.ID,
		ToIBAN:        req.IBAN,
		ToHolder:      req.Holder,
		Amount:        money.Format(cents),
		Currency:      ccy,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	a.engine.Create(p)
	got, _ := a.engine.Get(p.ID)
	httputilx.WriteJSON(w, 202, got)
}

type incomingPaymentReq struct {
	Currency  string `json:"currency"`
	Amount    string `json:"amount"`
	Reference string `json:"reference"`
}

// createIncomingPayment implements POST /internal/incoming-payments — the
// harness's "Worldline lump sum landed" trigger
// (docs/ARCHITECTURE-phase3-corrections.md section 1), same lab-only trust
// boundary as the B4B bridge above. Real Banking Circle has no equivalent
// caller-facing endpoint; this is the harness's stand-in for the acquirer
// actually wiring funds into the SGA.
func (a *app) createIncomingPayment(w http.ResponseWriter, r *http.Request) {
	var req incomingPaymentReq
	if err := httputilx.ReadJSON(r, &req); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	to, err := sgaAccountID(req.Currency)
	if err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	cents, err := money.Parse(req.Amount)
	if err != nil || cents <= 0 {
		httputilx.Error(w, 400, "amount must be a positive decimal")
		return
	}
	p, err := a.engine.CreditIncoming(to, req.Currency, money.Format(cents), req.Reference)
	if err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	httputilx.WriteJSON(w, 202, p)
}

// internalAccountBalances implements GET
// /internal/accounts/{accountId}/balances, mirroring buddy's own
// InternalAccountBalanceController: lets other Infinite services/Lambdas
// check SGA sufficiency without holding Banking Circle's mTLS+bearer
// credentials themselves (docs/ARCHITECTURE-phase3-corrections.md section
// 1). Read-only: unlike createInternalPayment, an unknown account id is a
// real 404, not an auto-vivify trigger.
func (a *app) internalAccountBalances(w http.ResponseWriter, r *http.Request) {
	acc, err := a.ledger.Get(r.PathValue("accountId"))
	if err != nil {
		httputilx.Error(w, 404, "account not found")
		return
	}
	httputilx.WriteJSON(w, 200, map[string]any{
		"accountId": acc.ID,
		"balances":  a.balanceEntries(acc),
	})
}

type reverseReq struct {
	Reason string `json:"reason"`
}

// reverseInternalPayment implements POST /internal/payments/{id}/reverse —
// the manual test hook replacing the old POST /payments/{id}/return, same
// semantics: only valid from an OutgoingPaymentProcessed payment.
func (a *app) reverseInternalPayment(w http.ResponseWriter, r *http.Request) {
	var req reverseReq
	if err := httputilx.ReadJSON(r, &req); err != nil && err != io.EOF {
		httputilx.Error(w, 400, err.Error())
		return
	}
	p, err := a.engine.Reverse(r.PathValue("id"), req.Reason)
	if err != nil {
		switch {
		case errors.Is(err, bankingcircle.ErrPaymentNotFound):
			httputilx.Error(w, 404, "payment not found")
		default:
			httputilx.Error(w, 409, err.Error())
		}
		return
	}
	httputilx.WriteJSON(w, 200, p)
}

// --- notification delivery ---------------------------------------------

// notification and notificationBatch are Banking Circle's real webhook
// payload shape verbatim (docs/ARCHITECTURE-vendor-corrections.md section 3).
func httpClient(caFile string) (*http.Client, error) {
	t := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if caFile != "" {
		if pem, err := os.ReadFile(caFile); err == nil {
			pool := x509.NewCertPool()
			if pool.AppendCertsFromPEM(pem) {
				t.TLSClientConfig.RootCAs = pool
			} else {
				log.Printf("CA_FILE %s had no certs, using system roots", caFile)
			}
		} else {
			log.Printf("CA_FILE %s not readable (%v); HTTPS to lab receiver will fail until certs exist", caFile, err)
		}
	}
	return &http.Client{Timeout: 8 * time.Second, Transport: t}, nil
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

func envInt(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func envDuration(k string, def time.Duration) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func logReq(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
