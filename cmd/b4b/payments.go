package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/webduvet/fintechlab-simple/internal/b4b"
	"github.com/webduvet/fintechlab-simple/internal/httputilx"
	"github.com/webduvet/fintechlab-simple/internal/money"
)

// The payout half: create a payment, walk it through the regulatory
// lifecycle, call back on every phase change, and bridge into Banking
// Circle once approved.

// The B4B Oversight wire shapes, byte-exact: amounts are JSON numbers,
// not strings, and account references carry an optional country.
type amountWire struct {
	Amount   json.Number `json:"amount"`
	Currency string      `json:"currency"`
}

type accountRefWire struct {
	Account              string `json:"account"`
	FinancialInstitution string `json:"financialInstitution,omitempty"`
	Country              string `json:"country,omitempty"`
}

// remittanceWire is the reference shown to the beneficiary, and the field
// transaction monitoring reads. A payment without it still goes through --
// it is not required -- but a client that omits it has given the payee
// nothing to reconcile against.
type remittanceWire struct {
	Line1 string `json:"line1,omitempty"`
}

type createPaymentReq struct {
	ExternalRef            string          `json:"external_ref"`
	BeneficiaryID          string          `json:"beneficiary_id"`
	CompanyID              string          `json:"company_id"`
	CallbackURL            string          `json:"callback_url"`
	SCAApplied             bool            `json:"sca_applied"`
	Amount                 amountWire      `json:"amount"`
	CurrencyOfTransfer     string          `json:"currencyOfTransfer"`
	DebtorViban            accountRefWire  `json:"debtorViban"`
	DebtorAccount          *accountRefWire `json:"debtorAccount,omitempty"`
	CreditorAccount        accountRefWire  `json:"creditorAccount"`
	CreditorName           string          `json:"creditorName"`
	ChargeBearer           string          `json:"chargeBearer"`
	RequestedExecutionDate string          `json:"requestedExecutionDate"`
	RemittanceInformation  *remittanceWire `json:"remittanceInformation,omitempty"`
}

type paymentPayload struct {
	Amount                amountWire      `json:"amount"`
	CurrencyOfTransfer    string          `json:"currencyOfTransfer"`
	DebtorViban           accountRefWire  `json:"debtorViban"`
	CreditorAccount       accountRefWire  `json:"creditorAccount"`
	CreditorName          string          `json:"creditorName"`
	RemittanceInformation *remittanceWire `json:"remittanceInformation,omitempty"`
	DebtorReference       string          `json:"debtorReference,omitempty"`
}

type createPaymentResp struct {
	ID      string         `json:"id"`
	Status  string         `json:"status"`
	Payload paymentPayload `json:"payload"`
}

func payloadOf(p *b4b.Payment) paymentPayload {
	pl := paymentPayload{
		Amount:             amountWire{Amount: json.Number(p.Amount.Amount), Currency: p.Amount.Currency},
		CurrencyOfTransfer: p.CurrencyOfTransfer,
		DebtorViban:        accountRefWire(p.DebtorViban),
		CreditorAccount:    accountRefWire(p.CreditorAccount),
		CreditorName:       p.CreditorName,
		DebtorReference:    p.DebtorReference,
	}
	if p.RemittanceInformation != "" {
		pl.RemittanceInformation = &remittanceWire{Line1: p.RemittanceInformation}
	}
	return pl
}

// getPayment implements GET /oversight/v1/payments/{id}.
//
// Oversight's payment callbacks are unsigned, unordered, may be delivered
// more than once, and have no replay endpoint. Reading the current state
// is the documented fallback for a client that missed one, and a
// simulator without it forces every client to be written as if callbacks
// were reliable -- which they are not.
func (a *app) getPayment(w http.ResponseWriter, r *http.Request) {
	p, err := a.engine.Get(r.PathValue("id"))
	if err != nil {
		httputilx.Error(w, 404, "payment not found")
		return
	}
	httputilx.WriteJSON(w, 200, map[string]any{
		"id":           p.ID,
		"status":       string(p.State),
		"external_ref": p.ExternalRef,
		"payload":      payloadOf(p),
	})
}

