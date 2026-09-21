package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/webduvet/fintechlab-simple/internal/activity"
	"github.com/webduvet/fintechlab-simple/internal/allowlist"
	"github.com/webduvet/fintechlab-simple/internal/bankingcircle"
)

// Account ids in these tests are UUIDs because Banking Circle's are, and
// because every service between the platform and this one validates them
// as such before forwarding. testMerchantAccount and friends are derived
// the same way B4B derives a creditor account from a beneficiary id.
var (
	sgaEUR          = bankingcircle.SGAAccountEUR
	merchantAccount = bankingcircle.AccountIDFor("merchant")
	brandNewAccount = bankingcircle.AccountIDFor("brand_new")
	someoneAccount  = bankingcircle.AccountIDFor("someone")
)

func newTestApp(t *testing.T) *app {
	t.Helper()
	list, err := allowlist.Parse("127.0.0.1,localhost")
	if err != nil {
		t.Fatal(err)
	}
	ledger := bankingcircle.NewLedger()
	a := &app{
		ledger:   ledger,
		client:   &http.Client{Timeout: 2 * time.Second},
		list:     list,
		notifKey: []byte("sim-bc-notification-key-32-chars"),
		tokens:   newTokenStore(),
		subs:     bankingcircle.NewSubscriptionStore(nil),
		mail:     &bankingcircle.MailBox{},
		tokenTTL: time.Hour,
	}
	// One retry, immediately, so a delivery failure in a test resolves in
	// milliseconds rather than acting out the real two-day schedule.
	cfg := bankingcircle.DeliveryConfig{
		Schedule:                          []bankingcircle.RetryStep{{After: bankingcircle.Duration(time.Millisecond)}},
		TimeScale:                         1,
		DeactivateAfterRetries:            1,
		BatchFlushInterval:                bankingcircle.Duration(5 * time.Millisecond),
		DefaultMaxNotificationsPerMessage: bankingcircle.MinNotificationsPerMessage,
		RequestTimeout:                    bankingcircle.Duration(2 * time.Second),
	}
	a.notifLog = activity.New("notifications", "Notifications sent", "")
	a.dispatch = bankingcircle.NewDispatcher(cfg, a.subs, a.mail, t.Logf)
	a.dispatch.Send = a.sendEncrypted
	a.dispatch.OnQueued = a.recordQueued
	a.engine = bankingcircle.NewEngine(ledger, 5*time.Millisecond, a.onTransition)
	return a
}

// subscribeAll registers a subscription for every event type this lab's
// engine fires, which is what a test that just wants to observe the wire
// format needs.
func subscribeAll(t *testing.T, a *app, endpoint string) *bankingcircle.Subscription {
	t.Helper()
	sub, err := a.subs.Create(bankingcircle.CreateParams{
		Endpoint:                   endpoint,
		EncryptionKey:              string(a.notifKey),
		Status:                     bankingcircle.StatusActive,
		MaxNotificationsPerMessage: bankingcircle.MinNotificationsPerMessage,
	})
	if err != nil {
		t.Fatal(err)
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
		if _, err := a.subs.AddEvent(sub.ID, string(et), bankingcircle.TargetCompany, nil); err != nil {
			t.Fatal(err)
		}
	}
	return sub
}

// received is one encrypted POST the fake subscriber saw.
type received struct {
	nonce, tag, checksum []byte
	body                 []byte
}

// notifStream flattens delivered envelopes into individual notifications.
//
// It exists because notifications are batched: how many arrive in one POST
// depends on how close together the events were, so a test that asserts
// "the next POST contains exactly one notification" is asserting a timing
// coincidence. What a test actually cares about is the order and content
// of the notifications, which is what this yields.
type notifStream struct {
	t   *testing.T
	ch  <-chan received
	key []byte
	buf []bankingcircle.Notification
}

