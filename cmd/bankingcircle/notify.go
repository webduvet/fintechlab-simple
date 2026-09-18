package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
	"unicode/utf16"

	"github.com/webduvet/fintechlab-simple/internal/bankingcircle"
	"github.com/webduvet/fintechlab-simple/internal/httputilx"
)

// notificationPayment is the "payment" object inside a notification.
type notificationPayment struct {
	PaymentID            string              `json:"paymentId,omitempty"`
	TransactionReference string              `json:"transactionReference,omitempty"`
	Status               string              `json:"status,omitempty"`
	Amount               *notificationAmount `json:"amount,omitempty"`
	CreditorInformation  *notificationParty  `json:"creditorInformation,omitempty"`
	DebtorInformation    *notificationParty  `json:"debtorInformation,omitempty"`
}

type notificationAmount struct {
	Amount   string `json:"amount,omitempty"`
	Currency string `json:"currency,omitempty"`
}

type notificationParty struct {
	AccountID string `json:"accountId,omitempty"`
}

// onTransition fires on every notification the engine produces.
//
// It routes by subscribed event type and target rather than fanning out to
// every active subscription. That is the behaviour a client is written
// against: it asks for IncomingPaymentProcessed on one account and expects
// not to be handed every outgoing payment in the system. Fanning out to
// everyone made the target and event-type configuration decorative, so a
// client with a broken subscription looked like a working one.
//
// It never blocks the engine: the dispatcher batches and delivers on its
// own goroutines, so a down or slow subscriber never stalls payments.
func (a *app) onTransition(p *bankingcircle.Payment) {
	eventType := string(p.State)

	// A payout that leaves the safeguarding account has to arrive
	// somewhere. Notifying a subscriber that a payment processed is not
	// the same statement as the beneficiary's bank holding the money, and
	// a lab that only did the first teaches that the two are one thing.
	if p.State == bankingcircle.NotificationOutgoingPaymentProcessed {
		go a.creditBeneficiaryBank(*p)
	}

	// The accounts this payment touches are the targets a subscription may
	// have scoped itself to.
	recipients := a.subs.Recipients(eventType, p.ToAccountID, p.FromAccountID)
	if len(recipients) == 0 {
		log.Printf("banking-circle: %s for payment %s matched no active subscription", eventType, p.ID)
		return
	}
	detail := &notificationPayment{
		PaymentID:            p.ID,
		TransactionReference: p.Reference,
		Status:               eventType,
		Amount:               &notificationAmount{Amount: p.Amount, Currency: p.Currency},
		CreditorInformation:  &notificationParty{AccountID: p.ToAccountID},
		DebtorInformation:    &notificationParty{AccountID: p.FromAccountID},
	}
	for _, rec := range recipients {
		a.dispatch.Enqueue(rec.Subscription, newNotification(rec, eventType, detail))
	}
}

// newNotification builds one envelope entry, putting the detail under the
// property the event type actually uses.
func newNotification(rec bankingcircle.Recipient, eventType string, detail any) bankingcircle.Notification {
	n := bankingcircle.Notification{
		EventID:             "evt_" + shortID(),
		SubscriptionID:      rec.Subscription.ID,
		SubscriptionEventID: rec.Event.ID,
		NotificationType:    eventType,
		Timestamp:           time.Now().UTC().Format(time.RFC3339),
	}
	if bankingcircle.UsesPayloadProperty(eventType) {
		n.Payload = detail
	} else {
		n.Payment = detail
	}
	return n
}

// clientTest implements POST /api/v1/notificationselfservice/clienttest/{id}
// -- a real sandbox feature: it sends a synthetic notification through the
// exact same encrypted delivery pipe every other notification uses, so a
// developer can confirm their endpoint is reachable without waiting for a
// real payment.
//
// It flushes immediately rather than waiting for the batch timer, because
// the whole point is an answer now.
func (a *app) clientTest(w http.ResponseWriter, r *http.Request) {
	sub, err := a.subs.Get(r.PathValue("subscriptionId"))
	if err != nil {
		writeSubscriptionError(w, err)
		return
	}
	eventType := string(bankingcircle.NotificationPaymentStatus)
	// A clienttest fires against the subscription itself, so it is not
	// filtered by event type or target the way a real event is -- it is a
	// reachability check, not an event.
	rec := bankingcircle.Recipient{Subscription: sub, Event: syntheticEvent(sub, eventType)}
	detail := map[string]any{
		"EndToEndId":        "clienttest_" + shortID(),
		"TransactionStatus": "PendingProcessing",
	}
	a.dispatch.Enqueue(sub, newNotification(rec, eventType, detail))
	a.dispatch.Flush(sub.ID)
	httputilx.WriteJSON(w, 200, map[string]any{
		"status":         "sent",
		"subscriptionId": sub.ID,
		"eventType":      eventType,
	})
}

