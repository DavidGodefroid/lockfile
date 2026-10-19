// Package index is the format of a marketplace's signed index.json: every
// plugin, its versions, the manifest hash and the per-platform artifacts.
// The marketplace produces it, lockfile consumes it.
package index

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Schema is the only index schema this package reads.
const Schema = 1

// ErrNotFound is returned by Find for an unknown plugin or version.
var ErrNotFound = errors.New("index: plugin version not found")

type Index struct {
	Schema      int       `json:"schema"`
	Marketplace string    `json:"marketplace"`
	Generated   time.Time `json:"generated"`
	Plugins     []Plugin  `json:"plugins"`
}

type Plugin struct {
	Name     string    `json:"name"`
	Versions []Version `json:"versions"`
}

type Version struct {
	Version        string     `json:"version"`
	ManifestURL    string     `json:"manifest_url"`
	ManifestSHA256 string     `json:"manifest_sha256"`
	Yanked         bool       `json:"yanked"`
	Artifacts      []Artifact `json:"artifacts"`
}

// Artifact is one built binary; BundleURL is its cosign bundle.
type Artifact struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	BundleURL string `json:"bundle_url"`
}

// Parse decodes an index strictly: unknown fields and other schemas are
// errors.
func Parse(b []byte) (Index, error) {
	var i Index
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&i); err != nil {
		return Index{}, fmt.Errorf("index: %w", err)
	}
	if i.Schema != Schema {
		return Index{}, fmt.Errorf("index: schema %d unsupported (want %d)", i.Schema, Schema)
	}
	return i, nil
}

// Find returns one exact version of a plugin, yanked or not: the caller
// decides what a yanked version means.
func (i Index) Find(name, version string) (Version, error) {
	for _, p := range i.Plugins {
		if p.Name != name {
			continue
		}
		for _, v := range p.Versions {
			if v.Version == version {
				return v, nil
			}
		}
	}
	return Version{}, fmt.Errorf("%w: %s@%s", ErrNotFound, name, version)
}

// Artifact returns the artifact built for os/arch.
func (v Version) Artifact(os, arch string) (Artifact, bool) {
	for _, a := range v.Artifacts {
		if a.OS == os && a.Arch == arch {
			return a, true
		}
	}
	return Artifact{}, false
}
