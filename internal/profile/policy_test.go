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
	setPrefix := func(s string) func(p *Policy) { return func(p *Policy) { p.P.Prefix = s } }
	// approvedFirst sets the approved policy of a case before the diff.
	approvedFirst := map[string]func(p *Policy){
		"prefix inv to invoices/":      setPrefix("inv"),
		"prefix invoices to invoices2": setPrefix("invoices"),
		"prefix any to invoices/":      setPrefix(""),
		"prefix trailing slash only":   setPrefix("invoices"),
	}
	for _, tc := range []struct {
		name    string
		change  func(p *Policy)
		field   string
		loosens bool
	}{
		{"prefix widened", func(p *Policy) { p.P.Prefix = "" }, "prefix", true},
		{"prefix narrowed", func(p *Policy) { p.P.Prefix = "invoices/2026/" }, "prefix", false},
		// The prefix is a directory: "inv" means "inv/", so "invoices/" is a
		// sibling key space, not a narrower one.
		{"prefix inv to invoices/", setPrefix("invoices/"), "prefix", true},
		{"prefix invoices to invoices2", setPrefix("invoices2"), "prefix", true},
		{"prefix any to invoices/", setPrefix("invoices/"), "prefix", false},
		{"prefix trailing slash only", setPrefix("invoices/"), "prefix", false},
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
		{"ttl shorter", func(p *Policy) { p.P.HandleTTL = time.Minute }, "limits.handle_ttl", false},
		{"work_dir moved", func(p *Policy) { p.Workspace.Dir = "/mnt/shared/work" }, "work_dir", true},
		{"workspace cap up", func(p *Policy) { p.Workspace.MaxBytes = 4 << 30 }, "max_workspace_bytes", true},
		{"workspace cap down", func(p *Policy) { p.Workspace.MaxBytes = 1 << 20 }, "max_workspace_bytes", false},
		{"production off", func(p *Policy) { p.P.Production = false }, "production", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			approved := basePolicy(t)
			cur := basePolicy(t)
			if f := approvedFirst[tc.name]; f != nil {
				f(approved)
				f(cur)
			}
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

// Removing idle_timeout or max_session falls back to lockkit's 20m / 4h:
// a longer timer, so a loosening, never a tightening to 0.
func TestUnsetTimeoutsLoosen(t *testing.T) {
	policyOf := func(extra string) *Policy {
		f, err := Parse([]byte(designExample+"\n[profiles.short]\nsource = \"local\"\npath = \"/srv/x\"\n"+extra), "c")
		if err != nil {
			t.Fatal(err)
		}
		p, err := FromFile(f, "short", plugin.Store{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	unset := policyOf("")
	if unset.P.IdleTimeout != DefaultIdleTimeout || unset.P.MaxSession != DefaultMaxSession {
		t.Fatalf("unset timers = %s / %s, want %s / %s", unset.P.IdleTimeout, unset.P.MaxSession, DefaultIdleTimeout, DefaultMaxSession)
	}
	approved := policyOf("idle_timeout = \"1m\"\nmax_session = \"10m\"\n")
	want := map[string]bool{"limits.idle_timeout": true, "limits.max_session": true}
	for _, c := range unset.Diff(approved) {
		if _, ok := want[c.Field]; ok {
			if !c.Loosens {
				t.Errorf("%s %s -> %s classified as a tightening", c.Field, c.Old, c.New)
			}
			delete(want, c.Field)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing changes: %v", want)
	}
	// An approved policy stored before the defaults were explicit (0 = the
	// lockkit default) is the same policy.
	old := policyOf("")
	old.P.IdleTimeout, old.P.MaxSession = 0, 0
	for _, c := range unset.Diff(old) {
		t.Errorf("legacy approved policy: unexpected change %+v", c)
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
