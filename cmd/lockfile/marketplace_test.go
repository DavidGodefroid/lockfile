package main

import (
	"os"
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

func TestMarketplaceKeyRootRefused(t *testing.T) {
	humanEnv(t, "official")
	code, _, errText := runArgs(t, "acme\n", "marketplace", "add", "acme", "--index", "https://example.org/index.json", "--public-key", "/k.pub")
	if code != product.ExitError || !strings.Contains(errText, "public-key trust roots need lockkit support") {
		t.Fatalf("exit %d: %s", code, errText)
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
