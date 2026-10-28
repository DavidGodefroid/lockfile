// Package profile reads lockfile's configuration (docs/usage.md) on lockkit's
// config base: source profiles, limits and marketplaces, from the config
// file of the lockfile account. It never reads a secret: credentials come
// from the console (ask, the OS keychain or Vault).
package profile

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DavidGodefroid/lockkit/config"
	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/secrets"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/source"
	"github.com/DavidGodefroid/lockfile/pluginspec"
)

// Source kinds.
const (
	SourceS3    = "s3"
	SourceSFTP  = "sftp"
	SourceLocal = "local"
)

// SFTP authentication modes.
const (
	AuthKey      = "key"
	AuthPassword = "password"
)

// Defaults, as docs/usage.md lists them.
const (
	DefaultMaxObjectBytes      = 50 << 20
	DefaultMaxObjectsPerPlan   = 5
	DefaultMaxPluginsPerHandle = 16
	DefaultHandleTTL           = 30 * time.Minute
	DefaultAnswerQuantum       = 5 * time.Second
	// DefaultIdleTimeout and DefaultMaxSession are lockkit's console
	// defaults, applied here so the approved policy states them.
	DefaultIdleTimeout     = 20 * time.Minute
	DefaultMaxSession      = 4 * time.Hour
	MaxPluginsPerHandleCap = 64
	DefaultSFTPPort        = 22
	httpsPort              = 443
)

// Raw is the config file as written.
type Raw struct {
	WorkDir           string                `toml:"work_dir"`
	MaxWorkspaceBytes string                `toml:"max_workspace_bytes"`
	Profiles          map[string]RawProfile `toml:"profiles"`
	Marketplaces      []RawMarketplace      `toml:"marketplaces"`
}

// RawProfile is one [profiles.NAME] table.
type RawProfile struct {
	Source string `toml:"source"`
	// s3
	Bucket   string `toml:"bucket"`
	Region   string `toml:"region"`
	Endpoint string `toml:"endpoint"`
	Prefix   string `toml:"prefix"`
	// sftp
	Host       string `toml:"host"`
	Port       int    `toml:"port"`
	Auth       string `toml:"auth"`
	Key        string `toml:"key"`
	KnownHosts string `toml:"known_hosts"`
	BaseDir    string `toml:"base_dir"`
	// local
	Path string `toml:"path"`
	// shared with lockkit's config.Base
	User           string `toml:"user"`
	Credentials    string `toml:"credentials"`
	VaultPath      string `toml:"vault_path"`
	CredentialsTTL string `toml:"credentials_ttl"`
	Production     bool   `toml:"production"`
	IdleTimeout    string `toml:"idle_timeout"`
	MaxSession     string `toml:"max_session"`
	// limits
	MaxObjectBytes      string `toml:"max_object_bytes"`
	MaxObjectsPerPlan   int    `toml:"max_objects_per_plan"`
	MaxPluginsPerHandle int    `toml:"max_plugins_per_handle"`
	HandleTTL           string `toml:"handle_ttl"`
	AnswerQuantum       string `toml:"answer_quantum"`
}

// Profile is a validated profile with its defaults applied.
type Profile struct {
	config.Base
	Name   string `json:"name"`
	Source string `json:"source"`
	// Prefix bounds the keys of every source kind ("" = any key).
	Prefix string            `json:"prefix"`
	S3     source.S3Config   `json:"s3"`
	SFTP   source.SFTPConfig `json:"sftp"`
	// SFTPAuth is key or password; SFTPKey the private key path (in the
	// lockfile account) for key.
	SFTPAuth  string `json:"sftp_auth,omitempty"`
	SFTPKey   string `json:"sftp_key,omitempty"`
	LocalPath string `json:"local_path,omitempty"`

	MaxObjectBytes      int64         `json:"max_object_bytes"`
	MaxObjectsPerPlan   int           `json:"max_objects_per_plan"`
	MaxPluginsPerHandle int           `json:"max_plugins_per_handle"`
	HandleTTL           time.Duration `json:"handle_ttl"`
	AnswerQuantum       time.Duration `json:"answer_quantum"`
}

