// Package sshftp is a brandless SSH/SFTP server and OpenPGP envelope.
//
// Vendor directory names and filenames belong in a recipe. This package
// serves whatever layout the caller passes and encrypts when asked.
// Every SFTP session is chrooted: client paths are jail-relative, never
// host-absolute, and a symlink that would walk out is refused.
package sshftp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureLayout creates each directory under root. Entries may contain
// slashes (disputes/download).
func EnsureLayout(root string, dirs []string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("sshftp: create root: %w", err)
	}
	for _, d := range dirs {
		clean, err := resolve(root, d)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(clean, 0o755); err != nil {
			return fmt.Errorf("sshftp: create %s: %w", d, err)
		}
	}
	return nil
}

// resolve joins a caller path onto root and refuses escape.
func resolve(root, p string) (string, error) {
	if p == "" || strings.Contains(p, "..") {
		return "", fmt.Errorf("sshftp: %q escapes the root", p)
	}
	clean := filepath.Clean(strings.TrimPrefix(p, "/"))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("sshftp: %q escapes the root", p)
	}
	full := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("sshftp: %q escapes the root", p)
	}
	return full, nil
}

// PGPFilename appends .pgp unless name already has that suffix.
func PGPFilename(name string) string {
	if strings.HasSuffix(name, ".pgp") {
		return name
	}
	return name + ".pgp"
}