func (s *notifStream) next() bankingcircle.Notification {
	s.t.Helper()
	for len(s.buf) == 0 {
		select {
		case r := <-s.ch:
			plain, err := decryptNotification(r.body, r.nonce, r.tag, s.key)
			if err != nil {
				s.t.Fatalf("decrypt notification: %v", err)
			}
			sum := sha256.Sum256(plain)
			if !bytes.Equal(sum[:], r.checksum) {
				s.t.Fatal("checksum does not match the decrypted plaintext JSON")
			}
			var env bankingcircle.Envelope
			if err := json.Unmarshal(plain, &env); err != nil {
				s.t.Fatalf("unmarshal decrypted envelope: %v", err)
			}
			if len(env.Notifications) == 0 {
				s.t.Fatal("delivered an envelope with no notifications in it")
			}
			s.buf = env.Notifications
		case <-time.After(3 * time.Second):
			s.t.Fatal("timed out waiting for a notification")
		}
	}
	n := s.buf[0]
	s.buf = s.buf[1:]
	return n
}

// expect reads notifications until it sees eventType, failing if something
// else terminal turns up first.
func (s *notifStream) expect(eventType bankingcircle.NotificationType) bankingcircle.Notification {
	s.t.Helper()
	for range 10 {
		n := s.next()
		if n.NotificationType == string(eventType) {
			return n
		}
	}
	s.t.Fatalf("never saw a %s notification", eventType)
	return bankingcircle.Notification{}
}

// captureSubscriber starts a fake subscriber that records every encrypted
// POST and answers 200.
func captureSubscriber(t *testing.T) (*httptest.Server, chan received) {
	t.Helper()
	deliveries := make(chan received, 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		nonce, _ := base64.StdEncoding.DecodeString(r.Header.Get("Nonce"))
		tag, _ := base64.StdEncoding.DecodeString(r.Header.Get("AuthenticationTag"))
		checksum, _ := base64.StdEncoding.DecodeString(r.Header.Get("Checksum"))
		if ct := r.Header.Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("Content-Type = %q, want application/octet-stream", ct)
		}
		deliveries <- received{nonce: nonce, tag: tag, checksum: checksum, body: body}
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	return srv, deliveries
}

// paymentOf re-decodes a notification's "payment" property, which is
// deliberately typed as `any` on the wire so PaymentStatus and
// AgencyBankingWhitelistResult can put their detail under "payload"
// instead.
func paymentOf(t *testing.T, n bankingcircle.Notification) notificationPayment {
	t.Helper()
	if n.Payment == nil {
		t.Fatalf("notification %s has no payment property", n.NotificationType)
	}
	raw, err := json.Marshal(n.Payment)
	if err != nil {
		t.Fatal(err)
	}
	var p notificationPayment
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// --- encryption algorithm -------------------------------------------------

// decryptNotification is the test-side mirror of encryptNotification, used
// to prove the wire format round-trips exactly as documented.
func decryptNotification(ciphertext, nonce, tag, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sealed := append(append([]byte{}, ciphertext...), tag...)
	utf16le, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, err
	}
	if len(utf16le)%2 != 0 {
		return nil, err
	}
	units := make([]uint16, len(utf16le)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(utf16le[i*2:])
	}
	return []byte(string(utf16.Decode(units))), nil
}

func TestEncryptNotificationRoundTrip(t *testing.T) {
	key := []byte("sim-bc-notification-key-32-chars")
	if len(key) != 32 {
		t.Fatalf("test key must be 32 bytes, got %d", len(key))
	}
	original := []byte(`{"notifications":[{"eventId":"evt_1","notificationType":"OutgoingPaymentBooked"}]}`)

	ciphertext, nonce, tag, checksum, err := encryptNotification(original, key)
	if err != nil {
		t.Fatalf("encryptNotification: %v", err)
	}
	if len(tag) != 16 {
		t.Fatalf("tag length = %d, want 16 (GCM auth tag)", len(tag))
	}
	if len(nonce) != 12 {
		t.Fatalf("nonce length = %d, want 12", len(nonce))
	}
	wantSum := sha256.Sum256(original)
	if !bytes.Equal(checksum, wantSum[:]) {
		t.Fatalf("checksum mismatch")
	}

	decrypted, err := decryptNotification(ciphertext, nonce, tag, key)
	if err != nil {
		t.Fatalf("decryptNotification: %v", err)
	}
	if !bytes.Equal(decrypted, original) {
		t.Fatalf("decrypted = %s, want %s", decrypted, original)
	}
}

