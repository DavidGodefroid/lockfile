package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveMarketplaceKeepsNextComments(t *testing.T) {
	const before = `# my config
[[marketplaces]]
name = "acme"
index_url = "https://example.org/acme/index.json"
identity_regexp = "^https://github\\.com/example/acme/"
oidc_issuer = "https://token.actions.githubusercontent.com"

# the beta marketplace, added for the pdf plugins
[[marketplaces]]
name = "beta"
index_url = "https://example.org/beta/index.json"
identity_regexp = "^https://github\\.com/example/beta/"
oidc_issuer = "https://token.actions.githubusercontent.com"

# samples, keep
[profiles.samples]
source = "local"
path = "/srv/samples"
`
	const after = `# my config

# the beta marketplace, added for the pdf plugins
[[marketplaces]]
name = "beta"
index_url = "https://example.org/beta/index.json"
identity_regexp = "^https://github\\.com/example/beta/"
oidc_issuer = "https://token.actions.githubusercontent.com"

# samples, keep
[profiles.samples]
source = "local"
path = "/srv/samples"
`
	const afterBeta = `# my config

# the beta marketplace, added for the pdf plugins

# samples, keep
[profiles.samples]
source = "local"
path = "/srv/samples"
`
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveMarketplace(path, "acme"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != after {
		t.Fatalf("after removing acme:\n%s", b)
	}
	if err := RemoveMarketplace(path, "beta"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != afterBeta {
		t.Fatalf("after removing beta:\n%s", b)
	}
	if err := RemoveMarketplace(path, "beta"); err == nil {
		t.Fatal("removed twice")
	}
}
