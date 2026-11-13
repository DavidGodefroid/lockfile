package profile

import (
	"fmt"
	"net/url"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/DavidGodefroid/lockkit/config"
	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/managed"

	"github.com/DavidGodefroid/lockfile/pluginspec"
)

// ManagedTool is lockfile's section of the company's managed file
// (lockkit's package managed): [lockfile.bounds], [lockfile.defaults] and
// [lockfile.profiles.<name>].
const ManagedTool = "lockfile"

// ManagedError is a config refused by the company's managed file: the
// file is unreadable, a profile redefines a managed one, or a managed
// default or profile is beyond the bounds. It is console.ErrManaged.
type ManagedError struct{ Err error }

func (e *ManagedError) Error() string { return e.Err.Error() }

func (e *ManagedError) Unwrap() error { return e.Err }

// Is makes a ManagedError console.ErrManaged.
func (e *ManagedError) Is(target error) bool { return target == console.ErrManaged }

// RawDefaults is [lockfile.defaults]: the keys of a profile, plus the
// file-level max_workspace_bytes.
type RawDefaults struct {
	RawProfile
	MaxWorkspaceBytes string `toml:"max_workspace_bytes"`
}

// RawBounds is [lockfile.bounds] as written.
type RawBounds struct {
	// Production = true makes every profile a production one.
	Production *bool `toml:"production"`
	// Allowed lists.
	Source      []string `toml:"source"`
	Hosts       []string `toml:"hosts"`
	Buckets     []string `toml:"buckets"`
	Credentials []string `toml:"credentials"`
	// Ceilings.
	CredentialsTTL      string `toml:"credentials_ttl"`
	IdleTimeout         string `toml:"idle_timeout"`
	MaxSession          string `toml:"max_session"`
	HandleTTL           string `toml:"handle_ttl"`
	MaxObjectBytes      string `toml:"max_object_bytes"`
	MaxObjectsPerPlan   int    `toml:"max_objects_per_plan"`
	MaxPluginsPerHandle int    `toml:"max_plugins_per_handle"`
	MaxWorkspaceBytes   string `toml:"max_workspace_bytes"`
	// Floor.
	AnswerQuantum string `toml:"answer_quantum"`
	// Marketplaces lists the marketplaces a config may trust ("official"
	// is lockfile's own, always there).
	Marketplaces *[]string `toml:"marketplaces"`
}

// bounds is [lockfile.bounds] parsed, with [family] vault.required.
type bounds struct {
	production                                      bool
	source, hosts, buckets, credentials             []string
	credentialsTTL, idleTimeout, maxSession         time.Duration
	handleTTL, answerQuantum                        time.Duration
	maxObjectBytes, maxWorkspaceBytes               int64
	maxObjectsPerPlan, maxPluginsPerHandle          int
	marketplaces                                    []string
	hasMarketplaces, vaultRequired, any, ceilingAny bool
}

// boundKeys maps a Policy.Diff field to its bound's key.
var boundKeys = map[string]string{
	"limits.idle_timeout":           "idle_timeout",
	"limits.max_session":            "max_session",
	"limits.handle_ttl":             "handle_ttl",
	"limits.answer_quantum":         "answer_quantum",
	"limits.max_object_bytes":       "max_object_bytes",
	"limits.max_objects_per_plan":   "max_objects_per_plan",
	"limits.max_plugins_per_handle": "max_plugins_per_handle",
}

// boundKey is the key of [lockfile.bounds] that bounds Diff field f.
func boundKey(f string) string {
	if k, ok := boundKeys[f]; ok {
		return k
	}
	return f
}

func boundsKey(k string) string { return ManagedTool + "." + managed.KindBounds + "." + k }

// VaultRequiredKey cites [family] vault.required.
const VaultRequiredKey = "family.vault.required"

