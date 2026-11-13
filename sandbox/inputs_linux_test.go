package sandbox

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockfile/pluginspec"
)

func runSpec(t *testing.T, bin string, content string, s Spec) (pluginspec.Outcome, Report) {
	t.Helper()
	in, _ := input(t, content)
	b, err := os.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	s.Binary, s.Input = b, in
	if s.Limits == (pluginspec.Limits{}) {
		s.Limits = defaultLimits
	}
	o, r, err := Run(context.Background(), s)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return o, r
}

// The typed values reach the plugin on fd 6, read-only (a write, a
// truncate and a pwrite fail), and nowhere else: not argv, not env.
func TestInputsOnFD6Only(t *testing.T) {
	vals := []byte(`{"needle":"alice@example.org"}`)
	o, r := runSpec(t, plugin(t, "inputsprobe"), "x", Spec{Protocol: 2, Inputs: vals})
	wantAnswer(t, o, r, true)
	o, r = runSpec(t, plugin(t, "inputsprobe", "-X main.wantDetail=1"), "x", Spec{Protocol: 2, Inputs: vals, Detail: &bytes.Buffer{}})
	wantAnswer(t, o, r, true)
}

func TestContainsWithDetail(t *testing.T) {
	vals := []byte(`{"needle":"alice@example.org"}`)
	var d bytes.Buffer
	o, r := runSpec(t, plugin(t, "contains"), "page 1 ... alice@example.org ...", Spec{Protocol: 2, Inputs: vals, Detail: &d})
	wantAnswer(t, o, r, true)
	if !strings.HasPrefix(d.String(), "found at byte 11") {
		t.Fatalf("detail %q", d.String())
	}
	d.Reset()
	o, r = runSpec(t, plugin(t, "contains"), "nothing here", Spec{Protocol: 2, Inputs: vals, Detail: &d})
	wantAnswer(t, o, r, false)
	if d.String() != "not found" {
		t.Fatalf("detail %q", d.String())
	}
	// Without a detail fd the plugin answers all the same.
	o, r = runSpec(t, plugin(t, "contains"), "alice@example.org", Spec{Protocol: 2, Inputs: vals})
	wantAnswer(t, o, r, true)
	// Without its values it fails.
	o, r = runSpec(t, plugin(t, "contains"), "alice@example.org", Spec{Protocol: 2})
	wantFailure(t, o, r, pluginspec.CauseExitCode)
}

// fd 7 is read up to 4 KiB, the rest drained: a plugin writing without end
// neither blocks nor fails.
func TestDetailCapped(t *testing.T) {
	var d bytes.Buffer
	o, r := runSpec(t, plugin(t, "detailflood"), "x", Spec{Protocol: 2, Detail: &d})
	wantAnswer(t, o, r, true)
	if d.Len() != pluginspec.MaxDetail {
		t.Fatalf("detail kept %d bytes, want %d", d.Len(), pluginspec.MaxDetail)
	}
}

// A protocol-1 plugin still gets fds 0-3 only and LOCKFILE_PLUGIN_PROTOCOL=1.
func TestProtocol1NoExtraFDs(t *testing.T) {
	o, r := runSpec(t, plugin(t, "fdleak"), "x", Spec{})
	wantAnswer(t, o, r, false)
	o, r = runSpec(t, plugin(t, "env"), "x", Spec{Protocol: 1})
	wantAnswer(t, o, r, true)
}
