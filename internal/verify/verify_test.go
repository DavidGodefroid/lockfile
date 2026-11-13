package verify

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/update"
)

// fakeCosign writes a cosign that records its argv and exits with code.
func fakeCosign(t *testing.T, code int) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "argv")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > " + log + "\nexit " + string(rune('0'+code)) + "\n"
	bin = filepath.Join(dir, "cosign")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func argv(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

func TestLockkitVerifierArgs(t *testing.T) {
	bin, log := fakeCosign(t, 0)
	v := NewLockkit(update.Options{Cosign: bin})
	if err := v.VerifyBlob(context.Background(), "/x/blob", "/x/blob.sigstore.json", Official); err != nil {
		t.Fatal(err)
	}
	want := "verify-blob --bundle /x/blob.sigstore.json --certificate-identity-regexp " + Official.IdentityRegexp +
		" --certificate-oidc-issuer https://token.actions.githubusercontent.com /x/blob"
	if got := argv(t, log); got != want {
		t.Fatalf("argv\n got %s\nwant %s", got, want)
	}
}

func TestVerifyFails(t *testing.T) {
	bin, _ := fakeCosign(t, 1)
	err := NewLockkit(update.Options{Cosign: bin}).VerifyBlob(context.Background(), "/x/blob", "/x/b.json", Official)
	var ve *update.VerifyError
	if !errors.As(err, &ve) {
		t.Fatalf("non-zero cosign exit: %v", err)
	}
}

func testKeyPEM(t *testing.T) string {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func TestLockkitVerifierKey(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	// Records argv, then the content of the file after --key (lockkit
	// writes the PEM to a temporary file and removes it after the run).
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > " + log + "\n" +
		"while [ $# -gt 0 ]; do if [ \"$1\" = --key ]; then cat \"$2\" > " + log + ".key; fi; shift; done\n"
	bin := filepath.Join(dir, "cosign")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	key := testKeyPEM(t)
	if err := NewLockkit(update.Options{Cosign: bin}).VerifyBlob(context.Background(), "/x/blob", "/x/b.json", TrustRoot{PublicKey: key}); err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(argv(t, log))
	if len(got) != 6 || got[0] != "verify-blob" || got[1] != "--bundle" || got[2] != "/x/b.json" || got[3] != "--key" || got[5] != "/x/blob" {
		t.Fatalf("argv = %q", got)
	}
	if k, _ := os.ReadFile(log + ".key"); string(k) != key {
		t.Fatalf("cosign got key %q, want the root's PEM", k)
	}
}

func TestLockkitVerifierBadKey(t *testing.T) {
	bin, log := fakeCosign(t, 0)
	const secretish = "-----BEGIN PUBLIC KEY-----\nbm90IGEga2V5IGF0IGFsbCwganVzdCBieXRlcw==\n-----END PUBLIC KEY-----\n"
	err := NewLockkit(update.Options{Cosign: bin}).VerifyBlob(context.Background(), "/x/blob", "/x/b.json", TrustRoot{PublicKey: secretish})
	if err == nil {
		t.Fatal("a key that does not parse was accepted")
	}
	if strings.Contains(err.Error(), "bm90IGEga2V5") {
		t.Fatalf("the error quotes the key: %v", err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("cosign ran with a bad key")
	}
}

func TestCosignMissingRefuses(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := NewLockkit(update.Options{}).VerifyBlob(context.Background(), "/x/blob", "/x/b.json", Official)
	if !errors.Is(err, ErrCosignMissing) {
		t.Fatalf("no cosign: %v", err)
	}
}

func TestTrustRootExactlyOne(t *testing.T) {
	for _, r := range []TrustRoot{
		{},
		{IdentityRegexp: "^x$"},
		{IdentityRegexp: "^x$", OIDCIssuer: "https://i", PublicKey: "k"},
	} {
		if r.Validate() == nil {
			t.Errorf("accepted %+v", r)
		}
	}
	if err := Official.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestOfficialIdentity(t *testing.T) {
	if Official.IdentityRegexp != `^https://github\.com/DavidGodefroid/lockfile-marketplace/\.github/workflows/release\.yml@refs/tags/` ||
		Official.OIDCIssuer != "https://token.actions.githubusercontent.com" {
		t.Fatalf("Official = %+v", Official)
	}
}
