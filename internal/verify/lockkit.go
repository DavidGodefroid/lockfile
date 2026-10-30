package verify

import (
	"context"
	"errors"

	"github.com/DavidGodefroid/lockkit/update"
)

// ErrPublicKeyRoot refuses a public-key trust root: lockkit's signature
// check (update.VerifyFiles) is keyless only.
var ErrPublicKeyRoot = errors.New("verify: public-key trust roots need lockkit support (not in lockkit yet)")

// ErrCosignMissing means cosign is not installed: nothing can be verified,
// so nothing is installed.
var ErrCosignMissing = update.ErrCosignMissing

type lockkitVerifier struct{ o update.Options }

// NewLockkit returns a Verifier backed by lockkit's update.VerifyFiles
// (cosign verify-blob, keyless). A missing cosign fails every check.
func NewLockkit(o update.Options) Verifier { return lockkitVerifier{o: o} }

func (v lockkitVerifier) VerifyBlob(ctx context.Context, blob, bundle string, root TrustRoot) error {
	if err := root.Validate(); err != nil {
		return err
	}
	if root.PublicKeyPath != "" {
		return ErrPublicKeyRoot
	}
	return update.VerifyFiles(ctx, blob, bundle, update.Identity{CertIdentityRegexp: root.IdentityRegexp, OIDCIssuer: root.OIDCIssuer}, v.o)
}