// Workspace is the workspace part of the config.
type Workspace struct {
	Dir      string `json:"dir"` // "" = the default under the state dir
	MaxBytes int64  `json:"max_bytes"`
}

// DefaultMaxWorkspaceBytes bounds all fetched files together.
const DefaultMaxWorkspaceBytes = 1 << 30

// withDefaults fills a zero MaxBytes (a policy approved before the
// workspace was part of it).
func (w Workspace) withDefaults() Workspace {
	if w.MaxBytes <= 0 {
		w.MaxBytes = DefaultMaxWorkspaceBytes
	}
	return w
}

// File is a decoded, validated config file.
type File struct {
	Workspace    Workspace
	Profiles     map[string]Profile
	Marketplaces []Trust
}

// ReadFile reads and validates the config file at path.
func ReadFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return Parse(data, path)
}

// Parse decodes and validates config data; name labels errors.
func Parse(data []byte, name string) (*File, error) {
	var raw Raw
	if _, err := config.Decode(product.P, data, name, &raw); err != nil {
		return nil, err
	}
	f := &File{Profiles: map[string]Profile{}}
	f.Workspace.Dir = raw.WorkDir
	if raw.WorkDir != "" && !filepath.IsAbs(raw.WorkDir) {
		return nil, fmt.Errorf("config: %s: work_dir must be absolute", name)
	}
	f.Workspace.MaxBytes = DefaultMaxWorkspaceBytes
	if raw.MaxWorkspaceBytes != "" {
		n, err := pluginspec.ParseBytes(raw.MaxWorkspaceBytes)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("config: %s: max_workspace_bytes: invalid size", name)
		}
		f.Workspace.MaxBytes = n
	}
	for pname, rp := range raw.Profiles {
		p, err := parseProfile(pname, rp)
		if err != nil {
			return nil, fmt.Errorf("config: %s: profile %s: %w", name, pname, err)
		}
		f.Profiles[pname] = p
	}
	for _, rm := range raw.Marketplaces {
		t, err := parseMarketplace(rm)
		if err != nil {
			return nil, fmt.Errorf("config: %s: %w", name, err)
		}
		if slices.ContainsFunc(f.Marketplaces, func(o Trust) bool { return o.Name == t.Name }) {
			return nil, fmt.Errorf("config: %s: marketplace %s defined twice", name, t.Name)
		}
		f.Marketplaces = append(f.Marketplaces, t)
	}
	slices.SortFunc(f.Marketplaces, func(a, b Trust) int { return strings.Compare(a.Name, b.Name) })
	return f, nil
}

func duration(key, s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%s: invalid duration", key)
	}
	return d, nil
}

