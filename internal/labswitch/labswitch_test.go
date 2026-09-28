package labswitch

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTheSwitchIsOnUnlessTheEnvSaysOtherwise(t *testing.T) {
	for v, want := range map[string]bool{"": true, "true": true, "1": true, "false": false, "off": false, "no": false, "0": false} {
		if got := FromEnv("svc", "x", v).Connected(); got != want {
			t.Errorf("FromEnv(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestTheSwitchAnswersOverHTTP(t *testing.T) {
	s := New("svc", "pulls files", true)
	mux := http.NewServeMux()
	s.Routes(mux, "/sim/connected")

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/sim/connected", strings.NewReader(`{"connected":false,"reason":"the platform took over"}`)))
	if w.Code != 200 || s.Connected() {
		t.Fatalf("POST = %d %s, connected %v", w.Code, w.Body.String(), s.Connected())
	}
	if !strings.Contains(w.Body.String(), "the platform took over") || !strings.Contains(w.Body.String(), "pulls files") {
		t.Errorf("the state should carry what and why: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/sim/connected", strings.NewReader(`{}`)))
	if w.Code != 400 {
		t.Errorf("a POST without connected = %d, want 400", w.Code)
	}
}
