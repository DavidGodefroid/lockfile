package backend

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/config"
	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/plans"
	"github.com/DavidGodefroid/lockkit/ticket"

	"github.com/DavidGodefroid/lockfile/internal/product"
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
	fp, hash, inputs, err := h.b.recompute(sp)
	if err != nil {
		t.Fatal(err)
	}
	// Everything but the content, which is checked at the run only.
	stored := maps.Clone(sp.Binding.Inputs)
	stored["sha256"] = ticket.UncheckedInput
	if fp != sp.Binding.PolicyFP || hash != sp.Binding.PlanHash || !maps.Equal(inputs, stored) {
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
	// The content was not checked: the renewal says so, by name, and is
	// never presented as unchanged.
	ch := ticket.DiffInputs(sp.Binding.Inputs, inputs)
	if len(ch) != 1 || ch[0].Input != "sha256" || ch[0].Change != ticket.ChangeUnchecked {
		t.Fatalf("changes of an unchanged plan %+v", ch)
	}
	// A plan without content (a handle) has nothing unchecked.
	hb := sampleBody(t, pol)
	hb.Key, hb.Handle, hb.SHA256 = "", "h_1", ""
	hp := storedPlan(t, pol, hb)
	_, _, hin, err := h.b.recompute(hp)
	if err != nil || len(ticket.DiffInputs(hp.Binding.Inputs, hin)) != 0 {
		t.Fatalf("handle plan: %v %v", hin, err)
	}
}

// An input that moves shows up by name, and by name only.
func TestRecomputeNamesMovedInput(t *testing.T) {
	h := newHarness(t, "test.answer1")
	pol := h.env.policy
	old := storedPlan(t, pol, sampleBody(t, pol))
	moved := sampleBody(t, pol)
	moved.Key = "invoices/b.pdf"
	_, hash, inputs, err := h.b.recompute(storedPlan(t, pol, moved))
	if err != nil {
		t.Fatal(err)
	}
	if hash == old.Binding.PlanHash {
		t.Fatal("the plan hash did not move")
	}
	ch := ticket.DiffInputs(old.Binding.Inputs, inputs)
	if len(ch) != 2 || ch[0].Input != "key" || ch[0].Change != ticket.ChangeChanged ||
		ch[1].Input != "sha256" || ch[1].Change != ticket.ChangeUnchecked {
		t.Fatalf("changes %+v", ch)
	}
	if b, _ := json.Marshal(ch); strings.Contains(string(b), "b.pdf") || strings.Contains(string(b), "sha256:") {
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
	// The policy in force is the one the console saved as approved.
	changed := *pol
	changed.P.MaxObjectBytes = 1
	if err := config.SaveApproved(product.P, h.state, config.ApprovedKey("", "samples"), &changed); err != nil {
		t.Fatal(err)
	}
	fp, _, _, err := h.b.recompute(sp)
	if err != nil || fp == sp.Binding.PolicyFP {
		t.Fatalf("changed policy: fingerprint %s %v", fp, err)
	}
	if err := config.SaveApproved(product.P, h.state, config.ApprovedKey("", "samples"), pol); err != nil {
		t.Fatal(err)
	}
	testenv.Install(t, h.store, "test.answer1", testenv.BuildPlugin(t, "answer0"), "Is it test.answer1?", 2)
	if _, _, _, err := h.b.recompute(sp); !errors.Is(err, plans.ErrStale) {
		t.Fatalf("replaced plugin: %v", err)
	}
}

// The content line of a hub plan (the object's sha256 and size, or that
// it could not be fetched) is on the hub's masked screen only, never on
// the console window the agent's account can read at desktop level 1.
func TestHubContentNotInWindow(t *testing.T) {
	sha := strings.Repeat("ab", 32)
	const size = 734821
	sc := console.Screen{Header: "file.plan", Note: "check the invoice",
		Body: []string{"key    invoices/a.pdf", "", "test.answer1  1.0.0"}, Warnings: []string{"unofficial plugin"}}
	for _, content := range []string{contentLine(sha, size), msgUnfetched} {
		win, masked := hubScreens(sc, content)
		shown := strings.Join(append(append([]string{win.Header, win.Requester, win.Note}, win.Body...), win.Warnings...), "\n")
		for _, s := range []string{sha, "734821", "bytes", "sha256", "fetch"} {
			if strings.Contains(shown, s) {
				t.Errorf("console window shows %q:\n%s", s, shown)
			}
		}
		if !slices.Equal(win.Body, sc.Body) {
			t.Errorf("window body %q, want %q", win.Body, sc.Body)
		}
		if !masked.Valid() {
			t.Fatal("masked screen not valid")
		}
		b, err := json.Marshal(masked.Screen())
		if err != nil {
			t.Fatal(err)
		}
		want := sha
		if content == msgUnfetched {
			want = "could not be fetched now"
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("the hub's masked screen lacks %q: %s", want, b)
		}
	}
}
