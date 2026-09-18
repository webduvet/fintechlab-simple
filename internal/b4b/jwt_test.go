package b4b

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"testing"
)

// signToken builds a compact RS512 JWS by hand (mirroring what a real
// caller like settlement's own JWT builder does), independent of
// VerifyBearerToken, so these tests genuinely exercise verification rather
// than round-tripping against themselves.
func signToken(t *testing.T, priv *rsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON)
	sum := sha512.Sum512([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA512, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func testKeyPair(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestVerifyBearerTokenValid(t *testing.T) {
	key := testKeyPair(t)
	token := signToken(t, key,
		map[string]any{"alg": "RS512", "kid": "b4b-mock-1"},
		map[string]any{"aud": "b4b-payments"},
	)
	if err := VerifyBearerToken(token, &key.PublicKey, "b4b-mock-1"); err != nil {
		t.Fatalf("expected valid token to verify, got %v", err)
	}
}

func TestVerifyBearerTokenWrongKid(t *testing.T) {
	key := testKeyPair(t)
	token := signToken(t, key,
		map[string]any{"alg": "RS512", "kid": "some-other-kid"},
		map[string]any{"aud": "b4b-payments"},
	)
	if err := VerifyBearerToken(token, &key.PublicKey, "b4b-mock-1"); err == nil {
		t.Fatal("expected wrong kid to be rejected")
	}
}

func TestVerifyBearerTokenWrongAlg(t *testing.T) {
	key := testKeyPair(t)
	token := signToken(t, key,
		map[string]any{"alg": "RS256", "kid": "b4b-mock-1"},
		map[string]any{"aud": "b4b-payments"},
	)
	if err := VerifyBearerToken(token, &key.PublicKey, "b4b-mock-1"); err == nil {
		t.Fatal("expected non-RS512 alg to be rejected")
	}
}

func TestVerifyBearerTokenWrongAudience(t *testing.T) {
	key := testKeyPair(t)
	token := signToken(t, key,
		map[string]any{"alg": "RS512", "kid": "b4b-mock-1"},
		map[string]any{"aud": "someone-else"},
	)
	if err := VerifyBearerToken(token, &key.PublicKey, "b4b-mock-1"); err == nil {
		t.Fatal("expected wrong aud to be rejected")
	}
}

func TestVerifyBearerTokenWrongSignature(t *testing.T) {
	key := testKeyPair(t)
	otherKey := testKeyPair(t)
	// signed by a different key than the one we verify against
	token := signToken(t, otherKey,
		map[string]any{"alg": "RS512", "kid": "b4b-mock-1"},
		map[string]any{"aud": "b4b-payments"},
	)
	if err := VerifyBearerToken(token, &key.PublicKey, "b4b-mock-1"); err == nil {
		t.Fatal("expected signature from a different key to be rejected")
	}
}

func TestVerifyBearerTokenMalformed(t *testing.T) {
	key := testKeyPair(t)
	for _, tok := range []string{"", "not-a-jwt", "a.b", "a.b.c.d"} {
		if err := VerifyBearerToken(tok, &key.PublicKey, "b4b-mock-1"); err == nil {
			t.Fatalf("expected malformed token %q to be rejected", tok)
		}
	}
}

func TestLoadOrGenerateKeyPairPersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	key1, err := LoadOrGenerateKeyPair(dir)
	if err != nil {
		t.Fatalf("first LoadOrGenerateKeyPair: %v", err)
	}
	key2, err := LoadOrGenerateKeyPair(dir)
	if err != nil {
		t.Fatalf("second LoadOrGenerateKeyPair: %v", err)
	}
	if key1.PublicKey.N.Cmp(key2.PublicKey.N) != 0 {
		t.Fatal("second load did not reuse the persisted key")
	}
	pubPEM, err := PublicKeyPEM(&key1.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if pubPEM == "" {
		t.Fatal("PublicKeyPEM returned empty string")
	}
}
