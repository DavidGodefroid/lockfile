package profile

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/managed"
)

const managedS3 = `
[profiles.dev]
source = "s3"
bucket = "invoices"
endpoint = "https://s3.example.com"
`

const acmeMarketplace = `
[[marketplaces]]
name = "acme"
index_url = "https://example.org/acme/index.json"
identity_regexp = "^https://github\\.com/example/acme/"
oidc_issuer = "https://token.actions.githubusercontent.com"
`

func parseManagedFile(t *testing.T, body string) *managed.File {
	t.Helper()
	m, err := managed.Parse([]byte("version = 1\norganisation = \"Acme\"\n" + body))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// devViolations parses cfg under the managed file and returns the lines
// that refuse its profile dev.
func devViolations(t *testing.T, managedBody, cfg string) []string {
	t.Helper()
	m := parseManagedFile(t, managedBody)
	f, err := ParseManaged([]byte(cfg), "c", m)
	if err != nil {
		t.Fatal(err)
	}
	return FileViolations(f, "dev")
}

// One test per bound family Policy.Diff decides: the profile beyond the
// bound is refused citing the managed key; at the bound, or stricter, it
// is not.
func TestManagedBoundsCeilings(t *testing.T) {
	for _, tc := range []struct {
		name, bound, beyond, within, want string
		marketplace                       bool
	}{
		{name: "production", bound: "production = true", beyond: "", within: "production = true",
			want: "blocked by company policy: lockfile.bounds.production = true (profile dev: production = false)"},
		{name: "credentials_ttl", bound: `credentials_ttl = "1h"`, beyond: `credentials_ttl = "2h"`, within: `credentials_ttl = "1h"`,
			want: "blocked by company policy: lockfile.bounds.credentials_ttl = 1h0m0s (profile dev: credentials_ttl = 2h0m0s)"},
		{name: "credentials_ttl unset", bound: `credentials_ttl = "1h"`, beyond: "", within: `credentials_ttl = "30m"`,
			want: "blocked by company policy: lockfile.bounds.credentials_ttl = 1h0m0s (profile dev: credentials_ttl = 0s)"},
		{name: "idle_timeout", bound: `idle_timeout = "10m"`, beyond: "", within: `idle_timeout = "5m"`,
			want: "blocked by company policy: lockfile.bounds.idle_timeout = 10m0s (profile dev: limits.idle_timeout = 20m0s)"},
		{name: "max_session", bound: `max_session = "1h"`, beyond: `max_session = "2h"`, within: `max_session = "1h"`,
			want: "blocked by company policy: lockfile.bounds.max_session = 1h0m0s (profile dev: limits.max_session = 2h0m0s)"},
		{name: "handle_ttl", bound: `handle_ttl = "10m"`, beyond: "", within: `handle_ttl = "10m"`,
			want: "blocked by company policy: lockfile.bounds.handle_ttl = 10m0s (profile dev: limits.handle_ttl = 30m0s)"},
		{name: "max_object_bytes", bound: `max_object_bytes = "10MiB"`, beyond: `max_object_bytes = "50MiB"`, within: `max_object_bytes = "5MiB"`,
			want: "blocked by company policy: lockfile.bounds.max_object_bytes = 10485760 (profile dev: limits.max_object_bytes = 52428800)"},
		{name: "max_objects_per_plan", bound: "max_objects_per_plan = 3", beyond: "", within: "max_objects_per_plan = 3",
			want: "blocked by company policy: lockfile.bounds.max_objects_per_plan = 3 (profile dev: limits.max_objects_per_plan = 5)"},
		{name: "max_plugins_per_handle", bound: "max_plugins_per_handle = 4", beyond: "max_plugins_per_handle = 8", within: "max_plugins_per_handle = 2",
			want: "blocked by company policy: lockfile.bounds.max_plugins_per_handle = 4 (profile dev: limits.max_plugins_per_handle = 8)"},
		{name: "answer_quantum", bound: `answer_quantum = "10s"`, beyond: "", within: `answer_quantum = "30s"`,
			want: "blocked by company policy: lockfile.bounds.answer_quantum = 10s (profile dev: limits.answer_quantum = 5s)"},
		{name: "marketplaces", bound: `marketplaces = ["official"]`, marketplace: true,
			want: "blocked by company policy: lockfile.bounds.marketplaces does not allow acme (profile dev: marketplaces + acme)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mb := "[lockfile.bounds]\n" + tc.bound + "\n"
			beyond := managedS3 + tc.beyond + "\n"
			within := managedS3 + tc.within + "\n"
			if tc.marketplace {
				beyond += acmeMarketplace
			}
			got := devViolations(t, mb, beyond)
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("beyond the bound: %q\nwant %q", got, tc.want)
			}
			if got := devViolations(t, mb, within); len(got) != 0 {
				t.Fatalf("within the bound: %q", got)
			}
		})
	}
}

