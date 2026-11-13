package profile

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
)

// A marketplace's public_key is the key itself, not a path: the base64 body
// of a PEM public key (cosign.pub) on one line. It travels with the config
// file (lox run copies the workspace's .lockfile/ to the console), and the
// approval of the config covers the key: a key file swapped on disk after
// :review cannot change who may sign. Errors never quote it.

// errKeyFormat is the one refusal of a value that is not a public key.
var errKeyFormat = errors.New("must be the base64 body of a PEM public key (cosign.pub), on one line")

// checkPublicKey parses a public_key value and returns the hex SHA-256 of
// its DER encoding. It accepts the key types cosign verifies with and
// lockkit takes: ECDSA P-256 or P-384, Ed25519, RSA of 2048 bits or more.
func checkPublicKey(v string) (string, error) {
	der, err := base64.StdEncoding.Strict().DecodeString(v)
	if err != nil {
		return "", errKeyFormat
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return "", errKeyFormat
	}
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		if k.Curve != elliptic.P256() && k.Curve != elliptic.P384() {
			return "", errors.New("an ECDSA key must be on P-256 or P-384")
		}
	case ed25519.PublicKey:
	case *rsa.PublicKey:
		if k.N.BitLen() < 2048 {
			return "", errors.New("an RSA key must have at least 2048 bits")
		}
	default:
		return "", errors.New("unsupported key type (ECDSA P-256/P-384, Ed25519 or RSA >= 2048)")
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:]), nil
}

// publicKeyPEM is a checked public_key value as a PEM block.
func publicKeyPEM(v string) string {
	der, _ := base64.StdEncoding.DecodeString(v)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// PublicKeyValue is the public_key value of a PEM public key file (a
// cosign.pub): its one PUBLIC KEY block, base64 on one line. Errors never
// quote the file.
func PublicKeyValue(pemData []byte) (string, error) {
	b, rest := pem.Decode(pemData)
	if b == nil || b.Type != "PUBLIC KEY" || len(b.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return "", errors.New("public key: the file must hold one PEM PUBLIC KEY block (cosign.pub)")
	}
	v := base64.StdEncoding.EncodeToString(b.Bytes)
	if _, err := checkPublicKey(v); errors.Is(err, errKeyFormat) {
		return "", errors.New("public key: the PEM block does not hold a public key")
	} else if err != nil {
		return "", fmt.Errorf("public key: %w", err)
	}
	return v, nil
}