func (a *app) createPayment(w http.ResponseWriter, r *http.Request) {
	var req createPaymentReq
	if _, err := httputilx.ReadJSONExtras(r, &req); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	if req.BeneficiaryID == "" {
		httputilx.Error(w, 400, "beneficiary_id required")
		return
	}
	// callback_url is a lab affordance, not a wire field: the real API
	// posts to one endpoint configured for the client account. Supplied,
	// it must pass the allowlist; omitted, this payment falls back to
	// B4B_CALLBACK_URL like every other callback here.
	if req.CallbackURL != "" {
		if err := b4b.ValidateCallbackURL(a.list, req.CallbackURL); err != nil {
			httputilx.Error(w, 400, "callback_url rejected: "+err.Error())
			return
		}
	} else if a.callbackURL == "" {
		log.Printf("b4b: payment for beneficiary %s has no callback_url and no B4B_CALLBACK_URL is configured; its lifecycle callbacks will go nowhere",
			req.BeneficiaryID)
	}
	if _, err := money.Parse(string(req.Amount.Amount)); err != nil {
		httputilx.Error(w, 400, "amount.amount must be a decimal number")
		return
	}
	if req.CurrencyOfTransfer == "" {
		httputilx.Error(w, 400, "currencyOfTransfer required")
		return
	}
	if req.DebtorViban.Account == "" {
		httputilx.Error(w, 400, "debtorViban.account required")
		return
	}
	if req.CreditorAccount.Account == "" {
		httputilx.Error(w, 400, "creditorAccount.account required")
		return
	}
	// SHA, BEN or OUR -- the ISO 20022 values the vendor documents. Not
	// DEBT/CRED, which appear in at least one client's own type and in no
	// specification: a mock that accepted them would confirm a mistake
	// rather than catch it.
	switch req.ChargeBearer {
	case "SHA", "BEN", "OUR":
	default:
		httputilx.Error(w, 400, "chargeBearer must be one of SHA, BEN, OUR")
		return
	}

	// The gates. All answer 422 and forward nothing: creditor fields check
	// the beneficiary, they do not override it, and accepting a mismatch
	// silently is how money reaches the wrong account.
	if a.gateBeneficiary(w, req) == nil {
		return
	}

	remittance := ""
	if req.RemittanceInformation != nil {
		remittance = req.RemittanceInformation.Line1
	}
	p := &b4b.Payment{
		ID:                     "b4bp_" + shortID(),
		ExternalRef:            req.ExternalRef,
		BeneficiaryID:          req.BeneficiaryID,
		CompanyID:              req.CompanyID,
		CallbackURL:            a.callbackTarget(req.CallbackURL),
		SCAApplied:             req.SCAApplied,
		Amount:                 b4b.Amount{Amount: string(req.Amount.Amount), Currency: req.Amount.Currency},
		CurrencyOfTransfer:     req.CurrencyOfTransfer,
		DebtorViban:            b4b.AccountRef(req.DebtorViban),
		CreditorAccount:        b4b.AccountRef(req.CreditorAccount),
		CreditorName:           req.CreditorName,
		ChargeBearer:           req.ChargeBearer,
		RequestedExecutionDate: req.RequestedExecutionDate,
		RemittanceInformation:  remittance,
		DebtorReference:        "B4BREF" + shortID(),
		ForceFail:              a.forceFails[req.BeneficiaryID],
	}
	if req.DebtorAccount != nil {
		ref := b4b.AccountRef(*req.DebtorAccount)
		p.DebtorAccount = &ref
	}
	a.engine.Submit(p)

	httputilx.WriteJSON(w, 202, createPaymentResp{
		ID:      p.ID,
		Status:  string(b4b.StateAccepted),
		Payload: payloadOf(p),
	})
}

// onTransition fires on every B4B payout lifecycle transition. It never
// blocks the engine: the Banking Circle bridge call (StateTMApproved only)
// and webhook delivery (its own retry/backoff loop) both run in their own
// goroutine, so a down or slow callback_url/Banking Circle never stalls
// payment processing.
//
// It also persists. A payment's state is the one piece of this service's
// memory that changes without anybody asking it to, so it is the one that
// most needs writing down before the process can die.
func (a *app) onTransition(p *b4b.Payment) {
	a.persist()
	if p.State == b4b.StateTMApproved {
		go a.approveAndBridge(p)
		return
	}
	go a.deliverPaymentCallback(p, nil)
}

// approveAndBridge generates this payment's Banking Circle paymentId and
// calls Banking Circle's internal bridge endpoint (docs/
// ARCHITECTURE-vendor-corrections.md section 3's B4B<->BC bridge, amended
// by Addendum section B to carry externalRef). The bridge call is
// best-effort and non-fatal to B4B's own webhook: on success or failure it
// still fires the B4BTMApproved webhook, with
// banking_circle_api_response.paymentId always set on this one transition.
func (a *app) approveAndBridge(p *b4b.Payment) {
	bcPaymentID := "bcp_" + shortID()
	bcResp := map[string]any{"paymentId": bcPaymentID}

	reqBody, err := json.Marshal(map[string]any{
		"paymentId":   bcPaymentID,
		"accountId":   "bc_acc_" + p.BeneficiaryID,
		"amount":      p.Amount.Amount,
		"currency":    p.CurrencyOfTransfer,
		"externalRef": p.ExternalRef,
	})
	if err != nil {
		log.Printf("b4b: marshal banking circle bridge request for payment %s: %v", p.ID, err)
		bcResp["error"] = err.Error()
		a.deliverPaymentCallback(p, bcResp)
		return
	}
	req, err := http.NewRequest(http.MethodPost, a.bcURL+"/internal/payments", bytes.NewReader(reqBody))
	if err != nil {
		log.Printf("b4b: build banking circle bridge request for payment %s: %v", p.ID, err)
		bcResp["error"] = err.Error()
		a.deliverPaymentCallback(p, bcResp)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("b4b: banking circle bridge call failed for payment %s: %v; continuing, own webhook still fires", p.ID, err)
		bcResp["error"] = err.Error()
		a.deliverPaymentCallback(p, bcResp)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		log.Printf("b4b: banking circle bridge call for payment %s returned %s; continuing, own webhook still fires", p.ID, resp.Status)
		bcResp["error"] = fmt.Sprintf("banking circle returned %s", resp.Status)
	} else {
		log.Printf("b4b: banking circle bridge call for payment %s ok, bcPaymentId=%s", p.ID, bcPaymentID)
	}
	a.deliverPaymentCallback(p, bcResp)
}

// deliverPaymentCallback POSTs the lifecycle callback for one transition.
func (a *app) deliverPaymentCallback(p *b4b.Payment, bcResp map[string]any) {
	body := map[string]any{
		"id":      p.ID,
		"status":  string(p.State),
		"payload": payloadOf(p),
	}
	if bcResp != nil {
		body["banking_circle_api_response"] = bcResp
	}
	a.deliverCallback(fmt.Sprintf("payment %s status=%s", p.ID, p.State), p.CallbackURL, body)
}
