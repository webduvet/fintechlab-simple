package wlsftp

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func TestLoadOrGenerateHostKeyIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host_key")

	first, err := LoadOrGenerateHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	second, err := LoadOrGenerateHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	dataAgain, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(first.PublicKey().Marshal()) != string(second.PublicKey().Marshal()) {
		t.Fatal("second call generated a different host key instead of reading back the persisted one")
	}
	if string(data) != string(dataAgain) {
		t.Fatal("host key file was rewritten on the second call")
	}
}

func TestEnsureLayoutCreatesAllFourRealDirectories(t *testing.T) {
	root := t.TempDir()
	if err := EnsureLayout(root); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{DirDownload, DirDisputesDownload, DirToWLNordic, DirFromWLNordic} {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(d)))
		if err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", d)
		}
	}
}

// TestSSHSFTPWireProtocolRoundTrip actually dials the server over a real
// TCP loopback connection with golang.org/x/crypto/ssh + github.com/pkg/sftp
// clients, proving the SSH handshake, password auth, and SFTP subsystem
// wiring work end-to-end, not just that the Go types compile.
func TestSSHSFTPWireProtocolRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := EnsureLayout(root); err != nil {
		t.Fatal(err)
	}
	want := []byte("encrypted-settlement-file-content")
	if err := os.WriteFile(filepath.Join(root, DirDownload, "sample.csv.pgp"), want, 0o644); err != nil {
		t.Fatal(err)
	}

	hostSigner, err := LoadOrGenerateHostKey(filepath.Join(t.TempDir(), "host_key"))
	if err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- Serve(ctx, ln, root, hostSigner, "test-password", "", t.Logf)
	}()

	clientConfig := &ssh.ClientConfig{
		User:            "worldline",
		Auth:            []ssh.AuthMethod{ssh.Password("test-password")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	conn, err := ssh.Dial("tcp", ln.Addr().String(), clientConfig)
	if err != nil {
		t.Fatalf("ssh dial: %v", err)
	}
	defer conn.Close()

	client, err := sftp.NewClient(conn)
	if err != nil {
		t.Fatalf("sftp client: %v", err)
	}
	defer client.Close()

	entries, err := client.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}
	for _, want := range []string{"download", "disputes", "to_WLNORDIC", "from_WLNORDIC"} {
		if !names[want] {
			t.Errorf("root listing missing %q: got %v", want, names)
		}
	}

	f, err := client.Open("download/sample.csv.pgp")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	got, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("file content = %q, want %q", got, want)
	}

	cancel()
	<-serveErr
}

func TestSFTPJail(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "jail")
	if err := EnsureLayout(root); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "secret.txt")
	if err := os.WriteFile(outside, []byte("PRIVATE"), 0o644); err != nil {
		t.Fatal(err)
	}
	hostSigner, err := LoadOrGenerateHostKey(filepath.Join(t.TempDir(), "host_key"))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = Serve(ctx, ln, root, hostSigner, "pw", "", t.Logf) }()

	conn, err := ssh.Dial("tcp", ln.Addr().String(), &ssh.ClientConfig{
		User:            "worldline",
		Auth:            []ssh.AuthMethod{ssh.Password("pw")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client, err := sftp.NewClient(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if f, err := client.Open(outside); err == nil {
		b, _ := io.ReadAll(f)
		f.Close()
		if bytes.Contains(b, []byte("PRIVATE")) {
			t.Fatal("absolute path read the host secret")
		}
	}
	if f, err := client.Open("../secret.txt"); err == nil {
		b, _ := io.ReadAll(f)
		f.Close()
		if bytes.Contains(b, []byte("PRIVATE")) {
			t.Fatal("relative .. read the host secret")
		}
	}
}

func TestSSHRejectsWrongPassword(t *testing.T) {
	root := t.TempDir()
	if err := EnsureLayout(root); err != nil {
		t.Fatal(err)
	}
	hostSigner, err := LoadOrGenerateHostKey(filepath.Join(t.TempDir(), "host_key"))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- Serve(ctx, ln, root, hostSigner, "correct-password", "", t.Logf)
	}()

	clientConfig := &ssh.ClientConfig{
		User:            "worldline",
		Auth:            []ssh.AuthMethod{ssh.Password("wrong-password")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	if _, err := ssh.Dial("tcp", ln.Addr().String(), clientConfig); err == nil {
		t.Fatal("expected wrong password to be rejected")
	}

	cancel()
	<-serveErr
}
