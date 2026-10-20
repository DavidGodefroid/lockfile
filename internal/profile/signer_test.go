package profile

import (
	"strings"
	"testing"
)

func TestSignerWarnings(t *testing.T) {
	for _, tc := range []struct {
		re        string
		dot, path bool
	}{
		{`^https://github\.com/acme/plugins/\.github/workflows/release\.yml@refs/tags/`, false, false},
		{`^https://github\.com/acme/plugins/.+@refs/tags/`, false, false},
		// An unescaped dot in the host also leaves the path unpinned.
		{`^https://github.com/acme/plugins/`, true, true},
		{`^https://github\.com/acme`, false, true},
		{`^https://github\.com/acme/plugins`, false, true},
		{`^https://github\.com/acme/plugins?/`, false, true},
		{`^https://github\.com/(acme|evil)/plugins/`, false, true},
		{`^https://github\.com/acme/[a-z.]+/`, false, true},
		{`^https://git.example.org/tools/plugins/.+@refs/tags/`, true, true},
		{`^https://git\.example\.org/tools/plugins/.+@refs/tags/`, false, false},
	} {
		w := strings.Join(SignerWarnings(tc.re), "\n")
		if got := strings.Contains(w, "unescaped"); got != tc.dot {
			t.Errorf("%s: dot warning %v, want %v", tc.re, got, tc.dot)
		}
		if got := strings.Contains(w, "repository path"); got != tc.path {
			t.Errorf("%s: path warning %v, want %v", tc.re, got, tc.path)
		}
	}
}
