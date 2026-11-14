package profile

import (
	"slices"
	"testing"

	"github.com/DavidGodefroid/lockkit/config"
)

// unconfined = true reaches the profile and lockkit's console profile; it
// is in the policy fingerprint, and switching it on is a loosening, off a
// tightening (lockkit#133).
func TestUnconfined(t *testing.T) {
	off, err := parseMain(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if off.Unconfined || off.Console().Unconfined {
		t.Fatal("unset unconfined reads as true")
	}
	on, err := parseMain(t, "unconfined = true\n")
	if err != nil {
		t.Fatal(err)
	}
	if !on.Unconfined || !on.Console().Unconfined {
		t.Fatalf("unconfined = true not read: profile %v, console %v", on.Unconfined, on.Console().Unconfined)
	}

	field := func(changes []config.Change, f string) *config.Change {
		for i := range changes {
			if changes[i].Field == f {
				return &changes[i]
			}
		}
		return nil
	}
	a := basePolicy(t)
	b := basePolicy(t)
	b.P.Unconfined = true
	if config.Fingerprint(b) == config.Fingerprint(a) {
		t.Error("unconfined is not in the fingerprint")
	}
	if c := field(b.Diff(a), "unconfined"); c == nil || !c.Loosens {
		t.Errorf("switching unconfined on: %+v, want a loosening", c)
	}
	if c := field(a.Diff(b), "unconfined"); c == nil || c.Loosens {
		t.Errorf("switching unconfined off: %+v, want a tightening", c)
	}
	if !slices.Contains(b.Describe(), "WARNING unconfined: agents that do not run confined are served") {
		t.Errorf("summary does not warn: %q", b.Describe())
	}
}
