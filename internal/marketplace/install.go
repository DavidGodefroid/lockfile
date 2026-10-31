package marketplace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"syscall"

	"github.com/DavidGodefroid/lockfile/index"
	"github.com/DavidGodefroid/lockfile/internal/plugin"
	"github.com/DavidGodefroid/lockfile/internal/verify"
	"github.com/DavidGodefroid/lockfile/pluginspec"
)

// ErrYanked refuses a version the marketplace withdrew. Its text is the
// agent code.
var ErrYanked = errors.New("plugin_yanked")

// Download caps.
const (
	maxIndex    = 4 << 20
	maxManifest = 64 << 10
	maxArtifact = 64 << 20
	maxBundle   = 1 << 20
)

type Installer struct {
	HTTP     *http.Client
	Verifier verify.Verifier
	Store    plugin.Store
	OS, Arch string
	// OnIndex, when set, receives every index whose signature verified
	// (to refresh the yanked status of installed plugins); its error
	// aborts the operation.
	OnIndex func(Marketplace, index.Index) error
	// StateDir, when set, keeps the generated time of the last verified
	// index of each marketplace: an older one (a replayed index, which could
	// un-yank a version) is refused.
	StateDir string
}

// Install downloads, verifies and installs one exact version of a plugin.
func (i Installer) Install(ctx context.Context, m Marketplace, name, version string) (plugin.LockEntry, error) {
	if !pluginspec.ValidName(name) {
		return plugin.LockEntry{}, errors.New("marketplace: invalid plugin name (lower-case letters and digits, dot-separated: vendor.name)")
	}
	if !pluginspec.ValidVersion(version) {
		return plugin.LockEntry{}, errors.New("marketplace: the version is not an exact version (X.Y.Z)")
	}
	if !m.Official && m.Name == plugin.OfficialMarketplace {
		return plugin.LockEntry{}, errors.New("marketplace: an unofficial marketplace cannot be named official")
	}
	tmp, err := os.MkdirTemp("", "lockfile-install-")
	if err != nil {
		return plugin.LockEntry{}, err
	}
	defer os.RemoveAll(tmp)

	// 1. The index and its signature.
	idx, err := i.index(ctx, m, tmp)
	if err != nil {
		return plugin.LockEntry{}, err
	}
	v, err := idx.Find(name, version)
	if err != nil {
		return plugin.LockEntry{}, err
	}
	if v.Yanked {
		return plugin.LockEntry{}, fmt.Errorf("%w: %s@%s", ErrYanked, name, version)
	}

	// 2. The manifest: signed by this version's own release run, and
	// matching the hash in the signed index.
	release := m.releaseRoot(name, version)
	manPath := filepath.Join(tmp, "plugin.toml")
	man, err := i.fetch(ctx, m.what(name, version, "manifest"), v.ManifestURL, manPath, maxManifest)
	if err != nil {
		return plugin.LockEntry{}, err
	}
	if v.ManifestBundleURL == "" {
		return plugin.LockEntry{}, fmt.Errorf("marketplace %s: %s@%s manifest has no signature", m.Name, name, version)
	}
	if _, err := i.fetch(ctx, m.what(name, version, "manifest signature"), v.ManifestBundleURL, manPath+".sigstore.json", maxBundle); err != nil {
		return plugin.LockEntry{}, err
	}
	if err := i.Verifier.VerifyBlob(ctx, manPath, manPath+".sigstore.json", release); err != nil {
		return plugin.LockEntry{}, fmt.Errorf("marketplace %s: %s@%s manifest signature: %w", m.Name, name, version, err)
	}
	if sum(man) != v.ManifestSHA256 {
		return plugin.LockEntry{}, fmt.Errorf("marketplace %s: %s@%s manifest does not match the index", m.Name, name, version)
	}
	mf, err := pluginspec.ParseManifest(man)
	if err != nil {
		return plugin.LockEntry{}, err
	}
	if mf.Name != name || mf.Version != version {
		return plugin.LockEntry{}, fmt.Errorf("marketplace %s: manifest is %s@%s", m.Name, mf.Name, mf.Version)
	}

	// 3. The artifact, its signature, its hash.
	a, ok := v.Artifact(i.OS, i.Arch)
	if !ok {
		return plugin.LockEntry{}, fmt.Errorf("marketplace %s: %s@%s has no build for %s/%s", m.Name, name, version, i.OS, i.Arch)
	}
	artPath := filepath.Join(tmp, "artifact")
	bin, err := i.fetch(ctx, m.what(name, version, "binary"), a.URL, artPath, maxArtifact)
	if err != nil {
		return plugin.LockEntry{}, err
	}
	if _, err := i.fetch(ctx, m.what(name, version, "binary signature"), a.BundleURL, artPath+".sigstore.json", maxBundle); err != nil {
		return plugin.LockEntry{}, err
	}
	if err := i.Verifier.VerifyBlob(ctx, artPath, artPath+".sigstore.json", release); err != nil {
		return plugin.LockEntry{}, fmt.Errorf("marketplace %s: %s@%s signature: %w", m.Name, name, version, err)
	}
	if sum(bin) != a.SHA256 {
		return plugin.LockEntry{}, fmt.Errorf("marketplace %s: %s@%s binary does not match the index", m.Name, name, version)
	}

	e := plugin.LockEntry{
		Name:           plugin.Qualify(m.Name, name, m.Official),
		Version:        version,
		Marketplace:    m.Name,
		BinarySHA256:   a.SHA256,
		ManifestSHA256: v.ManifestSHA256,
		Official:       m.Official,
	}
	if err := i.Store.Install(e, bin, man); err != nil {
		return plugin.LockEntry{}, err
	}
	return e, nil
}

