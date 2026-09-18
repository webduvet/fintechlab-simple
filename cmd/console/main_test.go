package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/webduvet/fintechlab-simple/internal/console"
)

func testApp(t *testing.T, peers *httptest.Server) *app {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	base := ""
	if peers != nil {
		base = peers.URL
		client = peers.Client()
	}
	cat := &console.Catalogue{Services: []console.Service{
		{ID: "worldline", Name: "Worldline", Kind: console.KindVendor, BaseURL: base, HealthPath: "/health", Ports: []string{"8084/http"}},
		{ID: "settlement", Name: "Settlement", Kind: console.KindPlatform, BaseURL: base, HealthPath: "/health", Ports: []string{"8083/http"}},
		{ID: "bank", Name: "Core ledger", Kind: console.KindPlatform, BaseURL: base, HealthPath: "/health", Ports: []string{"8081/http"}},
	}}
	reg, err := console.NewRegistry("")
	if err != nil {
		t.Fatal(err)
	}
	a := &app{
		cat: cat, reg: reg, client: client, secure: client,
		bcCfg: "../../config/banking-circle.json",
		prov:  &console.Provisioner{Client: client, Registry: reg, B4BURL: base, WorldlineURL: base},
	}
	a.mon = console.NewMonitor(cat, func(console.Service) *http.Client { return client }, time.Hour, time.Second)
	a.bc = &console.BankingCircleBank{BaseURL: base, Client: client}
	a.banks = console.NewBanks(
		&console.CoreLedgerBank{BaseURL: base, Client: client},
		a.bc,
	)
	return a
}

func call(t *testing.T, a *app, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, req)
	return w
}

