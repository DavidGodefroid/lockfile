// Package marketplace installs plugins from a signed marketplace index:
// the index, then the artifact, are checked against the marketplace's trust
// root, and every downloaded byte against the hashes the signed index
// lists.
package marketplace

import (
	"fmt"
	"slices"

	"github.com/DavidGodefroid/lockfile/internal/plugin"
	"github.com/DavidGodefroid/lockfile/internal/verify"
	"github.com/DavidGodefroid/lockfile/pluginspec"
)

// OfficialIndexURL is where the official marketplace publishes its index.
const OfficialIndexURL = "https://davidgodefroid.github.io/lockfile-marketplace/index.json"

type Marketplace struct {
	Name     string
	IndexURL string
	// Root signs the index.
	Root verify.TrustRoot
	// ReleaseRoot, when set, is the narrower root that must have signed one
	// plugin version's manifest and binaries; otherwise Root.
	ReleaseRoot func(name, version string) verify.TrustRoot
	Official    bool
}

// Official is the marketplace whose identity is pinned in this binary.
func Official() Marketplace {
	return Marketplace{Name: plugin.OfficialMarketplace, IndexURL: OfficialIndexURL, Root: verify.Official,
		ReleaseRoot: verify.OfficialRelease, Official: true}
}

func (m Marketplace) releaseRoot(name, version string) verify.TrustRoot {
	if m.ReleaseRoot != nil {
		return m.ReleaseRoot(name, version)
	}
	return m.Root
}

// DiffManifests lists what changed between two manifests of a plugin.
// questionChanged is set when the question or the meaning of 1 or 0
// changed: a policy loosening that needs a typed confirmation.
func DiffManifests(old, new pluginspec.Manifest) (questionChanged bool, lines []string) {
	field := func(name, a, b string) {
		if a != b {
			lines = append(lines, fmt.Sprintf("%s: %q -> %q", name, a, b))
		}
	}
	field("version", old.Version, new.Version)
	field("question", old.Question, new.Question)
	field("one", old.One, new.One)
	field("zero", old.Zero, new.Zero)
	questionChanged = old.Question != new.Question || old.One != new.One || old.Zero != new.Zero
	if !slices.Equal(old.Accepts, new.Accepts) {
		lines = append(lines, fmt.Sprintf("accepts: %v -> %v", old.Accepts, new.Accepts))
	}
	if old.Limits != new.Limits {
		lines = append(lines, fmt.Sprintf("limits: cpu %ds memory %d timeout %s -> cpu %ds memory %d timeout %s",
			old.Limits.CPUSeconds, old.Limits.Memory, old.Limits.Timeout,
			new.Limits.CPUSeconds, new.Limits.Memory, new.Limits.Timeout))
	}
	return questionChanged, lines
}
