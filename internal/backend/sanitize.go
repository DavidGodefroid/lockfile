package backend

import (
	"regexp"

	"github.com/DavidGodefroid/lockkit/sanitize"
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
