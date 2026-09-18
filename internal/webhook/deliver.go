// Package webhook performs one signed HTTP delivery against an allowlist.
package webhook

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/webduvet/fintechlab-simple/internal/allowlist"
	"github.com/webduvet/fintechlab-simple/internal/hmacx"
)

// Deliver POSTs body to dest with HMAC headers. dest must pass the allowlist
// — this is enforced here, not documented as a comment.
func Deliver(client *http.Client, dest string, list *allowlist.List, secret, eventID, body []byte, now time.Time) (int, error) {
	if err := list.Allowed(dest); err != nil {
		return 0, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := hmacx.Sign(secret, []byte(ts), body)
	req, err := http.NewRequest(http.MethodPost, dest, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(hmacx.HeaderSignature, sig)
	req.Header.Set(hmacx.HeaderTimestamp, ts)
	req.Header.Set(hmacx.HeaderEventID, string(eventID))
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("webhook: destination returned %s", resp.Status)
	}
	return resp.StatusCode, nil
}
