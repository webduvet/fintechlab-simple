package hmacx

import (
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	secret := []byte("sim-hmac-dev-only")
	body := []byte(`{"type":"payment.accepted","id":"evt_1"}`)
	ts := "1710000000"
	sig := Sign(secret, []byte(ts), body)
	if err := Verify(secret, ts, sig, body, time.Time{}, 0); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyRejectsTamperedBody(t *testing.T) {
	secret := []byte("sim-hmac-dev-only")
	ts := "1710000000"
	sig := Sign(secret, []byte(ts), []byte(`{"ok":true}`))
	err := Verify(secret, ts, sig, []byte(`{"ok":false}`), time.Time{}, 0)
	if err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	body := []byte("hello")
	ts := "1710000000"
	sig := Sign([]byte("sim-hmac-dev-only"), []byte(ts), body)
	if err := Verify([]byte("other-secret"), ts, sig, body, time.Time{}, 0); err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestVerifyRejectsMissingPrefix(t *testing.T) {
	err := Verify([]byte("s"), "1", "deadbeef", []byte("x"), time.Time{}, 0)
	if err == nil {
		t.Fatal("expected prefix error")
	}
}

func TestVerifySkew(t *testing.T) {
	secret := []byte("sim-hmac-dev-only")
	body := []byte("{}")
	now := time.Unix(1_800_000_000, 0)
	ts := "1800000000"
	sig := Sign(secret, []byte(ts), body)
	if err := Verify(secret, ts, sig, body, now, 5*time.Minute); err != nil {
		t.Fatalf("in-window: %v", err)
	}
	old := "1700000000"
	oldSig := Sign(secret, []byte(old), body)
	if err := Verify(secret, old, oldSig, body, now, 5*time.Minute); err == nil {
		t.Fatal("expected skew rejection")
	}
}
