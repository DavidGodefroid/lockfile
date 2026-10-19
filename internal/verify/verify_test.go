package verify

import (
	"context"
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

func TestKeyRootRefused(t *testing.T) {
	bin, log := fakeCosign(t, 0)
	err := NewLockkit(update.Options{Cosign: bin}).VerifyBlob(context.Background(), "/x/blob", "/x/b.json", TrustRoot{PublicKeyPath: "/k.pub"})
	if !errors.Is(err, ErrPublicKeyRoot) {
		t.Fatalf("key root: %v", err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("cosign ran for a key root")
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
		{IdentityRegexp: "^x$", OIDCIssuer: "https://i", PublicKeyPath: "/k"},
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
