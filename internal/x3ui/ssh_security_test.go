package x3ui

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestHostKeyVerification(t *testing.T) {
	dir := t.TempDir()
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(original)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "test_key")
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	pinned, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "known_hosts"), []byte(knownhosts.Line([]string{"host.test"}, pinned)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sh := &ServerHandler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	config, err := sh.createSshConfig("test", keyPath)
	if err != nil {
		t.Fatal(err)
	}
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22}
	if err = config.HostKeyCallback("host.test:22", addr, pinned); err != nil {
		t.Fatal("pinned host rejected")
	}
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := ssh.NewPublicKey(other)
	if err != nil {
		t.Fatal(err)
	}
	if err = config.HostKeyCallback("host.test:22", addr, changed); err == nil {
		t.Fatal("changed host key accepted")
	}
	if err = config.HostKeyCallback("unknown.test:22", addr, changed); err == nil {
		t.Fatal("unknown host key accepted")
	}
}
