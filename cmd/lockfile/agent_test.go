package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
)

func TestCLIPlanRun(t *testing.T) {
	// Policy, then the plan.
	r := startConsole(t, []string{"y", "y"}, "test.answer1", "test.answer0")
	defer r.stop(t)
	code, out, errText := runArgs(t, "", "plan", "--profile", "samples", "--key", "invoices/a.pdf",
		"--plugin", "test.answer1", "--plugin", "test.answer0", "--json")
	if code != 0 {
		t.Fatalf("plan exit %d: %s %s\n%s", code, out, errText, r.io.Output())
	}
	var plan rpc.PlanResult
	if err := json.Unmarshal([]byte(out), &plan); err != nil || plan.Answers != 2 || !strings.HasPrefix(plan.PlanID, "pl_") {
		t.Fatalf("plan %q: %v", out, err)
	}
	code, out, errText = runArgs(t, "", "run", plan.PlanID, "--json")
	if code != 0 {
		t.Fatalf("run exit %d: %s %s", code, out, errText)
	}
	var res answers.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Answers) != 2 ||
		*res.Answers[0].Result != 1 || *res.Answers[1].Result != 0 {
		t.Fatalf("run %q: %v", out, err)
	}
	// The same plan does not run twice.
	if code, _, errText := runArgs(t, "", "run", plan.PlanID, "--profile", "samples"); code != product.ExitRefused || !strings.Contains(errText, rpc.MsgNoSuchPlan) {
		t.Fatalf("second run exit %d: %s", code, errText)
	}
	code, out, _ = runArgs(t, "", "answers", res.Handle, "--profile", "samples")
	if code != 0 || !strings.Contains(out, "test.answer1@1.0.0: 1") || !strings.Contains(out, "test.answer0@1.0.0: 0") {
		t.Fatalf("answers exit %d: %q", code, out)
	}
	if code, out, _ := runArgs(t, "", "release", res.Handle, "--json"); code != 0 || strings.TrimSpace(out) != `{"released":true}` {
		t.Fatalf("release exit %d: %q", code, out)
	}
	if code, _, errText := runArgs(t, "", "answers", res.Handle); code != product.ExitRefused || !strings.Contains(errText, "E_HANDLE_EXPIRED") {
		t.Fatalf("answers after release exit %d: %s", code, errText)
	}
	code, out, _ = runArgs(t, "", "sources", "--json")
	var src []rpc.Source
	if err := json.Unmarshal([]byte(out), &src); code != 0 || err != nil || len(src) != 1 || src[0] != (rpc.Source{Profile: "samples"}) {
		t.Fatalf("sources exit %d: %q", code, out)
	}
	code, out, _ = runArgs(t, "", "plugins")
	if code != 0 || !strings.Contains(out, "? Is it test.answer1?") {
		t.Fatalf("plugins exit %d: %q", code, out)
	}
	code, out, _ = runArgs(t, "", "status")
	if code != 0 || !strings.Contains(out, "samples: console up, production no") || !strings.Contains(out, "sandbox ok") {
		t.Fatalf("status exit %d: %q", code, out)
	}
}

// forbidden matches what the CLI must never print: file content, paths,
// sizes, hashes, library or runtime error text.
var forbidden = regexp.MustCompile(`(?i)synthetic|/tmp|/srv|bytes|sha|\bsize|\.go\b|panic|goroutine`)

func TestCLIExitCodes(t *testing.T) {
	// Policy, plan 1 approved, plan 2 denied with a reason.
	r := startConsole(t, []string{"y", "y", "n it is 18 bytes, /srv/x"}, "test.answer0", "test.printthenhang")
	defer r.stop(t)
	var all strings.Builder
	check := func(name string, want int, args ...string) string {
		t.Helper()
		code, out, errText := runArgs(t, "", args...)
		all.WriteString(out + errText)
		if code != want {
			t.Fatalf("%s: exit %d, want %d: %s %s\n%s", name, code, want, out, errText, r.io.Output())
		}
		return out + errText
	}
	out := check("answered with a 0 and no answer", 0, "plan", "--key", "invoices/a.pdf", "--plugin", "test.answer0", "--plugin", "test.printthenhang", "--run", "--json")
	var res answers.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Answers) != 2 || *res.Answers[0].Result != 0 ||
		res.Answers[1].Result != nil || res.Answers[1].Error != "plugin_error" {
		t.Fatalf("result %s", out)
	}
	check("denied", product.ExitDenied, "plan", "--key", "invoices/a.pdf", "--plugin", "test.answer0")
	out = check("refused", product.ExitRefused, "plan", "--key", "../etc/passwd", "--plugin", "test.answer0", "--json")
	var ref map[string]any
	if err := json.Unmarshal([]byte(out), &ref); err != nil || ref["error"] != "key_outside_prefix" ||
		ref["refusal"].(map[string]any)["code"] != "E_KEY_OUTSIDE_PREFIX" || len(ref) != 2 {
		t.Fatalf("refusal JSON %s", out)
	}
	check("no such plugin", product.ExitRefused, "plan", "--key", "invoices/a.pdf", "--plugin", "test.nope")
	// unknown_profile is a refusal (2) whether the client or the console
	// detects it.
	out = check("no console for the profile", product.ExitRefused, "plugins", "--profile", "other")
	if !strings.Contains(out, "lockfile console --profile other") || !strings.Contains(out, "E_UNKNOWN_PROFILE") {
		t.Fatalf("no hint: %s", out)
	}
	check("no console for the profile, plan", product.ExitRefused, "plan", "--profile", "other", "--key", "invoices/a.pdf", "--plugin", "test.answer0")
	check("usage", product.ExitUsage, "plan", "--key", "k")
	if m := forbidden.FindString(all.String()); m != "" {
		t.Fatalf("CLI output holds %q:\n%s", m, all.String())
	}
	if !strings.Contains(all.String(), "approval_denied") {
		t.Fatalf("denial not reported:\n%s", all.String())
	}
}
