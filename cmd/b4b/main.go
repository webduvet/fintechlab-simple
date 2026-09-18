// B4B Payments is the mock for the vendor Infinite pays merchants through:
// buddy's real accounts-settlement -> B4B -> Banking Circle chain, per
// docs/ARCHITECTURE-vendor-corrections.md section 4 and its Addendum
// sections A/D/F, plus the company-boarding half of the Oversight API per
// docs/ARCHITECTURE-b4b-oversight.md.
//
// It verifies inbound RS512-signed bearer tokens, boards companies (people,
// addresses, extended profile, documents, vIBANs), registers beneficiaries,
// owns the B4BAccepted -> ... -> B4BTMApproved|B4BFailed lifecycle, fires
// plain JSON callbacks, and bridges an approved payment into the Banking
// Circle mock. Everything it is told is written to one state file, so a
// merchant boarded before a restart is still boarded after one.
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/webduvet/fintechlab-simple/internal/allowlist"
	"github.com/webduvet/fintechlab-simple/internal/b4b"
	"github.com/webduvet/fintechlab-simple/internal/httputilx"
	"github.com/webduvet/fintechlab-simple/internal/retry"
)

type app struct {
	engine        *b4b.Engine
	beneficiaries *b4b.BeneficiaryStore
	dir           *b4b.Directory
	pubKey        *rsa.PublicKey
	kid           string
	client        *http.Client
	list          *allowlist.List
	backs         []time.Duration
	maxTry        int
	bcURL         string
	forceFails    map[string]bool
	statePath     string
	// callbackURL is the client-account webhook endpoint: where a callback
	// goes when the entity it is about carries no callback_url of its own.
	// The real API has exactly this -- one endpoint agreed per client, not
	// a per-record field -- so without it, boarding callbacks have nowhere
	// to go and a client waits forever for a screening result that was
	// computed and then dropped.
	callbackURL string
	// enforceCompany turns on the beneficiary-belongs-to-the-paying-company
	// check. Off by default; see b4b.Beneficiary.CheckCompany for why it is
	// a switch and not a rule.
	enforceCompany bool
}

