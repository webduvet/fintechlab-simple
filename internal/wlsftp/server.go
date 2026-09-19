package wlsftp

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/webduvet/fintechlab-simple/internal/sshftp"
	"golang.org/x/crypto/ssh"
)

// The four real Worldline SFT directories (see
// docs/ARCHITECTURE-vendor-corrections.md §2: two path conventions for the
// same content — /download + /disputes/download from one acquirer-fts
// config vintage, /to_WLNORDIC + /from_WLNORDIC from another — both must
// exist as real, listable directories under the server's root).
const (
	DirDownload         = "download"
	DirDisputesDownload = "disputes/download"
	DirToWLNordic       = "to_WLNORDIC"
	DirFromWLNordic     = "from_WLNORDIC"
)

// EnsureLayout creates the four real Worldline SFT directories under root
// if they do not already exist.
func EnsureLayout(root string) error {
	for _, d := range []string{DirDownload, DirDisputesDownload, DirToWLNordic, DirFromWLNordic} {
		dir := filepath.Join(root, filepath.FromSlash(d))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("wlsftp: create %s: %w", dir, err)
		}
	}
	return nil
}

// Logf matches log.Printf's signature, letting callers pass their own
// per-binary logger without this package importing "log" directly.
type Logf func(format string, args ...any)

// newServerConfig builds the SSH server authentication configuration.
//
// Password and public-key auth are enabled independently, and both may be
// on at once: real Worldline SFT accounts are key-authenticated, while a
// local run is far easier to drive with a password, and a client that
// offers a key and falls back to a password works against either without
// reconfiguration. Previously setting a password silently disabled key
// auth entirely, which meant the lab could not exercise the
// authentication method production actually uses.
//
// If neither is set, this lab accepts any authentication attempt at all --
// a deliberate, loudly-logged lab-only default, matching this repo's
// graceful-degradation philosophy rather than refusing to start.
func newServerConfig(password, authorizedKey string, logf Logf) (*ssh.ServerConfig, error) {
	cfg := &ssh.ServerConfig{}
	if password != "" {
		cfg.PasswordCallback = func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) != password {
				return nil, fmt.Errorf("wlsftp: invalid password for user %q", c.User())
			}
			return nil, nil
		}
	}
	if authorizedKey != "" {
		var allowed []ssh.PublicKey
		rest := []byte(authorizedKey)
		for len(bytes.TrimSpace(rest)) > 0 {
			key, _, _, remainder, err := ssh.ParseAuthorizedKey(rest)
			if err != nil {
				return nil, fmt.Errorf("wlsftp: parse WORLDLINE_SFTP_AUTHORIZED_KEY: %w", err)
			}
			allowed = append(allowed, key)
			rest = remainder
		}
		cfg.PublicKeyCallback = func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			for _, a := range allowed {
				if bytes.Equal(key.Marshal(), a.Marshal()) {
					return nil, nil
				}
			}
			return nil, fmt.Errorf("wlsftp: unrecognized public key for user %q", c.User())
		}
	}
	if password == "" && authorizedKey == "" {
		logf("wlsftp: WORLDLINE_SFTP_PASSWORD and WORLDLINE_SFTP_AUTHORIZED_KEY are both unset " +
			"-- accepting ANY SFTP authentication attempt. Lab-only default, do not use beyond local development.")
		cfg.PasswordCallback = func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }
		cfg.PublicKeyCallback = func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }
	}
	return cfg, nil
}

// ListenAndServe listens on addr and serves SSH/SFTP connections until ctx
// is cancelled or the listener fails to open; see Serve for the per-
// connection behavior.
func ListenAndServe(ctx context.Context, addr, root string, hostSigner ssh.Signer, password, authorizedKey string, logf Logf) error {
	return ListenAndServeObserved(ctx, addr, root, hostSigner, password, authorizedKey, logf, nil)
}

// Observer is told what a connected client did: a session opening, and
// then each request it made. It exists so the acquirer binary can show an
// operator that the platform's file pull actually happened — the single
// most common thing to be waiting on, and the one thing an SFTP server
// otherwise reveals only in its own stdout.
//
// Op is "session" when a client authenticates; otherwise it is the jail's
// own verb ("list", "download", "upload", "rename", …).
type Observer func(user, remote, op, path string, err error)

// ListenAndServeObserved is ListenAndServe with that hook.
func ListenAndServeObserved(ctx context.Context, addr, root string, hostSigner ssh.Signer, password, authorizedKey string, logf Logf, obs Observer) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("wlsftp: listen %s: %w", addr, err)
	}
	return ServeObserved(ctx, ln, root, hostSigner, password, authorizedKey, logf, obs)
}

// Serve accepts SSH connections on ln, serving an SFTP subsystem jailed at
// root for each session channel, until ctx is cancelled or ln itself fails. A single connection's protocol error is
// logged and only that connection is closed, never the whole listener —
// matching this lab's graceful-degradation style elsewhere (log and
// continue, never panic on a downstream/peer being unreachable or
// misbehaving). Serve takes ownership of ln and closes it before
// returning.
func Serve(ctx context.Context, ln net.Listener, root string, hostSigner ssh.Signer, password, authorizedKey string, logf Logf) error {
	return ServeObserved(ctx, ln, root, hostSigner, password, authorizedKey, logf, nil)
}

// ServeObserved is Serve with an Observer attached to every session.
func ServeObserved(ctx context.Context, ln net.Listener, root string, hostSigner ssh.Signer, password, authorizedKey string, logf Logf, obs Observer) error {
	sshConfig, err := newServerConfig(password, authorizedKey, logf)
	if err != nil {
		_ = ln.Close()
		return err
	}
	sshConfig.AddHostKey(hostSigner)

	defer ln.Close()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		nConn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("wlsftp: accept: %w", err)
		}
		go handleConn(nConn, sshConfig, root, logf, obs)
	}
}

func handleConn(nConn net.Conn, sshConfig *ssh.ServerConfig, root string, logf Logf, obs Observer) {
	remote := hostOf(nConn.RemoteAddr())
	sshConn, chans, reqs, err := ssh.NewServerConn(nConn, sshConfig)
	if err != nil {
		logf("wlsftp: ssh handshake from %s: %v", nConn.RemoteAddr(), err)
		// A refused handshake is worth seeing: a client with the wrong key
		// and a client that never connected look the same from the outside.
		if obs != nil {
			obs("", remote, "session", "", err)
		}
		return
	}
	user := sshConn.User()
	if obs != nil {
		obs(user, remote, "session", "", nil)
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			logf("wlsftp: accept channel from %s: %v", nConn.RemoteAddr(), err)
			continue
		}
		go serveSessionRequests(requests)
		go sshftp.ServeSFTPObserved(channel, root, func(f string, a ...any) {
			if logf != nil {
				logf(f, a...)
			}
		}, func(op, path string, err error) {
			if obs != nil {
				obs(user, remote, op, path, err)
			}
		})
	}
}

// serveSessionRequests answers the session channel's out-of-band requests,
// accepting only the "subsystem sftp" request an SFTP client sends.
func serveSessionRequests(in <-chan *ssh.Request) {
	for req := range in {
		ok := req.Type == "subsystem" && len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp"
		if req.WantReply {
			_ = req.Reply(ok, nil)
		}
	}
}

// hostOf drops the ephemeral port from a client address: the port changes
// on every connection and is never the thing an operator is looking at.
func hostOf(addr net.Addr) string {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}
