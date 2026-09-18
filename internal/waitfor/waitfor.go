// Package waitfor gives services a bounded, self-healing wait for a startup
// dependency (a file on disk, an HTTP health endpoint) instead of relying on
// compose-level "depends_on" ordering. Ordering guarantees differ across
// Docker Compose, podman-compose, and bare `docker run`/`podman run`, so a
// service that only works when its orchestrator enforces the same ordering
// semantics is not actually connected — it is lucky.
package waitfor

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// Files blocks until every path exists, or returns an error once timeout
// elapses. Poll interval is fixed and short: this is a lab, not a
// production supervisor tree.
func Files(timeout time.Duration, paths ...string) error {
	return poll(timeout, func() (string, bool) {
		for _, p := range paths {
			if _, err := os.Stat(p); err != nil {
				return p, false
			}
		}
		return "", true
	})
}

// HTTP blocks until a GET against url returns any response (status is not
// checked — a 404 still proves the peer is accepting connections), or
// returns an error once timeout elapses.
func HTTP(client *http.Client, timeout time.Duration, url string) error {
	if client == nil {
		client = http.DefaultClient
	}
	return poll(timeout, func() (string, bool) {
		resp, err := client.Get(url)
		if err != nil {
			return url, false
		}
		_ = resp.Body.Close()
		return "", true
	})
}

func poll(timeout time.Duration, check func() (missing string, ready bool)) error {
	deadline := time.Now().Add(timeout)
	missing, ready := check()
	for !ready {
		if time.Now().After(deadline) {
			return fmt.Errorf("waitfor: timed out after %s waiting for %s", timeout, missing)
		}
		time.Sleep(200 * time.Millisecond)
		missing, ready = check()
	}
	return nil
}