func TestOverviewReportsEveryServiceEvenBeforeItHasBeenProbed(t *testing.T) {
	a := testApp(t, nil)
	w := call(t, a, "GET", "/api/overview", "")
	if w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
	var got struct {
		Services []struct {
			ID     string `json:"id"`
			Status struct {
				State string `json:"state"`
			} `json:"status"`
		} `json:"services"`
		Counts map[string]int `json:"counts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Services) != 3 {
		t.Fatalf("services = %d", len(got.Services))
	}
	// "unknown", not "down": the console has not looked yet, and claiming
	// a service is broken on that basis is how a status page loses trust.
	for _, s := range got.Services {
		if s.Status.State != console.StateUnknown {
			t.Errorf("%s state = %q before any probe", s.ID, s.Status.State)
		}
	}
	if got.Counts["unknown"] != 3 {
		t.Fatalf("counts = %v", got.Counts)
	}
}

func TestProbeOfAnUnknownServiceIs404(t *testing.T) {
	a := testApp(t, nil)
	if w := call(t, a, "POST", "/api/services/nope/probe", ""); w.Code != 404 {
		t.Fatalf("code = %d, want 404", w.Code)
	}
}

func TestOpeningAnAccountWhereTheVendorHasNoEndpointIs501(t *testing.T) {
	a := testApp(t, nil)
	w := call(t, a, "POST", "/api/banks/banking-circle/accounts", `{"holder":"X","currency":"EUR","opening_balance":""}`)
	// Not 500: a UI that cannot tell "this vendor has no such endpoint"
	// from "this call failed" will offer a retry button forever.
	if w.Code != 501 {
		t.Fatalf("code = %d body=%s, want 501", w.Code, w.Body)
	}
	if w := call(t, a, "POST", "/api/banks/nope/accounts", `{"holder":"X"}`); w.Code != 404 {
		t.Fatalf("unknown bank code = %d, want 404", w.Code)
	}
}

func TestMerchantValidationAndLookupStatuses(t *testing.T) {
	a := testApp(t, nil)
	if w := call(t, a, "POST", "/api/merchants", `{"legal_name":""}`); w.Code != 400 {
		t.Fatalf("nameless merchant = %d, want 400", w.Code)
	}
	if w := call(t, a, "POST", "/api/merchants/mer_nope/outlets", `{"name":""}`); w.Code != 404 {
		t.Fatalf("unknown merchant = %d, want 404", w.Code)
	}
	w := call(t, a, "POST", "/api/merchants", `{"legal_name":"Handled Ltd","outlets":2}`)
	if w.Code != 201 {
		t.Fatalf("create = %d body=%s", w.Code, w.Body)
	}
	var m console.Merchant
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Outlets) != 2 || m.Outlets[0].MID == "" {
		t.Fatalf("created merchant = %+v", m)
	}
	if w := call(t, a, "DELETE", "/api/merchants/"+m.ID, ""); w.Code != 200 {
		t.Fatalf("delete = %d", w.Code)
	}
	if w := call(t, a, "DELETE", "/api/merchants/"+m.ID, ""); w.Code != 404 {
		t.Fatalf("second delete = %d, want 404", w.Code)
	}
}

func TestCallerFixableErrorsAre400NotBadGateway(t *testing.T) {
	a := testApp(t, nil)
	w := call(t, a, "POST", "/api/merchants", `{"legal_name":"Bounded Ltd"}`)
	var m console.Merchant
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	// 502 "bad gateway" for a number the operator typed would send them
	// reading container logs for a form-validation problem.
	if w := call(t, a, "POST", "/api/merchants/"+m.ID+"/trading", `{"count":5000}`); w.Code != 400 {
		t.Fatalf("over-cap trading count = %d, want 400", w.Code)
	}
	if w := call(t, a, "POST", "/api/merchants/"+m.ID+"/status", `{"status":"frozen"}`); w.Code != 400 {
		t.Fatalf("invented status = %d, want 400", w.Code)
	}
}

func TestActionsPassThroughThePeersOwnStatusAndBody(t *testing.T) {
	peers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sim/settlement-cycle/run":
			// 207: some files published, some did not. The console must
			// not round that up to 200 -- the operator needs to see it.
			w.WriteHeader(207)
			_ = json.NewEncoder(w).Encode(map[string]any{"slot": r.URL.Query().Get("slot"), "errors": []string{"boom"}})
		case "/worldline/pull":
			_ = json.NewEncoder(w).Encode(map[string]any{"files": []any{}})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer peers.Close()
	a := testApp(t, peers)

	w := call(t, a, "POST", "/api/actions/worldline-cycle?slot=afternoon", "")
	if w.Code != 207 {
		t.Fatalf("code = %d, want the peer's 207", w.Code)
	}
	if !strings.Contains(w.Body.String(), "afternoon") {
		t.Fatalf("slot was not forwarded: %s", w.Body)
	}
	if w := call(t, a, "POST", "/api/actions/settlement-pull", ""); w.Code != 200 {
		t.Fatalf("pull = %d", w.Code)
	}
}

func TestActionsReportAnUnreachablePeerRatherThanHanging(t *testing.T) {
	a := testApp(t, nil)
	a.cat.Services[0].BaseURL = "http://127.0.0.1:1"
	w := call(t, a, "POST", "/api/actions/worldline-cycle?slot=morning", "")
	if w.Code != 502 {
		t.Fatalf("code = %d, want 502", w.Code)
	}
}

func TestConfigViewAlwaysRendersSomething(t *testing.T) {
	a := testApp(t, nil)
	w := call(t, a, "GET", "/api/config", "")
	if w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// The shipped delivery config is the answer to "how do I not wait two
	// days for a subscription to deactivate", so it has to be readable.
	// Banking Circle is unreachable here, so this exercises the fallback
	// to the file -- which must say that it is a fallback, because
	// BC_TIME_SCALE overrides what the file claims.
	if _, ok := got["banking_circle"]; !ok {
		t.Fatalf("banking_circle missing: %v", got["banking_circle_error"])
	}
	src, _ := got["banking_circle_source"].(string)
	if !strings.Contains(src, "BC_TIME_SCALE") {
		t.Fatalf("banking_circle_source = %q; a file-sourced schedule must warn that it can be overridden", src)
	}
	if len(got["groups"].([]any)) == 0 {
		t.Fatal("no setting groups")
	}
	// Worldline is unreachable in this test; the view must still render
	// and say why that panel is empty.
	if _, ok := got["worldline_channel_error"]; !ok {
		t.Fatal("an unreachable worldline was not reported")
	}
}

func TestUIIsServedAndNeedsNoNetwork(t *testing.T) {
	a := testApp(t, nil)
	w := call(t, a, "GET", "/", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<!doctype html>") {
		t.Fatalf("index not served: %d", w.Code)
	}
	external := regexp.MustCompile(`(?i)(https?:)?//(?:cdn|unpkg|fonts\.|ajax\.|code\.jquery)`)
	for _, asset := range []string{"/app.css", "/app.js"} {
		w := call(t, a, "GET", asset, "")
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("%s not served: %d", asset, w.Code)
		}
		// The lab's promise is one `make up` on a laptop with no network.
		// A CDN reference in the console would quietly break that.
		if loc := external.FindString(w.Body.String()); loc != "" {
			t.Errorf("%s reaches out to %q; the console must work offline", asset, loc)
		}
	}
}

func TestHiddenElementsStayHidden(t *testing.T) {
	a := testApp(t, nil)
	css := call(t, a, "GET", "/app.css", "").Body.String()
	// .modal-backdrop sets display, and a class selector beats the
	// browser's own bare [hidden] rule -- so without an explicit override
	// the modal backdrop covers and blurs the whole page from first paint,
	// which is exactly what it did.
	if !regexp.MustCompile(`\[hidden\]\s*\{[^}]*display:\s*none\s*!important`).MatchString(css) {
		t.Fatal("app.css does not force [hidden] to display:none; anything with a display-setting class will show through the attribute")
	}
	html := call(t, a, "GET", "/", "").Body.String()
	if !strings.Contains(html, `id="modal-backdrop" hidden`) {
		t.Fatal("the modal backdrop does not start hidden")
	}
}

func TestMonitorRunStopsWithItsContext(t *testing.T) {
	a := testApp(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.mon.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Monitor.Run ignored its context")
	}
}
