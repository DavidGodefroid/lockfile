package backend

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/console"

	"github.com/DavidGodefroid/lockfile/internal/profile"
	"github.com/DavidGodefroid/lockfile/internal/source"
	"github.com/DavidGodefroid/lockfile/internal/testenv"
)

const payload = "%PDF-1.7 synthetic"

func readAll(t *testing.T, s *session, key string) string {
	t.Helper()
	rc, err := s.src.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestConnectS3UsesUserAndSecret(t *testing.T) {
	srv := testenv.NewS3(t, "documents")
	srv.Put(t, "invoices/a.pdf", []byte(payload))
	p := profile.Profile{Source: profile.SourceS3, Prefix: "invoices/",
		S3: source.S3Config{Bucket: "documents", Region: "us-east-1", Endpoint: srv.Endpoint, Prefix: "invoices/"}}
	if _, err := connect(p, console.Credentials{User: "", Secret: []byte("x")}); err == nil {
		t.Fatal("no access key id accepted")
	}
	if _, err := connect(p, console.Credentials{User: "id"}); err == nil {
		t.Fatal("no secret accepted")
	}
	secret := []byte("reader-secret")
	s, err := connect(p, console.Credentials{User: "id", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	// The console wipes the secret after Connect: the S3 client copied it.
	for i := range secret {
		secret[i] = 0
	}
	if got := readAll(t, s, "invoices/a.pdf"); got != payload {
		t.Fatalf("read %q", got)
	}
}

func TestConnectLocalNeedsNoSecret(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.pdf"), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := connect(profile.Profile{Source: profile.SourceLocal, LocalPath: root}, console.Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, s, "a.pdf"); got != payload {
		t.Fatalf("read %q", got)
	}
}

// sftpProfile reads under base dir root (the test server serves the real
// file system, so absolute paths are host paths).
func sftpProfile(srv *testenv.SFTP, auth, key, root string) profile.Profile {
	return profile.Profile{Source: profile.SourceSFTP, SFTPAuth: auth, SFTPKey: key,
		SFTP: source.SFTPConfig{Host: srv.Host, Port: srv.Port, User: srv.User, KnownHosts: srv.KnownHosts, BaseDir: root}}
}

func writeKey(t *testing.T, pemKey []byte, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(p, pemKey, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func sftpRoot(t *testing.T) string {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.pdf"), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSFTPPassword(t *testing.T) {
	root := sftpRoot(t)
	srv := testenv.NewSFTP(t, root, testenv.SFTPOptions{User: "reader", Password: "pw"})
	s, err := connect(sftpProfile(srv, profile.AuthPassword, "", root), console.Credentials{User: "reader", Secret: []byte("pw")})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, s, "a.pdf"); got != payload {
		t.Fatalf("read %q", got)
	}
}

func TestSFTPKnownHostsInHome(t *testing.T) {
	root := sftpRoot(t)
	srv := testenv.NewSFTP(t, root, testenv.SFTPOptions{User: "reader", Password: "pw"})
	home := t.TempDir()
	t.Setenv("HOME", home)
	kh, err := os.ReadFile(srv.KnownHosts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), kh, 0o600); err != nil {
		t.Fatal(err)
	}
	p := sftpProfile(srv, profile.AuthPassword, "", root)
	p.SFTP.KnownHosts = "~/.ssh/known_hosts"
	s, err := connect(p, console.Credentials{User: "reader", Secret: []byte("pw")})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, s, "a.pdf"); got != payload {
		t.Fatalf("read %q", got)
	}
}

func TestSFTPEncryptedKey(t *testing.T) {
	pemKey, pub := testenv.ClientKey(t, []byte("passphrase"))
	root := sftpRoot(t)
	srv := testenv.NewSFTP(t, root, testenv.SFTPOptions{User: "reader", AuthorizedKey: pub})
	p := sftpProfile(srv, profile.AuthKey, writeKey(t, pemKey, 0o600), root)
	if _, err := connect(p, console.Credentials{User: "reader", Secret: []byte("wrong")}); err == nil {
		t.Fatal("wrong passphrase accepted")
	}
	s, err := connect(p, console.Credentials{User: "reader", Secret: []byte("passphrase")})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, s, "a.pdf"); got != payload {
		t.Fatalf("read %q", got)
	}
}

func TestSFTPUnencryptedKey(t *testing.T) {
	pemKey, pub := testenv.ClientKey(t, nil)
	root := sftpRoot(t)
	srv := testenv.NewSFTP(t, root, testenv.SFTPOptions{User: "reader", AuthorizedKey: pub})
	s, err := connect(sftpProfile(srv, profile.AuthKey, writeKey(t, pemKey, 0o400), root), console.Credentials{User: "reader"})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, s, "a.pdf"); got != payload {
		t.Fatalf("read %q", got)
	}
}

func TestSFTPKeyFileModeRefused(t *testing.T) {
	pemKey, _ := testenv.ClientKey(t, nil)
	path := writeKey(t, pemKey, 0o644)
	srv := &testenv.SFTP{Host: "127.0.0.1", Port: 1, User: "reader", KnownHosts: "/dev/null"}
	_, err := connect(sftpProfile(srv, profile.AuthKey, path, "/"), console.Credentials{})
	if err == nil {
		t.Fatal("0644 key accepted")
	}
	if strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "(key)") {
		t.Fatalf("error = %v", err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(writeKey(t, pemKey, 0o600), link); err != nil {
		t.Fatal(err)
	}
	if _, err := connect(sftpProfile(srv, profile.AuthKey, link, "/"), console.Credentials{}); err == nil {
		t.Fatal("symlinked key accepted")
	}
}

// An unknown source kind (a pasted token in source) is not quoted back.
func TestConnectUnknownSourceDoesNotEcho(t *testing.T) {
	const canary = "glsa_CanarySecretToken123"
	_, err := connect(profile.Profile{Source: canary}, console.Credentials{})
	if err == nil || strings.Contains(err.Error(), "glsa_Canary") || !strings.Contains(err.Error(), "source") {
		t.Fatalf("err = %v", err)
	}
}
