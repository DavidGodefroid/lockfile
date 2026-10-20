package backend

import (
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/DavidGodefroid/lockkit/sanitize"

	"github.com/DavidGodefroid/lockfile/internal/profile"
)

// sanitizePatterns scrub source-library and OS error text before it is
// written to the audit or printed on the console window (which the agent
// can read in desktop Level 1). The agent never receives error text at all.
// Order matters: URLs go before paths and host names.
var sanitizePatterns = []*regexp.Regexp{
	sanitize.URLUserinfo,
	// Whole URLs: endpoint, bucket and key of an S3 request.
	regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://[^\s"'<>]+`),
	// AWS access key ids and presigned or signed query parameters.
	regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
	regexp.MustCompile(`(?i)\bX-Amz-[A-Za-z-]+=[^&\s"']+`),
	regexp.MustCompile(`(?i)\bauthorization:[^\r\n]*`),
	// Request and host ids of S3 errors.
	regexp.MustCompile(`(?i)\b(?:request ?id|host ?id)[:=] ?[A-Za-z0-9+/=-]+`),
	// Absolute paths (remote or local file names).
	regexp.MustCompile(`(?:^|[\s"'(=:\[])(/[^\s"')\]]+)`),
	// IP addresses and host names, with a port.
	regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`),
	regexp.MustCompile(`\[[0-9a-fA-F:]+\](?::\d+)?`),
	regexp.MustCompile(`\b(?:[A-Za-z0-9-]+\.)+[A-Za-z]{2,}(?::\d+)?\b`),
}

// Sanitizer returns lockfile's sanitizer: lockkit's, with the patterns of
// the S3 and SFTP clients and the OS.
func Sanitizer() *sanitize.Sanitizer { return sanitize.New(sanitizePatterns...) }

// minProfileValue is the shortest profile value registered as a pattern:
// shorter ones would scrub ordinary words.
const minProfileValue = 3

// profileSanitizer is Sanitizer plus the location values of the profile
// the console serves (bucket, endpoint, host, user, base_dir, paths), each
// registered once as a literal pattern: the generic patterns miss values
// such as single-label hosts ("minio:9000") or relative directories.
type profileSanitizer struct {
	*sanitize.Sanitizer
	mu   sync.Mutex
	seen map[string]bool
}

// Sanitizer is the backend's sanitizer, for console.Options: the profile
// values join it as policies are loaded and sources connected.
func (b *Backend) Sanitizer() *sanitize.Sanitizer { return b.sanitizer().Sanitizer }

func (b *Backend) sanitizer() *profileSanitizer {
	b.sanOnce.Do(func() { b.san = &profileSanitizer{Sanitizer: Sanitizer(), seen: map[string]bool{}} })
	return b.san
}

func (s *profileSanitizer) addProfile(p profile.Profile) {
	vals := []string{p.S3.Bucket, p.S3.Endpoint, p.SFTP.Host, p.SFTP.User, p.SFTP.BaseDir, p.SFTP.KnownHosts, p.SFTPKey, p.LocalPath}
	if u, err := url.Parse(p.S3.Endpoint); err == nil && u.Host != "" {
		vals = append(vals, u.Host, u.Hostname())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range vals {
		v = strings.TrimSuffix(v, "/")
		if len(v) < minProfileValue || s.seen[v] {
			continue
		}
		s.seen[v] = true
		s.Register(regexp.MustCompile(regexp.QuoteMeta(v)))
	}
}
