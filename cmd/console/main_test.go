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

// --- activity ---------------------------------------------------------

// TestActivityIsProxiedFromTheServiceItBelongsTo. The console holds the
// only credentials that reach some of these vendors, so the panel reads
// through here rather than from the browser.
func TestActivityIsProxiedFromTheServiceItBelongsTo(t *testing.T) {
	var gotPath string
	peers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"logs":[{"name":"sftp","title":"File exchange","total":2,"events":[]}]}`))
	}))
	defer peers.Close()

	a := testApp(t, peers)
	a.cat.Services[0].Activity = "/sim/activity"

	w := call(t, a, http.MethodGet, "/api/services/worldline/activity?limit=25", "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if gotPath != "/sim/activity?limit=25" {
		t.Errorf("asked the service for %q — the limit must be carried through", gotPath)
	}
	var out struct {
		Logs []struct {
			Name  string `json:"name"`
			Total int    `json:"total"`
		} `json:"logs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Logs) != 1 || out.Logs[0].Name != "sftp" || out.Logs[0].Total != 2 {
		t.Errorf("the service's own answer was not passed through: %s", w.Body.String())
	}
}

// TestActivityOfAServiceThatKeepsNoLogIs501. A UI that cannot tell "this
// vendor does not keep one" from "the call failed" will offer a retry
// button forever.
func TestActivityOfAServiceThatKeepsNoLogIs501(t *testing.T) {
	a := testApp(t, nil)
	if w := call(t, a, http.MethodGet, "/api/services/bank/activity", ""); w.Code != 501 {
		t.Errorf("status %d, want 501: %s", w.Code, w.Body.String())
	}
	if w := call(t, a, http.MethodGet, "/api/services/nope/activity", ""); w.Code != 404 {
		t.Errorf("unknown service: status %d, want 404", w.Code)
	}
}