// Index downloads a marketplace's index and checks its signature.
func (i Installer) Index(ctx context.Context, m Marketplace) (index.Index, error) {
	tmp, err := os.MkdirTemp("", "lockfile-index-")
	if err != nil {
		return index.Index{}, err
	}
	defer os.RemoveAll(tmp)
	return i.index(ctx, m, tmp)
}

// index fetches and verifies the index of m into tmp, then hands it to
// OnIndex.
func (i Installer) index(ctx context.Context, m Marketplace, tmp string) (index.Index, error) {
	if err := m.Root.Validate(); err != nil {
		return index.Index{}, err
	}
	idxPath := filepath.Join(tmp, "index.json")
	idxBytes, err := i.fetch(ctx, m.what("", "", "index"), m.IndexURL, idxPath, maxIndex)
	if err != nil {
		return index.Index{}, err
	}
	if _, err := i.fetch(ctx, m.what("", "", "index signature"), m.IndexURL+".sigstore.json", idxPath+".sigstore.json", maxBundle); err != nil {
		return index.Index{}, err
	}
	if err := i.Verifier.VerifyBlob(ctx, idxPath, idxPath+".sigstore.json", m.Root); err != nil {
		return index.Index{}, fmt.Errorf("marketplace %s: index signature: %w", m.Name, err)
	}
	idx, err := index.Parse(idxBytes)
	if err != nil {
		return index.Index{}, err
	}
	if idx.Marketplace != m.Name {
		return index.Index{}, fmt.Errorf("marketplace %s: the index is for another marketplace", m.Name)
	}
	if i.StateDir != "" {
		if err := checkFresh(i.StateDir, m.Name, idx.Generated); err != nil {
			return index.Index{}, err
		}
	}
	if i.OnIndex != nil {
		if err := i.OnIndex(m, idx); err != nil {
			return index.Index{}, err
		}
	}
	return idx, nil
}

// MarkYanked records in store the yanked status idx, a verified index of
// marketplace m, gives every installed plugin of m. It returns the entries
// whose status changed.
func MarkYanked(store plugin.Store, m Marketplace, idx index.Index) ([]plugin.LockEntry, error) {
	return store.MarkYanked(m.Name, func(bare, version string) (bool, bool) {
		v, err := idx.Find(bare, version)
		if err != nil {
			return false, false
		}
		return v.Yanked, true
	})
}

// what labels a download in errors: the marketplace, the plugin version
// (validated names), and the file. Errors never quote a URL: an index_url
// is typed in the config, and a URL can carry a token (userinfo, query).
func (m Marketplace) what(name, version, file string) string {
	if name == "" {
		return fmt.Sprintf("marketplace %s: %s", m.Name, file)
	}
	return fmt.Sprintf("marketplace %s: %s@%s %s", m.Name, name, version, file)
}

// fetch downloads an https URL, at most max bytes, into path; what labels
// its errors, which never quote the URL.
func (i Installer) fetch(ctx context.Context, what, rawURL, path string, max int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("%s: refusing a non-https URL", what)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid URL", what)
	}
	resp, err := SafeClient(i.HTTP).Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: download: %w", what, transportError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: download: HTTP %d", what, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("%s: download: %w", what, transportError(err))
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", what, max)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, err
	}
	return bytes.Clone(b), nil
}

// transportError is err without the URL, host or address it quotes
// (*url.Error, *net.DNSError, *net.OpError, certificate errors): its kind
// only. Lockfile's own and the context's errors stay matchable.
func transportError(err error) error {
	var dns *net.DNSError
	var cert *tls.CertificateVerificationError
	var op *net.OpError
	switch {
	case errors.Is(err, ErrInsecureRedirect):
		return ErrInsecureRedirect
	case errors.Is(err, errTooManyRedirects):
		return errTooManyRedirects
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.As(err, &dns):
		return errors.New("host not found")
	case errors.As(err, &cert):
		return errors.New("TLS certificate not trusted")
	case errors.Is(err, syscall.ECONNREFUSED):
		return errors.New("connection refused")
	case isTimeout(err):
		return errors.New("timed out")
	case errors.As(err, &op):
		return errors.New("connection failed")
	}
	return errors.New("request failed")
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
