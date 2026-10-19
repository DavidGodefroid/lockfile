package marketplace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

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
}

// Install downloads, verifies and installs one exact version of a plugin.
func (i Installer) Install(ctx context.Context, m Marketplace, name, version string) (plugin.LockEntry, error) {
	if !pluginspec.ValidName(name) {
		return plugin.LockEntry{}, fmt.Errorf("marketplace: invalid plugin name %q", name)
	}
	if !pluginspec.ValidVersion(version) {
		return plugin.LockEntry{}, fmt.Errorf("marketplace: %q is not an exact version (X.Y.Z)", version)
	}
	if !m.Official && m.Name == plugin.OfficialMarketplace {
		return plugin.LockEntry{}, errors.New("marketplace: an unofficial marketplace cannot be named official")
	}
	if err := m.Root.Validate(); err != nil {
		return plugin.LockEntry{}, err
	}
	tmp, err := os.MkdirTemp("", "lockfile-install-")
	if err != nil {
		return plugin.LockEntry{}, err
	}
	defer os.RemoveAll(tmp)

	// 1. The index and its signature.
	idxPath := filepath.Join(tmp, "index.json")
	idxBytes, err := i.fetch(ctx, m.IndexURL, idxPath, maxIndex)
	if err != nil {
		return plugin.LockEntry{}, err
	}
	if _, err := i.fetch(ctx, m.IndexURL+".sigstore.json", idxPath+".sigstore.json", maxBundle); err != nil {
		return plugin.LockEntry{}, err
	}
	if err := i.Verifier.VerifyBlob(ctx, idxPath, idxPath+".sigstore.json", m.Root); err != nil {
		return plugin.LockEntry{}, fmt.Errorf("marketplace %s: index signature: %w", m.Name, err)
	}
	idx, err := index.Parse(idxBytes)
	if err != nil {
		return plugin.LockEntry{}, err
	}
	if idx.Marketplace != m.Name {
		return plugin.LockEntry{}, fmt.Errorf("marketplace %s: index is for marketplace %q", m.Name, idx.Marketplace)
	}
	v, err := idx.Find(name, version)
	if err != nil {
		return plugin.LockEntry{}, err
	}
	if v.Yanked {
		return plugin.LockEntry{}, fmt.Errorf("%w: %s@%s", ErrYanked, name, version)
	}

	// 2. The manifest, against the hash in the signed index.
	man, err := i.fetch(ctx, v.ManifestURL, filepath.Join(tmp, "plugin.toml"), maxManifest)
	if err != nil {
		return plugin.LockEntry{}, err
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
	bin, err := i.fetch(ctx, a.URL, artPath, maxArtifact)
	if err != nil {
		return plugin.LockEntry{}, err
	}
	if _, err := i.fetch(ctx, a.BundleURL, artPath+".sigstore.json", maxBundle); err != nil {
		return plugin.LockEntry{}, err
	}
	if err := i.Verifier.VerifyBlob(ctx, artPath, artPath+".sigstore.json", m.Root); err != nil {
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

// fetch downloads an https URL, at most max bytes, into path.
func (i Installer) fetch(ctx context.Context, rawURL, path string, max int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("marketplace: refusing non-https URL %q", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	client := i.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("marketplace: get %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("marketplace: get %s: %s", rawURL, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("marketplace: get %s: %w", rawURL, err)
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("marketplace: %s is larger than %d bytes", rawURL, max)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, err
	}
	return bytes.Clone(b), nil
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