func main() {
	addr := env("LISTEN", ":8086")
	keysDir := env("B4B_JWT_KEYS_DIR", "/b4b-keys")
	pubKeyPath := env("B4B_JWT_PUBLIC_KEY_PATH", "")
	kid := env("B4B_JWT_KEY_ID", "b4b-mock-1")
	delay := envDuration("B4B_PROCESSING_DELAY", 300*time.Millisecond)
	spec := env("WEBHOOK_ALLOWLIST", "settlement,settlement:8083,localhost,127.0.0.1")
	bcURL := env("BANKING_CIRCLE_URL", "http://banking-circle:8095")
	forceFailSpec := env("B4B_FORCE_FAILURE_BENEFICIARY_IDS", "")
	statePath := env("B4B_STATE_PATH", "/b4b-data/state.json")
	callbackURL := env("B4B_CALLBACK_URL", "")

	list, err := allowlist.Parse(spec)
	if err != nil {
		log.Fatalf("allowlist: %v", err)
	}
	backs, err := retry.ParseBackoffs(env("RETRY_BACKOFF", "1s,2s,4s"))
	if err != nil {
		log.Fatalf("backoff: %v", err)
	}
	if callbackURL != "" {
		if err := b4b.ValidateCallbackURL(list, callbackURL); err != nil {
			log.Fatalf("B4B_CALLBACK_URL: %v", err)
		}
	}

	pub, err := loadVerificationKey(keysDir, pubKeyPath, kid)
	if err != nil {
		log.Fatalf("b4b jwt: %v", err)
	}

	a := &app{
		beneficiaries:  b4b.NewBeneficiaryStore(func() string { return "ben_" + shortID() }),
		dir:            b4b.NewDirectory(nil),
		pubKey:         pub,
		kid:            kid,
		client:         &http.Client{Timeout: 8 * time.Second},
		list:           list,
		backs:          backs,
		maxTry:         envInt("MAX_ATTEMPTS", 4),
		bcURL:          bcURL,
		forceFails:     parseForceFailSet(forceFailSpec),
		statePath:      statePath,
		callbackURL:    callbackURL,
		enforceCompany: envBool("B4B_ENFORCE_BENEFICIARY_COMPANY", false),
	}
	a.engine = b4b.NewEngine(delay, a.onTransition)

	// Restore before anything can serve: a company that existed a second
	// before the restart has to exist a second after it, and a payment
	// caught mid-lifecycle resumes rather than hanging (b4b.Engine.Restore).
	state, err := b4b.LoadState(statePath)
	if err != nil {
		log.Fatalf("b4b state: %v", err)
	}
	a.dir.Restore(state)
	a.beneficiaries.Restore(state)
	a.engine.Restore(state)
	log.Printf("b4b: state %s restored: %d companies, %d people, %d beneficiaries, %d payments",
		statePathLabel(statePath), len(state.Companies), len(state.People), len(state.Beneficiaries), len(state.Payments))

	// Persist after every mutation. Whole-file writes at lab volumes; see
	// internal/b4b/state.go for why this is a file and not a database.
	a.dir.OnChange = a.persist
	a.beneficiaries.OnChange = a.persist

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		httputilx.WriteJSON(w, 200, map[string]string{"status": "ok", "service": "b4b"})
	})

	// Boarding: the chain a platform walks before it can pay anybody.
	mux.HandleFunc("POST /oversight/v1/companies", a.requireAuth(a.createCompany))
	mux.HandleFunc("GET /oversight/v1/companies", a.requireAuth(a.listCompanies))
	mux.HandleFunc("GET /oversight/v1/companies/{id}", a.requireAuth(a.getCompany))
	mux.HandleFunc("POST /oversight/v1/companies/{id}/addresses", a.requireAuth(a.createAddress))
	mux.HandleFunc("GET /oversight/v1/companies/{id}/addresses", a.requireAuth(a.listAddresses))
	mux.HandleFunc("POST /oversight/v1/companies/{id}/people", a.requireAuth(a.createPerson))
	mux.HandleFunc("GET /oversight/v1/companies/{id}/people", a.requireAuth(a.listPeople))
	mux.HandleFunc("GET /oversight/v1/companies/{id}/people/{personID}", a.requireAuth(a.getPerson))
	mux.HandleFunc("POST /oversight/v1/companies/{id}/extended", a.requireAuth(a.createExtended))
	// Both spellings of the read. The one with an id is what the vendor
	// documents; the one without is what a client actually has at the point
	// it needs to ask "did I already create this?", because it does not
	// know the id until it has read it.
	mux.HandleFunc("GET /oversight/v1/companies/{id}/extended", a.requireAuth(a.getExtended))
	mux.HandleFunc("GET /oversight/v1/companies/{id}/extended/{extendedID}", a.requireAuth(a.getExtended))
	mux.HandleFunc("POST /oversight/v1/companies/{id}/vibans", a.requireAuth(a.createViban))
	mux.HandleFunc("GET /oversight/v1/companies/{id}/vibans", a.requireAuth(a.listVibans))
	mux.HandleFunc("POST /oversight/v1/uploads", a.requireAuth(a.uploadDocument))
	mux.HandleFunc("GET /oversight/v1/uploads/{id}", a.requireAuth(a.getDocument))

	// Payout.
	mux.HandleFunc("POST /oversight/v1/beneficiaries", a.requireAuth(a.registerBeneficiary))
	mux.HandleFunc("GET /oversight/v1/beneficiaries/{id}", a.requireAuth(a.getBeneficiary))
	mux.HandleFunc("POST /oversight/v1/payments", a.requireAuth(a.createPayment))
	// The documented recovery path for a missed callback: callbacks are
	// unsigned, unordered and may repeat, so a client that lost one reads
	// the current state instead of trying to replay it.
	mux.HandleFunc("GET /oversight/v1/payments/{id}", a.requireAuth(a.getPayment))

	// Lab-only. Real screening decides these, and they can change at any
	// time under continuous screening. Exposing them lets a scenario
	// exercise the paths a client has to handle -- a payee going from pass
	// to fail, a clean payee that is nonetheless disabled, a PEP flagged
	// separately from a sanctions hit -- instead of reading that it would.
	mux.HandleFunc("PUT /sim/beneficiaries/{id}/sanctions", a.requireAuth(a.setSanctions))
	mux.HandleFunc("PUT /sim/beneficiaries/{id}/status", a.requireAuth(a.setBeneficiaryStatus))
	mux.HandleFunc("PUT /sim/companies/{id}/sanctions", a.requireAuth(a.setCompanySanctions))
	mux.HandleFunc("PUT /sim/people/{id}/sanctions", a.requireAuth(a.setPersonSanctions))

	log.Printf("b4b listening on %s kid=%s allowlist=%s delay=%s banking_circle_url=%s state=%s callback_url=%q enforce_beneficiary_company=%t",
		addr, kid, spec, delay, bcURL, statePathLabel(statePath), callbackURL, a.enforceCompany)
	log.Fatal(http.ListenAndServe(addr, logReq(mux)))
}

