package plugin

import (
	"errors"
	"strings"
	"testing"
)

const (
	canary    = "glsa_CanarySecretToken123"
	canaryHit = "glsa_Canary"
)

// TestErrorsDoNotEchoValues: a name that is not a plugin name (a pasted
// token) is not quoted back; a lock entry that does not match its manifest
// is refused without quoting either.
func TestErrorsDoNotEchoValues(t *testing.T) {
	s := tempStore(t)
	for _, name := range []string{canary, "https://user:" + canary + "@h", "acme/" + canary} {
		if err := s.Remove(name); !errors.Is(err, ErrNotInstalled) || strings.Contains(err.Error(), canaryHit) {
			t.Errorf("Remove(%q) = %v", name, err)
		}
		if _, _, err := s.Open(name); !errors.Is(err, ErrNotInstalled) || strings.Contains(err.Error(), canaryHit) {
			t.Errorf("Open(%q) = %v", name, err)
		}
	}
	if err := s.Remove("acme/pdf.parses"); err == nil || !strings.Contains(err.Error(), "acme/pdf.parses") {
		t.Errorf("a valid name is named: %v", err)
	}
	bin := []byte("bin")
	man := manifestFor("pdf.parses", "1.0.0")
	for name, e := range map[string]LockEntry{
		"name":        entry(canary, OfficialMarketplace, true, bin, man),
		"marketplace": entry(canary+"/pdf.parses", canary, false, bin, man),
		"official":    entry("pdf.parses", canary, true, bin, man),
		"version":     {Name: "pdf.parses", Version: canary, Marketplace: OfficialMarketplace, Official: true, BinarySHA256: sum(bin), ManifestSHA256: sum(man)},
	} {
		if err := s.Install(e, bin, man); err == nil || strings.Contains(err.Error(), canaryHit) || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if !ValidQualified("pdf.parses") || !ValidQualified("acme/pdf.parses") || ValidQualified(canary) || ValidQualified("acme/"+canary) {
		t.Error("ValidQualified")
	}
}
