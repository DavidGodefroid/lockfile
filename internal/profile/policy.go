package profile

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/DavidGodefroid/lockkit/config"
	"github.com/DavidGodefroid/lockkit/console"

	"github.com/DavidGodefroid/lockfile/internal/plugin"
)

// PluginPin is an installed plugin as the policy sees it. A new pin, or a
// pin whose manifest changed (its question may have), loosens the policy.
type PluginPin struct {
	Name           string `json:"name"` // qualified
	Version        string `json:"version"`
	Marketplace    string `json:"marketplace"`
	ManifestSHA256 string `json:"manifest_sha256"`
	BinarySHA256   string `json:"binary_sha256"`
	Official       bool   `json:"official"`
}

func (p PluginPin) key() string { return p.Name + "@" + p.Version }

// Policy is what the console enforces for one profile: the profile, the
// installed plugins and the trusted marketplaces. Its JSON form is the
// approved policy lockkit stores, so it is canonical (sorted, defaults
// applied).
type Policy struct {
	P            Profile     `json:"profile"`
	Plugins      []PluginPin `json:"plugins"`
	Marketplaces []Trust     `json:"marketplaces"`
}

var _ console.Policy = (*Policy)(nil)

// Load reads the profile name of the config file at path and the plugins
// installed in store.
func Load(path, name string, store plugin.Store) (*Policy, error) {
	f, err := ReadFile(path)
	if err != nil {
		return nil, err
	}
	return FromFile(f, name, store)
}

// FromFile builds the policy of profile name.
func FromFile(f *File, name string, store plugin.Store) (*Policy, error) {
	p, ok := f.Profiles[name]
	if !ok {
		return nil, fmt.Errorf("config: no profile %q", name)
	}
	installed, err := store.Installed()
	if err != nil {
		return nil, err
	}
	// A tampered plugin is left out: it can never be approved, and Open
	// refuses it anyway.
	installed = slices.DeleteFunc(installed, func(in plugin.Installed) bool { return in.Tampered })
	pol := &Policy{P: p, Plugins: []PluginPin{}, Marketplaces: f.Marketplaces}
	if pol.Marketplaces == nil {
		pol.Marketplaces = []Trust{}
	}
	for _, in := range installed {
		pol.Plugins = append(pol.Plugins, PluginPin{
			Name: in.Name, Version: in.Version, Marketplace: in.Marketplace, // lock names are already qualified
			ManifestSHA256: in.ManifestSHA256, BinarySHA256: in.BinarySHA256, Official: in.Official,
		})
	}
	slices.SortFunc(pol.Plugins, func(a, b PluginPin) int { return strings.Compare(a.key(), b.key()) })
	return pol, nil
}

// Profile implements console.Policy.
func (p *Policy) Profile() console.Profile { return p.P.Console() }

// Pin returns the pin of the qualified plugin name.
func (p *Policy) Pin(name string) (PluginPin, bool) {
	for _, x := range p.Plugins {
		if x.Name == name {
			return x, true
		}
	}
	return PluginPin{}, false
}

