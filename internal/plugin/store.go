// Package plugin is the local plugin store: verified plugin binaries and
// manifests under <Dir>/plugins/<qualified name>/<version>/ (0500), and
// <Dir>/plugins.lock recording what was verified at install time. Every
// Open re-hashes the binary and the manifest against the lock.
package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/DavidGodefroid/lockfile/pluginspec"
)

var (
	ErrTampered     = errors.New("plugin_tampered")
	ErrNotInstalled = errors.New("plugin_not_installed")
	// ErrYanked refuses a version its marketplace withdrew (docs/security-model.md).
	ErrYanked = errors.New("plugin_yanked")
)

// OfficialMarketplace is the marketplace name whose plugins have bare names.
const OfficialMarketplace = "official"

const (
	binaryFile   = "plugin"
	manifestFile = "plugin.toml"
	lockName     = "plugins.lock"
)

// Marketplace names have no dot, so a qualified name can never be read as a
// plugin name.
var marketplaceRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

type Store struct{ Dir string }

// Installed is a lock entry with its manifest as read from disk. Tampered
// is set, and Manifest left empty, when the manifest on disk no longer
// matches the lock.
type Installed struct {
	LockEntry
	Manifest pluginspec.Manifest
	Binary   string
	Tampered bool
}

// Qualify returns the store name of a plugin from a marketplace.
func Qualify(marketplace, name string, official bool) string {
	if official {
		return name
	}
	return marketplace + "/" + name
}

func (s Store) lockPath() string { return filepath.Join(s.Dir, lockName) }

func (s Store) versionDir(e LockEntry) string {
	return filepath.Join(s.Dir, "plugins", filepath.FromSlash(e.Name), e.Version)
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func checkEntry(e LockEntry, m pluginspec.Manifest) error {
	if e.Official != (e.Marketplace == OfficialMarketplace) {
		return fmt.Errorf("plugin: entry %q: official flag and marketplace %q disagree", e.Name, e.Marketplace)
	}
	if !e.Official && !marketplaceRe.MatchString(e.Marketplace) {
		return fmt.Errorf("plugin: invalid marketplace name %q", e.Marketplace)
	}
	if want := Qualify(e.Marketplace, m.Name, e.Official); e.Name != want {
		return fmt.Errorf("plugin: entry name %q, want %q", e.Name, want)
	}
	if e.Version != m.Version {
		return fmt.Errorf("plugin: entry version %q, manifest %q", e.Version, m.Version)
	}
	return nil
}

// Install stores a plugin whose binary and manifest have already been
// verified against the marketplace, after checking them against e. A
// previously installed version of the same plugin is replaced.
func (s Store) Install(e LockEntry, binary, manifest []byte) error {
	if sha(binary) != e.BinarySHA256 {
		return errors.New("plugin: binary does not match its sha256")
	}
	if sha(manifest) != e.ManifestSHA256 {
		return errors.New("plugin: manifest does not match its sha256")
	}
	m, err := pluginspec.ParseManifest(manifest)
	if err != nil {
		return err
	}
	if err := checkEntry(e, m); err != nil {
		return err
	}
	entries, err := readLock(s.lockPath())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	parent := filepath.Dir(s.versionDir(e))
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".install-")
	if err != nil {
		return err
	}
	defer removeAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, binaryFile), binary, 0o500); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, manifestFile), manifest, 0o400); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o500); err != nil {
		return err
	}

	var old []LockEntry
	kept := entries[:0:0]
	for _, x := range entries {
		if x.Name == e.Name {
			old = append(old, x)
			continue
		}
		kept = append(kept, x)
	}
	dst := s.versionDir(e)
	if err := removeAll(dst); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	if err := writeLock(s.lockPath(), append(kept, e)); err != nil {
		return err
	}
	for _, x := range old {
		if x.Version != e.Version {
			_ = removeAll(s.versionDir(x))
		}
	}
	return nil
}

// Installed lists the lock with each manifest read and checked.
func (s Store) Installed() ([]Installed, error) {
	entries, err := readLock(s.lockPath())
	if err != nil {
		return nil, err
	}
	out := make([]Installed, 0, len(entries))
	for _, e := range entries {
		in := Installed{LockEntry: e, Binary: filepath.Join(s.versionDir(e), binaryFile)}
		if m, err := s.manifest(e); err != nil {
			in.Tampered = true
		} else {
			in.Manifest = m
		}
		out = append(out, in)
	}
	return out, nil
}