func parseProfile(name string, r RawProfile) (Profile, error) {
	if err := config.CheckProfileName(name, r.Production); err != nil {
		return Profile{}, err
	}
	for _, kv := range []struct{ k, v string }{
		{"bucket", r.Bucket}, {"region", r.Region}, {"endpoint", r.Endpoint}, {"prefix", r.Prefix},
		{"host", r.Host}, {"key", r.Key}, {"known_hosts", r.KnownHosts}, {"base_dir", r.BaseDir},
		{"path", r.Path}, {"user", r.User}, {"vault_path", r.VaultPath},
	} {
		if err := config.CheckValue(product.P, kv.k, kv.v); err != nil {
			return Profile{}, err
		}
	}
	p := Profile{Name: name, Source: r.Source, Prefix: r.Prefix}
	p.Production, p.User, p.VaultPath = r.Production, r.User, r.VaultPath
	var err error
	if p.CredentialsTTL, err = duration("credentials_ttl", r.CredentialsTTL, 0); err != nil {
		return Profile{}, err
	}
	// lockkit reads 0 as its default; state the default so that removing a
	// value diffs as the longer timer it really is.
	if p.IdleTimeout, err = duration("idle_timeout", r.IdleTimeout, DefaultIdleTimeout); err != nil {
		return Profile{}, err
	}
	if p.MaxSession, err = duration("max_session", r.MaxSession, DefaultMaxSession); err != nil {
		return Profile{}, err
	}
	p.Base = withTimerDefaults(p.Base)
	if p.HandleTTL, err = duration("handle_ttl", r.HandleTTL, DefaultHandleTTL); err != nil {
		return Profile{}, err
	}
	if p.AnswerQuantum, err = duration("answer_quantum", r.AnswerQuantum, DefaultAnswerQuantum); err != nil {
		return Profile{}, err
	}
	if p.AnswerQuantum < time.Second {
		return Profile{}, errors.New("answer_quantum must be 1s or more")
	}
	p.MaxObjectBytes = DefaultMaxObjectBytes
	if r.MaxObjectBytes != "" {
		if p.MaxObjectBytes, err = pluginspec.ParseBytes(r.MaxObjectBytes); err != nil || p.MaxObjectBytes <= 0 {
			return Profile{}, errors.New("max_object_bytes: invalid size")
		}
	}
	p.MaxObjectsPerPlan = orDefault(r.MaxObjectsPerPlan, DefaultMaxObjectsPerPlan)
	p.MaxPluginsPerHandle = orDefault(r.MaxPluginsPerHandle, DefaultMaxPluginsPerHandle)
	if r.MaxObjectsPerPlan < 0 || r.MaxPluginsPerHandle < 0 || p.MaxPluginsPerHandle > MaxPluginsPerHandleCap {
		return Profile{}, fmt.Errorf("max_objects_per_plan and max_plugins_per_handle must be positive, max_plugins_per_handle at most %d", MaxPluginsPerHandleCap)
	}

	switch r.Source {
	case SourceS3:
		err = p.s3(r)
	case SourceSFTP:
		err = p.sftp(r)
	case SourceLocal:
		err = p.local(r)
	default:
		return Profile{}, errors.New(`source must be "s3", "sftp" or "local"`)
	}
	if err != nil {
		return Profile{}, err
	}
	if p.Credentials == config.CredentialsVault {
		if err := secrets.CheckVaultPath(p.VaultPath); err != nil {
			return Profile{}, fmt.Errorf("vault_path: %w", err)
		}
	} else if p.VaultPath != "" {
		return Profile{}, errors.New(`vault_path is set but credentials is not "vault"`)
	}
	return p, nil
}

// withTimerDefaults replaces a zero idle_timeout or max_session (lockkit's
// "default") with the default it stands for.
func withTimerDefaults(b config.Base) config.Base {
	if b.IdleTimeout == 0 {
		b.IdleTimeout = DefaultIdleTimeout
	}
	if b.MaxSession == 0 {
		b.MaxSession = DefaultMaxSession
	}
	return b
}

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// credentials checks the credentials mode; none is allowed only when
// allowNone.
func (p *Profile) credentials(mode string, allowNone bool) error {
	switch mode {
	case "":
		mode = config.CredentialsAsk
	case config.CredentialsAsk, config.CredentialsKeychain, config.CredentialsVault:
	case console.CredentialsNone:
		if !allowNone {
			return errors.New(`credentials "none" is only for a local source or an unencrypted sftp key`)
		}
	default:
		return errors.New(`credentials must be "ask", "keychain", "vault" or "none"`)
	}
	p.Credentials = mode
	return nil
}

func unused(fields map[string]bool) error {
	for k, set := range fields {
		if set {
			return fmt.Errorf("%s does not apply to this source", k)
		}
	}
	return nil
}

func (p *Profile) s3(r RawProfile) error {
	if err := unused(map[string]bool{"host": r.Host != "", "port": r.Port != 0, "auth": r.Auth != "", "key": r.Key != "",
		"known_hosts": r.KnownHosts != "", "base_dir": r.BaseDir != "", "path": r.Path != ""}); err != nil {
		return err
	}
	if r.Bucket == "" {
		return errors.New("bucket is required")
	}
	if r.Region == "" && r.Endpoint == "" {
		return errors.New("region or endpoint is required")
	}
	if r.Endpoint != "" {
		if err := checkEndpoint(r.Endpoint); err != nil {
			return err
		}
	}
	p.S3 = source.S3Config{Bucket: r.Bucket, Region: r.Region, Endpoint: r.Endpoint, Prefix: r.Prefix}
	return p.credentials(r.Credentials, false)
}