// loadVerificationKey decides what this service verifies inbound tokens
// against.
//
// B4B_JWT_PUBLIC_KEY_PATH is the real-shaped option and takes precedence: a
// real vendor holds your *public* key and nothing else. The keys-directory
// path is the lab default, where this service generates the keypair and
// this lab's own settlement stand-in reads the private half back to sign
// with -- convenient, and inverted from how it works with a real vendor,
// which is why the other option exists. Point a third-party client at this
// mock by exporting its public key here rather than handing the mock its
// private one.
func loadVerificationKey(keysDir, pubKeyPath, kid string) (*rsa.PublicKey, error) {
	if pubKeyPath != "" {
		pub, err := b4b.LoadPublicKey(pubKeyPath)
		if err != nil {
			return nil, err
		}
		log.Printf("b4b: verifying inbound JWTs against %s (kid=%s)", pubKeyPath, kid)
		return pub, nil
	}
	key, err := b4b.LoadOrGenerateKeyPair(keysDir)
	if err != nil {
		return nil, err
	}
	pubPEM, err := b4b.PublicKeyPEM(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	// Printed to stdout (not the log package, which defaults to stderr) so
	// an operator/harness reading this service's stdout can pick it up.
	fmt.Printf("b4b: JWT public key (kid=%s):\n%s\n", kid, pubPEM)
	return &key.PublicKey, nil
}

// persist writes the whole store out. Fired by the directory and the
// beneficiary store after every mutation, and by onTransition after every
// payment state change.
//
// A failure here is logged and not fatal: losing the ability to remember
// is bad, but taking the service down mid-payment because a disk is full
// is worse, and the in-memory state is still correct.
func (a *app) persist() {
	if a.statePath == "" {
		return
	}
	var s b4b.State
	a.dir.Snapshot(&s)
	a.beneficiaries.Snapshot(&s)
	a.engine.Snapshot(&s)
	if err := b4b.SaveState(a.statePath, s); err != nil {
		log.Printf("b4b: persist state to %s: %v", a.statePath, err)
	}
}

// requireAuth verifies the inbound Authorization: Bearer <RS512 JWT> header
// against this service's configured public key before delegating to next.
// Any parse/verify failure is rejected 401 with a clear log message -- B4B
// is the server here, so it is the one doing the verifying (docs/
// ARCHITECTURE-vendor-corrections.md section 4).
func (a *app) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		authz := r.Header.Get("Authorization")
		if !strings.HasPrefix(authz, prefix) {
			log.Printf("b4b: rejected %s %s: missing Bearer authorization header", r.Method, r.URL.Path)
			httputilx.Error(w, 401, "missing bearer token")
			return
		}
		token := strings.TrimPrefix(authz, prefix)
		if err := b4b.VerifyBearerToken(token, a.pubKey, a.kid); err != nil {
			log.Printf("b4b: rejected %s %s: %v", r.Method, r.URL.Path, err)
			httputilx.Error(w, 401, "invalid bearer token")
			return
		}
		next(w, r)
	}
}