func (s Store) manifest(e LockEntry) (pluginspec.Manifest, error) {
	b, err := readNoFollow(filepath.Join(s.versionDir(e), manifestFile))
	if err != nil || sha(b) != e.ManifestSHA256 {
		return pluginspec.Manifest{}, ErrTampered
	}
	m, err := pluginspec.ParseManifest(b)
	if err != nil {
		return pluginspec.Manifest{}, ErrTampered
	}
	if err := checkEntry(e, m); err != nil {
		return pluginspec.Manifest{}, ErrTampered
	}
	return m, nil
}

// find resolves a name as the agent types it: a bare name is the official
// plugin, a qualified one names its marketplace.
func (s Store) find(name string) (LockEntry, error) {
	entries, err := readLock(s.lockPath())
	if err != nil {
		return LockEntry{}, err
	}
	for _, e := range entries {
		if e.Name == name {
			return e, nil
		}
	}
	return LockEntry{}, fmt.Errorf("%w: %s", ErrNotInstalled, name)
}

// Open opens an installed plugin's binary and checks, from the opened file
// itself, that it still hashes to the lock entry; the returned file is
// positioned at its start and is the one to execute.
func (s Store) Open(name string) (*os.File, Installed, error) {
	e, err := s.find(name)
	if err != nil {
		return nil, Installed{}, err
	}
	if e.Yanked {
		return nil, Installed{}, fmt.Errorf("%w: %s@%s", ErrYanked, name, e.Version)
	}
	m, err := s.manifest(e)
	if err != nil {
		return nil, Installed{}, fmt.Errorf("%w: %s manifest", ErrTampered, name)
	}
	path := filepath.Join(s.versionDir(e), binaryFile)
	f, err := os.OpenFile(path, os.O_RDONLY|noFollow|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, Installed{}, fmt.Errorf("%w: %s binary: %v", ErrTampered, name, err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil || hex.EncodeToString(h.Sum(nil)) != e.BinarySHA256 {
		f.Close()
		return nil, Installed{}, fmt.Errorf("%w: %s binary", ErrTampered, name)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, Installed{}, err
	}
	return f, Installed{LockEntry: e, Manifest: m, Binary: path}, nil
}

// Bare is the plugin name of e without its marketplace.
func (e LockEntry) Bare() string {
	if e.Official {
		return e.Name
	}
	return strings.TrimPrefix(e.Name, e.Marketplace+"/")
}

// MarkYanked records, for every installed plugin of marketplace, the
// yanked status a freshly verified index gives its version. status returns
// known=false for a version the index does not list: its status is kept.
// It returns the entries whose status changed.
func (s Store) MarkYanked(marketplace string, status func(bare, version string) (yanked, known bool)) ([]LockEntry, error) {
	entries, err := readLock(s.lockPath())
	if err != nil {
		return nil, err
	}
	var changed []LockEntry
	for i, e := range entries {
		if e.Marketplace != marketplace {
			continue
		}
		y, known := status(e.Bare(), e.Version)
		if !known || y == e.Yanked {
			continue
		}
		entries[i].Yanked = y
		changed = append(changed, entries[i])
	}
	if len(changed) == 0 {
		return nil, nil
	}
	return changed, writeLock(s.lockPath(), entries)
}

// Remove uninstalls a plugin by its qualified name.
func (s Store) Remove(name string) error {
	entries, err := readLock(s.lockPath())
	if err != nil {
		return err
	}
	kept := entries[:0:0]
	var gone []LockEntry
	for _, e := range entries {
		if e.Name == name {
			gone = append(gone, e)
		} else {
			kept = append(kept, e)
		}
	}
	if len(gone) == 0 {
		return fmt.Errorf("%w: %s", ErrNotInstalled, name)
	}
	if err := writeLock(s.lockPath(), kept); err != nil {
		return err
	}
	for _, e := range gone {
		if err := removeAll(s.versionDir(e)); err != nil {
			return err
		}
	}
	return nil
}

// RemoveAll deletes the whole store (a staging store, once its plugin is
// copied into the real one).
func (s Store) RemoveAll() error { return removeAll(s.Dir) }

func readNoFollow(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|noFollow|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 1<<20))
}

// removeAll removes a tree whose directories may be read-only (0500).
func removeAll(path string) error {
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(path)
}
