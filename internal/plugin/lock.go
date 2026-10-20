package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// LockEntry is one installed plugin in plugins.lock. Name is qualified:
// bare for the official marketplace, "<marketplace>/<name>" otherwise.
type LockEntry struct {
	Name           string `toml:"name"`
	Version        string `toml:"version"`
	Marketplace    string `toml:"marketplace"`
	BinarySHA256   string `toml:"binary_sha256"`
	ManifestSHA256 string `toml:"manifest_sha256"`
	Official       bool   `toml:"official"`
	// Yanked is set from a refreshed, signature-verified marketplace index
	// that withdrew this version: Open refuses it.
	Yanked bool `toml:"yanked,omitempty"`
}

type lockFile struct {
	Plugin []LockEntry `toml:"plugin"`
}

func readLock(path string) ([]LockEntry, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var l lockFile
	md, err := toml.NewDecoder(bytes.NewReader(b)).Decode(&l)
	if err != nil {
		return nil, fmt.Errorf("plugins.lock: %w", err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		return nil, fmt.Errorf("plugins.lock: unknown keys %v", u)
	}
	return l.Plugin, nil
}

// writeLock replaces the lock file atomically (temp file + rename).
func writeLock(path string, entries []LockEntry) error {
	var buf bytes.Buffer
	buf.WriteString("# Installed lockfile plugins. Written by lockfile; do not edit.\n")
	if err := toml.NewEncoder(&buf).Encode(lockFile{Plugin: entries}); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".plugins.lock-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
