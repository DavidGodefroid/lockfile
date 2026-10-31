package testenv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/DavidGodefroid/lockkit/mcpkit"
	"github.com/DavidGodefroid/lockkit/refusal"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
)

// Vocab is the closed vocabulary of what the agent may read
// (docs/security-model.md), plus what the agent sent and the fixed texts of
// the output under test. Tests check every agent-visible output against it
// as a whitelist: a value, a key or a word outside it fails.
type Vocab struct {
	Strings  map[string]bool
	Patterns []*regexp.Regexp
	Keys     map[string]bool
}

// wireKeys are the keys an agent-visible object may carry on the wire.
var wireKeys = []string{
	// JSON-RPC
	"jsonrpc", "id", "result", "error", "code", "message", "data",
	// refusals
	"retry", "hint",
	// results
	"handle", "answers", "plugin", "version", "official", "warning",
	"plan_id", "released", "marketplace", "question", "one", "zero",
	// lockkit hello and status, lockfile's status part
	"protocol_major", "console_version", "profile", "production", "auto_approve",
	"allow_unmask", "show_results", "idle_timeout_in_s", "session_ends_in_s", "level",
	"product", "protocol", "sandbox", "answer_quantum_ms",
	// change.request, sources and status (busy: documented addition)
	"queued", "busy",
	// a plan or run pending on the hub (console.PendingResp)
	"status", "ticket", "lock", "instance",
}

// ClientKeys are the keys the CLI and MCP outputs add around the wire's
// values: the MCP structured outputs and the CLI's JSON refusal.
var ClientKeys = []string{"consoles", "console", "sources", "plugins", "refusal"}

// numericKeys are the keys whose value may be a number.
var numericKeys = map[string]bool{
	"id": true, "code": true, "result": true, "answers": true, "protocol_major": true, "level": true,
	"idle_timeout_in_s": true, "session_ends_in_s": true, "protocol": true, "answer_quantum_ms": true,
}

// ClosedVocab is the closed vocabulary plus extra: the agent's own inputs,
// the test plugins' manifest texts and the fixed texts of the output
// checked.
func ClosedVocab(extra ...string) Vocab {
	v := Vocab{Strings: map[string]bool{
		"2.0": true, product.Version: true, "samples": true, "ok": true, "unavailable": true,
		"official": true, "acme": true, "1.0.0": true, "yes": true, "no": true, answers.WarningUnofficial: true,
		rpc.MsgPlanParams: true, rpc.MsgParams: true, rpc.MsgNoSuchPlan: true, rpc.MsgConnLost: true,
		rpc.MsgPending: true, rpc.MsgClosed: true, rpc.MsgInternal: true, rpc.MsgRunTicket: true,
		// Pending on the hub: the status, the lock and the instance the
		// tests' hub knows the console by (consoletest.HubInstance), and the
		// fixed text the CLI and MCP print with the ticket.
		"pending": true, product.P.Name: true, "files": true, mcpkit.PendingText(product.P.Name, ""): true,
	}, Keys: map[string]bool{}}
	for _, c := range answers.Codes {
		v.Strings[string(c)] = true
		if product.Mapped(c) {
			rc := product.Code(c)
			v.Strings[string(rc)], v.Strings[rc.Hint()], v.Strings[string(rc.Retry())] = true, true, true
		}
	}
	for _, r := range []refusal.Retry{refusal.RetryRewrite, refusal.RetryAskUser, refusal.RetryStop} {
		v.Strings[string(r)] = true
	}
	for _, s := range extra {
		v.Strings[s] = true
	}
	for _, k := range wireKeys {
		v.Keys[k] = true
	}
	v.Patterns = []*regexp.Regexp{
		regexp.MustCompile(`^pl_[0-9A-Za-z]{10}$`),
		regexp.MustCompile(`^f_[0-9A-Za-z]+$`),
		// a hub ticket id (ULID, random)
		regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`),
		// lockkit's fixed texts naming the agent's own method.
		regexp.MustCompile(`^invalid params for [a-z.]+$`),
		regexp.MustCompile(`^unknown method "[a-z.]+"$`),
	}
	return v
}

// WithKeys is v allowing keys too.
func (v Vocab) WithKeys(keys ...string) Vocab {
	k := make(map[string]bool, len(v.Keys)+len(keys))
	for x := range v.Keys {
		k[x] = true
	}
	for _, x := range keys {
		k[x] = true
	}
	v.Keys = k
	return v
}

// JSONOutside lists every key, string or number of one JSON document
// outside the vocabulary.
func (v Vocab) JSONOutside(raw []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return []string{"not JSON"}
	}
	var out []string
	var walk func(key string, x any)
	walk = func(key string, x any) {
		switch y := x.(type) {
		case map[string]any:
			for k, val := range y {
				if !v.Keys[k] {
					out = append(out, fmt.Sprintf("key %q", k))
				}
				walk(k, val)
			}
		case []any:
			for _, val := range y {
				walk(key, val)
			}
		case string:
			if !v.Allowed(y) {
				out = append(out, fmt.Sprintf("string %q (key %s)", y, key))
			}
		case json.Number:
			if !numericKeys[key] || (key == "result" && y != "0" && y != "1") {
				out = append(out, fmt.Sprintf("number %s (key %s)", y, key))
			}
		}
	}
	walk("", x)
	return out
}

// CheckJSON fails on anything of a JSON document outside the vocabulary.
func (v Vocab) CheckJSON(t testing.TB, raw []byte) {
	t.Helper()
	for _, o := range v.JSONOutside(raw) {
		t.Errorf("%s outside the closed list in %s", o, raw)
	}
}

var wordRe = regexp.MustCompile(`[A-Za-z0-9_]+`)

// maxTextNumber bounds a number in text output: counts (answers of a
// plan, security level, protocol), never a size.
const maxTextNumber = 64

// TextOutside lists every word of text that is not a word of the
// vocabulary's strings, an id of its patterns, or a small count.
func (v Vocab) TextOutside(text string) []string {
	words := map[string]bool{}
	for s := range v.Strings {
		for _, w := range wordRe.FindAllString(s, -1) {
			words[w] = true
		}
	}
	var out []string
	for _, w := range wordRe.FindAllString(text, -1) {
		if words[w] || v.Allowed(w) {
			continue
		}
		if n, err := strconv.Atoi(w); err == nil && n >= 0 && n <= maxTextNumber {
			continue
		}
		out = append(out, w)
	}
	return out
}

// CheckText fails on any word of text outside the vocabulary.
func (v Vocab) CheckText(t testing.TB, text string) {
	t.Helper()
	for _, w := range v.TextOutside(text) {
		t.Errorf("word %q outside the closed list in:\n%s", w, text)
	}
}

// Allowed reports whether s is a value of the vocabulary.
func (v Vocab) Allowed(s string) bool {
	if v.Strings[s] {
		return true
	}
	for _, p := range v.Patterns {
		if p.MatchString(s) {
			return true
		}
	}
	return false
}