// readBounds decodes and validates the bounds m sets on lockfile.
func readBounds(m *managed.File) (bounds, error) {
	var b bounds
	if m == nil {
		return b, nil
	}
	b.vaultRequired = m.Family.Vault != nil && m.Family.Vault.Required
	var r RawBounds
	ok, err := m.Decode(&r, ManagedTool, managed.KindBounds)
	if err != nil || !ok {
		b.any = b.vaultRequired
		return b, err
	}
	b.any = true
	bad := func(key, why string) error { return fmt.Errorf("managed: %s: %s", boundsKey(key), why) }
	if r.Production != nil {
		if !*r.Production {
			return b, bad("production", "only true bounds anything")
		}
		b.production, b.ceilingAny = true, true
	}
	for _, s := range r.Source {
		if s != SourceS3 && s != SourceSFTP && s != SourceLocal {
			return b, bad("source", `the sources are "s3", "sftp" and "local"`)
		}
	}
	for _, h := range r.Hosts {
		if _, err := path.Match(strings.ToLower(h), ""); err != nil || h == "" {
			return b, bad("hosts", "invalid host pattern")
		}
	}
	for _, c := range r.Credentials {
		switch c {
		case config.CredentialsAsk, config.CredentialsKeychain, config.CredentialsVault, console.CredentialsNone:
		default:
			return b, bad("credentials", `the credentials modes are "ask", "keychain", "vault" and "none"`)
		}
	}
	b.source, b.hosts, b.buckets, b.credentials = r.Source, lower(r.Hosts), r.Buckets, r.Credentials
	for _, d := range []struct {
		key, s string
		to     *time.Duration
	}{{"credentials_ttl", r.CredentialsTTL, &b.credentialsTTL}, {"idle_timeout", r.IdleTimeout, &b.idleTimeout},
		{"max_session", r.MaxSession, &b.maxSession}, {"handle_ttl", r.HandleTTL, &b.handleTTL}, {"answer_quantum", r.AnswerQuantum, &b.answerQuantum}} {
		if d.s == "" {
			continue
		}
		v, err := time.ParseDuration(d.s)
		if err != nil || v <= 0 {
			return b, bad(d.key, "invalid duration (a positive one)")
		}
		*d.to, b.ceilingAny = v, true
	}
	for _, n := range []struct {
		key, s string
		to     *int64
	}{{"max_object_bytes", r.MaxObjectBytes, &b.maxObjectBytes}, {"max_workspace_bytes", r.MaxWorkspaceBytes, &b.maxWorkspaceBytes}} {
		if n.s == "" {
			continue
		}
		v, err := pluginspec.ParseBytes(n.s)
		if err != nil || v <= 0 {
			return b, bad(n.key, "invalid size")
		}
		*n.to, b.ceilingAny = v, true
	}
	if r.MaxObjectsPerPlan < 0 || r.MaxPluginsPerHandle < 0 {
		return b, bad("max_objects_per_plan", "max_objects_per_plan and max_plugins_per_handle must be positive")
	}
	b.maxObjectsPerPlan, b.maxPluginsPerHandle = r.MaxObjectsPerPlan, r.MaxPluginsPerHandle
	if b.maxObjectsPerPlan > 0 || b.maxPluginsPerHandle > 0 {
		b.ceilingAny = true
	}
	if r.Marketplaces != nil {
		b.hasMarketplaces, b.marketplaces, b.ceilingAny = true, *r.Marketplaces, true
	}
	return b, nil
}

func lower(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = strings.ToLower(v)
	}
	return out
}

// ManagedBounds implements console.ManagedBackend for lockfile's policy:
// pol at the ceiling [lockfile.bounds] of m allows, for managed.Check and
// Policy.Diff to decide. The allowed lists Diff cannot express (source,
// hosts, buckets, credentials, and [family] vault.required) are checked
// here: an error names the managed key.
func ManagedBounds(pol *Policy, m *managed.File) (managed.Bounds, error) {
	b, err := readBounds(m)
	if err != nil || !b.any {
		return managed.Bounds{}, err
	}
	if err := b.allowed(pol.P); err != nil {
		return managed.Bounds{}, err
	}
	out := managed.Bounds{Tool: ManagedTool, Profile: pol.P.Name, Keys: boundKeys}
	if b.ceilingAny {
		out.Ceiling = b.ceiling(pol)
	}
	return out, nil
}

// allowed checks p's single values against the allowed lists. An empty
// value (a default that sets nothing) is not checked.
func (b bounds) allowed(p Profile) error {
	refuse := func(key, field, value string) error {
		return fmt.Errorf("%s does not allow %s (profile %s: %s = %s)", key, value, p.Name, field, value)
	}
	if b.source != nil && p.Source != "" && !slices.Contains(b.source, p.Source) {
		return refuse(boundsKey("source"), "source", p.Source)
	}
	if b.credentials != nil && p.Credentials != "" && !slices.Contains(b.credentials, p.Credentials) {
		return refuse(boundsKey("credentials"), "credentials", p.Credentials)
	}
	if b.vaultRequired && p.Credentials != "" && p.Credentials != config.CredentialsVault {
		return fmt.Errorf("%s requires credentials = vault (profile %s: credentials = %s)", VaultRequiredKey, p.Name, p.Credentials)
	}
	if b.hosts != nil {
		if field, host := p.managedHost(); host != "" && !slices.ContainsFunc(b.hosts, func(pat string) bool {
			ok, _ := path.Match(pat, strings.ToLower(host))
			return ok
		}) {
			return refuse(boundsKey("hosts"), field, host)
		}
	}
	if b.buckets != nil && p.S3.Bucket != "" && !slices.Contains(b.buckets, p.S3.Bucket) {
		return refuse(boundsKey("buckets"), "bucket", p.S3.Bucket)
	}
	return nil
}

