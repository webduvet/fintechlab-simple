package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestAFileGoesToThePlatformUntouched. The console passes an upload on
// to the path the plugin declared — name in the query, bytes as they came —
// and stores nothing itself.
func TestAFileGoesToThePlatformUntouched(t *testing.T) {
	var mu sync.Mutex
	var gotName, gotBody, gotLines, gotDelete string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("POST /sim/files/upload", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotName, gotBody = r.URL.Query().Get("name"), string(b)
		mu.Unlock()
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"name":"mine.csv","bytes":11}`))
	})
	mux.HandleFunc("DELETE /sim/files/upload", func(w http.ResponseWriter, r *http.Request) {
		gotDelete = r.URL.Query().Get("name")
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /sim/files/preview", func(w http.ResponseWriter, r *http.Request) {
		gotLines = r.URL.Query().Get("lines")
		_, _ = w.Write([]byte(`{"name":"mine.csv","lines":["a,b"],"truncated":false}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := testApp(t, srv)
	p := testPlugin(srv.URL)
	p.Settlement.UploadPath = "/sim/files/upload"
	p.Settlement.PreviewPath = "/sim/files/preview"
	register(t, a, p)

	w := call(t, a, http.MethodPost, "/api/plugins/test-platform/files?name=my%20file.csv", "hello,world")
	if w.Code != 201 || gotName != "my file.csv" || gotBody != "hello,world" {
		t.Fatalf("upload = %d %s; platform got name %q body %q", w.Code, w.Body.String(), gotName, gotBody)
	}
	if w := call(t, a, http.MethodPost, "/api/plugins/test-platform/files", "x"); w.Code != 400 {
		t.Errorf("an upload without a name = %d, want 400", w.Code)
	}

	// A preview is bounded however many lines are asked for.
	if w := call(t, a, http.MethodGet, "/api/plugins/test-platform/files/preview?name=mine.csv&lines=1000000", ""); w.Code != 200 || gotLines != "1000" {
		t.Errorf("preview = %d, platform asked for %q lines, want 1000", w.Code, gotLines)
	}
	if w := call(t, a, http.MethodDelete, "/api/plugins/test-platform/files?name=mine.csv", ""); w.Code != 204 || gotDelete != "mine.csv" {
		t.Errorf("delete = %d, platform got %q", w.Code, gotDelete)
	}
}

// TestNoUploadPathIsNoUploadButton: a plugin that declares no upload_path
// answers 501, which the page reads as "not offered", not "failed".
func TestNoUploadPathIsNoUploadButton(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := testApp(t, srv)
	register(t, a, testPlugin(srv.URL))
	w := call(t, a, http.MethodPost, "/api/plugins/test-platform/files?name=a.csv", "x")
	if w.Code != 501 || !strings.Contains(w.Body.String(), "uploads") {
		t.Errorf("upload without upload_path = %d %s, want 501", w.Code, w.Body.String())
	}
}