// Diff implements config.Policy: the loosening rules of design §4, §8, §9.
func (p *Policy) Diff(approved config.Policy) []config.Change {
	a, ok := approved.(*Policy)
	if !ok {
		return []config.Change{{Field: "policy", Old: "?", New: "lockfile", Loosens: true}}
	}
	// A policy approved before the timer defaults were explicit stores 0.
	out := config.DiffBase(withTimerDefaults(a.P.Base), withTimerDefaults(p.P.Base))
	same := func(field, o, n string) {
		if o != n {
			out = append(out, config.Change{Field: field, Old: o, New: n, Loosens: true})
		}
	}
	o, n := a.P, p.P
	same("source", o.Source, n.Source)
	same("bucket", o.S3.Bucket, n.S3.Bucket)
	same("region", o.S3.Region, n.S3.Region)
	same("endpoint", o.S3.Endpoint, n.S3.Endpoint)
	same("host", o.SFTP.Host, n.SFTP.Host)
	same("port", strconv.Itoa(o.SFTP.Port), strconv.Itoa(n.SFTP.Port))
	same("sftp_user", o.SFTP.User, n.SFTP.User)
	same("base_dir", o.SFTP.BaseDir, n.SFTP.BaseDir)
	same("known_hosts", o.SFTP.KnownHosts, n.SFTP.KnownHosts)
	same("auth", o.SFTPAuth, n.SFTPAuth)
	same("key", o.SFTPKey, n.SFTPKey)
	same("path", o.LocalPath, n.LocalPath)
	if o.Prefix != n.Prefix {
		// A prefix under the old one narrows the keys; anything else widens.
		// The prefix is a directory (source.CheckKey), so compare on path
		// boundaries: "inv" -> "invoices/" is a sibling, not a narrowing.
		out = append(out, config.Change{Field: "prefix", Old: o.Prefix, New: n.Prefix, Loosens: !prefixWithin(n.Prefix, o.Prefix)})
	}
	limit := func(field string, ov, nv int64) {
		if ov != nv {
			out = append(out, config.Change{Field: "limits." + field, Old: strconv.FormatInt(ov, 10), New: strconv.FormatInt(nv, 10),
				Loosens: config.LooserLimit(ov, nv)})
		}
	}
	limit("max_object_bytes", o.MaxObjectBytes, n.MaxObjectBytes)
	limit("max_objects_per_plan", int64(o.MaxObjectsPerPlan), int64(n.MaxObjectsPerPlan))
	limit("max_plugins_per_handle", int64(o.MaxPluginsPerHandle), int64(n.MaxPluginsPerHandle))
	if o.HandleTTL != n.HandleTTL {
		out = append(out, config.Change{Field: "limits.handle_ttl", Old: o.HandleTTL.String(), New: n.HandleTTL.String(), Loosens: n.HandleTTL > o.HandleTTL})
	}
	if o.AnswerQuantum != n.AnswerQuantum {
		out = append(out, config.Change{Field: "limits.answer_quantum", Old: o.AnswerQuantum.String(), New: n.AnswerQuantum.String(),
			Loosens: n.AnswerQuantum < o.AnswerQuantum})
	}

	// Plugins: a new one is a new question; a changed manifest may be one.
	oldPins := map[string]PluginPin{}
	for _, x := range a.Plugins {
		oldPins[x.key()] = x
	}
	names := func(ps []PluginPin) []string {
		s := make([]string, len(ps))
		for i, x := range ps {
			s[i] = x.key()
		}
		return s
	}
	out = append(out, config.SetDiff("plugins", names(a.Plugins), names(p.Plugins), false)...)
	for _, x := range p.Plugins {
		if old, ok := oldPins[x.key()]; ok && (old.ManifestSHA256 != x.ManifestSHA256 || old.BinarySHA256 != x.BinarySHA256 || old.Official != x.Official) {
			out = append(out, config.Change{Field: "plugins." + x.key(), Old: "manifest " + short(old.ManifestSHA256) + " binary " + short(old.BinarySHA256),
				New: "manifest " + short(x.ManifestSHA256) + " binary " + short(x.BinarySHA256), Loosens: true})
		}
	}

	// Marketplaces: a new trust root lets new plugins in.
	oldTrust := map[string]Trust{}
	for _, t := range a.Marketplaces {
		oldTrust[t.Name] = t
	}
	tnames := func(ts []Trust) []string {
		s := make([]string, len(ts))
		for i, t := range ts {
			s[i] = t.Name
		}
		return s
	}
	out = append(out, config.SetDiff("marketplaces", tnames(a.Marketplaces), tnames(p.Marketplaces), false)...)
	for _, t := range p.Marketplaces {
		if old, ok := oldTrust[t.Name]; ok && old != t {
			out = append(out, config.Change{Field: "marketplaces." + t.Name, Old: old.String(), New: t.String(), Loosens: true})
		}
	}
	return out
}

// prefixWithin reports whether the key space of prefix n lies inside the
// one of prefix o. "" is any key; any other prefix is the directory p + "/".
func prefixWithin(n, o string) bool {
	if o == "" {
		return true
	}
	if n == "" {
		return false
	}
	norm := func(p string) string { return strings.TrimSuffix(p, "/") + "/" }
	return strings.HasPrefix(norm(n), norm(o))
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// Describe implements config.Policy: the lines of the console's policy
// review and status screen.
func (p *Policy) Describe() []string {
	pr := p.P
	lines := []string{
		fmt.Sprintf("profile %s: %s source %s", pr.Name, pr.Source, pr.Where()),
		fmt.Sprintf("credentials %s", pr.Credentials),
		fmt.Sprintf("limits: max_object_bytes %d · max_plugins_per_handle %d · handle_ttl %s · answer_quantum %s",
			pr.MaxObjectBytes, pr.MaxPluginsPerHandle, pr.HandleTTL, pr.AnswerQuantum),
	}
	if pr.Production {
		lines = append(lines, "PRODUCTION")
	}
	if len(p.Plugins) == 0 {
		lines = append(lines, "plugins: none installed")
	}
	for _, x := range p.Plugins {
		lines = append(lines, fmt.Sprintf("plugin %s@%s (%s)", x.Name, x.Version, x.Marketplace))
	}
	for _, t := range p.Marketplaces {
		lines = append(lines, "WARNING unofficial marketplace "+t.String())
	}
	return lines
}