// managedHost is the host the hosts bound applies to, and the profile key
// it comes from: the s3 endpoint's host (AWS's regional one without an
// endpoint), the sftp host; none for a local source.
func (p Profile) managedHost() (field, host string) {
	switch {
	case p.S3.Endpoint != "":
		if u, err := url.Parse(p.S3.Endpoint); err == nil {
			return "endpoint", u.Hostname()
		}
		return "endpoint", p.S3.Endpoint
	case p.S3.Region != "":
		return "region", "s3." + p.S3.Region + ".amazonaws.com"
	case p.SFTP.Host != "":
		return "host", p.SFTP.Host
	}
	return "", ""
}

// ceiling is pol with each bounded field set to its bound: the loosest
// policy the managed file allows for this profile.
func (b bounds) ceiling(pol *Policy) *Policy {
	c := *pol
	if b.production {
		c.P.Production = true
	}
	set := func(to *time.Duration, v time.Duration) {
		if v > 0 {
			*to = v
		}
	}
	set(&c.P.CredentialsTTL, b.credentialsTTL)
	set(&c.P.IdleTimeout, b.idleTimeout)
	set(&c.P.MaxSession, b.maxSession)
	set(&c.P.HandleTTL, b.handleTTL)
	set(&c.P.AnswerQuantum, b.answerQuantum)
	if b.maxObjectBytes > 0 {
		c.P.MaxObjectBytes = b.maxObjectBytes
	}
	if b.maxObjectsPerPlan > 0 {
		c.P.MaxObjectsPerPlan = b.maxObjectsPerPlan
	}
	if b.maxPluginsPerHandle > 0 {
		c.P.MaxPluginsPerHandle = b.maxPluginsPerHandle
	}
	if b.maxWorkspaceBytes > 0 {
		c.Workspace.MaxBytes = b.maxWorkspaceBytes
	}
	if b.hasMarketplaces {
		c.Marketplaces = slices.DeleteFunc(slices.Clone(pol.Marketplaces), func(t Trust) bool { return !slices.Contains(b.marketplaces, t.Name) })
	}
	return &c
}

// managedConfig is what the managed file adds to the config: its defaults
// (the keys they set) and its profiles.
type managedConfig struct {
	defaults RawDefaults
	profiles map[string]Profile
}

// readManaged decodes lockfile's section of m and checks its defaults and
// profiles against its bounds.
func readManaged(m *managed.File) (managedConfig, error) {
	var mc managedConfig
	if m == nil {
		return mc, nil
	}
	fail := func(err error) (managedConfig, error) { return managedConfig{}, &ManagedError{Err: err} }
	b, err := readBounds(m)
	if err != nil {
		return fail(err)
	}
	if _, err := m.Decode(&mc.defaults, ManagedTool, managed.KindDefaults); err != nil {
		return fail(err)
	}
	if err := checkDefaults(mc.defaults, b); err != nil {
		return fail(err)
	}
	for _, name := range m.Profiles(ManagedTool) {
		var rp RawProfile
		if _, err := m.Decode(&rp, ManagedTool, managed.KindProfiles, name); err != nil {
			return fail(err)
		}
		p, err := parseProfile(name, rp)
		if err != nil {
			return fail(fmt.Errorf("managed: [%s.%s.%s]: %w", ManagedTool, managed.KindProfiles, name, err))
		}
		// A managed profile is checked on its own: the marketplaces and the
		// workspace are the user's.
		pol := &Policy{P: p, Plugins: []PluginPin{}, Marketplaces: []Trust{}, Workspace: Workspace{MaxBytes: DefaultMaxWorkspaceBytes}}
		if b.maxWorkspaceBytes > 0 {
			pol.Workspace.MaxBytes = b.maxWorkspaceBytes
		}
		if lines := Violations(pol, m); len(lines) > 0 {
			for i, l := range lines {
				lines[i] = strings.TrimPrefix(l, blocked)
			}
			return fail(fmt.Errorf("managed: [%s.%s.%s] is beyond the bounds: %s", ManagedTool, managed.KindProfiles, name, strings.Join(lines, "; ")))
		}
		if mc.profiles == nil {
			mc.profiles = map[string]Profile{}
		}
		mc.profiles[name] = p
	}
	return mc, nil
}

