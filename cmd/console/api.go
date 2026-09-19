package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/webduvet/fintechlab-simple/internal/bankingcircle"
	"github.com/webduvet/fintechlab-simple/internal/console"

	"github.com/webduvet/fintechlab-simple/internal/httputilx"
)

// Every handler here is a thin translation between the UI and a service's
// own API. Where a call to a peer fails, the peer's own message is passed
// through rather than flattened into "something went wrong" -- the whole
// value of a control panel over a terminal is that the error arrives next
// to the thing that caused it.

const callTimeout = 15 * time.Second

// reqContext bounds a call to a peer service so a hung vendor cannot hold
// a console request open indefinitely, and cancels it if the operator
// navigates away mid-click.
func reqContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), callTimeout)
}

// --- services ---------------------------------------------------------

type serviceView struct {
	console.Service
	Status console.Status `json:"status"`
}

func (a *app) overview(w http.ResponseWriter, r *http.Request) {
	out := make([]serviceView, 0, len(a.cat.Services))
	counts := map[string]int{}
	for _, s := range a.cat.Services {
		st, _ := a.mon.Get(s.ID)
		counts[st.State]++
		out = append(out, serviceView{Service: s, Status: st})
	}
	httputilx.WriteJSON(w, 200, map[string]any{
		"services":           out,
		"counts":             counts,
		"provisioning_ready": a.prov.Ready(),
		"provisioning_error": a.provErr,
	})
}

func (a *app) probeService(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqContext(r)
	defer cancel()
	st, ok := a.mon.Probe(ctx, r.PathValue("id"))
	if !ok {
		httputilx.Error(w, 404, "unknown service "+r.PathValue("id"))
		return
	}
	httputilx.WriteJSON(w, 200, st)
}

// --- pods -------------------------------------------------------------

// listPods returns every recipe in the configured directory, enriched with
// the live schematic of any pod that answers.
//
// A missing recipes directory is reported as `enabled: false` rather than
// as an error: the pod architecture sits alongside the standalone services
// and a checkout without it must still run the rest of the console.

// --- banks ------------------------------------------------------------

func (a *app) listBanks(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqContext(r)
	defer cancel()
	type bankView struct {
		console.Bank
		Accounts []console.BankAccount `json:"accounts"`
		Error    string                `json:"error,omitempty"`
	}
	out := []bankView{}
	for _, meta := range a.banks.List() {
		be, _ := a.banks.Backend(meta.ID)
		v := bankView{Bank: meta, Accounts: []console.BankAccount{}}
		accts, err := be.Accounts(ctx)
		if err != nil {
			// A bank that is down is shown as a bank that is down, not
			// omitted -- an empty list would read as "no accounts".
			v.Error = err.Error()
		} else {
			v.Accounts = accts
		}
		out = append(out, v)
	}
	httputilx.WriteJSON(w, 200, map[string]any{"banks": out})
}

type openAccountReq struct {
	Holder         string `json:"holder"`
	Currency       string `json:"currency"`
	OpeningBalance string `json:"opening_balance"`
}

func (a *app) openAccount(w http.ResponseWriter, r *http.Request) {
	var req openAccountReq
	if err := httputilx.ReadJSON(r, &req); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	be, ok := a.banks.Backend(r.PathValue("id"))
	if !ok {
		httputilx.Error(w, 404, "unknown bank "+r.PathValue("id"))
		return
	}
	ctx, cancel := reqContext(r)
	defer cancel()
	acct, err := be.OpenAccount(ctx, req.Holder, req.Currency, req.OpeningBalance)
	if err != nil {
		// 501, not 500: the vendor genuinely has no such endpoint, and a
		// UI that cannot tell that from a failure will keep retrying.
		if errors.Is(err, console.ErrUnsupported) {
			httputilx.Error(w, 501, err.Error())
			return
		}
		httputilx.Error(w, 502, err.Error())
		return
	}
	httputilx.WriteJSON(w, 201, acct)
}

// --- merchants --------------------------------------------------------

func (a *app) listMerchants(w http.ResponseWriter, r *http.Request) {
	httputilx.WriteJSON(w, 200, map[string]any{
		"distributors":       a.reg.Distributors(),
		"partners":           a.reg.Partners(),
		"merchants":          a.reg.Merchants(),
		"countries":          console.Countries(),
		"provisioning_ready": a.prov.Ready(),
		"provisioning_error": a.provErr,
	})
}

func (a *app) createMerchant(w http.ResponseWriter, r *http.Request) {
	var p console.NewMerchantParams
	if err := httputilx.ReadJSON(r, &p); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	m, err := a.reg.AddMerchant(p)
	if err != nil {
		httputilx.Error(w, statusFor(err), err.Error())
		return
	}
	httputilx.WriteJSON(w, 201, m)
}

func (a *app) deleteMerchant(w http.ResponseWriter, r *http.Request) {
	if err := a.reg.DeleteMerchant(r.PathValue("id")); err != nil {
		httputilx.Error(w, statusFor(err), err.Error())
		return
	}
	httputilx.WriteJSON(w, 200, map[string]string{"deleted": r.PathValue("id")})
}

