package waitfor

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFilesSucceedsWhenPresent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.pem")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Files(time.Second, p); err != nil {
		t.Fatalf("Files: %v", err)
	}
}

func TestFilesTimesOutWhenMissing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "never.pem")
	start := time.Now()
	err := Files(150*time.Millisecond, p)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("returned before timeout: %s", elapsed)
	}
}

func TestFilesWaitsThenSucceeds(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "later.pem")
	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = os.WriteFile(p, []byte("x"), 0o644)
	}()
	if err := Files(2*time.Second, p); err != nil {
		t.Fatalf("Files: %v", err)
	}
}

func TestHTTPSucceedsOnAnyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()
	if err := HTTP(srv.Client(), time.Second, srv.URL); err != nil {
		t.Fatalf("HTTP: %v", err)
	}
}

func TestHTTPTimesOutWhenUnreachable(t *testing.T) {
	if err := HTTP(nil, 150*time.Millisecond, "http://127.0.0.1:1/unreachable"); err == nil {
		t.Fatal("expected timeout error")
	}
}