// max_workspace_bytes is a file-level key: above its bound is refused.
func TestManagedBoundsWorkspace(t *testing.T) {
	mb := "[lockfile.bounds]\nmax_workspace_bytes = \"100MiB\"\n"
	got := devViolations(t, mb, "max_workspace_bytes = \"200MiB\"\n"+managedS3)
	want := "blocked by company policy: lockfile.bounds.max_workspace_bytes = 104857600 (profile dev: max_workspace_bytes = 209715200)"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("%q\nwant %q", got, want)
	}
	// The default (1 GiB) is above it too.
	if got := devViolations(t, mb, managedS3); len(got) != 1 {
		t.Fatalf("default workspace: %q", got)
	}
	if got := devViolations(t, mb, "max_workspace_bytes = \"100MiB\"\n"+managedS3); len(got) != 0 {
		t.Fatalf("at the bound: %q", got)
	}
}

// A profile stricter than or equal to every bound has no violation.
func TestManagedBoundsStrictProfile(t *testing.T) {
	mb := `[lockfile.bounds]
production = true
source = ["s3"]
hosts = ["*.example.com"]
buckets = ["invoices"]
credentials = ["vault", "ask"]
credentials_ttl = "1h"
idle_timeout = "10m"
max_session = "1h"
handle_ttl = "10m"
max_object_bytes = "10MiB"
max_objects_per_plan = 3
max_plugins_per_handle = 4
max_workspace_bytes = "100MiB"
answer_quantum = "10s"
marketplaces = ["official", "acme"]
`
	cfg := "max_workspace_bytes = \"100MiB\"\n" + managedS3 + `production = true
credentials_ttl = "30m"
idle_timeout = "10m"
max_session = "30m"
handle_ttl = "5m"
max_object_bytes = "10MiB"
max_objects_per_plan = 1
max_plugins_per_handle = 4
answer_quantum = "10s"
` + acmeMarketplace
	if got := devViolations(t, mb, cfg); len(got) != 0 {
		t.Fatalf("violations: %q", got)
	}
}

// The allowed lists Diff cannot express are checked in ManagedBounds: a
// value outside is an error naming the managed key, one inside passes.
func TestManagedBoundsAllowLists(t *testing.T) {
	sftp := `
[profiles.dev]
source = "sftp"
host = "files.corp.example"
user = "u"
known_hosts = "/kh"
auth = "key"
key = "/id"
`
	for _, tc := range []struct {
		name, bound, beyond, within, want string
	}{
		{"source", `source = ["sftp"]`, managedS3, sftp,
			"blocked by company policy: lockfile.bounds.source does not allow s3 (profile dev: source = s3)"},
		{"hosts endpoint", `hosts = ["*.corp.example"]`, managedS3, strings.Replace(managedS3, "s3.example.com", "S3.Corp.Example", 1),
			"blocked by company policy: lockfile.bounds.hosts does not allow s3.example.com (profile dev: endpoint = s3.example.com)"},
		{"hosts sftp", `hosts = ["s3.example.com"]`, sftp, managedS3,
			"blocked by company policy: lockfile.bounds.hosts does not allow files.corp.example (profile dev: host = files.corp.example)"},
		{"hosts region", `hosts = ["s3.eu-west-1.amazonaws.com"]`, strings.Replace(managedS3, `endpoint = "https://s3.example.com"`, `region = "us-east-1"`, 1),
			strings.Replace(managedS3, `endpoint = "https://s3.example.com"`, `region = "eu-west-1"`, 1),
			"blocked by company policy: lockfile.bounds.hosts does not allow s3.us-east-1.amazonaws.com (profile dev: region = s3.us-east-1.amazonaws.com)"},
		{"buckets", `buckets = ["archive"]`, managedS3, strings.Replace(managedS3, "invoices", "archive", 1),
			"blocked by company policy: lockfile.bounds.buckets does not allow invoices (profile dev: bucket = invoices)"},
		{"credentials", `credentials = ["keychain"]`, managedS3, managedS3 + "credentials = \"keychain\"\n",
			"blocked by company policy: lockfile.bounds.credentials does not allow ask (profile dev: credentials = ask)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mb := "[lockfile.bounds]\n" + tc.bound + "\n"
			got := devViolations(t, mb, tc.beyond)
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("outside the list: %q\nwant %q", got, tc.want)
			}
			if got := devViolations(t, mb, tc.within); len(got) != 0 {
				t.Fatalf("inside the list: %q", got)
			}
		})
	}
}

