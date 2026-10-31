package pluginspec

import (
	"strings"
	"testing"
)

const (
	canary    = "glsa_CanarySecretToken123"
	canaryHit = "glsa_Canary"
)

// TestErrorsDoNotEchoValues: a manifest or size error names the key and
// the rule, never the value.
func TestErrorsDoNotEchoValues(t *testing.T) {
	if _, err := ParseBytes(canary); err == nil || strings.Contains(err.Error(), canaryHit) || !strings.Contains(err.Error(), "size") {
		t.Errorf("ParseBytes: %v", err)
	}
	base := `name = "pdf.parses"
version = "1.0.0"
protocol = 1
question = "Is it?"
one = "yes"
zero = "no"
accepts = ["application/pdf"]
limits = { cpu_s = 5, memory = "64MiB", timeout_s = 5 }
`
	for key, repl := range map[string][2]string{
		"name":          {`name = "pdf.parses"`, `name = "` + canary + `"`},
		"version":       {`version = "1.0.0"`, `version = "` + canary + `"`},
		"accepts":       {`accepts = ["application/pdf"]`, `accepts = ["` + canary + `"]`},
		"limits.memory": {`memory = "64MiB"`, `memory = "` + canary + `"`},
	} {
		_, err := ParseManifest([]byte(strings.Replace(base, repl[0], repl[1], 1)))
		if err == nil || strings.Contains(err.Error(), canaryHit) || !strings.Contains(err.Error(), key) {
			t.Errorf("%s: %v", key, err)
		}
	}
}
