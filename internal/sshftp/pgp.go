package sshftp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

const pgpKeyBits = 3072

// Keys is an encrypt-to-recipient (or self) plus sign-with-own envelope.
type Keys struct {
	Own       *openpgp.Entity
	Recipient *openpgp.Entity
}

// EncryptAndSign encrypts to Recipient if set, otherwise to Own, and signs
// with Own.
func (k *Keys) EncryptAndSign(plaintext []byte) ([]byte, error) {
	if k == nil || k.Own == nil {
		return nil, fmt.Errorf("sshftp: no PGP keys")
	}
	to := k.Own
	if k.Recipient != nil {
		to = k.Recipient
	}
	return Encrypt(plaintext, to, k.Own)
}

// Encrypt returns a binary (not armored) OpenPGP message.
func Encrypt(plaintext []byte, recipient, signer *openpgp.Entity) ([]byte, error) {
	var buf bytes.Buffer
	w, err := openpgp.Encrypt(&buf, []*openpgp.Entity{recipient}, signer, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("sshftp: encrypt: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, fmt.Errorf("sshftp: encrypt write: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("sshftp: encrypt close: %w", err)
	}
	return buf.Bytes(), nil
}

// Decrypt decrypts with keyring and verifies a signature if one is present.
func Decrypt(ciphertext []byte, keyring openpgp.EntityList) ([]byte, error) {
	md, err := openpgp.ReadMessage(bytes.NewReader(ciphertext), keyring, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("sshftp: read pgp message: %w", err)
	}
	plaintext, err := io.ReadAll(md.UnverifiedBody)
	if err != nil {
		return nil, fmt.Errorf("sshftp: decrypt: %w", err)
	}
	if md.IsSigned && md.SignatureError != nil {
		return nil, fmt.Errorf("sshftp: signature verification failed: %w", md.SignatureError)
	}
	if md.IsSigned && md.SignedBy == nil {
		return nil, errors.New("sshftp: message signed by an unknown key")
	}
	return plaintext, nil
}

// ParseArmoredEntity reads one OpenPGP entity from armored text.
func ParseArmoredEntity(armored string) (*openpgp.Entity, error) {
	el, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
	if err != nil {
		return nil, fmt.Errorf("sshftp: parse armored key: %w", err)
	}
	if len(el) == 0 {
		return nil, fmt.Errorf("sshftp: armored key contains no entities")
	}
	return el[0], nil
}

// LoadOrGenerateKeypair loads pub/priv, generating a fresh RSA-3072 pair
// the first time either file is missing.
func LoadOrGenerateKeypair(pubPath, privPath, name, comment, email string) (*openpgp.Entity, error) {
	_, pubErr := os.Stat(pubPath)
	_, privErr := os.Stat(privPath)
	switch {
	case pubErr == nil && privErr == nil:
		return readPrivateKeypair(privPath)
	case pubErr != nil && !os.IsNotExist(pubErr):
		return nil, fmt.Errorf("sshftp: stat %s: %w", pubPath, pubErr)
	case privErr != nil && !os.IsNotExist(privErr):
		return nil, fmt.Errorf("sshftp: stat %s: %w", privPath, privErr)
	}
	e, err := openpgp.NewEntity(name, comment, email, &packet.Config{RSABits: pgpKeyBits})
	if err != nil {
		return nil, fmt.Errorf("sshftp: generate pgp entity: %w", err)
	}
	if err := persistKeypair(e, pubPath, privPath); err != nil {
		return nil, err
	}
	return e, nil
}

func readPrivateKeypair(privPath string) (*openpgp.Entity, error) {
	f, err := os.Open(privPath)
	if err != nil {
		return nil, fmt.Errorf("sshftp: open private key %s: %w", privPath, err)
	}
	defer f.Close()
	el, err := openpgp.ReadArmoredKeyRing(f)
	if err != nil {
		return nil, fmt.Errorf("sshftp: parse private key %s: %w", privPath, err)
	}
	if len(el) == 0 {
		return nil, fmt.Errorf("sshftp: private key file %s contains no entities", privPath)
	}
	return el[0], nil
}

func persistKeypair(e *openpgp.Entity, pubPath, privPath string) error {
	if err := os.MkdirAll(filepath.Dir(pubPath), 0o755); err != nil {
		return fmt.Errorf("sshftp: create pgp key dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(privPath), 0o755); err != nil {
		return fmt.Errorf("sshftp: create pgp key dir: %w", err)
	}
	var pub bytes.Buffer
	w, err := armor.Encode(&pub, openpgp.PublicKeyType, nil)
	if err != nil {
		return fmt.Errorf("sshftp: armor public key: %w", err)
	}
	if err := e.Serialize(w); err != nil {
		return fmt.Errorf("sshftp: serialize public key: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("sshftp: close public key armor: %w", err)
	}
	if err := os.WriteFile(pubPath, pub.Bytes(), 0o644); err != nil {
		return fmt.Errorf("sshftp: write public key: %w", err)
	}
	var priv bytes.Buffer
	privW, err := armor.Encode(&priv, openpgp.PrivateKeyType, nil)
	if err != nil {
		return fmt.Errorf("sshftp: armor private key: %w", err)
	}
	if err := e.SerializePrivate(privW, nil); err != nil {
		return fmt.Errorf("sshftp: serialize private key: %w", err)
	}
	if err := privW.Close(); err != nil {
		return fmt.Errorf("sshftp: close private key armor: %w", err)
	}
	if err := os.WriteFile(privPath, priv.Bytes(), 0o644); err != nil {
		return fmt.Errorf("sshftp: write private key: %w", err)
	}
	return nil
}
