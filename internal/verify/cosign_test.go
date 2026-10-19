package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCosign puts a cosign on PATH that records its argv and exits with code.
func fakeCosign(t *testing.T, code int) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > " + log + "\nexit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "cosign"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return log
}

func argv(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

func TestVerifyArgs(t *testing.T) {
	log := fakeCosign(t, 0)
	v, err := NewCosign()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.VerifyBlob(context.Background(), "/x/blob", "/x/blob.sigstore.json", Official); err != nil {
		t.Fatal(err)
	}
	want := "verify-blob --bundle /x/blob.sigstore.json --certificate-identity-regexp " + Official.IdentityRegexp +
		" --certificate-oidc-issuer https://token.actions.githubusercontent.com /x/blob"
	if got := argv(t, log); got != want {
		t.Fatalf("argv\n got %s\nwant %s", got, want)
	}

	if err := v.VerifyBlob(context.Background(), "/x/blob", "/x/b.json", TrustRoot{PublicKeyPath: "/k.pub"}); err != nil {
		t.Fatal(err)
	}
	if got := argv(t, log); got != "verify-blob --bundle /x/b.json --key /k.pub /x/blob" {
		t.Fatalf("key argv %s", got)
	}
}

func TestVerifyFails(t *testing.T) {
	fakeCosign(t, 1)
	v, err := NewCosign()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.VerifyBlob(context.Background(), "/x/blob", "/x/b.json", Official); err == nil {
		t.Fatal("non-zero cosign exit accepted")
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

func TestNoCosign(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := NewCosign(); err == nil {
		t.Fatal("NewCosign without cosign on PATH")
	}
}

func TestOfficialIdentity(t *testing.T) {
	if Official.IdentityRegexp != `^https://github\.com/DavidGodefroid/lockfile-marketplace/\.github/workflows/release\.yml@refs/tags/` ||
		Official.OIDCIssuer != "https://token.actions.githubusercontent.com" {
		t.Fatalf("Official = %+v", Official)
	}
}