type addOutletReq struct {
	Name string `json:"name"`
}

func (a *app) addOutlet(w http.ResponseWriter, r *http.Request) {
	var req addOutletReq
	if err := httputilx.ReadJSON(r, &req); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	m, o, err := a.reg.AddOutlet(r.PathValue("id"), req.Name)
	if err != nil {
		httputilx.Error(w, statusFor(err), err.Error())
		return
	}
	httputilx.WriteJSON(w, 201, map[string]any{"merchant": m, "outlet": o})
}

type statusReq struct {
	Status string `json:"status"`
}

func (a *app) setMerchantStatus(w http.ResponseWriter, r *http.Request) {
	var req statusReq
	if err := httputilx.ReadJSON(r, &req); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	m, err := a.reg.SetStatus(r.PathValue("id"), req.Status)
	if err != nil {
		httputilx.Error(w, statusFor(err), err.Error())
		return
	}
	httputilx.WriteJSON(w, 200, m)
}

func (a *app) createPartner(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DistributorID string `json:"distributor_id"`
		Name          string `json:"name"`
		Country       string `json:"country"`
	}
	if err := httputilx.ReadJSON(r, &req); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	p, err := a.reg.AddPartner(req.DistributorID, req.Name, req.Country)
	if err != nil {
		httputilx.Error(w, statusFor(err), err.Error())
		return
	}
	httputilx.WriteJSON(w, 201, p)
}

func (a *app) provisionMerchant(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqContext(r)
	defer cancel()
	res, err := a.prov.ProvisionMerchant(ctx, r.PathValue("id"))
	if err != nil {
		httputilx.Error(w, statusFor(err), err.Error())
		return
	}
	// 200 even when some outlets failed: the per-outlet results carry the
	// detail, and a blanket 502 would hide the ones that worked.
	httputilx.WriteJSON(w, 200, map[string]any{"outlets": res})
}

func (a *app) seedTrading(w http.ResponseWriter, r *http.Request) {
	var tp console.TradeParams
	if err := httputilx.ReadJSON(r, &tp); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	ctx, cancel := reqContext(r)
	defer cancel()
	n, date, err := a.prov.SeedTrading(ctx, r.PathValue("id"), tp)
	if err != nil {
		httputilx.Error(w, statusFor(err), err.Error())
		return
	}
	httputilx.WriteJSON(w, 201, map[string]any{
		"accepted": n,
		"date":     date,
		"note":     "Worldline settles T+1: run the morning cycle to have these appear in a settlement file.",
	})
}

func (a *app) setSanctions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SanctionsStatus string `json:"sanctions_status"`
	}
	if err := httputilx.ReadJSON(r, &req); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	ctx, cancel := reqContext(r)
	defer cancel()
	if err := a.prov.SetSanctions(ctx, r.PathValue("mid"), req.SanctionsStatus); err != nil {
		httputilx.Error(w, statusFor(err), err.Error())
		return
	}
	httputilx.WriteJSON(w, 200, map[string]string{"mid": r.PathValue("mid"), "sanctions_status": req.SanctionsStatus})
}

// --- configuration ----------------------------------------------------

func (a *app) getConfig(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqContext(r)
	defer cancel()

	out := map[string]any{"groups": console.SettingGroups()}

	// Banking Circle's delivery table. Asked of the service first, because
	// BC_TIME_SCALE overrides the file: reading the file alone would show
	// a schedule nobody is on, which is exactly the kind of confidently
	// wrong number that costs an hour. The file is the fallback, labelled
	// as such.
	var live bankingcircle.DeliveryConfig
	if err := a.bc.AuthorizedGet(ctx, "/sim/delivery-config", &live); err == nil {
		out["banking_circle"] = live
		out["banking_circle_path"] = a.bcCfg
		out["banking_circle_source"] = "live from banking-circle (the file plus any BC_TIME_SCALE override)"
	} else if cfg, ferr := bankingcircle.LoadDeliveryConfig(a.bcCfg); ferr != nil {
		out["banking_circle_error"] = ferr.Error()
	} else {
		out["banking_circle"] = cfg
		out["banking_circle_path"] = a.bcCfg
		out["banking_circle_source"] = "the config file — banking-circle could not be asked (" + err.Error() + "), so BC_TIME_SCALE may override this"
		if _, serr := os.Stat(a.bcCfg); serr != nil {
			out["banking_circle_note"] = "file not readable from the console (" + serr.Error() + "); showing the built-in defaults"
		}
	}

	// Worldline's file-exchange channel config, which really is live and
	// really is editable.
	var ch map[string]any
	if err := a.getJSON(ctx, a.client, a.baseURL("worldline")+"/config", &ch); err != nil {
		out["worldline_channel_error"] = err.Error()
	} else {
		out["worldline_channel"] = ch
	}
	httputilx.WriteJSON(w, 200, out)
}