// writeStoreError maps a domain error to the status code the real API
// answers with. The distinction that matters is 400 vs 422: a missing
// field is something the caller fixes and resends, while "you already
// created this" is something it has to go and read instead -- and a client
// that cannot tell them apart retries the one it should reconcile.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, b4b.ErrNotFound):
		httputilx.Error(w, 404, err.Error())
	case errors.Is(err, b4b.ErrDuplicate), errors.Is(err, b4b.ErrRequirementUnmet):
		httputilx.Error(w, 422, err.Error())
	default:
		httputilx.Error(w, 400, err.Error())
	}
}

// callbackTarget picks where a callback for this entity goes: its own
// callback_url if the caller supplied one (a lab affordance), otherwise the
// client-account endpoint from B4B_CALLBACK_URL. Empty means nowhere, and
// the caller logs that rather than silently doing nothing.
func (a *app) callbackTarget(override string) string {
	if override != "" {
		return override
	}
	return a.callbackURL
}

// deliverCallback POSTs a plain-JSON callback, retrying with backoff until
// 2xx or MAX_ATTEMPTS. B4B's Oversight callbacks sign nothing -- no HMAC,
// no AES-GCM -- and are retried by the real vendor for about a day, which
// is why every callback here goes through one retrying path rather than
// some of them being best-effort single shots.
func (a *app) deliverCallback(what, dest string, body any) {
	if dest == "" {
		log.Printf("b4b: %s callback not delivered: no callback_url and no B4B_CALLBACK_URL configured", what)
		return
	}
	payload, err := json.Marshal(body)
	if err != nil {
		log.Printf("b4b: marshal %s callback: %v", what, err)
		return
	}
	var last int
	var lastErr error
	for attempt := 1; attempt <= a.maxTry; attempt++ {
		if wait := retry.SleepBeforeAttempt(attempt, a.backs); wait > 0 {
			time.Sleep(wait)
		}
		st, err := a.postWebhook(dest, payload)
		last, lastErr = st, err
		if err == nil {
			log.Printf("b4b: %s callback attempt=%d http=%d delivered to %s", what, attempt, st, dest)
			return
		}
		log.Printf("b4b: %s callback attempt=%d http=%d err=%v", what, attempt, st, err)
		if !retry.ShouldRetry(st, attempt, a.maxTry) {
			break
		}
	}
	log.Printf("b4b: %s callback failed after retries: http=%d err=%v", what, last, lastErr)
}

// postWebhook does the plain JSON POST. The allowlist is enforced here as
// well as at creation time, so a delivery can never bypass policy even if
// the destination was stored before the policy changed.
func (a *app) postWebhook(dest string, body []byte) (int, error) {
	if err := a.list.Allowed(dest); err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, dest, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("b4b: webhook destination returned %s", resp.Status)
	}
	return resp.StatusCode, nil
}

func parseForceFailSet(spec string) map[string]bool {
	set := map[string]bool{}
	for _, raw := range strings.Split(spec, ",") {
		raw = strings.TrimSpace(raw)
		if raw != "" {
			set[raw] = true
		}
	}
	return set
}

func statePathLabel(p string) string {
	if p == "" {
		return "(disabled, in-memory only)"
	}
	return p
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

func envBool(k string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(k))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
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
