package verify

import (
	"context"

	"github.com/DavidGodefroid/lockkit/update"
)

// ErrCosignMissing means cosign is not installed: nothing can be verified,
// so nothing is installed.
var ErrCosignMissing = update.ErrCosignMissing

type lockkitVerifier struct{ o update.Options }

// NewLockkit returns a Verifier backed by lockkit's update.VerifyFiles
// (cosign verify-blob, keyless or with a public key, the Rekor entry
// required in both modes). A missing cosign fails every check.
func NewLockkit(o update.Options) Verifier { return lockkitVerifier{o: o} }

func (v lockkitVerifier) VerifyBlob(ctx context.Context, blob, bundle string, root TrustRoot) error {
	if err := root.Validate(); err != nil {
		return err
	}
	id := update.Identity{CertIdentityRegexp: root.IdentityRegexp, OIDCIssuer: root.OIDCIssuer}
	if root.PublicKey != "" {
		// lockkit refuses a key that does not parse without quoting it.
		id = update.Identity{PublicKey: []byte(root.PublicKey)}
	}
	return update.VerifyFiles(ctx, blob, bundle, id, v.o)
}
