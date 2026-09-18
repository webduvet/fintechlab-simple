package harness

import (
	"fmt"

	"github.com/ProtonMail/go-crypto/openpgp"

	"github.com/webduvet/fintechlab-simple/internal/wlsftp"
)

// downloadWorldlineFile dials the acquirer's real SFTP+PGP channel,
// downloads one file from its download/ directory, and decrypts it with
// the PGP keypair worldline generated -- loaded, not regenerated, via the
// shared wlsftp-keys volume (LoadOrGenerateKeypair reads an existing
// keypair back rather than creating a new one).
//
// This is the harness's own second opinion. The platform pulls the same
// file through the same channel; having the harness do it independently is
// what makes a passing run mean the bytes were really on the server,
// rather than that the platform reported success about a file only it
// could see.
func downloadWorldlineFile(env *Env, filename string) ([]byte, error) {
	c, err := wlsftp.Dial(wlsftp.ClientConfig{
		Host:     env.WorldlineSFTPHost,
		Port:     env.WorldlineSFTPPort,
		User:     "harness",
		Password: env.WorldlineSFTPPassword,
		// No host-key pinning: this is a throwaway test client, and the
		// platform side (cmd/settlement) is where pinning is configured
		// and therefore where it matters that it works.
	})
	if err != nil {
		return nil, fmt.Errorf("harness: %w", err)
	}
	defer c.Close()

	own, err := wlsftp.LoadOrGenerateKeypair(env.WorldlinePGPPublicKeyPath, env.WorldlinePGPPrivateKeyPath,
		"harness", "fintechlab-simple", "harness@fintechlab-simple.local")
	if err != nil {
		return nil, fmt.Errorf("harness: load worldline pgp keypair: %w", err)
	}
	plaintext, err := c.Download(filename, openpgp.EntityList{own})
	if err != nil {
		return nil, fmt.Errorf("harness: %w", err)
	}
	return plaintext, nil
}
