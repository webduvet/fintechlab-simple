// Package wlsftp implements the real Worldline SFTP+PGP transport described
// in docs/ARCHITECTURE-vendor-corrections.md §2 (refined by its Addendum
// §E): an SSH/SFTP server backed by github.com/pkg/sftp, serving a real
// directory tree, plus the mock-Worldline OpenPGP keypair and
// encrypt/decrypt/sign helpers used to stage files into that tree. This is
// the one package in the lab that speaks a real wire protocol and real
// cryptographic file formats instead of simulating one over plain HTTP.
package wlsftp

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// LoadOrGenerateHostKey loads an SSH host private key (OpenSSH PEM,
// ed25519) from path, generating and persisting a fresh one the first time
// the file does not exist yet — matching this package's "generate if
// missing, then persist" convention for every credential it owns.
func LoadOrGenerateHostKey(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("wlsftp: parse host key %s: %w", path, err)
		}
		return signer, nil
	case os.IsNotExist(err):
		// Fall through to generation below.
	default:
		return nil, fmt.Errorf("wlsftp: read host key %s: %w", path, err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("wlsftp: generate host key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "wlsftp host key")
	if err != nil {
		return nil, fmt.Errorf("wlsftp: marshal host key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("wlsftp: create host key dir: %w", err)
	}
	// 644, not 600: lab-only fake key material (gitignored), consistent
	// with every other generated credential in this package -- see
	// persistKeypair's comment in pgp.go for the full rationale.
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o644); err != nil {
		return nil, fmt.Errorf("wlsftp: write host key %s: %w", path, err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("wlsftp: signer from generated host key: %w", err)
	}
	return signer, nil
}
