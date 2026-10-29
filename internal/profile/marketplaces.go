package profile

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/DavidGodefroid/lockkit/config"

	"github.com/DavidGodefroid/lockfile/internal/marketplace"
	"github.com/DavidGodefroid/lockfile/internal/plugin"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/verify"
)

// RawMarketplace is one [[marketplaces]] entry (docs/usage.md).
type RawMarketplace struct {
	Name           string `toml:"name"`
	IndexURL       string `toml:"index_url"`
	IdentityRegexp string `toml:"identity_regexp"`
	OIDCIssuer     string `toml:"oidc_issuer"`
	PublicKey      string `toml:"public_key"`
}

// Trust is an unofficial marketplace and its keyless trust root.
type Trust struct {
	Name           string `json:"name"`
	IndexURL       string `json:"index_url"`
	IdentityRegexp string `json:"identity_regexp"`
	OIDCIssuer     string `json:"oidc_issuer"`
}

func (t Trust) String() string {
	return fmt.Sprintf("%s (%s, identity %s, issuer %s)", t.Name, t.IndexURL, t.IdentityRegexp, t.OIDCIssuer)
}

// Marketplace is t for the installer: never official.
func (t Trust) Marketplace() marketplace.Marketplace {
	return marketplace.Marketplace{Name: t.Name, IndexURL: t.IndexURL,
		Root: verify.TrustRoot{IdentityRegexp: t.IdentityRegexp, OIDCIssuer: t.OIDCIssuer}}
}

var marketplaceName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// ErrPublicKeyRoot refuses a public-key trust root: lockkit's signature
// check is keyless only (v0.2.1).
var ErrPublicKeyRoot = errors.New("public-key trust roots need lockkit support (not in lockkit v0.2.1): use a keyless identity_regexp and oidc_issuer")

// CheckMarketplace validates an unofficial marketplace entry.
func CheckMarketplace(r RawMarketplace) (Trust, error) { return parseMarketplace(r) }

func parseMarketplace(r RawMarketplace) (Trust, error) {
	for _, kv := range []struct{ k, v string }{{"name", r.Name}, {"index_url", r.IndexURL},
		{"identity_regexp", r.IdentityRegexp}, {"oidc_issuer", r.OIDCIssuer}, {"public_key", r.PublicKey}} {
		if err := config.CheckValue(product.P, kv.k, kv.v); err != nil {
			return Trust{}, fmt.Errorf("marketplace: %w", err)
		}
	}
	if !marketplaceName.MatchString(r.Name) || r.Name == plugin.OfficialMarketplace {
		return Trust{}, fmt.Errorf("marketplace: invalid name %q (lower-case letters, digits and '-'; not %q)", r.Name, plugin.OfficialMarketplace)
	}
	if r.PublicKey != "" {
		return Trust{}, fmt.Errorf("marketplace %s: %w", r.Name, ErrPublicKeyRoot)
	}
	u, err := url.Parse(r.IndexURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return Trust{}, fmt.Errorf("marketplace %s: index_url must be an https:// URL", r.Name)
	}
	if !strings.HasPrefix(r.IdentityRegexp, "^https://") {
		return Trust{}, fmt.Errorf("marketplace %s: identity_regexp must start with ^https://", r.Name)
	}
	if _, err := regexp.Compile(r.IdentityRegexp); err != nil {
		return Trust{}, fmt.Errorf("marketplace %s: identity_regexp does not compile", r.Name)
	}
	if !strings.HasPrefix(r.OIDCIssuer, "https://") {
		return Trust{}, fmt.Errorf("marketplace %s: oidc_issuer must be an https:// URL", r.Name)
	}
	return Trust{Name: r.Name, IndexURL: r.IndexURL, IdentityRegexp: r.IdentityRegexp, OIDCIssuer: r.OIDCIssuer}, nil
}

// SignerWarnings lists the weaknesses of an identity_regexp that let other
// signers in: an unescaped '.' between two name characters
// ("github.com" also matches "githubXcom"), and a literal prefix that does
// not pin host, owner and repository each followed by '/'
// ("^https://github\.com/acme" also matches "acme-evil").
func SignerWarnings(re string) []string {
	var out []string
	if unescapedDot(re) {
		out = append(out, `the signer pattern has an unescaped "." (it matches any character): write \.`)
	}
	lit := literalPrefix(strings.TrimPrefix(re, "^"))
	if rest, ok := strings.CutPrefix(lit, "https://"); !ok || strings.Count(rest, "/") < 3 {
		out = append(out, "the signer pattern does not pin a repository path ending in '/' (https://HOST/OWNER/REPO/...): other repositories match it")
	}
	return out
}

func isNameChar(c byte) bool {
	return c == '-' || c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// unescapedDot reports a '.' outside a character class, not escaped,
// between two name characters.
func unescapedDot(re string) bool {
	inClass := false
	for i := 0; i < len(re); i++ {
		switch c := re[i]; {
		case c == '\\':
			i++
		case inClass:
			if c == ']' {
				inClass = false
			}
		case c == '[':
			inClass = true
		case c == '.' && i > 0 && i+1 < len(re) && isNameChar(re[i-1]) && isNameChar(re[i+1]):
			return true
		}
	}
	return false
}

// literalPrefix is the text re matches literally at its start: escaped
// punctuation is unescaped, and it stops at the first operator.
func literalPrefix(re string) string {
	var b strings.Builder
	for i := 0; i < len(re); i++ {
		c := re[i]
		switch {
		case c == '\\' && i+1 < len(re) && !isNameChar(re[i+1]):
			i++
			b.WriteByte(re[i])
		case c == '*' || c == '?' || c == '{' || c == '|':
			// The previous character (or, for |, everything) is optional.
			s := b.String()
			if c == '|' || s == "" {
				return ""
			}
			return s[:len(s)-1]
		case strings.IndexByte(`\.[]()+}$^`, c) >= 0:
			return b.String()
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
