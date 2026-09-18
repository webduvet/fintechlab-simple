package wlsftp

import (
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// ClientConfig points a settlement-file consumer at an SFTP server. It is
// deliberately the whole configuration surface: swapping this lab's
// simulator for real Worldline is meant to be a change of these values and
// nothing else, which is only true if the client here uses the same
// authentication and the same directory conventions the real one does.
type ClientConfig struct {
	Host string
	Port string
	User string

	// Password and PrivateKeyPath are the two authentication methods a
	// real Worldline SFT account supports. Both may be set: the client
	// offers the key first and falls back to the password, which is what
	// an ssh client does and what lets one configuration work against both
	// a key-only real host and a password-only local one.
	Password       string
	PrivateKeyPath string

	// HostKeyPath, when set, pins the server's public host key. Left
	// empty the client accepts any host key and says so, because a lab
	// that refuses to connect until you have copied a fingerprint around
	// is a lab nobody runs -- but a real deployment must set it.
	HostKeyPath string

	// Dir is the remote directory to list and download from. Defaults to
	// DirDownload.
	Dir string

	Timeout time.Duration
}

// RemoteFile is one file seen on the server.
type RemoteFile struct {
	Name    string
	Size    int64
	ModTime time.Time
}

// Client is a connected SFTP session. Close it when done.
type Client struct {
	ssh  *ssh.Client
	sftp *sftp.Client
	dir  string
}

// Dial opens an SSH+SFTP session using cfg.
func Dial(cfg ClientConfig) (*Client, error) {
	auth, err := authMethods(cfg)
	if err != nil {
		return nil, err
	}
	hostKey, err := hostKeyCallback(cfg)
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	addr := fmt.Sprintf("%s:%s", cfg.Host, cfg.Port)
	conn, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auth,
		HostKeyCallback: hostKey,
		Timeout:         timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("wlsftp: dial %s: %w", addr, err)
	}
	sc, err := sftp.NewClient(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("wlsftp: open sftp subsystem on %s: %w", addr, err)
	}
	dir := cfg.Dir
	if dir == "" {
		dir = DirDownload
	}
	return &Client{ssh: conn, sftp: sc, dir: dir}, nil
}

func authMethods(cfg ClientConfig) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if cfg.PrivateKeyPath != "" {
		pem, err := os.ReadFile(cfg.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("wlsftp: read client key %s: %w", cfg.PrivateKeyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(pem)
		if err != nil {
			return nil, fmt.Errorf("wlsftp: parse client key %s: %w", cfg.PrivateKeyPath, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if cfg.Password != "" {
		methods = append(methods, ssh.Password(cfg.Password))
	}
	if len(methods) == 0 {
		return nil, fmt.Errorf("wlsftp: no authentication configured (set a password or a private key)")
	}
	return methods, nil
}

// hostKeyCallback pins the server's key when one is configured. The
// unpinned fallback is explicit rather than implicit so that reading this
// function tells you exactly what security you are getting.
func hostKeyCallback(cfg ClientConfig) (ssh.HostKeyCallback, error) {
	if cfg.HostKeyPath == "" {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	pem, err := os.ReadFile(cfg.HostKeyPath)
	if err != nil {
		return nil, fmt.Errorf("wlsftp: read host key %s: %w", cfg.HostKeyPath, err)
	}
	// Accept either an authorized_keys-style public key or a private key
	// whose public half is taken -- this lab generates the latter, a real
	// deployment hands you the former.
	if pub, _, _, _, err := ssh.ParseAuthorizedKey(pem); err == nil {
		return ssh.FixedHostKey(pub), nil
	}
	signer, err := ssh.ParsePrivateKey(pem)
	if err != nil {
		return nil, fmt.Errorf("wlsftp: host key %s is neither a public nor a private key", cfg.HostKeyPath)
	}
	return ssh.FixedHostKey(signer.PublicKey()), nil
}

// Close releases the SFTP and SSH connections.
func (c *Client) Close() error {
	if c.sftp != nil {
		_ = c.sftp.Close()
	}
	if c.ssh != nil {
		return c.ssh.Close()
	}
	return nil
}

// List returns the files in the configured remote directory, sorted by
// name -- which, given the leading YYYYMMDDHHMMSS in the settlement
// filename pattern, is also oldest-first.
func (c *Client) List() ([]RemoteFile, error) {
	infos, err := c.sftp.ReadDir(c.dir)
	if err != nil {
		return nil, fmt.Errorf("wlsftp: list %s: %w", c.dir, err)
	}
	out := make([]RemoteFile, 0, len(infos))
	for _, fi := range infos {
		if fi.IsDir() {
			continue
		}
		out = append(out, RemoteFile{Name: fi.Name(), Size: fi.Size(), ModTime: fi.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Download fetches one file and, when it carries the ".pgp" suffix,
// decrypts it with keyring. A file without that suffix is returned as-is:
// this is a transport, and deciding a plain file must be encrypted is the
// caller's policy, not the transport's.
func (c *Client) Download(name string, keyring openpgp.EntityList) ([]byte, error) {
	remote := path.Join(c.dir, name)
	f, err := c.sftp.Open(remote)
	if err != nil {
		return nil, fmt.Errorf("wlsftp: open %s: %w", remote, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("wlsftp: read %s: %w", remote, err)
	}
	if !strings.HasSuffix(name, ".pgp") {
		return raw, nil
	}
	plaintext, err := Decrypt(raw, keyring)
	if err != nil {
		return nil, fmt.Errorf("wlsftp: decrypt %s: %w", remote, err)
	}
	return plaintext, nil
}
