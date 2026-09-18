package sshftp

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// LoadOrGenerateHostKey loads an OpenSSH ed25519 host key, generating and
// persisting one the first time path is missing.
func LoadOrGenerateHostKey(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("sshftp: parse host key %s: %w", path, err)
		}
		return signer, nil
	case os.IsNotExist(err):
	default:
		return nil, fmt.Errorf("sshftp: read host key %s: %w", path, err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("sshftp: generate host key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "sshftp host key")
	if err != nil {
		return nil, fmt.Errorf("sshftp: marshal host key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("sshftp: create host key dir: %w", err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o644); err != nil {
		return nil, fmt.Errorf("sshftp: write host key %s: %w", path, err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("sshftp: signer from generated host key: %w", err)
	}
	return signer, nil
}
