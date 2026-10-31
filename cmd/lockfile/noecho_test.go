package main

import (
	"strings"
	"testing"
	"time"
)

const (
	canary    = "glsa_CanarySecretToken123"
	urlCanary = "https://user:glsa_Canary@h"
	canaryHit = "glsa_Canary"
)

// TestErrorsDoNotEchoValues: a typed value (a pasted token, a URL with
// one) is never quoted back; the error names the flag or argument.
func TestErrorsDoNotEchoValues(t *testing.T) {
	humanEnv(t, "official")
	for _, v := range []string{canary, urlCanary} {
		if _, err := sinceTime(v, time.Now()); err == nil || strings.Contains(err.Error(), canaryHit) || !strings.Contains(err.Error(), "--since") {
			t.Errorf("--since %q: %v", v, err)
		}
		if _, err := findMarketplace(v); err == nil || strings.Contains(err.Error(), canaryHit) || !strings.Contains(err.Error(), "marketplace") {
			t.Errorf("--marketplace %q: %v", v, err)
		}
	}
	for _, args := range [][]string{
		{"plugin", "install", canary + "@1.0.0"},
		{"plugin", "install", urlCanary + "@1.0.0"},
		{"plugin", "install", "pdf.parses@" + canary},
		{"plugin", "update", canary + "@1.0.0"},
		{"plugin", "install", "--marketplace", canary, "pdf.parses@1.0.0"},
		{"plugin", "install", canary + "/pdf.parses@1.0.0"},
		{"plugin", "verify", canary},
		{"plugin", "remove", canary},
		{"marketplace", "remove", canary},
		{"audit", "--since", canary},
	} {
		code, out, errText := runArgs(t, "", args...)
		if code == 0 || strings.Contains(out+errText, canaryHit) {
			t.Errorf("%v: exit %d: %s %s", args, code, out, errText)
		}
	}
}