func TestUtf16LEBytesMatchesStdlib(t *testing.T) {
	s := "hello, banking circle"
	got := utf16LEBytes(s)
	units := utf16.Encode([]rune(s))
	if len(got) != len(units)*2 {
		t.Fatalf("length = %d, want %d", len(got), len(units)*2)
	}
	for i, u := range units {
		lo := got[i*2]
		hi := got[i*2+1]
		if uint16(lo)|uint16(hi)<<8 != u {
			t.Fatalf("unit %d mismatch", i)
		}
	}
}

// --- token store -----------------------------------------------------------

func TestTokenStoreIssueValidateExpire(t *testing.T) {
	ts := newTokenStore()
	tok := ts.issue(20 * time.Millisecond)
	if !ts.valid(tok) {
		t.Fatal("freshly issued token should be valid")
	}
	if ts.valid("bogus") {
		t.Fatal("unknown token should be invalid")
	}
	time.Sleep(40 * time.Millisecond)
	if ts.valid(tok) {
		t.Fatal("expired token should be invalid")
	}
}

// --- HTTP handlers over the real muxes (plain HTTP; TLS tested separately) --

func TestAuthorizeAndBearerGate(t *testing.T) {
	a := newTestApp(t)
	srv := httptest.NewServer(a.mux())
	defer srv.Close()

	// No credentials at all.
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/authorizations/authorize", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("authorize without basic auth = %d, want 401", resp.StatusCode)
	}

	// Any non-empty basic auth succeeds.
	req, _ = http.NewRequest("GET", srv.URL+"/api/v1/authorizations/authorize", nil)
	req.SetBasicAuth("anyone", "anypass")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("authorize = %d, want 200", resp.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.AccessToken == "" || body.TokenType != "bearer" || body.ExpiresIn <= 0 {
		t.Fatalf("unexpected authorize body: %+v", body)
	}

	// Balances without a bearer token: rejected.
	resp, err = http.Get(srv.URL + "/api/v1/accounts/" + sgaEUR + "/balances")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("balances without bearer = %d, want 401", resp.StatusCode)
	}

	// Balances with the issued bearer token: succeeds.
	req, _ = http.NewRequest("GET", srv.URL+"/api/v1/accounts/"+sgaEUR+"/balances?pageNumber=2&pageSize=5", nil)
	req.Header.Set("Authorization", "Bearer "+body.AccessToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("balances with bearer = %d, want 200", resp.StatusCode)
	}
	var balBody struct {
		Result []struct {
			Type     string `json:"type"`
			Currency string `json:"currency"`
		} `json:"result"`
		PageInfo struct {
			CurrentPage int `json:"currentPage"`
			PageSize    int `json:"pageSize"`
			RowCount    int `json:"rowCount"`
		} `json:"pageInfo"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&balBody); err != nil {
		t.Fatal(err)
	}
	if len(balBody.Result) != 1 || balBody.Result[0].Type != "CurrentBalance" || balBody.Result[0].Currency != "EUR" {
		t.Fatalf("unexpected balances result: %+v", balBody.Result)
	}
	if balBody.PageInfo.CurrentPage != 2 || balBody.PageInfo.PageSize != 5 {
		t.Fatalf("unexpected pageInfo: %+v", balBody.PageInfo)
	}
}

func TestSubscriptionCRUDOverHTTP(t *testing.T) {
	a := newTestApp(t)
	srv := httptest.NewServer(a.mux())
	defer srv.Close()

	tok := a.tokens.issue(time.Hour)
	authedWith := func(method, path, body, ifMatch string) *http.Response {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	authed := func(method, path, body string) *http.Response {
		return authedWith(method, path, body, "")
	}

	const key32 = "sim-bc-notification-key-32-chars"

	// Disallowed host: 400, not a silent later delivery failure.
	resp := authed("POST", "/api/v1/notificationselfservice/subscription",
		`{"endpoint":"https://evil.example.com/hook","status":2,"encryptionKey":"`+key32+`"}`)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("create subscription with disallowed endpoint = %d, want 400", resp.StatusCode)
	}

	// A key of the wrong length cannot work as an AES-256 key, so it is
	// rejected at create time rather than at the first delivery.
	resp = authed("POST", "/api/v1/notificationselfservice/subscription",
		`{"endpoint":"https://localhost/hook","status":2,"encryptionKey":"too-short"}`)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("create subscription with a short encryptionKey = %d, want 400", resp.StatusCode)
	}

	// Out-of-range batch size is rejected, not clamped.
	resp = authed("POST", "/api/v1/notificationselfservice/subscription",
		`{"endpoint":"https://localhost/hook","status":2,"encryptionKey":"`+key32+`","maxNotificationsPerMessage":2000}`)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("create subscription with maxNotificationsPerMessage=2000 = %d, want 400", resp.StatusCode)
	}

	resp = authed("POST", "/api/v1/notificationselfservice/subscription",
		`{"endpoint":"https://localhost/hook","status":2,"encryptionKey":"`+key32+`","email":"alerts@example.com","maxNotificationsPerMessage":25}`)
	var created bankingcircle.PublicSubscription
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || created.ID == "" || !created.IsActive {
		t.Fatalf("create subscription: status=%d body=%+v", resp.StatusCode, created)
	}
	if created.MaxNotificationsPerMessage != 25 {
		t.Fatalf("maxNotificationsPerMessage = %d, want 25", created.MaxNotificationsPerMessage)
	}
	// The key is stored but never echoed.
	if created.EncryptionKey != "*Hidden*" {
		t.Fatalf("encryptionKey = %q, want *Hidden* -- the key must never be returned", created.EncryptionKey)
	}
	if created.RowVersion == "" {
		t.Fatal("create returned no rowVersion; every mutation needs one to send back as If-Match")
	}

	// Endpoints are unique across subscriptions.
	resp = authed("POST", "/api/v1/notificationselfservice/subscription",
		`{"endpoint":"https://localhost/hook","status":2,"encryptionKey":"`+key32+`"}`)
	resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Fatalf("duplicate endpoint = %d, want 409", resp.StatusCode)
	}

	// Adding an event needs a target type, and the same event type cannot
	// be added twice.
	resp = authed("POST", "/api/v1/notificationselfservice/subscriptionEvent",
		`{"subscriptionId":"`+created.ID+`","eventType":"OutgoingPaymentProcessed","targetType":1}`)
	var ev bankingcircle.SubscriptionEvent
	if err := json.NewDecoder(resp.Body).Decode(&ev); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || ev.ID == "" {
		t.Fatalf("create subscriptionEvent: status=%d body=%+v", resp.StatusCode, ev)
	}
	resp = authed("POST", "/api/v1/notificationselfservice/subscriptionEvent",
		`{"subscriptionId":"`+created.ID+`","eventType":"OutgoingPaymentProcessed","targetType":1}`)
	resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Fatalf("duplicate eventType on one subscription = %d, want 409", resp.StatusCode)
	}

	// A mutation without a matching If-Match is refused. This is the whole
	// point of the header: a caller that has not read the current state
	// must not be able to write over it.
	resp = authedWith("PUT", "/api/v1/notificationselfservice/subscription/"+created.ID, `{"email":"new@example.com"}`, "")
	resp.Body.Close()
	if resp.StatusCode != 412 {
		t.Fatalf("update with no If-Match = %d, want 412", resp.StatusCode)
	}
	resp = authedWith("PUT", "/api/v1/notificationselfservice/subscription/"+created.ID, `{"email":"new@example.com"}`, "AAAAAAAAAAA=")
	resp.Body.Close()
	if resp.StatusCode != 412 {
		t.Fatalf("update with a stale If-Match = %d, want 412", resp.StatusCode)
	}

	// Re-read to get the current rowVersion, then update.
	resp = authed("GET", "/api/v1/notificationselfservice/subscription/"+created.ID, "")
	var fresh bankingcircle.PublicSubscription
	if err := json.NewDecoder(resp.Body).Decode(&fresh); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp = authedWith("PUT", "/api/v1/notificationselfservice/subscription/"+created.ID,
		`{"maxNotificationsPerMessage":100}`, fresh.RowVersion)
	var updated bankingcircle.PublicSubscription
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || updated.MaxNotificationsPerMessage != 100 {
		t.Fatalf("update: status=%d maxNotificationsPerMessage=%d", resp.StatusCode, updated.MaxNotificationsPerMessage)
	}
	if updated.RowVersion == fresh.RowVersion {
		t.Fatal("rowVersion did not change on update; a second writer could clobber this one")
	}

	// The token the update just consumed is now stale.
	resp = authedWith("PUT", "/api/v1/notificationselfservice/subscription/"+created.ID+"/deactivate", "", fresh.RowVersion)
	resp.Body.Close()
	if resp.StatusCode != 412 {
		t.Fatalf("deactivate with the pre-update rowVersion = %d, want 412", resp.StatusCode)
	}

	resp = authedWith("PUT", "/api/v1/notificationselfservice/subscription/"+created.ID+"/deactivate", "", updated.RowVersion)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("deactivate = %d, want 200", resp.StatusCode)
	}

	resp = authed("GET", "/api/v1/notificationselfservice/subscription/"+created.ID, "")
	var deactivated bankingcircle.PublicSubscription
	if err := json.NewDecoder(resp.Body).Decode(&deactivated); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if deactivated.IsActive || deactivated.Status != bankingcircle.StatusInactive {
		t.Fatalf("after deactivate: isActive=%v status=%d", deactivated.IsActive, deactivated.Status)
	}

	resp = authedWith("DELETE", "/api/v1/notificationselfservice/subscription/"+created.ID, "", deactivated.RowVersion)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("delete = %d, want 200", resp.StatusCode)
	}
}

// --- the B4B bridge: full lifecycle end to end, including real webhook delivery ---

func TestInternalPaymentLifecycleAndWebhookDelivery(t *testing.T) {
	a := newTestApp(t)
	receiver, deliveries := captureSubscriber(t)
	sub := subscribeAll(t, a, receiver.URL)
	stream := &notifStream{t: t, ch: deliveries, key: a.notifKey}

	internalSrv := httptest.NewServer(a.internalMux())
	defer internalSrv.Close()

	// The SGA starts at zero -- fund it first via the "Worldline lump sum
	// landed" trigger, same as any real flow through this mock must, and
	// drain its notifications before the outgoing payment under test.
	fundBody := `{"currency":"EUR","amount":"1000.00","reference":"worldline-lump-sum-test"}`
	fundResp, err := http.Post(internalSrv.URL+"/internal/incoming-payments", "application/json", strings.NewReader(fundBody))
	if err != nil {
		t.Fatal(err)
	}
	fundResp.Body.Close()
	if fundResp.StatusCode != 202 {
		t.Fatalf("create incoming payment = %d, want 202", fundResp.StatusCode)
	}
	stream.expect(bankingcircle.NotificationIncomingPaymentProcessed)
	if sga, err := a.ledger.Get(sgaEUR); err != nil || sga.Balance != "1000.00" {
		t.Fatalf("sga balance after funding = %+v, err=%v", sga, err)
	}

	createBody := `{"paymentId":"bcp_lifecycle1","accountId":"` + merchantAccount + `","amount":"123.45","currency":"EUR","externalRef":"settle_42"}`
	resp, err := http.Post(internalSrv.URL+"/internal/payments", "application/json", strings.NewReader(createBody))
	if err != nil {
		t.Fatal(err)
	}
	var created bankingcircle.Payment
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("create internal payment = %d, want 202", resp.StatusCode)
	}
	if created.ID != "bcp_lifecycle1" {
		t.Fatalf("paymentId = %q, want bcp_lifecycle1 (must not be mutated)", created.ID)
	}
	if created.SettlementID != "settle_42" {
		t.Fatalf("SettlementID = %q, want externalRef passed through verbatim", created.SettlementID)
	}
	if created.FromAccountID != sgaEUR {
		t.Fatalf("FromAccountID = %q, want the EUR safeguarding account", created.FromAccountID)
	}

	// Booked notification, delivered and decryptable.
	n := stream.expect(bankingcircle.NotificationOutgoingPaymentBooked)
	if n.SubscriptionID != sub.ID {
		t.Fatalf("subscriptionId = %s, want %s", n.SubscriptionID, sub.ID)
	}
	if n.SubscriptionEventID == "" {
		t.Fatal("notification carries no subscriptionEventId; a client cannot tell which of its events fired")
	}
	pay := paymentOf(t, n)
	if pay.PaymentID != "bcp_lifecycle1" {
		t.Fatalf("payment = %+v", pay)
	}
	if pay.CreditorInformation == nil || pay.CreditorInformation.AccountID != merchantAccount {
		t.Fatalf("creditorInformation = %+v", pay.CreditorInformation)
	}
	if pay.DebtorInformation == nil || pay.DebtorInformation.AccountID != sgaEUR {
		t.Fatalf("debtorInformation = %+v", pay.DebtorInformation)
	}

	// Processed notification follows after PROCESSING_DELAY.
	stream.expect(bankingcircle.NotificationOutgoingPaymentProcessed)

	merchant, _ := a.ledger.Get(merchantAccount)
	if merchant.Balance != "123.45" {
		t.Fatalf("merchant balance = %s, want 123.45", merchant.Balance)
	}

	// Reverse via the internal bridge's manual test hook.
	resp, err = http.Post(internalSrv.URL+"/internal/payments/bcp_lifecycle1/reverse", "application/json", strings.NewReader(`{"reason":"lab test"}`))
	if err != nil {
		t.Fatal(err)
	}
	var reversed bankingcircle.Payment
	if err := json.NewDecoder(resp.Body).Decode(&reversed); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("reverse = %d, want 200", resp.StatusCode)
	}
	if reversed.State != bankingcircle.NotificationReversed {
		t.Fatalf("state after reverse = %s, want Reversed", reversed.State)
	}

	stream.expect(bankingcircle.NotificationReversed)

	merchant, _ = a.ledger.Get(merchantAccount)
	if merchant.Balance != "0.00" {
		t.Fatalf("merchant balance after reversal = %s, want 0.00", merchant.Balance)
	}

	// Reversing again must fail: no longer Processed.
	resp, err = http.Post(internalSrv.URL+"/internal/payments/bcp_lifecycle1/reverse", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Fatalf("second reverse = %d, want 409", resp.StatusCode)
	}
}

func TestInternalPaymentAutoVivifiesUnknownCreditorAccount(t *testing.T) {
	a := newTestApp(t)
	srv := httptest.NewServer(a.internalMux())
	defer srv.Close()

	if _, err := a.ledger.Get(brandNewAccount); err != bankingcircle.ErrAccountNotFound {
		t.Fatalf("account should not exist before the payment: err = %v", err)
	}

	resp, err := http.Post(srv.URL+"/internal/payments", "application/json",
		strings.NewReader(`{"paymentId":"bcp_x","accountId":"`+brandNewAccount+`","amount":"10.00","currency":"EUR"}`))
	if err != nil {
		t.Fatal(err)
	}
	var created bankingcircle.Payment
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("status = %d, want 202 (unknown creditor accounts auto-vivify, no more 404)", resp.StatusCode)
	}
	if created.ToAccountID != brandNewAccount {
		t.Fatalf("toAccountId = %q, want the derived account", created.ToAccountID)
	}

	acc, err := a.ledger.Get(brandNewAccount)
	if err != nil {
		t.Fatalf("account should have been auto-vivified: %v", err)
	}
	if acc.Currency != "EUR" {
		t.Fatalf("vivified account currency = %s, want EUR", acc.Currency)
	}
}

func TestInternalPaymentRejectsUnconfiguredCurrency(t *testing.T) {
	a := newTestApp(t)
	srv := httptest.NewServer(a.internalMux())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/internal/payments", "application/json",
		strings.NewReader(`{"paymentId":"bcp_y","accountId":"`+someoneAccount+`","amount":"10.00","currency":"USD"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400 (no USD safeguarding account configured)", resp.StatusCode)
	}
}

