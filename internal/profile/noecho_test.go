package profile

import (
	"path/filepath"
	"strings"
	"testing"
)

// Canaries: a pasted token, and a URL whose userinfo holds one.
const (
	canary    = "glsa_CanarySecretToken123"
	urlCanary = "https://user:glsa_Canary@h"
	canaryHit = "glsa_Canary"
)

// TestErrorsDoNotEchoValues: a value typed in the config (a secret pasted
// in the wrong key, a URL with a token) never appears in an error, which
// still names the key.
func TestErrorsDoNotEchoValues(t *testing.T) {
	s3 := "[profiles.dev]\nsource = \"s3\"\nbucket = \"documents\"\nregion = \"eu-west-1\"\n"
	sftp := "[profiles.dev]\nsource = \"sftp\"\nhost = \"sftp.example.org\"\nuser = \"reader\"\nknown_hosts = \"/k\"\n"
	mk := func(name, index, identity, issuer string) string {
		return s3 + "[[marketplaces]]\nname = \"" + name + "\"\nindex_url = \"" + index + "\"\nidentity_regexp = \"" + identity + "\"\noidc_issuer = \"" + issuer + "\"\n"
	}
	okIndex, okIdentity, okIssuer := "https://plugins.example.org/index.json", "^https://x/", "https://x"
	for _, tc := range []struct{ key, cfg string }{
		{"source", "[profiles.dev]\nsource = \"" + canary + "\"\n"},
		{"source", "[profiles.dev]\nsource = \"" + urlCanary + "\"\n"},
		{"credentials", s3 + "credentials = \"" + canary + "\"\n"},
		{"auth", sftp + "auth = \"" + canary + "\"\n"},
		{"endpoint", s3 + "endpoint = \"" + urlCanary + "\"\n"},
		{"endpoint", s3 + "endpoint = \"http://" + canary + ".example.org\"\n"},
		{"credentials_ttl", s3 + "credentials_ttl = \"" + canary + "\"\n"},
		{"idle_timeout", s3 + "idle_timeout = \"" + canary + "\"\n"},
		{"max_session", s3 + "max_session = \"" + canary + "\"\n"},
		{"handle_ttl", s3 + "handle_ttl = \"" + canary + "\"\n"},
		{"answer_quantum", s3 + "answer_quantum = \"" + canary + "\"\n"},
		{"max_object_bytes", s3 + "max_object_bytes = \"" + canary + "\"\n"},
		{"vault_path", s3 + "credentials = \"vault\"\nvault_path = \"-" + canary + "\"\n"},
		{"vault_path", s3 + "vault_path = \"" + canary + "\"\n"},
		{"user", s3 + "user = \"" + canary + "\\u0007\"\n"},
		{"port", sftp + "auth = \"password\"\nport = \"" + canary + "\"\n"},
		{"path", "[profiles.dev]\nsource = \"local\"\npath = \"" + canary + "\"\n"},
		{"credentials", "[profiles.dev]\nsource = \"local\"\npath = \"/srv\"\ncredentials = \"" + canary + "\"\n"},
		{"work_dir", "work_dir = \"" + canary + "\"\n"},
		{"max_workspace_bytes", "max_workspace_bytes = \"" + canary + "\"\n"},
		{"name", mk(canary, okIndex, okIdentity, okIssuer)},
		{"name", mk("official", okIndex, okIdentity, okIssuer)},
		{"index_url", mk("acme", urlCanary, okIdentity, okIssuer)},
		{"index_url", mk("acme", "http://"+canary+".example.org/index.json", okIdentity, okIssuer)},
		{"identity_regexp", mk("acme", okIndex, canary, okIssuer)},
		{"identity_regexp", mk("acme", okIndex, "^https://("+canary, okIssuer)},
		{"oidc_issuer", mk("acme", okIndex, okIdentity, canary)},
		{"public-key", s3 + "[[marketplaces]]\nname = \"acme\"\nindex_url = \"" + okIndex + "\"\npublic_key = \"" + canary + "\"\n"},
	} {
		_, err := Parse([]byte(tc.cfg), "c")
		if err == nil {
			t.Errorf("%s: accepted:\n%s", tc.key, tc.cfg)
			continue
		}
		if strings.Contains(err.Error(), canaryHit) {
			t.Errorf("%s: the error quotes the value: %v", tc.key, err)
		}
		if !strings.Contains(err.Error(), tc.key) {
			t.Errorf("%s: the error does not name the key: %v", tc.key, err)
		}
	}
}

func TestRemoveMarketplaceDoesNotEchoName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	for _, name := range []string{canary, urlCanary} {
		err := RemoveMarketplace(path, name)
		if err == nil || strings.Contains(err.Error(), canaryHit) || !strings.Contains(err.Error(), "name") {
			t.Errorf("RemoveMarketplace(%q) = %v", name, err)
		}
	}
	if _, err := CheckMarketplace(RawMarketplace{Name: canary}); err == nil || strings.Contains(err.Error(), canaryHit) {
		t.Errorf("CheckMarketplace: %v", err)
	}
}
