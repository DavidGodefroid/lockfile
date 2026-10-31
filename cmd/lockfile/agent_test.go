package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/DavidGodefroid/lockkit/ticket"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/clientcmd"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
	"github.com/DavidGodefroid/lockfile/internal/testenv"
)

// planTicket plans through the CLI (args after "plan", with --json) and
// returns the ticket the plan waits on at the hub.
func planTicket(t *testing.T, r *consoleRun, args ...string) string {
	t.Helper()
	code, out, errText := runArgs(t, "", append([]string{"plan", "--json"}, args...)...)
	var p rpc.PlanResult
	if code != 0 || json.Unmarshal([]byte(out), &p) != nil || p.Status != "pending" || !ticket.ValidID(p.Ticket) || p.PlanID != "" {
		t.Fatalf("plan %v: exit %d: %s %s\n%s", args, code, out, errText, r.io.Output())
	}
	return p.Ticket
}

// runTicket runs `lockfile run tk extra...` until the hub's decision has
// reached the console (the answer no longer pending on tk).
func runTicket(t *testing.T, tk string, extra ...string) (int, string, string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		code, out, errText := runArgs(t, "", append([]string{"run", tk}, extra...)...)
		if code != 0 || !strings.Contains(out, tk) || !strings.Contains(strings.ToLower(out), "pending") {
			return code, out, errText
		}
		if time.Now().After(deadline) {
			t.Fatal("the decision never arrived")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestCLIPlanRun(t *testing.T) {
	// The policy at the terminal; the plans are decided on the hub.
	r := startConsole(t, []string{"y"}, "test.answer1", "test.answer0")
	defer r.stop(t)
	tk := planTicket(t, r, "--profile", "samples", "--key", "invoices/a.pdf", "--plugin", "test.answer1", "--plugin", "test.answer0")
	// Before the decision: pending again, nothing runs.
	if code, out, _ := runArgs(t, "", "run", tk, "--json"); code != 0 || !strings.Contains(out, `"pending"`) {
		t.Fatalf("undecided run exit %d: %s", code, out)
	}
	r.hub.Decide(tk, ticket.Approved, "")
	code, out, errText := runTicket(t, tk, "--json")
	if code != 0 {
		t.Fatalf("run exit %d: %s %s", code, out, errText)
	}
	var res answers.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Answers) != 2 || res.Answers[0].Result == nil || res.Answers[1].Result == nil ||
		*res.Answers[0].Result != 1 || *res.Answers[1].Result != 0 {
		t.Fatalf("run %q: %v", out, err)
	}
	// The same ticket does not run twice.
	if code, out, errText := runArgs(t, "", "run", tk, "--profile", "samples"); code == 0 {
		t.Fatalf("second run exit %d: %s %s", code, out, errText)
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
	text := out
	code, out, _ = runArgs(t, "", "status")
	if code != 0 || !strings.Contains(out, "samples: console up, production no") || !strings.Contains(out, "sandbox ok") {
		t.Fatalf("status exit %d: %q", code, out)
	}
	// plan --run waits on the hub: the pending text, then the ticket runs.
	_, pendingText, _ := runArgs(t, "", "plan", "--key", "invoices/a.pdf", "--plugin", "test.answer1", "--run")
	tk = strings.TrimSuffix(strings.Fields(strings.TrimPrefix(pendingText, "Pending human approval: ticket "))[0], ".")
	if !strings.HasPrefix(pendingText, "Pending human approval: ticket ") || !ticket.ValidID(tk) {
		t.Fatalf("plan --run text %q", pendingText)
	}
	r.hub.Decide(tk, ticket.Approved, "")
	_, answersText, _ := runTicket(t, tk)
	if !strings.Contains(answersText, "test.answer1@1.0.0: 1") {
		t.Fatalf("run text %q", answersText)
	}
	v := cliVocab("invoices/a.pdf", "test.answer1", "test.answer0", "Is it test.answer1?", "Is it test.answer0?", "official", "1 = yes", "0 = no")
	for _, s := range []string{text, out, pendingText, answersText} {
		v.CheckText(t, s)
	}
}

// cliVocab is the closed vocabulary of the agent commands' output: the
// closed list, the agent's inputs of the test, the test plugins' manifest
// texts and the commands' fixed texts.
func cliVocab(inputs ...string) testenv.Vocab {
	extra := append([]string{
		"lockfile:", "refusal", "retry:", "usage: lockfile",
		"plan [--profile P] (--key KEY | --handle HANDLE) --plugin NAME... [--intent TEXT] [--run] [--json]",
		"use a profile listed by lockfile sources, or ask the user to run, in the lockfile account: " + clientcmd.StartCommand("other"),
		"handle", "no answer", "(unofficial marketplace)", "released", "UNOFFICIAL MARKETPLACE",
		"plan approved: it reveals at most answers", "run it once: lockfile run --profile",
		"console up, production no, auto no, security level, plugin protocol, sandbox ok",
	}, inputs...)
	return testenv.ClosedVocab(extra...).WithKeys(testenv.ClientKeys...)
}

func TestCLIExitCodes(t *testing.T) {
	// The policy at the terminal; on the hub, plan 1 approved, plan 2
	// denied with a reason.
	r := startConsole(t, []string{"y"}, "test.answer0", "test.printthenhang")
	defer r.stop(t)
	var all strings.Builder
	v := cliVocab("invoices/a.pdf", "../etc/passwd", "k", "other", "test.answer0", "test.printthenhang", "test.nope")
	check := func(name string, want int, args ...string) string {
		t.Helper()
		code, out, errText := runArgs(t, "", args...)
		all.WriteString(out + errText)
		if code != want {
			t.Fatalf("%s: exit %d, want %d: %s %s\n%s", name, code, want, out, errText, r.io.Output())
		}
		// Whitelist: JSON on stdout walked value by value, text word by word.
		if slices.Contains(args, "--json") {
			v.CheckJSON(t, []byte(out))
			v.CheckText(t, errText)
		} else {
			v.CheckText(t, out+errText)
		}
		return out + errText
	}
	// ran: the run of a decided ticket, its output checked as check does.
	ran := func(name string, want int, tk string, extra ...string) string {
		t.Helper()
		code, out, errText := runTicket(t, tk, extra...)
		all.WriteString(out + errText)
		if code != want {
			t.Fatalf("%s: exit %d, want %d: %s %s\n%s", name, code, want, out, errText, r.io.Output())
		}
		if slices.Contains(extra, "--json") {
			v.CheckJSON(t, []byte(out))
			v.CheckText(t, errText)
		} else {
			v.CheckText(t, out+errText)
		}
		return out + errText
	}
	tk := planTicket(t, r, "--key", "invoices/a.pdf", "--plugin", "test.answer0", "--plugin", "test.printthenhang")
	r.hub.Decide(tk, ticket.Approved, "")
	out := ran("answered with a 0 and no answer", 0, tk, "--json")
	var res answers.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Answers) != 2 || res.Answers[0].Result == nil || *res.Answers[0].Result != 0 ||
		res.Answers[1].Result != nil || res.Answers[1].Error != "plugin_error" {
		t.Fatalf("result %s", out)
	}
	tk = planTicket(t, r, "--key", "invoices/a.pdf", "--plugin", "test.answer0")
	r.hub.Decide(tk, ticket.Denied, "it is 18 bytes, /srv/x")
	ran("denied", product.ExitDenied, tk)
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
	if !strings.Contains(all.String(), "approval_denied") {
		t.Fatalf("denial not reported:\n%s", all.String())
	}
}

func TestTextOutputCapped(t *testing.T) {
	var out, errOut strings.Builder
	e := env{stdout: &out, stderr: &errOut}
	long := strings.Repeat("é", 5000)
	if code := emit(e, false, nil, func() []string { return []string{"  ? " + long, "short"} }); code != product.ExitOK {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 || utf8.RuneCountInString(lines[0]) != outCaps.MaxCellChars || !strings.HasSuffix(lines[0], "…") || lines[1] != "short" {
		t.Fatalf("cell cap not applied: %d lines, first %d chars", len(lines), utf8.RuneCountInString(lines[0]))
	}
	out.Reset()
	many := make([]string, 1000)
	for i := range many {
		many[i] = strings.Repeat("x", 100)
	}
	if code := emit(e, false, nil, func() []string { return many }); code != product.ExitError || out.Len() != 0 ||
		!strings.Contains(errOut.String(), "output too large") {
		t.Fatalf("total cap not applied: exit %d, %d bytes printed", code, out.Len())
	}
}
