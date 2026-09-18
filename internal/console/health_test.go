package console

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func probeCatalogue(url string) *Catalogue {
	return &Catalogue{Services: []Service{
		{ID: "up-svc", BaseURL: url, HealthPath: "/health"},
		{ID: "no-health", BaseURL: url},
	}}
}

func plainClient(Service) *http.Client { return &http.Client{Timeout: 2 * time.Second} }

func TestMonitorReportsUpDownAndNotProbed(t *testing.T) {
	code := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
	}))
	defer srv.Close()

	m := NewMonitor(probeCatalogue(srv.URL), plainClient, time.Hour, time.Second)
	// A service with no health endpoint must never be reported as down --
	// permanent red teaches an operator to ignore red.
	st, _ := m.Get("no-health")
	if st.State != StateNotProbed {
		t.Fatalf("no-health state = %q, want %q", st.State, StateNotProbed)
	}

	m.ProbeAll(context.Background())
	st, _ = m.Get("up-svc")
	if st.State != StateUp {
		t.Fatalf("state = %q detail=%q, want up", st.State, st.Detail)
	}
	up := st.Since

	// A non-2xx is down, but it says which -- a 401 from a credentialed
	// listener is a different problem from a refused connection.
	code = http.StatusUnauthorized
	m.ProbeAll(context.Background())
	st, _ = m.Get("up-svc")
	if st.State != StateDown || st.HTTPStatus != 401 || st.Detail == "" {
		t.Fatalf("status after 401 = %+v", st)
	}
	if !st.Since.After(up) {
		t.Fatal("Since did not move when the state changed")
	}
}

func TestMonitorHistoryIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	m := NewMonitor(probeCatalogue(srv.URL), plainClient, time.Hour, time.Second)
	for i := 0; i < historyLen*2; i++ {
		m.ProbeAll(context.Background())
	}
	st, _ := m.Get("up-svc")
	if len(st.History) != historyLen {
		t.Fatalf("history = %d entries, want it capped at %d", len(st.History), historyLen)
	}
}

func TestMonitorMarksADeadServiceDownWithoutHanging(t *testing.T) {
	// Port 1 on loopback refuses immediately on Linux; the point is that a
	// dead peer produces a status, not a stuck page load.
	cat := &Catalogue{Services: []Service{{ID: "dead", BaseURL: "http://127.0.0.1:1", HealthPath: "/health"}}}
	m := NewMonitor(cat, plainClient, time.Hour, 2*time.Second)
	done := make(chan struct{})
	go func() { m.ProbeAll(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ProbeAll hung on an unreachable service")
	}
	st, _ := m.Get("dead")
	if st.State != StateDown || st.Detail == "" {
		t.Fatalf("status = %+v, want down with a reason", st)
	}
}

func TestProbeUnknownServiceIsReported(t *testing.T) {
	m := NewMonitor(probeCatalogue("http://127.0.0.1:1"), plainClient, time.Hour, time.Second)
	if _, ok := m.Probe(context.Background(), "nope"); ok {
		t.Fatal("probing an unknown service claimed success")
	}
}