// checkEndpoint accepts https, and plain http to the local machine only
// (a local S3-compatible store for tests).
func checkEndpoint(e string) error {
	u, err := url.Parse(e)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return errors.New("endpoint must be a bare https:// URL")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if h := u.Hostname(); h == "localhost" || h == "127.0.0.1" || h == "::1" {
			return nil
		}
	}
	return errors.New("endpoint must be https:// (http:// only to localhost)")
}

func (p *Profile) sftp(r RawProfile) error {
	if err := unused(map[string]bool{"bucket": r.Bucket != "", "region": r.Region != "", "endpoint": r.Endpoint != "",
		"path": r.Path != ""}); err != nil {
		return err
	}
	if r.Host == "" {
		return errors.New("host is required")
	}
	if r.User == "" {
		return errors.New("user is required")
	}
	if r.KnownHosts == "" {
		return errors.New("known_hosts is required (unknown hosts are refused)")
	}
	port := r.Port
	if port == 0 {
		port = DefaultSFTPPort
	}
	if port < 1 || port > 65535 {
		return errors.New("port must be 1 to 65535")
	}
	p.SFTP = source.SFTPConfig{Host: r.Host, Port: port, User: r.User, BaseDir: r.BaseDir, KnownHosts: r.KnownHosts}
	switch r.Auth {
	case AuthKey:
		if r.Key == "" {
			return errors.New(`auth "key" needs key, the private key path`)
		}
		p.SFTPAuth, p.SFTPKey = AuthKey, r.Key
		return p.credentials(r.Credentials, true)
	case AuthPassword:
		if r.Key != "" {
			return errors.New(`key is set but auth is "password"`)
		}
		p.SFTPAuth = AuthPassword
		return p.credentials(r.Credentials, false)
	}
	return errors.New(`auth must be "key" or "password"`)
}

func (p *Profile) local(r RawProfile) error {
	if err := unused(map[string]bool{"bucket": r.Bucket != "", "region": r.Region != "", "endpoint": r.Endpoint != "",
		"host": r.Host != "", "port": r.Port != 0, "auth": r.Auth != "", "key": r.Key != "", "known_hosts": r.KnownHosts != "",
		"base_dir": r.BaseDir != "", "user": r.User != "", "vault_path": r.VaultPath != ""}); err != nil {
		return err
	}
	if r.Path == "" || !filepath.IsAbs(r.Path) {
		return errors.New("path must be an absolute directory")
	}
	if r.Credentials != "" && r.Credentials != console.CredentialsNone {
		return errors.New(`a local source takes no credentials`)
	}
	p.LocalPath = filepath.Clean(r.Path)
	p.Credentials = console.CredentialsNone
	return nil
}

// Where names the source for the human: never a secret.
func (p Profile) Where() string {
	switch p.Source {
	case SourceS3:
		if p.S3.Endpoint != "" {
			return p.S3.Endpoint + "/" + p.S3.Bucket + "/" + p.Prefix
		}
		return "s3://" + p.S3.Bucket + "/" + p.Prefix
	case SourceSFTP:
		return fmt.Sprintf("sftp://%s@%s:%d%s", p.SFTP.User, p.SFTP.Host, p.SFTP.Port, "/"+strings.TrimPrefix(p.SFTP.BaseDir, "/"))
	}
	return "local:" + p.LocalPath
}

// Console is the profile as lockkit's console needs it.
func (p Profile) Console() console.Profile {
	c := console.Profile{
		Name: p.Name, Production: p.Production, Where: p.Where(), User: p.User,
		Credentials: p.Credentials, VaultPath: p.VaultPath, CredentialsTTL: p.CredentialsTTL,
		IdleTimeout: p.IdleTimeout, MaxSession: p.MaxSession,
	}
	switch p.Source {
	case SourceS3:
		c.Host, c.Port, c.DefaultPort = "s3."+p.S3.Region, httpsPort, httpsPort
		if u, err := url.Parse(p.S3.Endpoint); err == nil && p.S3.Endpoint != "" {
			c.Host = u.Hostname()
		}
	case SourceSFTP:
		c.Host, c.Port, c.DefaultPort = p.SFTP.Host, p.SFTP.Port, DefaultSFTPPort
	}
	return c
}
