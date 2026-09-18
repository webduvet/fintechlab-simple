package aci

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Encrypt implements ACI's real webhook payload encryption, exactly
// (docs/ARCHITECTURE-phase3-corrections.md section 2):
//  1. AES-256-GCM-encrypt plaintext with a random 12-byte IV and key used as
//     raw bytes (the caller hex-decodes ACI_WEBHOOK_SECRET before calling —
//     this function never touches hex itself on the key side).
//  2. Split Go's combined Seal output into ciphertext and the trailing
//     16-byte auth tag — ACI's wire format keeps them separate, unlike Go's
//     GCM default (same split-tag shape as Banking Circle, independently
//     required here: Node's setAuthTag()+final() also needs them apart).
//  3. Hex-encode ciphertext, IV, and tag separately (encoding/hex) — ACI's
//     wire format is hex, not base64 like Banking Circle's; each vendor's
//     encoding is implemented exactly as that vendor does it.
func Encrypt(plaintext, key []byte) (ciphertextHex, ivHex, tagHex string, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", "", err
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		return "", "", "", err
	}
	sealed := gcm.Seal(nil, iv, plaintext, nil)
	tagLen := gcm.Overhead()
	ciphertext := sealed[:len(sealed)-tagLen]
	tag := sealed[len(sealed)-tagLen:]
	return hex.EncodeToString(ciphertext), hex.EncodeToString(iv), hex.EncodeToString(tag), nil
}

// Decrypt reverses Encrypt: hex-decode ciphertext/IV/tag, recombine into
// Go's expected [ciphertext||tag] shape, and GCM-open. A tag mismatch (wrong
// key or tampered ciphertext/IV/tag) makes Open return an error — in ACI's
// real contract that error IS the auth failure, not a separate check.
func Decrypt(ciphertextHex, ivHex, tagHex string, key []byte) ([]byte, error) {
	ciphertext, err := hex.DecodeString(ciphertextHex)
	if err != nil {
		return nil, fmt.Errorf("aci: decode ciphertext hex: %w", err)
	}
	iv, err := hex.DecodeString(ivHex)
	if err != nil {
		return nil, fmt.Errorf("aci: decode iv hex: %w", err)
	}
	tag, err := hex.DecodeString(tagHex)
	if err != nil {
		return nil, fmt.Errorf("aci: decode tag hex: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sealed := make([]byte, 0, len(ciphertext)+len(tag))
	sealed = append(sealed, ciphertext...)
	sealed = append(sealed, tag...)
	plaintext, err := gcm.Open(nil, iv, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("aci: decrypt: %w", err)
	}
	return plaintext, nil
}