// [family] vault.required acts as credentials = ["vault"], cited as
// family.vault.required.
func TestManagedVaultRequired(t *testing.T) {
	mb := "[family.vault]\naddress = \"https://vault.example.com\"\nrequired = true\n"
	got := devViolations(t, mb, managedS3)
	want := "blocked by company policy: family.vault.required requires credentials = vault (profile dev: credentials = ask)"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("%q\nwant %q", got, want)
	}
	if got := devViolations(t, mb, managedS3+"credentials = \"vault\"\nvault_path = \"secret/data/s3\"\n"); len(got) != 0 {
		t.Fatalf("vault: %q", got)
	}
}

const managedProfiles = `
[lockfile.bounds]
max_object_bytes = "10MiB"

[lockfile.profiles.erp]
source = "sftp"
host = "erp.corp.example"
user = "files"
known_hosts = "/etc/ssh/known_hosts"
auth = "key"
key = "/keys/erp"
credentials = "none"
max_object_bytes = "5MiB"
`

// A managed profile is served as written; a file profile of its name is
// refused, naming it; a managed profile beyond the bounds is a load error.
func TestManagedProfiles(t *testing.T) {
	m := parseManagedFile(t, managedProfiles)
	f, err := ParseManaged([]byte(managedS3+"max_object_bytes = \"1MiB\"\n"), "c", m)
	if err != nil {
		t.Fatal(err)
	}
	erp, ok := f.Profiles["erp"]
	if !ok || erp.SFTP.Host != "erp.corp.example" || erp.MaxObjectBytes != 5<<20 || erp.Credentials != "none" {
		t.Fatalf("managed profile: %+v", erp)
	}
	if _, ok := f.Profiles["dev"]; !ok || !slices.Equal(f.ManagedProfiles, []string{"erp"}) {
		t.Fatalf("profiles %v, managed %v", f.Profiles, f.ManagedProfiles)
	}
	if got := FileViolations(f, "erp"); len(got) != 0 {
		t.Fatalf("managed profile violations: %q", got)
	}

	_, err = ParseManaged([]byte(managedS3+"\n[profiles.erp]\nsource = \"local\"\npath = \"/srv\"\n"), "c", m)
	if err == nil || !strings.Contains(err.Error(), `profile "erp" is provided by the company's managed configuration`) || !errors.Is(err, console.ErrManaged) {
		t.Fatalf("redefined managed profile: %v", err)
	}

	beyond := parseManagedFile(t, strings.Replace(managedProfiles, `max_object_bytes = "5MiB"`, `max_object_bytes = "20MiB"`, 1))
	_, err = ParseManaged([]byte(managedS3), "c", beyond)
	if err == nil || !strings.Contains(err.Error(), "[lockfile.profiles.erp] is beyond the bounds") ||
		!strings.Contains(err.Error(), "lockfile.bounds.max_object_bytes") || !errors.Is(err, console.ErrManaged) {
		t.Fatalf("managed profile beyond the bounds: %v", err)
	}

	bad := parseManagedFile(t, "[lockfile.profiles.erp]\nsource = \"ftp\"\n")
	if _, err := ParseManaged([]byte(managedS3), "c", bad); err == nil || !strings.Contains(err.Error(), "[lockfile.profiles.erp]") {
		t.Fatalf("invalid managed profile: %v", err)
	}
	unknown := parseManagedFile(t, "[lockfile.profiles.erp]\nsource = \"local\"\npath = \"/srv\"\nmax_workspace_bytes = \"1GiB\"\n")
	if _, err := ParseManaged([]byte(managedS3), "c", unknown); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("unknown key in a managed profile: %v", err)
	}
}

