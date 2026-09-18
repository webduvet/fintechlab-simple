package sshftp

import (
	"bytes"
	"context"
	"fmt"
	"net"

	"golang.org/x/crypto/ssh"
)

// Logf matches log.Printf.
type Logf func(format string, args ...any)

// Auth is how an SFTP session authenticates.
//
// It is a struct rather than two strings so that "nobody is checked" has
// to be written down. A caller that supplies no credential and does not
// set Open is refused: an unauthenticated SFTP server is a thing somebody
// chose, never a thing that happened because a config key was missing.
type Auth struct {
	Password      string
	AuthorizedKey string
	// Open accepts any credential presented. Lab-only, and never inferred
	// from the absence of the other two.
	Open bool
}

// ListenAndServe binds addr and serves SFTP rooted at root until ctx is done.
func ListenAndServe(ctx context.Context, addr, root string, hostSigner ssh.Signer, auth Auth, logf Logf) (net.Listener, error) {
	if _, err := serverConfig(auth, func(string, ...any) {}); err != nil {
		// Fail before binding: a listener that exists but cannot accept is
		// worse than no listener, because the schematic would show it.
		return nil, err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("sshftp: listen %s: %w", addr, err)
	}
	go func() {
		if err := Serve(ctx, ln, root, hostSigner, auth, logf); err != nil && logf != nil {
			logf("sshftp: serve: %v", err)
		}
	}()
	return ln, nil
}

// Serve accepts SSH connections on ln until ctx is cancelled.
func Serve(ctx context.Context, ln net.Listener, root string, hostSigner ssh.Signer, auth Auth, logf Logf) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	sshConfig, err := serverConfig(auth, logf)
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
			return fmt.Errorf("sshftp: accept: %w", err)
		}
		go handleConn(nConn, sshConfig, root, logf)
	}
}

func serverConfig(auth Auth, logf Logf) (*ssh.ServerConfig, error) {
	password, authorizedKey := auth.Password, auth.AuthorizedKey
	if auth.Open && (password != "" || authorizedKey != "") {
		return nil, fmt.Errorf("sshftp: auth is Open and also carries a credential — pick one")
	}
	if !auth.Open && password == "" && authorizedKey == "" {
		return nil, fmt.Errorf("sshftp: no password and no authorized key — set one, or set Open to accept any credential on purpose")
	}
	cfg := &ssh.ServerConfig{}
	if password != "" {
		cfg.PasswordCallback = func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) != password {
				return nil, fmt.Errorf("sshftp: invalid password for user %q", c.User())
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
				return nil, fmt.Errorf("sshftp: parse authorized key: %w", err)
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
			return nil, fmt.Errorf("sshftp: unrecognized public key for user %q", c.User())
		}
	}
	if auth.Open {
		logf("sshftp: auth.open is set — accepting ANY SFTP authentication. Lab-only.")
		cfg.PasswordCallback = func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }
		cfg.PublicKeyCallback = func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }
	}
	return cfg, nil
}

func handleConn(nConn net.Conn, sshConfig *ssh.ServerConfig, root string, logf Logf) {
	sshConn, chans, reqs, err := ssh.NewServerConn(nConn, sshConfig)
	if err != nil {
		logf("sshftp: ssh handshake from %s: %v", nConn.RemoteAddr(), err)
		return
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
			logf("sshftp: accept channel from %s: %v", nConn.RemoteAddr(), err)
			continue
		}
		go serveSessionRequests(requests)
		go ServeSFTP(channel, root, logf)
	}
}

func serveSessionRequests(in <-chan *ssh.Request) {
	for req := range in {
		ok := req.Type == "subsystem" && len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp"
		if req.WantReply {
			_ = req.Reply(ok, nil)
		}
	}
}