func (a *app) putWorldlineChannel(w http.ResponseWriter, r *http.Request) {
	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httputilx.Error(w, 400, "body must be JSON: "+err.Error())
		return
	}
	ctx, cancel := reqContext(r)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, a.baseURL("worldline")+"/config", strings.NewReader(string(body)))
	if err != nil {
		httputilx.Error(w, 500, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		httputilx.Error(w, 502, err.Error())
		return
	}
	defer resp.Body.Close()
	var out any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	httputilx.WriteJSON(w, resp.StatusCode, out)
}

// --- one-click actions ------------------------------------------------
//
// These drive the flow the docs describe, without asking an operator to
// remember two curl invocations. Each is exactly one call to a service's
// own endpoint.

func (a *app) runWorldlineCycle(w http.ResponseWriter, r *http.Request) {
	slot := r.URL.Query().Get("slot")
	if slot == "" {
		slot = "morning"
	}
	url := a.baseURL("worldline") + "/sim/settlement-cycle/run?slot=" + slot
	if d := r.URL.Query().Get("date"); d != "" {
		url += "&date=" + d
	}
	a.proxyPost(w, r, url)
}

func (a *app) settlementPull(w http.ResponseWriter, r *http.Request) {
	a.proxyPost(w, r, a.baseURL("settlement")+"/worldline/pull")
}

// runnerSettle asks the local runner to drive one settlement end to end.
//
// One button rather than four terminals, but it is still exactly one POST a
// human could make with curl: the runner owns the sequence — upload under a
// name not used before, trigger, wait for the balance check, fund, tick,
// follow the stages — because that sequence is the platform's, not the
// lab's. This console only asks.
func (a *app) runnerSettle(w http.ResponseWriter, r *http.Request) {
	a.proxyPost(w, r, a.baseURL("local-runner")+"/sim/run")
}

func (a *app) runnerFundSGA(w http.ResponseWriter, r *http.Request) {
	a.proxyPost(w, r, a.baseURL("local-runner")+"/sim/fund-sga")
}

func (a *app) proxyPost(w http.ResponseWriter, r *http.Request, url string) {
	ctx, cancel := reqContext(r)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		httputilx.Error(w, 500, err.Error())
		return
	}
	resp, err := a.client.Do(req)
	if err != nil {
		httputilx.Error(w, 502, err.Error())
		return
	}
	defer resp.Body.Close()
	var out any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		httputilx.Error(w, 502, fmt.Sprintf("%s answered HTTP %d with an unreadable body", url, resp.StatusCode))
		return
	}
	httputilx.WriteJSON(w, resp.StatusCode, out)
}

// --- helpers ----------------------------------------------------------

func (a *app) getJSON(ctx context.Context, c *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// statusFor maps a domain error onto an HTTP status. An unknown id is a
// 404, something the caller can fix is a 400, and everything else is a 502
// because the only remaining source of failure is a peer service.
func statusFor(err error) int {
	switch {
	case errors.Is(err, console.ErrNotFound):
		return 404
	case errors.Is(err, console.ErrInvalid):
		return 400
	case errors.Is(err, console.ErrUnsupported):
		return 501
	default:
		return 502
	}
}

// --- activity ---------------------------------------------------------

// serviceActivity proxies a service's recent-history endpoint.
//
// It is a proxy rather than a link because the console is the only thing
// on the page that can reach a vendor over mTLS, and because a browser
// fetching twelve origins directly would be a CORS problem the lab does
// not need to have.
//
// A service that is down produces a 502 carrying its own error, and the
// panel says so. That matters more here than anywhere else in the console:
// an empty activity list and an unreachable service look identical if the
// failure is swallowed, and one of them means "nothing has happened yet"
// while the other means "you are looking at a corpse".
func (a *app) serviceActivity(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	svc, ok := a.cat.Get(id)
	if !ok {
		httputilx.Error(w, 404, "unknown service "+id)
		return
	}
	if svc.Activity == "" {
		// 501, not 500 or an empty list: this service keeps no log, and a
		// UI that cannot tell that from "it failed" will offer a retry
		// button forever.
		httputilx.Error(w, 501, svc.Name+" keeps no activity log")
		return
	}

	ctx, cancel := reqContext(r)
	defer cancel()
	path := svc.Activity
	if limit := r.URL.Query().Get("limit"); limit != "" {
		path += "?limit=" + url.QueryEscape(limit)
	}

	var out any
	var err error
	if id == "banking-circle" {
		// Banking Circle serves this on its credentialed API, so it goes
		// through the same authorized client the Banks view uses rather
		// than a bare GET that would be refused.
		err = a.bc.AuthorizedGet(ctx, path, &out)
	} else {
		err = a.getJSON(ctx, a.client, svc.BaseURL+path, &out)
	}
	if err != nil {
		httputilx.Error(w, 502, err.Error())
		return
	}
	httputilx.WriteJSON(w, 200, out)
}