// syntheticEvent returns the subscription's own event for eventType if it
// has one, so a clienttest carries a real subscriptionEventId, and a
// stand-in otherwise.
func syntheticEvent(sub *bankingcircle.Subscription, eventType string) *bankingcircle.SubscriptionEvent {
	for _, e := range sub.Events {
		if e.EventType == eventType {
			return e
		}
	}
	return &bankingcircle.SubscriptionEvent{ID: "subev_clienttest", EventType: eventType, SubscriptionID: sub.ID}
}

// sendEncrypted is the dispatcher's Send: marshal the envelope, encrypt it
// with this subscription's own key, and POST it.
//
// The key is per subscription, not per server. A client that changed its
// encryption key with a PUT and kept decrypting successfully was being
// told a lie about its own configuration.
func (a *app) sendEncrypted(sub *bankingcircle.Subscription, env bankingcircle.Envelope) error {
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal notification batch: %w", err)
	}
	key := []byte(sub.EncryptionKey)
	if len(key) != bankingcircle.EncryptionKeyLength {
		return fmt.Errorf("subscription %s has a %d-character encryption key, need %d",
			sub.ID, len(key), bankingcircle.EncryptionKeyLength)
	}
	ciphertext, nonce, tag, checksum, err := encryptNotification(body, key)
	if err != nil {
		return fmt.Errorf("encrypt notification: %w", err)
	}
	_, err = a.postNotification(sub.Endpoint, ciphertext, nonce, tag, checksum, sub.Version)
	return err
}

// encryptNotification implements Banking Circle's real webhook encryption,
// exactly:
//  1. checksum = SHA-256 of the original JSON string's UTF-8 bytes.
//  2. UTF-16LE-encode that JSON string.
//  3. AES-256-GCM-encrypt the UTF-16LE bytes with a random 12-byte nonce
//     and key = the raw UTF-8 bytes of the 32-character configured key
//     (NOT base64-decoded).
//  4. Split Seal's output into ciphertext and the trailing 16-byte auth
//     tag -- real BC's wire format keeps them separate, unlike Go's GCM
//     default.
func encryptNotification(jsonBody, key []byte) (ciphertext, nonce, tag, checksum []byte, err error) {
	sum := sha256.Sum256(jsonBody)
	checksum = sum[:]

	utf16le := utf16LEBytes(string(jsonBody))

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, nil, nil, err
	}
	sealed := gcm.Seal(nil, nonce, utf16le, nil)
	tagLen := gcm.Overhead()
	ciphertext = sealed[:len(sealed)-tagLen]
	tag = sealed[len(sealed)-tagLen:]
	return ciphertext, nonce, tag, checksum, nil
}

func utf16LEBytes(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(out[i*2:], u)
	}
	return out
}

func (a *app) postNotification(dest string, ciphertext, nonce, tag, checksum []byte, version int) (int, error) {
	if err := a.list.Allowed(dest); err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, dest, bytes.NewReader(ciphertext))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Nonce", base64.StdEncoding.EncodeToString(nonce))
	req.Header.Set("AuthenticationTag", base64.StdEncoding.EncodeToString(tag))
	req.Header.Set("Checksum", base64.StdEncoding.EncodeToString(checksum))
	req.Header.Set("SubscriptionVersion", strconv.Itoa(version))
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("notification: destination returned %s", resp.Status)
	}
	return resp.StatusCode, nil
}

// creditBeneficiaryBank posts an arriving payment to the receiving bank.
//
// Fire-and-forget on its own goroutine, and a failure is logged rather than
// retried: this hop stands in for an interbank rail, and modelling its
// retry semantics would be inventing a protocol rather than simulating one.
// What matters here is that the money is observable on the other side.
//
// Disabled when BANK_CREDIT_URL is empty, so a stack without a beneficiary
// bank behaves exactly as it did before.
func (a *app) creditBeneficiaryBank(p bankingcircle.Payment) {
	if a.bankCreditURL == "" {
		return
	}
	// The rail names the beneficiary by IBAN. Without one there is nothing
	// for the receiving bank to open an account against, and inventing an
	// identifier here would put money into an account nobody can reconcile.
	iban := p.ToIBAN
	if iban == "" {
		log.Printf("banking-circle: payment %s has no beneficiary IBAN; not crediting the beneficiary bank", p.ID)
		return
	}
	if err := a.list.Allowed(a.bankCreditURL); err != nil {
		log.Printf("banking-circle: beneficiary bank %s not allowlisted: %v", a.bankCreditURL, err)
		return
	}
	body, err := json.Marshal(map[string]any{
		"iban":      iban,
		"holder":    p.ToHolder,
		"amount":    p.Amount,
		"currency":  p.Currency,
		"reference": firstNonEmpty(p.Reference, p.SettlementID, p.ID),
	})
	if err != nil {
		log.Printf("banking-circle: credit payload for %s: %v", p.ID, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.bankCreditURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("banking-circle: credit request for %s: %v", p.ID, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("banking-circle: crediting beneficiary bank for %s: %v", p.ID, err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		log.Printf("banking-circle: beneficiary bank returned %s for payment %s", resp.Status, p.ID)
		return
	}
	log.Printf("banking-circle: %s %s credited to %s at the beneficiary bank", p.Amount, p.Currency, iban)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
