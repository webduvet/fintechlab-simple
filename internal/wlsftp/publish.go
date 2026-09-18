package wlsftp

import (
	"fmt"
	"os"
	"path/filepath"
)

// Publish writes one settlement file into the SFTP server's own
// directories, PGP-encrypted and signed, under filename+".pgp".
//
// This replaces the previous arrangement, where the platform wrote plain
// files into a bind mount shared with the SFTP server and a background
// poller mirrored them across. That made the platform the producer of a
// file it was meant to be receiving, and meant nothing ever exercised the
// download path. Here the acquirer writes into its own root and the
// platform's only route to the content is over the wire.
//
// The file lands in both real path conventions (download/ and
// to_WLNORDIC/): the same content is reachable under either, which is what
// a real Worldline SFT account exposes.
func Publish(root, filename string, plaintext []byte, keys *PGPKeys) (string, error) {
	if keys == nil {
		return "", fmt.Errorf("wlsftp: publish %s: no PGP keys configured", filename)
	}
	encrypted, err := keys.EncryptAndSign(plaintext)
	if err != nil {
		return "", fmt.Errorf("wlsftp: encrypt %s: %w", filename, err)
	}
	outName := PGPFilename(filename)
	for _, d := range []string{DirDownload, DirToWLNordic} {
		dir := filepath.Join(root, filepath.FromSlash(d))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("wlsftp: create %s: %w", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, outName), encrypted, 0o644); err != nil {
			return "", fmt.Errorf("wlsftp: write %s: %w", filepath.Join(dir, outName), err)
		}
	}
	return outName, nil
}