// TestActivityReportsAnUnreachableServiceRatherThanAnEmptyList, because
// "nothing has happened yet" and "this service is not answering" look
// identical once the error is swallowed, and they send an operator to
// opposite ends of the stack.
func TestActivityReportsAnUnreachableServiceRatherThanAnEmptyList(t *testing.T) {
	a := testApp(t, nil)
	a.cat.Services[0].Activity = "/sim/activity"
	a.cat.Services[0].BaseURL = "http://127.0.0.1:1" // nothing listens here

	w := call(t, a, http.MethodGet, "/api/services/worldline/activity", "")
	if w.Code != 502 {
		t.Fatalf("status %d, want 502: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "127.0.0.1:1") {
		t.Errorf("the failure should name what could not be reached: %s", w.Body.String())
	}
}

// --- system in test ---------------------------------------------------

// flowPeers stands in for the four services the diagram reads, each under
// its own path prefix so one server can be all of them.
func flowPeers(t *testing.T) *httptest.Server {
	t.Helper()
	logs := map[string]string{
		"/worldline/sim/activity": `{"logs":[{"name":"sftp","total":9,"events":[
			{"seq":9,"at":"2026-09-18T21:00:03Z","op":"sftp.download","summary":"infinitepay collected file.csv.pgp","status":"ok"},
			{"seq":8,"at":"2026-09-18T21:00:02Z","op":"sftp.list","summary":"listed /download","status":"ok"},
			{"seq":7,"at":"2026-09-18T21:00:01Z","op":"sftp.session","summary":"infinitepay connected over SFTP","status":"ok"}]}]}`,
		"/b4b/sim/activity": `{"logs":[
			{"name":"payments","total":6,"events":[
				{"seq":6,"at":"2026-09-18T21:01:00Z","op":"payment.create","summary":"payout 951.98 EUR","status":"ok"},
				{"seq":5,"at":"2026-09-18T21:01:00Z","op":"payment.create","summary":"payout 12.00 EUR","status":"warn"}]},
			{"name":"callbacks","total":30,"events":[
				{"seq":30,"at":"2026-09-18T21:02:00Z","op":"callback","summary":"delivered","status":"ok"},
				{"seq":29,"at":"2026-09-18T21:02:00Z","op":"callback","summary":"refused by the client","status":"bad"}]}]}`,
		"/bc/sim/activity": `{"logs":[
			{"name":"payments","total":3,"events":[
				{"seq":3,"at":"2026-09-18T21:03:00Z","op":"payment.create","summary":"payout at the bank","status":"ok"},
				{"seq":2,"at":"2026-09-18T21:02:00Z","op":"payment.incoming","summary":"funds in","status":"ok"}]},
			{"name":"notifications","total":2,"events":[
				{"seq":2,"at":"2026-09-18T21:04:00Z","op":"notification","summary":"batch of 5","status":"ok"}]}]}`,
		"/runner/sim/activity": `{"logs":[{"name":"runs","total":4,"events":[]}]}`,
		"/runner/status": `{"status":"ok","running_now":null,"last_run":{"id":"run-1","root":"r1",
			"payouts":{"count":6,"total":"27698.78"},
			"stages":[{"stage":"SETTLEMENT_FILE_INGESTION","status":"COMPLETED"},
			          {"stage":"DAILY_MOVEMENT_PROCESSING","status":"COMPLETED"},
			          {"stage":"DAILY_SETTLEMENT_REPORT","status":"FAILED"}]}}`,
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/authorizations/authorize") {
			_, _ = w.Write([]byte(`{"access_token":"t","expires_in":300}`))
			return
		}
		body, ok := logs[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func flowApp(t *testing.T, peers *httptest.Server) *app {
	t.Helper()
	a := testApp(t, peers)
	a.cat.Services = []console.Service{
		{ID: "worldline", Name: "Worldline", Kind: console.KindVendor, BaseURL: peers.URL + "/worldline", Activity: "/sim/activity", HealthPath: "/health"},
		{ID: "b4b", Name: "B4B Payments", Kind: console.KindVendor, BaseURL: peers.URL + "/b4b", Activity: "/sim/activity", HealthPath: "/health"},
		{ID: "banking-circle", Name: "Banking Circle", Kind: console.KindVendor, BaseURL: peers.URL + "/bc", Activity: "/sim/activity", HealthPath: "/health"},
		{ID: "local-runner", Name: "Local runner", Kind: console.KindPlatform, BaseURL: peers.URL + "/runner", Activity: "/sim/activity", HealthPath: "/status"},
		{ID: "receiver", Name: "Webhook receiver", Kind: console.KindPlatform, BaseURL: peers.URL, HealthPath: "/health"},
	}
	a.bc.BaseURL = peers.URL + "/bc"
	return a
}

func flowOf(t *testing.T, a *app) map[string]flowStep {
	t.Helper()
	w := call(t, a, http.MethodGet, "/api/flow", "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got flowResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	steps := map[string]flowStep{}
	for _, s := range got.Steps {
		steps[s.ID] = s
	}
	return steps
}

// TestFlowCountsWhatEachHopActuallyCarried. The diagram's whole vocabulary
// is these numbers: the browser diffs them to decide what just lit up, so a
// hop counted against the wrong log would animate the wrong arrow.
func TestFlowCountsWhatEachHopActuallyCarried(t *testing.T) {
	peers := flowPeers(t)
	defer peers.Close()
	steps := flowOf(t, flowApp(t, peers))

	for _, tc := range []struct {
		id    string
		count int
		from  string
		to    string
	}{
		{"pull", 2, "platform", "worldline"},    // session + list
		{"collect", 1, "worldline", "platform"}, // the download
		{"payouts", 2, "platform", "b4b"},
		{"bridge", 1, "b4b", "banking-circle"},    // payment.create only
		{"fund", 1, "platform", "banking-circle"}, // payment.incoming only
		{"callbacks", 2, "b4b", "platform"},
		{"notify", 1, "banking-circle", "receiver"},
	} {
		got := steps[tc.id]
		if got.Count != tc.count {
			t.Errorf("%s carried %d, want %d", tc.id, got.Count, tc.count)
		}
		if got.From != tc.from || got.To != tc.to {
			t.Errorf("%s goes %s->%s, want %s->%s", tc.id, got.From, got.To, tc.from, tc.to)
		}
	}
}

// TestFlowSeparatesAFailureFromARefusal: red and amber mean different
// things on this diagram — one is the vendor breaking, the other is the
// vendor refusing, and an operator reacts to them differently.
func TestFlowSeparatesAFailureFromARefusal(t *testing.T) {
	peers := flowPeers(t)
	defer peers.Close()
	steps := flowOf(t, flowApp(t, peers))

	if steps["callbacks"].Failed != 1 {
		t.Errorf("a refused callback should count as failed, got %+v", steps["callbacks"])
	}
	if steps["payouts"].Refused != 1 {
		t.Errorf("a 4xx payout should count as refused, not failed: %+v", steps["payouts"])
	}
	if steps["payouts"].Failed != 0 {
		t.Errorf("a refusal must not be reported as a failure: %+v", steps["payouts"])
	}
}

// TestFlowReadsStagesForTheHopsThatNeverLeaveThePlatform.
func TestFlowReadsStagesForTheHopsThatNeverLeaveThePlatform(t *testing.T) {
	peers := flowPeers(t)
	defer peers.Close()
	steps := flowOf(t, flowApp(t, peers))

	if steps["ingest"].Count != 2 {
		t.Errorf("ingest should count its two stages, got %d", steps["ingest"].Count)
	}
	if steps["reports"].Count != 1 || steps["reports"].Failed != 1 {
		t.Errorf("the failed report stage should show as failed: %+v", steps["reports"])
	}
}

// TestFlowSurvivesAVendorBeingDown. The view is the first thing an operator
// opens; it must render with one participant unreachable and say which,
// rather than 500 and leave them with nothing.
func TestFlowSurvivesAVendorBeingDown(t *testing.T) {
	peers := flowPeers(t)
	defer peers.Close()
	a := flowApp(t, peers)
	for i := range a.cat.Services {
		if a.cat.Services[i].ID == "b4b" {
			a.cat.Services[i].BaseURL = "http://127.0.0.1:1"
		}
	}

	w := call(t, a, http.MethodGet, "/api/flow", "")
	if w.Code != 200 {
		t.Fatalf("status %d, want the page to still render: %s", w.Code, w.Body.String())
	}
	var got flowResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Errors["b4b"] == "" {
		t.Error("the unreachable vendor should be named in errors")
	}
	if len(got.Participants) != 5 || len(got.Steps) != 9 {
		t.Errorf("the diagram should still be whole: %d participants, %d steps",
			len(got.Participants), len(got.Steps))
	}
}

// --- banking circle subscriptions -------------------------------------

// bcFake is Banking Circle's notification surface, enough of it to drive
// the card: the token exchange, the subscription list, the queue depth, the
// clienttest, and the notification ring the result is read back from.
type bcFake struct {
	sent          int
	deliveredWith string // the status the ring reports for the delivery
}

func (f *bcFake) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/authorizations/authorize"):
			_, _ = w.Write([]byte(`{"access_token":"t","expires_in":300}`))
		case strings.HasSuffix(r.URL.Path, "/notificationselfservice/subscription"):
			_, _ = w.Write([]byte(`{"result":[{
				"id":"sub_1","endpoint":"https://receiver:8443/raw-events","isActive":true,
				"status":2,"statusMessage":"Subscription successfully created","version":1,
				"mtlsEnabled":false,"maxNotificationsPerMessage":5,
				"subscriptionEvents":[
					{"eventType":"OutgoingPaymentBooked","isActive":true,"subscriptionEventTargetDetails":[]},
					{"eventType":"MissingFunding","isActive":false,"subscriptionEventTargetDetails":[{"id":"t1"}]}]}]}`))
		case strings.Contains(r.URL.Path, "/sim/subscription/") && strings.HasSuffix(r.URL.Path, "/pending"):
			_, _ = w.Write([]byte(`{"pending":3}`))
		case strings.Contains(r.URL.Path, "/clienttest/"):
			f.sent++
			_, _ = w.Write([]byte(`{"status":"sent"}`))
		case strings.HasPrefix(r.URL.Path, "/sim/activity"):
			if f.sent == 0 {
				_, _ = w.Write([]byte(`{"logs":[{"name":"notifications","total":0,"events":[]}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"logs":[{"name":"notifications","total":1,"events":[
				{"seq":1,"at":"2026-09-19T10:00:00Z","op":"notification","status":"` + f.deliveredWith + `",
				 "summary":"batch of 1 to https://receiver:8443/raw-events",
				 "detail":{"http":"200"}}]}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
}

// TestSubscriptionsAreNamedNotNumbered. The vendor sends status 2; an
// operator needs the word. A card that printed the number would be a lookup
// table, and the point of the card is not having to hold one.
func TestSubscriptionsAreNamedNotNumbered(t *testing.T) {
	fake := &bcFake{}
	srv := fake.server(t)
	defer srv.Close()
	a := testApp(t, srv)
	a.bc.BaseURL = srv.URL

	w := call(t, a, http.MethodGet, "/api/banking-circle/subscriptions", "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Subscriptions []subscriptionView `json:"subscriptions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Subscriptions) != 1 {
		t.Fatalf("got %d subscriptions", len(got.Subscriptions))
	}
	s := got.Subscriptions[0]
	if s.Status != "active" {
		t.Errorf("status = %q, want the word", s.Status)
	}
	if s.Endpoint != "https://receiver:8443/raw-events" {
		t.Errorf("endpoint = %q", s.Endpoint)
	}
	if len(s.Events) != 2 || s.Events[0].Type != "OutgoingPaymentBooked" {
		t.Errorf("events = %+v — the card is about which events go where", s.Events)
	}
	if s.Events[1].Active {
		t.Error("an inactive event must not be shown as active")
	}
	if s.Events[1].Targets != 1 {
		t.Errorf("targets = %d, want the count that makes an event narrower", s.Events[1].Targets)
	}
	if s.Pending != 3 {
		t.Errorf("pending = %d — a queue that is not draining is the first thing to check", s.Pending)
	}
}

// TestProbeReportsWhatTheEndpointDidNotThatItWasSent. The vendor's
// clienttest answers 200 "sent" whether or not anything took it, and "sent"
// is not the question being asked.
func TestProbeReportsWhatTheEndpointDidNotThatItWasSent(t *testing.T) {
	for _, tc := range []struct {
		ring      string
		delivered bool
	}{
		{"ok", true},
		{"bad", false},
	} {
		fake := &bcFake{deliveredWith: tc.ring}
		srv := fake.server(t)
		a := testApp(t, srv)
		a.bc.BaseURL = srv.URL

		w := call(t, a, http.MethodPost, "/api/banking-circle/subscriptions/sub_1/test", "")
		if w.Code != 200 {
			srv.Close()
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		var got struct {
			Delivered bool   `json:"delivered"`
			Result    string `json:"result"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			srv.Close()
			t.Fatal(err)
		}
		if got.Delivered != tc.delivered {
			t.Errorf("ring said %q, console reported delivered=%v", tc.ring, got.Delivered)
		}
		if !strings.Contains(got.Result, "batch of 1") {
			t.Errorf("the vendor's own line should be the answer, got %q", got.Result)
		}
		if fake.sent != 1 {
			t.Errorf("clienttest fired %d times, want 1", fake.sent)
		}
		srv.Close()
	}
}

// TestSubscriptionListIsAnArrayWhenEmpty, because the card decides between
// "nothing is subscribed" and rows by counting, and null renders neither.
func TestSubscriptionListIsAnArrayWhenEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/authorizations/authorize") {
			_, _ = w.Write([]byte(`{"access_token":"t","expires_in":300}`))
			return
		}
		_, _ = w.Write([]byte(`{"result":[]}`))
	}))
	defer srv.Close()
	a := testApp(t, srv)
	a.bc.BaseURL = srv.URL

	w := call(t, a, http.MethodGet, "/api/banking-circle/subscriptions", "")
	if !strings.Contains(w.Body.String(), `"subscriptions":[]`) {
		t.Errorf("empty list marshalled as %s", w.Body.String())
	}
}
