package answers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DavidGodefroid/lockfile/internal/source"
	"github.com/DavidGodefroid/lockfile/internal/workspace"
)

// DefaultQuantum is the step at which plan results are released.
const DefaultQuantum = 5 * time.Second

// Result is a plan's agent-visible outcome: the handle and the answers.
type Result struct {
	Handle  string   `json:"handle"`
	Answers []Answer `json:"answers"`
}

// Refusal is a plan that gave no answers. It holds a Code only: it is never
// built from an error's text.
type Refusal struct {
	Code Code `json:"refusal"`
}

func refuse(c Code) *Refusal {
	if !c.Valid() {
		panic(fmt.Sprintf("answers: refusal code %q is not in the closed list", c))
	}
	return &Refusal{Code: c}
}

// FetchAudit is what the audit keeps about the fetch of a plan.
type FetchAudit struct {
	Profile string
	Key     string
	Size    int64
	SHA256  string
	Cause   string
}

// Plan is a request: a fetch of Key from Profile, or (Handle set) a
// follow-up on an already fetched file, and the plugins to run.
type Plan struct {
	Profile string
	Key     string
	Handle  workspace.Handle
	Plugins []string
}

type Deps struct {
	Sources        map[string]source.Source
	MaxObjectBytes func(profile string) int64
	WS             *workspace.Manager
	Runner         *Runner
	Quantum        time.Duration
	Now            func() time.Time
}

// nextQuantum is the first multiple of q after start that is not before
// now, and at least start+q.
func nextQuantum(start, now time.Time, q time.Duration) time.Time {
	el := now.Sub(start)
	n := el / q
	if el%q != 0 || n == 0 {
		n++
	}
	return start.Add(n * q)
}

// Quantize blocks until the next multiple of q after start, so the time a
// result takes says nothing finer than q about the file. It returns early
// with ctx's error.
func Quantize(ctx context.Context, start time.Time, q time.Duration, now func() time.Time) error {
	if q <= 0 {
		q = DefaultQuantum
	}
	if now == nil {
		now = time.Now
	}
	wait := nextQuantum(start, now(), q).Sub(now())
	if wait <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ExecutePlan fetches (unless the plan names a handle), runs the plugins
// and releases the outcome at the next quantum. Every failure maps to a
// Code; causes go to the audit entries.
func ExecutePlan(ctx context.Context, p Plan, d Deps) (res Result, ref *Refusal, audit []AuditEntry) {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	start := now()
	defer func() {
		if err := Quantize(ctx, start, d.Quantum, now); err != nil && ref == nil {
			// Cancelled (console shutting down): the workspace goes with it.
			res, ref = Result{}, refuse(CodeHandleExpired)
		}
	}()

	max := d.Runner.MaxPerHandle
	if max <= 0 {
		max = DefaultMaxPerHandle
	}
	if len(p.Plugins) > max {
		return Result{}, refuse(CodeTooManyPlugins), nil
	}

	h := p.Handle
	if h == "" {
		src, ok := d.Sources[p.Profile]
		if !ok {
			return Result{}, refuse(CodeUnknownProfile), nil
		}
		fa := &FetchAudit{Profile: p.Profile, Key: p.Key}
		audit = append(audit, AuditEntry{Fetch: fa})
		var info workspace.Info
		var err error
		h, info, err = source.Fetch(ctx, src, p.Key, d.MaxObjectBytes(p.Profile), d.WS)
		if err != nil {
			var fe *source.FetchError
			if errors.As(err, &fe) {
				fa.Cause = fe.Cause.Error()
			} else {
				fa.Cause = err.Error()
			}
			if errors.Is(err, source.ErrKeyOutsidePrefix) {
				return Result{}, refuse(CodeKeyOutsidePrefix), audit
			}
			return Result{}, refuse(CodeFetchFailed), audit
		}
		fa.Size, fa.SHA256 = info.Size, info.SHA256
	}

	answers, entries, err := d.Runner.Answer(ctx, h, p.Plugins)
	audit = append(audit, entries...)
	switch {
	case errors.Is(err, ErrTooManyPlugins):
		return Result{}, refuse(CodeTooManyPlugins), audit
	case err != nil:
		// Expired, released, unknown handle, or cancelled run.
		return Result{}, refuse(CodeHandleExpired), audit
	}
	return Result{Handle: string(h), Answers: answers}, nil, audit
}
