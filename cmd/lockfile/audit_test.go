package main

import (
	"strings"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/audit"
	"github.com/DavidGodefroid/lockkit/sysconf"

	"github.com/DavidGodefroid/lockfile/internal/backend"
	"github.com/DavidGodefroid/lockfile/internal/product"
)

func TestAuditOnlyServiceAccount(t *testing.T) {
	h, _ := humanEnv(t, "official")
	log, err := audit.Open(product.P, h.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Append(backend.Record{Base: audit.Base{Event: audit.EventRun, Profile: "docs"}, PlanID: "pl_1", Key: "invoices/a.pdf", Size: 4821}); err != nil {
		t.Fatal(err)
	}
	code, out, errText := runArgs(t, "", "audit")
	if code != 0 || !strings.Contains(out, `key="invoices/a.pdf"`) || !strings.Contains(out, "size=4821") {
		t.Fatalf("exit %d: %s %s", code, out, errText)
	}
	if code, out, _ := runArgs(t, "", "audit", "--since", time.Now().Add(time.Hour).Format(time.RFC3339)); code != 0 || out != "" {
		t.Fatalf("since the future: %q", out)
	}
	if code, out, _ := runArgs(t, "", "audit", "--json", "--since", "1h"); code != 0 || !strings.HasPrefix(out, `{"ts":`) {
		t.Fatalf("json: %q", out)
	}
	h.sys = func() (*sysconf.Config, error) { return &sysconf.Config{ServiceUser: "root"}, nil }
	h.euid = func() int { return 4242 }
	if code, out, errText := runArgs(t, "", "audit"); code != product.ExitError || out != "" || !strings.Contains(errText, "root account") {
		t.Fatalf("other account: exit %d: %q %s", code, out, errText)
	}
}

func TestForgetNeedsKnownProfile(t *testing.T) {
	humanEnv(t, "official")
	if code, _, _ := runArgs(t, "", "forget"); code != product.ExitUsage {
		t.Fatalf("no profile: exit %d", code)
	}
	if code, _, errText := runArgs(t, "", "forget", "--profile", "nope"); code != product.ExitError || !strings.Contains(errText, "config") {
		t.Fatalf("exit %d: %s", code, errText)
	}
}
