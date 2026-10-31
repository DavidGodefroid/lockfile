package clientcmd

import (
	"strings"
	"testing"
)

// The unknown_profile hint quotes the profile only when it is a valid
// profile name.
func TestUnknownProfileHintDoesNotEcho(t *testing.T) {
	for _, p := range []string{"glsa_CanarySecretToken123 x", "https://user:glsa_Canary@h"} {
		if h := unknownProfile(p).Refusal.Hint; strings.Contains(h, "glsa_Canary") || !strings.Contains(h, "--profile NAME") {
			t.Errorf("%q: hint %q", p, h)
		}
	}
	if h := unknownProfile("other").Refusal.Hint; !strings.Contains(h, "--profile other") {
		t.Errorf("valid name: %q", h)
	}
}
