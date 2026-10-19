package profile

import (
	"strings"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/config"
	"github.com/DavidGodefroid/lockkit/console"
)

const designExample = `
[profiles.prod-docs]
source = "s3"
bucket = "documents"
region = "eu-west-1"
endpoint = ""
prefix = "invoices/"
credentials = "vault"
vault_path = "secret/data/lockfile/prod-docs"
production = true
max_object_bytes = "50MiB"
max_objects_per_plan = 5
handle_ttl = "30m"

[profiles.partner-sftp]
source = "sftp"
host = "sftp.example.org"
port = 22
user = "reader"
auth = "key"
key = "~/.ssh/partner_ed25519"
known_hosts = "~/.ssh/known_hosts"
base_dir = "/outgoing"
credentials = "none"

[profiles.local-samples]
source = "local"
path = "/srv/samples"
`

func TestDecodeDesignExample(t *testing.T) {
	f, err := Parse([]byte(designExample), "config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Profiles) != 3 {
		t.Fatalf("profiles = %d", len(f.Profiles))
	}
	p := f.Profiles["prod-docs"]
	if p.Source != SourceS3 || p.S3.Bucket != "documents" || p.Prefix != "invoices/" || !p.Production ||
		p.Credentials != config.CredentialsVault || p.MaxObjectBytes != 50<<20 || p.HandleTTL != 30*time.Minute {
		t.Errorf("prod-docs = %+v", p)
	}
	s := f.Profiles["partner-sftp"]
	if s.SFTP.Host != "sftp.example.org" || s.SFTP.Port != 22 || s.SFTPAuth != AuthKey || s.Credentials != console.CredentialsNone {
		t.Errorf("partner-sftp = %+v", s)
	}
	l := f.Profiles["local-samples"]
	if l.LocalPath != "/srv/samples" || l.Credentials != console.CredentialsNone {
		t.Errorf("local-samples = %+v", l)
	}
}

func TestDefaults(t *testing.T) {
	f, err := Parse([]byte("[profiles.dev]\nsource = \"s3\"\nbucket = \"documents\"\nregion = \"eu-west-1\"\n"), "c")
	if err != nil {
		t.Fatal(err)
	}
	p := f.Profiles["dev"]
	if p.MaxObjectBytes != DefaultMaxObjectBytes || p.MaxObjectsPerPlan != 5 || p.MaxPluginsPerHandle != 16 ||
		p.HandleTTL != 30*time.Minute || p.AnswerQuantum != 5*time.Second || p.Credentials != config.CredentialsAsk {
		t.Fatalf("defaults = %+v", p)
	}
	if f.Workspace.MaxBytes != 1<<30 {
		t.Fatalf("workspace = %+v", f.Workspace)
	}
}

// bidiOverride is U+202E, a character that could hide text on the approval
// screen.
var bidiOverride = string(rune(0x202e))

func TestRejects(t *testing.T) {
	s3 := "[profiles.dev]\nsource = \"s3\"\nbucket = \"documents\"\nregion = \"eu-west-1\"\n"
	sftp := "[profiles.dev]\nsource = \"sftp\"\nhost = \"sftp.example.org\"\nuser = \"reader\"\nauth = \"password\"\n"
	for name, cfg := range map[string]string{
		"secret key":               s3 + "secret_access_key = \"x\"\n",
		"unknown key":              s3 + "colour = \"red\"\n",
		"unknown source":           "[profiles.dev]\nsource = \"ftp\"\n",
		"missing bucket":           "[profiles.dev]\nsource = \"s3\"\nregion = \"eu-west-1\"\n",
		"remote http endpoint":     s3 + "endpoint = \"http://minio.example.org\"\n",
		"endpoint with userinfo":   s3 + "endpoint = \"https://a:b@minio.example.org\"\n",
		"sftp without known_hosts": sftp,
		"none on s3":               s3 + "credentials = \"none\"\n",
		"none on sftp password":    sftp + "known_hosts = \"/k\"\ncredentials = \"none\"\n",
		"bad vault path":           s3 + "credentials = \"vault\"\nvault_path = \"-x\"\n",
		"vault path without vault": s3 + "vault_path = \"secret/x\"\n",
		"production named y":       "[profiles.y]\nsource = \"s3\"\nbucket = \"documents\"\nregion = \"eu-west-1\"\nproduction = true\n",
		"bidi in prefix":           s3 + "prefix = \"inv" + bidiOverride + "oices/\"\n",
		"s3 with host":             s3 + "host = \"x\"\n",
		"relative local path":      "[profiles.dev]\nsource = \"local\"\npath = \"samples\"\n",
		"local with credentials":   "[profiles.dev]\nsource = \"local\"\npath = \"/srv\"\ncredentials = \"ask\"\n",
		"too many plugins":         s3 + "max_plugins_per_handle = 65\n",
		"quantum too short":        s3 + "answer_quantum = \"100ms\"\n",
		"public key marketplace":   s3 + "[[marketplaces]]\nname = \"acme\"\nindex_url = \"https://plugins.example.org/index.json\"\npublic_key = \"/k.pub\"\n",
		"http marketplace":         s3 + "[[marketplaces]]\nname = \"acme\"\nindex_url = \"http://plugins.example.org/index.json\"\nidentity_regexp = \"^https://x/\"\noidc_issuer = \"https://x\"\n",
		"official name":            s3 + "[[marketplaces]]\nname = \"official\"\nindex_url = \"https://plugins.example.org/index.json\"\nidentity_regexp = \"^https://x/\"\noidc_issuer = \"https://x\"\n",
		"unanchored identity":      s3 + "[[marketplaces]]\nname = \"acme\"\nindex_url = \"https://plugins.example.org/index.json\"\nidentity_regexp = \".*\"\noidc_issuer = \"https://x\"\n",
	} {
		if _, err := Parse([]byte(cfg), "c"); err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "\"x\"") && name == "secret key" {
			t.Errorf("%s: error quotes the value: %v", name, err)
		}
	}
}

func TestKeylessMarketplaceAccepted(t *testing.T) {
	cfg := "[[marketplaces]]\nname = \"acme\"\nindex_url = \"https://plugins.example.org/index.json\"\n" +
		"identity_regexp = \"^https://git.example.org/tools/plugins/.+@refs/tags/\"\noidc_issuer = \"https://git.example.org\"\n"
	f, err := Parse([]byte(cfg), "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Marketplaces) != 1 || f.Marketplaces[0].Name != "acme" {
		t.Fatalf("marketplaces = %+v", f.Marketplaces)
	}
	if m := f.Marketplaces[0].Marketplace(); m.Official || m.Root.IdentityRegexp == "" {
		t.Fatalf("marketplace = %+v", m)
	}
}

func TestWhereHasNoSecret(t *testing.T) {
	f, err := Parse([]byte(designExample), "c")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range f.Profiles {
		w := p.Where()
		if strings.Contains(w, "secret/") || strings.Contains(w, "ed25519") {
			t.Errorf("%s: where = %q", p.Name, w)
		}
	}
	if got := f.Profiles["prod-docs"].Console(); got.Host != "s3.eu-west-1" || got.Port != 443 || !got.Production {
		t.Errorf("console profile = %+v", got)
	}
}