// blocked starts every line of a refusal by the managed file.
const blocked = "blocked by company policy: "

// Violations are the lines that refuse pol under m, as the console prints
// them: "blocked by company policy: …", one per violation.
func Violations(pol *Policy, m *managed.File) []string {
	bs, err := ManagedBounds(pol, m)
	if err != nil {
		return []string{blocked + err.Error()}
	}
	var out []string
	for _, v := range managed.Check(pol, bs) {
		out = append(out, v.String())
	}
	return out
}

// FileViolations are the Violations of profile name of f under f's
// managed file, its plugins aside (the bounds name none): what `lockfile
// managed check` prints. Nil without a managed file.
func FileViolations(f *File, name string) []string {
	if f.Managed == nil {
		return nil
	}
	pol := &Policy{P: f.Profiles[name], Plugins: []PluginPin{}, Marketplaces: f.Marketplaces, Workspace: f.Workspace}
	if pol.Marketplaces == nil {
		pol.Marketplaces = []Trust{}
	}
	return Violations(pol, f.Managed)
}

// fill sets what the config file leaves unset from the managed defaults.
func (mc managedConfig) fill(raw *Raw) {
	if raw.MaxWorkspaceBytes == "" {
		raw.MaxWorkspaceBytes = mc.defaults.MaxWorkspaceBytes
	}
	for name, rp := range raw.Profiles {
		fillZero(&rp, mc.defaults.RawProfile)
		raw.Profiles[name] = rp
	}
}

// fillZero sets each zero field of to from def.
func fillZero(to *RawProfile, def RawProfile) {
	tv, dv := reflect.ValueOf(to).Elem(), reflect.ValueOf(def)
	for i := range tv.NumField() {
		if tv.Field(i).IsZero() {
			tv.Field(i).Set(dv.Field(i))
		}
	}
}

// setKeys are the TOML keys d sets.
func setKeys(d RawDefaults) map[string]bool {
	keys := map[string]bool{}
	v, t := reflect.ValueOf(d.RawProfile), reflect.TypeFor[RawProfile]()
	for i := range v.NumField() {
		if !v.Field(i).IsZero() {
			keys[t.Field(i).Tag.Get("toml")] = true
		}
	}
	if d.MaxWorkspaceBytes != "" {
		keys["max_workspace_bytes"] = true
	}
	return keys
}

// checkDefaults refuses a managed default beyond the bounds: the values
// the defaults set, as a profile would carry them.
func checkDefaults(d RawDefaults, b bounds) error {
	keys := setKeys(d)
	if len(keys) == 0 || !b.any {
		return nil
	}
	p := Profile{Name: "defaults", Source: d.Source, Prefix: d.Prefix}
	p.Production, p.Credentials = d.Production, d.Credentials
	p.S3.Bucket, p.S3.Region, p.S3.Endpoint, p.SFTP.Host = d.Bucket, d.Region, d.Endpoint, d.Host
	if err := p.limits(d.RawProfile); err != nil {
		return fmt.Errorf("managed: [%s.%s]: %w", ManagedTool, managed.KindDefaults, err)
	}
	ws, err := workspaceBytes(d.MaxWorkspaceBytes)
	if err != nil {
		return fmt.Errorf("managed: [%s.%s]: %w", ManagedTool, managed.KindDefaults, err)
	}
	pol := &Policy{P: p, Plugins: []PluginPin{}, Marketplaces: []Trust{}, Workspace: Workspace{MaxBytes: ws}}
	beyond := func(key, value string) error {
		return fmt.Errorf("managed: %s.%s.%s = %s is beyond the bounds", ManagedTool, managed.KindDefaults, key, value)
	}
	if err := b.allowed(p); err != nil {
		return fmt.Errorf("managed: [%s.%s] is beyond the bounds: %w", ManagedTool, managed.KindDefaults, err)
	}
	if !b.ceilingAny {
		return nil
	}
	for _, c := range pol.Diff(b.ceiling(pol)) {
		if k := boundKey(c.Field); c.Loosens && keys[k] {
			return beyond(k, c.New)
		}
	}
	return nil
}
