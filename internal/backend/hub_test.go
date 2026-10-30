package backend

import (
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/plans"
	"github.com/DavidGodefroid/lockkit/ticket"

	"github.com/DavidGodefroid/lockfile/internal/profile"
	"github.com/DavidGodefroid/lockfile/internal/testenv"
)

// storedPlan is a hub plan as hubPlan stores it: its body and the binding
// made from it.
func storedPlan(t *testing.T, pol *profile.Policy, body hubBody) *plans.Plan {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	hash, inputs, err := body.hashes()
	if err != nil {
		t.Fatal(err)
	}
	return &plans.Plan{Body: raw, Binding: plans.Binding{PolicyFP: policyFPOf(pol), PlanHash: hash, Inputs: inputs}}
}

func sampleBody(t *testing.T, pol *profile.Policy) hubBody {
	t.Helper()
	pin, ok := pol.Pin("test.answer1")
	if !ok {
		t.Fatal("test.answer1 is not pinned")
	}
	return hubBody{Profile: "samples", Key: "invoices/a.pdf", SHA256: strings.Repeat("a", 64),
		Plugins: []string{"test.answer1"}, Pins: []profile.PluginPin{pin}}
}

// The recompute gives what the plan stored and what the run's recheck
// checks: the same policy fingerprint, plan hash and inputs, which name
// the fields of the hashed body.
func TestRecomputeIsTheRecheck(t *testing.T) {
	h := newHarness(t, "test.answer1")
	pol := h.env.policy
	sp := storedPlan(t, pol, sampleBody(t, pol))
	fp, hash, inputs, err := h.b.recompute(h.env)(sp)
	if err != nil {
		t.Fatal(err)
	}
	if fp != sp.Binding.PolicyFP || hash != sp.Binding.PlanHash || !maps.Equal(inputs, sp.Binding.Inputs) {
		t.Fatalf("recompute %s %s %v, stored %+v", fp, hash, inputs, sp.Binding)
	}
	want, err := ticket.HashOf(sampleBody(t, pol))
	if err != nil || hash != want {
		t.Fatalf("plan hash %s, HashOf(body) %s %v", hash, want, err)
	}
	_, rfp, rhash, _, err := h.b.hubCheck(pol, sp)
	if err != nil || rfp != fp || rhash != hash {
		t.Fatalf("recheck %s %s %v", rfp, rhash, err)
	}
	for _, k := range []string{"profile", "key", "sha256", "plugins", "pins"} {
		if _, ok := inputs[k]; !ok {
			t.Errorf("input %q missing from %v", k, inputs)
		}
	}
	if len(ticket.DiffInputs(sp.Binding.Inputs, inputs)) != 0 {
		t.Fatal("an unchanged plan shows changes")
	}
}

// An input that moves shows up by name, and by name only.
func TestRecomputeNamesMovedInput(t *testing.T) {
	h := newHarness(t, "test.answer1")
	pol := h.env.policy
	old := storedPlan(t, pol, sampleBody(t, pol))
	moved := sampleBody(t, pol)
	moved.SHA256 = strings.Repeat("b", 64)
	_, hash, inputs, err := h.b.recompute(h.env)(storedPlan(t, pol, moved))
	if err != nil {
		t.Fatal(err)
	}
	if hash == old.Binding.PlanHash {
		t.Fatal("the plan hash did not move")
	}
	ch := ticket.DiffInputs(old.Binding.Inputs, inputs)
	if len(ch) != 1 || ch[0].Input != "sha256" || ch[0].Change != ticket.ChangeChanged {
		t.Fatalf("changes %+v", ch)
	}
	if b, _ := json.Marshal(ch); strings.Contains(string(b), moved.SHA256) || strings.Contains(string(b), "sha256:") {
		t.Fatalf("a change carries a value or a digest: %s", b)
	}
}

// A plugin replaced since the plan: the recompute refuses it stale, as the
// run would (the renewal is not asked); a changed policy gives another
// fingerprint, which lockkit refuses to renew across.
func TestRecomputeStaleAndPolicy(t *testing.T) {
	h := newHarness(t, "test.answer1")
	pol := h.env.policy
	sp := storedPlan(t, pol, sampleBody(t, pol))
	changed := *pol
	changed.P.MaxObjectBytes = 1
	h.env.policy = &changed
	fp, _, _, err := h.b.recompute(h.env)(sp)
	if err != nil || fp == sp.Binding.PolicyFP {
		t.Fatalf("changed policy: fingerprint %s %v", fp, err)
	}
	h.env.policy = pol
	testenv.Install(t, h.store, "test.answer1", testenv.BuildPlugin(t, "answer0"), "Is it test.answer1?", 2)
	if _, _, _, err := h.b.recompute(h.env)(sp); !errors.Is(err, plans.ErrStale) {
		t.Fatalf("replaced plugin: %v", err)
	}
}
