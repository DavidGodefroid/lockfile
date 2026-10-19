// Package verify checks signatures of marketplace indexes and plugin
// artifacts against a trust root. There is no unsigned mode.
package verify

import (
	"context"
	"errors"
)

// TrustRoot is who may sign: a keyless identity (certificate identity
// regexp and OIDC issuer) or a public key, exactly one of the two.
type TrustRoot struct {
	IdentityRegexp string
	OIDCIssuer     string
	PublicKeyPath  string
}

// Official is the trust root of the official marketplace: its release
// workflow, run on a tag, signing keyless through GitHub Actions OIDC.
var Official = TrustRoot{
	IdentityRegexp: `^https://github\.com/DavidGodefroid/lockfile-marketplace/\.github/workflows/release\.yml@refs/tags/`,
	OIDCIssuer:     "https://token.actions.githubusercontent.com",
}

// Validate checks that exactly one kind of trust root is set.
func (r TrustRoot) Validate() error {
	keyless := r.IdentityRegexp != "" || r.OIDCIssuer != ""
	switch {
	case keyless && r.PublicKeyPath != "":
		return errors.New("verify: trust root has both an identity and a key")
	case keyless && (r.IdentityRegexp == "" || r.OIDCIssuer == ""):
		return errors.New("verify: keyless trust root needs an identity regexp and an OIDC issuer")
	case !keyless && r.PublicKeyPath == "":
		return errors.New("verify: empty trust root")
	}
	return nil
}

// Verifier checks that bundle is a valid signature of the file blob by
// root. blob and bundle are paths.
type Verifier interface {
	VerifyBlob(ctx context.Context, blob, bundle string, root TrustRoot) error
}
