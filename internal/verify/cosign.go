package verify

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type cosign struct{ path string }

// NewCosign returns a Verifier backed by the cosign CLI found on PATH.
func NewCosign() (Verifier, error) {
	p, err := exec.LookPath("cosign")
	if err != nil {
		return nil, fmt.Errorf("verify: cosign not found on PATH: %w", err)
	}
	return cosign{path: p}, nil
}

func (c cosign) VerifyBlob(ctx context.Context, blob, bundle string, root TrustRoot) error {
	if err := root.Validate(); err != nil {
		return err
	}
	args := []string{"verify-blob", "--bundle", bundle}
	if root.PublicKeyPath != "" {
		args = append(args, "--key", root.PublicKeyPath)
	} else {
		args = append(args, "--certificate-identity-regexp", root.IdentityRegexp,
			"--certificate-oidc-issuer", root.OIDCIssuer)
	}
	args = append(args, blob)
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, c.path, args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("verify: cosign verify-blob %s: %w: %s", blob, err, strings.TrimSpace(out.String()))
	}
	return nil
}
