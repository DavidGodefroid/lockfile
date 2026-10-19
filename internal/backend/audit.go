package backend

import (
	"context"

	"github.com/DavidGodefroid/lockkit/audit"
	"github.com/DavidGodefroid/lockkit/console"

	"github.com/DavidGodefroid/lockfile/internal/answers"
)

// Record is lockfile's audit record: lockkit's base fields and the plan's.
// The audit file is the lockfile account's (0600): the one place where
// file metadata (key, size, sha256, failure causes) is kept, design §10.
// Never file bytes; error text is sanitized first.
type Record struct {
	audit.Base
	PlanID       string   `json:"plan_id,omitempty"`
	Handle       string   `json:"handle,omitempty"`
	Key          string   `json:"key,omitempty"`
	Plugins      []string `json:"plugins,omitempty"`
	Size         int64    `json:"size,omitempty"`
	SHA256       string   `json:"sha256,omitempty"`
	FetchCause   string   `json:"fetch_cause,omitempty"`
	Plugin       string   `json:"plugin,omitempty"`
	Version      string   `json:"version,omitempty"`
	BinarySHA256 string   `json:"binary_sha256,omitempty"`
	Result       *int     `json:"result,omitempty"`
	Cause        string   `json:"cause,omitempty"`
	ConsoleError string   `json:"console_error,omitempty"`
	ExitCode     int      `json:"exit_code,omitempty"`
	Signal       string   `json:"signal,omitempty"`
	StderrBytes  int64    `json:"stderr_bytes,omitempty"`
	DurationMS   int64    `json:"duration_ms,omitempty"`
}

// stamp fills the base fields of a record of the request behind ctx.
func stamp(ctx context.Context, profile, event, decision string) audit.Base {
	b := audit.Base{Event: event, Profile: profile, Decision: decision}
	console.StampPeer(ctx, &b)
	return b
}

// auditRun writes the records of one run: the fetch, then one per plugin.
func (b *Backend) auditRun(ctx context.Context, env console.Env, p *plan, entries []answers.AuditEntry, refusal string) {
	san := env.Sanitizer()
	decision := "answered"
	if refusal != "" {
		decision = "refused"
	}
	head := Record{Base: stamp(ctx, b.o.Profile, audit.EventRun, decision), PlanID: p.id, Handle: string(p.handle), Key: p.key, Plugins: p.plugins}
	head.Code = refusal
	for _, e := range entries {
		if e.Fetch != nil {
			head.Size, head.SHA256 = e.Fetch.Size, e.Fetch.SHA256
			head.FetchCause = san.Clean(e.Fetch.Cause)
		}
	}
	env.Audit(head)
	for _, e := range entries {
		if e.Fetch != nil {
			continue
		}
		r := Record{Base: stamp(ctx, b.o.Profile, audit.EventRun, "plugin"), PlanID: p.id, Plugin: e.Plugin, Version: e.Version,
			BinarySHA256: e.BinarySHA256, Result: e.Result, Cause: string(e.Cause), ConsoleError: san.Clean(e.ConsoleError),
			ExitCode: e.Report.ExitCode, Signal: e.Report.Signal, StderrBytes: e.Report.StderrBytes, DurationMS: e.Report.Duration.Milliseconds()}
		env.Audit(r)
	}
}
