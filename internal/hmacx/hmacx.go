// Package hmacx signs and verifies generic lab webhook payloads.
// Algorithm: HMAC-SHA256 over "timestamp.rawBody". This is a teaching
// stand-in, not a copy of any vendor scheme.
package hmacx

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderSignature = "X-Sim-Signature"
	HeaderTimestamp = "X-Sim-Timestamp"
	HeaderEventID   = "X-Sim-Event-Id"
	sigPrefix       = "sha256="
)

// Sign returns the header value "sha256=<hex>" for timestamp + "." + body.
func Sign(secret, timestamp, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(timestamp)
	mac.Write([]byte("."))
	mac.Write(body)
	return sigPrefix + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks the signature. maxSkew is applied when ts is unix seconds
// and now is non-zero; pass 0 to skip skew checks (unit tests).
func Verify(secret []byte, timestamp, signature string, body []byte, now time.Time, maxSkew time.Duration) error {
	if !strings.HasPrefix(signature, sigPrefix) {
		return fmt.Errorf("hmacx: missing %s prefix", sigPrefix)
	}
	want := Sign(secret, []byte(timestamp), body)
	if subtle.ConstantTimeCompare([]byte(want), []byte(signature)) != 1 {
		return fmt.Errorf("hmacx: signature mismatch")
	}
	if maxSkew > 0 && !now.IsZero() {
		sec, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			return fmt.Errorf("hmacx: bad timestamp")
		}
		ts := time.Unix(sec, 0)
		delta := now.Sub(ts)
		if delta < 0 {
			delta = -delta
		}
		if delta > maxSkew {
			return fmt.Errorf("hmacx: timestamp outside skew window")
		}
	}
	return nil
}
