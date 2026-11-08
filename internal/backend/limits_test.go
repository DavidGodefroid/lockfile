package backend

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/hubclient"
	"github.com/DavidGodefroid/lockkit/hubclient/hubtest"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
)

// openHub opens a console hub on a fake lockhub, as console.Run would.
func openHub(t *testing.T) *console.Hub {
	t.Helper()
	home := t.TempDir()
	key, err := hubclient.InstanceKey(home, "files")
	if err != nil {
		t.Fatal(err)
	}
	hub := hubtest.New(t, "files", key.Public().(ed25519.PublicKey))
	hubKey := filepath.Join(t.TempDir(), "hub.pub")
	if err := os.WriteFile(hubKey, []byte(base64.StdEncoding.EncodeToString(hub.Pub)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := console.OpenHub(product.P, console.HubOptions{URL: hub.URL(), KeyFile: hubKey, Instance: "files", Home: home}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

// The ticket limits of the policy in force reach the hub client, whether
// it was applied before the hub opened or after; 0 is the default
// (lockkit#150).
func TestApplyPolicyTicketLimits(t *testing.T) {
	h := newHarness(t)
	pol := *h.env.policy
	pol.P.TicketsPerWindow, pol.P.TicketWindow, pol.P.MaxPending = 2, 10*time.Minute, 7
	if err := h.b.ApplyPolicy(&pol); err != nil {
		t.Fatal(err)
	}
	hub := openHub(t)
	h.b.OnHub(hub)
	want := hubclient.Limits{MaxPending: 7, RateN: 2, RateWindow: 10 * time.Minute, RateTotal: hubclient.RateTotal}
	if got := hub.Client.Limits(); got != want {
		t.Fatalf("hub client limits %+v, want %+v", got, want)
	}
	next := pol
	next.P.TicketsPerWindow, next.P.TicketWindow, next.P.MaxPending = 60, 0, 0
	if err := h.b.ApplyPolicy(&next); err != nil {
		t.Fatal(err)
	}
	want = hubclient.DefaultLimits()
	want.RateN = 60
	if got := hub.Client.Limits(); got != want {
		t.Fatalf("after the policy change %+v, want %+v", got, want)
	}
	if err := h.b.ApplyPolicy(&fakePolicy{}); err == nil {
		t.Fatal("a foreign policy was applied")
	}
	if got := hub.Client.Limits(); got != want {
		t.Fatalf("a refused policy changed the limits: %+v", got)
	}
}

// fakePolicy is a policy of another product.
type fakePolicy struct{ profile.Policy }
