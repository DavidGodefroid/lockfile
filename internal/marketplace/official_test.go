//go:build official

package marketplace

import (
	"context"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockfile/internal/verify"
	"github.com/DavidGodefroid/lockkit/update"
)

// TestOfficialIndexPublished checks the published official marketplace:
// OfficialIndexURL answers, its signature verifies against the identity
// pinned in this binary (cosign on the PATH), and it lists the plugin
// docs/plugins.md installs first. Run by the official-index workflow:
//
//	go test -count=1 -tags official -run TestOfficialIndexPublished ./internal/marketplace/
func TestOfficialIndexPublished(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	idx, err := Installer{Verifier: verify.NewLockkit(update.Options{})}.Index(ctx, Official())
	if err != nil {
		t.Fatal(err)
	}
	v, err := idx.Find("file.non-empty", "1.0.0")
	if err != nil {
		t.Fatalf("file.non-empty@1.0.0: %v", err)
	}
	if v.Yanked {
		t.Fatal("file.non-empty@1.0.0 is yanked")
	}
	t.Logf("%s: %d plugins, generated %s", OfficialIndexURL, len(idx.Plugins), idx.Generated)
}
