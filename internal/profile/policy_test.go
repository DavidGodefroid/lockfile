package profile

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/config"

	"github.com/DavidGodefroid/lockfile/internal/plugin"
)

func basePolicy(t *testing.T) *Policy {
	t.Helper()
	f, err := Parse([]byte(designExample), "c")
	if err != nil {
		t.Fatal(err)
	}
	p, err := FromFile(f, "prod-docs", plugin.Store{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	p.Plugins = []PluginPin{{Name: "pdf.parses", Version: "1.0.0", Marketplace: "official", ManifestSHA256: "aa", BinarySHA256: "bb", Official: true}}
	return p
}

func TestPolicyRoundTrip(t *testing.T) {
	p := basePolicy(t)
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var back Policy
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*p, back) {
		t.Fatalf("round trip:\n%+v\n%+v", *p, back)
	}
	if config.Fingerprint(p) != config.Fingerprint(&back) {
		t.Fatal("fingerprint changed")
	}
	if len(p.Diff(&back)) != 0 {
		t.Fatalf("diff with itself: %+v", p.Diff(&back))
	}
}

func TestUnknownProfile(t *testing.T) {
	f, _ := Parse([]byte(designExample), "c")
	if _, err := FromFile(f, "nope", plugin.Store{Dir: t.TempDir()}); err == nil {
		t.Fatal("unknown profile accepted")
	}
}

func TestDiffLoosens(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(p *Policy)
		field   string
		loosens bool
	}{
		{"prefix widened", func(p *Policy) { p.P.Prefix = "" }, "prefix", true},
		{"prefix narrowed", func(p *Policy) { p.P.Prefix = "invoices/2026/" }, "prefix", false},
		{"bucket", func(p *Policy) { p.P.S3.Bucket = "other" }, "bucket", true},
		{"plugin added", func(p *Policy) {
			p.Plugins = append(p.Plugins, PluginPin{Name: "pdf.truncated", Version: "1.0.0", Marketplace: "official", Official: true})
		}, "plugins", true},
		{"plugin removed", func(p *Policy) { p.Plugins = nil }, "plugins", false},
		{"manifest changed", func(p *Policy) { p.Plugins[0].ManifestSHA256 = "cc" }, "plugins.pdf.parses@1.0.0", true},
		{"marketplace added", func(p *Policy) {
			p.Marketplaces = append(p.Marketplaces, Trust{Name: "acme", IndexURL: "https://x/i.json", IdentityRegexp: "^https://x/", OIDCIssuer: "https://x"})
		}, "marketplaces", true},
		{"max plugins up", func(p *Policy) { p.P.MaxPluginsPerHandle = 32 }, "limits.max_plugins_per_handle", true},
		{"max plugins down", func(p *Policy) { p.P.MaxPluginsPerHandle = 8 }, "limits.max_plugins_per_handle", false},
		{"quantum shorter", func(p *Policy) { p.P.AnswerQuantum = time.Second }, "limits.answer_quantum", true},
		{"ttl longer", func(p *Policy) { p.P.HandleTTL = time.Hour }, "limits.handle_ttl", true},
		{"production off", func(p *Policy) { p.P.Production = false }, "production", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			approved := basePolicy(t)
			cur := basePolicy(t)
			tc.change(cur)
			changes := cur.Diff(approved)
			found := false
			for _, c := range changes {
				if c.Field == tc.field {
					found = true
					if c.Loosens != tc.loosens {
						t.Errorf("%s loosens = %v, want %v", c.Field, c.Loosens, tc.loosens)
					}
				}
			}
			if !found {
				t.Fatalf("no change on %s: %+v", tc.field, changes)
			}
		})
	}
}

func TestDescribeWarnsUnofficial(t *testing.T) {
	p := basePolicy(t)
	p.Marketplaces = []Trust{{Name: "acme", IndexURL: "https://x/i.json", IdentityRegexp: "^https://x/", OIDCIssuer: "https://x"}}
	lines := p.Describe()
	if lines[len(lines)-1][:7] != "WARNING" {
		t.Fatalf("describe = %q", lines)
	}
}