// A managed default applies when the profile omits the key; the file's
// value within the bounds wins; a default beyond the bounds is a load
// error.
func TestManagedDefaults(t *testing.T) {
	m := parseManagedFile(t, `
[lockfile.bounds]
max_object_bytes = "10MiB"
max_workspace_bytes = "500MiB"

[lockfile.defaults]
max_object_bytes = "8MiB"
handle_ttl = "5m"
max_workspace_bytes = "200MiB"
credentials = "keychain"
`)
	f, err := ParseManaged([]byte(managedS3), "c", m)
	if err != nil {
		t.Fatal(err)
	}
	p := f.Profiles["dev"]
	if p.MaxObjectBytes != 8<<20 || p.HandleTTL.String() != "5m0s" || p.Credentials != "keychain" || f.Workspace.MaxBytes != 200<<20 {
		t.Fatalf("defaults not applied: %d %s %s %d", p.MaxObjectBytes, p.HandleTTL, p.Credentials, f.Workspace.MaxBytes)
	}
	// Built-in defaults still fill what neither sets.
	if p.MaxObjectsPerPlan != DefaultMaxObjectsPerPlan {
		t.Fatalf("max_objects_per_plan %d", p.MaxObjectsPerPlan)
	}
	f, err = ParseManaged([]byte("max_workspace_bytes = \"300MiB\"\n"+managedS3+"max_object_bytes = \"2MiB\"\ncredentials = \"ask\"\n"), "c", m)
	if err != nil {
		t.Fatal(err)
	}
	if p := f.Profiles["dev"]; p.MaxObjectBytes != 2<<20 || p.Credentials != "ask" || f.Workspace.MaxBytes != 300<<20 {
		t.Fatalf("the file's values lost: %d %s %d", p.MaxObjectBytes, p.Credentials, f.Workspace.MaxBytes)
	}

	for key, body := range map[string]string{
		"max_object_bytes":    "[lockfile.bounds]\nmax_object_bytes = \"10MiB\"\n[lockfile.defaults]\nmax_object_bytes = \"20MiB\"\n",
		"max_workspace_bytes": "[lockfile.bounds]\nmax_workspace_bytes = \"10MiB\"\n[lockfile.defaults]\nmax_workspace_bytes = \"20MiB\"\n",
		"answer_quantum":      "[lockfile.bounds]\nanswer_quantum = \"10s\"\n[lockfile.defaults]\nanswer_quantum = \"2s\"\n",
		"credentials":         "[lockfile.bounds]\ncredentials = [\"vault\"]\n[lockfile.defaults]\ncredentials = \"ask\"\n",
	} {
		_, err := ParseManaged([]byte(managedS3), "c", parseManagedFile(t, body))
		if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "beyond the bounds") || !errors.Is(err, console.ErrManaged) {
			t.Errorf("default %s beyond the bounds: %v", key, err)
		}
	}
	// A default the bounds do not name is never a load error.
	ok := "[lockfile.bounds]\nmax_object_bytes = \"10MiB\"\n[lockfile.defaults]\nhandle_ttl = \"2h\"\n"
	if _, err := ParseManaged([]byte(managedS3), "c", parseManagedFile(t, ok)); err != nil {
		t.Fatal(err)
	}
}

// Invalid bounds are refused, naming the key.
func TestManagedBoundsInvalid(t *testing.T) {
	for key, bound := range map[string]string{
		"production":       "production = false",
		"source":           `source = ["ftp"]`,
		"hosts":            `hosts = ["[a"]`,
		"credentials":      `credentials = ["plain"]`,
		"idle_timeout":     `idle_timeout = "soon"`,
		"max_object_bytes": `max_object_bytes = "big"`,
	} {
		m := parseManagedFile(t, "[lockfile.bounds]\n"+bound+"\n")
		_, err := ParseManaged([]byte(managedS3), "c", m)
		if err == nil || !strings.Contains(err.Error(), "lockfile.bounds."+key) {
			t.Errorf("%s: %v", key, err)
		}
	}
	m := parseManagedFile(t, "[lockfile.bounds]\nplugins = [\"x\"]\n")
	if _, err := ParseManaged([]byte(managedS3), "c", m); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("unknown bound: %v", err)
	}
}

// With no managed file, nothing changes.
func TestNoManagedFile(t *testing.T) {
	f, err := ParseManaged([]byte(managedS3), "c", nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.Managed != nil || len(f.ManagedProfiles) != 0 || len(FileViolations(f, "dev")) != 0 {
		t.Fatalf("%+v", f)
	}
}
