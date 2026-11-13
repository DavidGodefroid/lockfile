package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
)

const humanConfig = `# my profiles
[profiles.samples] # local samples
source = "local"
path = "/srv/samples"

# keep this comment
`

func addArgs(name string) []string {
	return []string{"marketplace", "add", name, "--index", "https://example.org/" + name + "/index.json", "--identity-regexp", `^https://github\.com/example/` + name + `/`}
}

func TestMarketplaceAddNeedsTypedName(t *testing.T) {
	h, _ := humanEnv(t, "official")
	if code, out, _ := runArgs(t, "y\n", addArgs("acme")...); code != product.ExitDenied || !strings.Contains(out, "UNOFFICIAL MARKETPLACE acme") {
		t.Fatalf("y: exit %d: %s", code, out)
	}
	if _, err := os.Stat(h.configPath); !os.IsNotExist(err) {
		t.Fatal("config written without the typed name")
	}
	if code, out, errText := runArgs(t, "acme\n", addArgs("acme")...); code != 0 {
		t.Fatalf("typed: exit %d: %s %s", code, out, errText)
	}
	f, err := profile.ReadFile(h.configPath)
	if err != nil || len(f.Marketplaces) != 1 || f.Marketplaces[0].IdentityRegexp != `^https://github\.com/example/acme/` ||
		f.Marketplaces[0].OIDCIssuer != "https://token.actions.githubusercontent.com" {
		t.Fatalf("config %+v %v", f, err)
	}
	if code, _, errText := runArgs(t, "acme\n", addArgs("acme")...); code != product.ExitError || !strings.Contains(errText, "already configured") {
		t.Fatalf("duplicate: exit %d: %s", code, errText)
	}
	code, out, _ := runArgs(t, "", "marketplace", "list")
	if code != 0 || !strings.Contains(out, "official  https://") || !strings.Contains(out, "acme (https://example.org/acme/index.json") {
		t.Fatalf("list %d: %s", code, out)
	}
}

// The trust banner warns about a signer pattern other signers match.
func TestMarketplaceAddWarnsWeakSigner(t *testing.T) {
	humanEnv(t, "official")
	weak := []string{"marketplace", "add", "acme", "--index", "https://example.org/index.json", "--identity-regexp", `^https://github.com/acme`}
	code, out, _ := runArgs(t, "n\n", weak...)
	if code != product.ExitDenied || !strings.Contains(out, `WARNING the signer pattern has an unescaped "."`) ||
		!strings.Contains(out, "WARNING the signer pattern does not pin a repository path") {
		t.Fatalf("weak signer: exit %d: %s", code, out)
	}
	if _, out, _ := runArgs(t, "n\n", addArgs("acme")...); strings.Contains(out, "WARNING") {
		t.Fatalf("strict signer warned: %s", out)
	}
}

func writeTestKey(t *testing.T) (path, value string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	path = filepath.Join(t.TempDir(), "cosign.pub")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, base64.StdEncoding.EncodeToString(der)
}

func TestMarketplaceAddPublicKey(t *testing.T) {
	h, _ := humanEnv(t, "official")
	key, value := writeTestKey(t)
	args := []string{"marketplace", "add", "acme", "--index", "https://example.org/index.json", "--public-key", key}
	code, out, errText := runArgs(t, "acme\n", args...)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errText)
	}
	f, err := profile.ReadFile(h.configPath)
	if err != nil || len(f.Marketplaces) != 1 {
		t.Fatalf("config %+v %v", f, err)
	}
	tr := f.Marketplaces[0]
	if tr.PublicKey != value || tr.IdentityRegexp != "" || tr.OIDCIssuer != "" || tr.Marketplace().Root.Validate() != nil {
		t.Fatalf("trust %+v", tr)
	}
	if !strings.Contains(out, "key      sha256:"+tr.KeySHA256) || strings.Contains(out, "signer") || strings.Contains(out, value[20:40]) {
		t.Fatalf("banner: %s", out)
	}
	code, out, _ = runArgs(t, "", "marketplace", "list")
	if code != 0 || !strings.Contains(out, "acme (https://example.org/index.json, public key sha256:"+tr.KeySHA256+")") || strings.Contains(out, value[20:40]) {
		t.Fatalf("list %d: %s", code, out)
	}
}

func TestMarketplaceAddPublicKeyRefused(t *testing.T) {
	humanEnv(t, "official")
	key, value := writeTestKey(t)
	notKey := filepath.Join(t.TempDir(), "notkey.pub")
	if err := os.WriteFile(notKey, []byte("glsa_CanarySecretToken123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := []string{"marketplace", "add", "acme", "--index", "https://example.org/index.json"}
	for name, extra := range map[string][]string{
		"missing file":  {"--public-key", filepath.Join(t.TempDir(), "none.pub")},
		"not a key":     {"--public-key", notKey},
		"with identity": {"--public-key", key, "--identity-regexp", `^https://github\.com/acme/p/`},
		"with issuer":   {"--public-key", key, "--issuer", "https://token.actions.githubusercontent.com"},
		"neither":       {},
	} {
		code, out, errText := runArgs(t, "acme\n", append(append([]string{}, idx...), extra...)...)
		if code == 0 || code == product.ExitDenied {
			t.Errorf("%s: exit %d: %s", name, code, out)
		}
		if strings.Contains(errText+out, "glsa_Canary") || strings.Contains(errText+out, value[20:40]) {
			t.Errorf("%s: the output quotes the file: %s %s", name, out, errText)
		}
	}
}

func TestMarketplaceAddKeepsFile(t *testing.T) {
	h, _ := humanEnv(t, "official")
	if err := os.WriteFile(h.configPath, []byte(humanConfig), 0o640); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"acme", "beta"} {
		if code, _, errText := runArgs(t, n+"\n", addArgs(n)...); code != 0 {
			t.Fatal(errText)
		}
	}
	b, _ := os.ReadFile(h.configPath)
	if !strings.HasPrefix(string(b), humanConfig) {
		t.Fatalf("human part changed:\n%s", b)
	}
	if fi, _ := os.Stat(h.configPath); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if code, _, errText := runArgs(t, "", "marketplace", "remove", "acme"); code != 0 {
		t.Fatal(errText)
	}
	b, _ = os.ReadFile(h.configPath)
	f, err := profile.Parse(b, "config")
	if err != nil || len(f.Marketplaces) != 1 || f.Marketplaces[0].Name != "beta" || !strings.HasPrefix(string(b), humanConfig) {
		t.Fatalf("after remove:\n%s\n%v", b, err)
	}
	if code, _, _ := runArgs(t, "", "marketplace", "remove", "acme"); code != product.ExitError {
		t.Fatal("removed twice")
	}
}