// --- mTLS listener: proves the real production wiring, not just handler logic ---

func generateTestCA(t *testing.T) (caCertPEM, caKeyPEM []byte, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, cert, key
}

func generateTestLeaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, serial int64, cn string, isServer bool) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if isServer {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		tmpl.DNSNames = []string{"localhost"}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

func TestMTLSListenerRequiresClientCert(t *testing.T) {
	dir := t.TempDir()
	caCertPEM, _, caCert, caKey := generateTestCA(t)
	serverCertPEM, serverKeyPEM := generateTestLeaf(t, caCert, caKey, 2, "banking-circle-test", true)
	clientCertPEM, clientKeyPEM := generateTestLeaf(t, caCert, caKey, 3, "buddy-test-client", false)

	caFile := filepath.Join(dir, "ca.pem")
	certFile := filepath.Join(dir, "server.pem")
	keyFile := filepath.Join(dir, "server-key.pem")
	if err := os.WriteFile(caFile, caCertPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certFile, serverCertPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, serverKeyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	ln, err := tlsListener("127.0.0.1:0", certFile, keyFile, caFile, "require")
	if err != nil {
		t.Fatalf("tlsListener: %v", err)
	}
	defer ln.Close()

	a := newTestApp(t)
	go http.Serve(ln, a.mux())
	addr := ln.Addr().String()

	serverPool := x509.NewCertPool()
	serverPool.AppendCertsFromPEM(caCertPEM)

	// No client cert at all: TLS handshake must fail.
	noCertClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs: serverPool,
		}},
	}
	if _, err := noCertClient.Get("https://" + addr + "/health"); err == nil {
		t.Fatal("request without client cert should fail the mTLS handshake, but succeeded")
	}

	// Valid client cert signed by the trusted CA: succeeds.
	clientCert, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	okClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs:      serverPool,
			Certificates: []tls.Certificate{clientCert},
		}},
	}
	resp, err := okClient.Get("https://" + addr + "/health")
	if err != nil {
		t.Fatalf("request with valid client cert should succeed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestPauseQueuesNotificationsAndResumeReleasesThem drives the console's
// pause switch over HTTP, end to end: a real payment, a real subscriber, a
// real encrypted delivery — stopped, held where it can be seen, and let
// go.
//
// The assertion that matters is the middle one. A pause that simply
// dropped notifications, or one that stopped the engine producing them,
// would pass a test that only checked "nothing arrived at the endpoint" —
// and would be a different, much less useful thing than a subscriber that
// is temporarily not being called.
func TestPauseQueuesNotificationsAndResumeReleasesThem(t *testing.T) {
	a := newTestApp(t)
	receiver, deliveries := captureSubscriber(t)
	sub := subscribeAll(t, a, receiver.URL)
	stream := &notifStream{t: t, ch: deliveries, key: a.notifKey}

	srv := httptest.NewServer(a.mux())
	defer srv.Close()
	internalSrv := httptest.NewServer(a.internalMux())
	defer internalSrv.Close()

	tok := a.tokens.issue(time.Hour)
	sim := func(method, path string) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s %s = %d, want 200", method, path, resp.StatusCode)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	paused := sim("POST", "/sim/subscription/"+sub.ID+"/pause")
	if paused["paused"] != true {
		t.Fatalf("pause answered %+v", paused)
	}

	// A real event, through the real bridge: money into the safeguarding
	// account produces an IncomingPaymentProcessed notification.
	fund := `{"currency":"EUR","amount":"1000.00","reference":"paused-run"}`
	resp, err := http.Post(internalSrv.URL+"/internal/incoming-payments", "application/json", strings.NewReader(fund))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("fund = %d, want 202", resp.StatusCode)
	}

	// Long enough that an unpaused delivery would have landed several
	// times over: the flush interval is 5ms.
	select {
	case got := <-deliveries:
		t.Fatalf("a paused subscription was delivered to anyway (%d bytes)", len(got.body))
	case <-time.After(150 * time.Millisecond):
	}

	// Funding the safeguarding account is a two-notification event --
	// booked, then processed -- and both of them waited.
	pending := sim("GET", "/sim/subscription/"+sub.ID+"/pending")
	if pending["paused"] != true || pending["queued"] != float64(2) {
		t.Fatalf("pending reports %+v, want paused with 2 queued -- a paused notification waits, it is not dropped", pending)
	}
	// And it is visible: an operator watching the log sees the event exists
	// and is going nowhere, which is the entire point of pausing rather
	// than pulling the endpoint down.
	last := a.notifLog.Snapshot(1).Last
	if last == nil || last.Op != "notification.queued" || last.Status != activity.StatusWarn {
		t.Fatalf("notification log's last entry = %+v, want a queued line", last)
	}
	if !strings.Contains(last.Summary, "IncomingPayment") {
		t.Fatalf("queued line does not name the event type: %q", last.Summary)
	}

	released := sim("POST", "/sim/subscription/"+sub.ID+"/resume")
	if released["released"] != float64(2) {
		t.Fatalf("resume answered %+v, want 2 released", released)
	}
	// The same notifications, encrypted and delivered exactly as they would
	// have been had nobody paused anything, oldest first.
	if n := stream.next(); n.NotificationType != string(bankingcircle.NotificationIncomingPaymentBooked) {
		t.Fatalf("first released notification is %s, want the booked one -- a catch-up replays the queue in order", n.NotificationType)
	}
	stream.expect(bankingcircle.NotificationIncomingPaymentProcessed)

	pending = sim("GET", "/sim/subscription/"+sub.ID+"/pending")
	if pending["paused"] != false || pending["queued"] != float64(0) {
		t.Fatalf("after resume: %+v", pending)
	}
}

// TestPauseOnAnUnknownSubscriptionIs404. The console renders the button
// from a list it polled; by the time somebody clicks, the subscription may
// be gone. Answering 404 is what lets the UI say so rather than report a
// pause that paused nothing.
func TestPauseOnAnUnknownSubscriptionIs404(t *testing.T) {
	a := newTestApp(t)
	srv := httptest.NewServer(a.mux())
	defer srv.Close()

	for _, path := range []string{"/sim/subscription/sub_nope/pause", "/sim/subscription/sub_nope/resume"} {
		req, _ := http.NewRequest("POST", srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+a.tokens.issue(time.Hour))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("POST %s = %d, want 404", path, resp.StatusCode)
		}
	}
}
