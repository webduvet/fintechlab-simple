package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/webduvet/fintechlab-simple/internal/allowlist"
	"github.com/webduvet/fintechlab-simple/internal/hmacx"
	"github.com/webduvet/fintechlab-simple/internal/retry"
)

func TestDeliverSignsAndAccepts2xx(t *testing.T) {
	secret := []byte("sim-hmac-dev-only")
	var gotSig, gotTS, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotSig = r.Header.Get(hmacx.HeaderSignature)
		gotTS = r.Header.Get(hmacx.HeaderTimestamp)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	if i := strings.LastIndex(host, ":"); i > 0 {
		// allow host:port of httptest
	}
	u, _ := srv.URL, srv.URL
	list, err := allowlist.Parse("127.0.0.1,localhost")
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"type":"payment.accepted"}`)
	st, err := Deliver(srv.Client(), u, list, secret, []byte("evt_1"), body, time.Unix(1_700_000_000, 0))
	if err != nil || st != 202 {
		t.Fatalf("deliver: st=%d err=%v", st, err)
	}
	if gotBody != string(body) {
		t.Fatalf("body: %s", gotBody)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(gotTS + "."))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != want {
		t.Fatalf("sig %s want %s", gotSig, want)
	}
}

func TestDeliverRejectsOffAllowlistWithoutCalling(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	list, _ := allowlist.Parse("receiver")
	_, err := Deliver(srv.Client(), srv.URL, list, []byte("x"), []byte("e"), []byte("{}"), time.Now())
	if err == nil {
		t.Fatal("expected allowlist error")
	}
	if called {
		t.Fatal("must not POST off-list destinations")
	}
}

func TestRetryThenSuccess(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n < 3 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	list, _ := allowlist.Parse("127.0.0.1,localhost")
	backoffs := []time.Duration{time.Millisecond, time.Millisecond}
	var last int
	var err error
	for attempt := 1; attempt <= 4; attempt++ {
		if d := retry.SleepBeforeAttempt(attempt, backoffs); d > 0 {
			time.Sleep(d)
		}
		last, err = Deliver(srv.Client(), srv.URL, list, []byte("sim-hmac-dev-only"), []byte("evt"), []byte("{}"), time.Now())
		if err == nil {
			break
		}
		if !retry.ShouldRetry(last, attempt, 4) {
			t.Fatalf("gave up early: %v", err)
		}
	}
	if err != nil || last != 200 {
		t.Fatalf("final: st=%d err=%v hits=%d", last, err, n)
	}
	if n != 3 {
		t.Fatalf("hits=%d", n)
	}
}

func TestRetryMarksFailedAfterN(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(503)
	}))
	defer srv.Close()
	list, _ := allowlist.Parse("127.0.0.1,localhost")
	max := 3
	failed := false
	for attempt := 1; attempt <= max; attempt++ {
		st, err := Deliver(srv.Client(), srv.URL, list, []byte("sim-hmac-dev-only"), []byte("evt"), []byte("{}"), time.Now())
		if err == nil {
			t.Fatal("unexpected success")
		}
		if !retry.ShouldRetry(st, attempt, max) && retry.Exhausted(attempt, max) {
			failed = true
		}
	}
	if !failed || n != max {
		t.Fatalf("failed=%v hits=%d", failed, n)
	}
}
