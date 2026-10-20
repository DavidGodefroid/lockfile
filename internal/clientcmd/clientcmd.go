// Package clientcmd is the agent's side of lockfile, shared by the CLI and
// the MCP server: it calls a running console and turns every answer into a
// value of the closed list of docs/security-model.md. Nothing the console sends is
// passed on as text: results are decoded into typed values, and every
// error becomes a registered refusal or a fixed message chosen here.
package clientcmd

import (
	"context"
	"errors"

	"github.com/DavidGodefroid/lockkit/client"
	"github.com/DavidGodefroid/lockkit/ipc"
	"github.com/DavidGodefroid/lockkit/mcpkit"
	"github.com/DavidGodefroid/lockkit/refusal"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
)

// Kind classifies an error for the CLI's exit code.
type Kind int

const (
	KindOther     Kind = iota // console error, transport
	KindNoConsole             // no console serves the profile
	KindRefused               // a refusal (E_* code), or an unknown plan
	KindDenied                // the human denied, or did not answer in time
)

// Error is an error as the agent reads it: Msg is a closed code or a fixed
// text, Refusal the structured refusal when there is one.
type Error struct {
	Kind    Kind
	Msg     string
	Refusal *refusal.Info
}

func (e *Error) Error() string { return e.Msg }

// Tool is e for the MCP server: refusals carry their structured form.
func (e *Error) Tool() *mcpkit.ToolError { return &mcpkit.ToolError{Msg: e.Msg, Refusal: e.Refusal} }

// caller is a connection to one console.
type caller interface {
	Call(ctx context.Context, method string, params, result any) error
	Close() error
}

// dial connects to profile's console. Profiles live in the lockfile
// account, never in a project: the client dials from "/" so that no
// project directory changes the socket it looks for.
var dial = func(profile string) (caller, error) {
	return client.Dial(product.P, "/", profile)
}

// StartCommand is the command that starts profile's console.
func StartCommand(profile string) string { return client.StartCommand(product.P, profile, "") }

// unknownProfile is the refusal for a profile no running console serves;
// the hint names the start command of the profile the agent passed.
func unknownProfile(profile string) *Error {
	c := product.Code(answers.CodeUnknownProfile)
	info := refusal.Info{Code: c, Retry: c.Retry(),
		Hint: "use a profile listed by lockfile sources, or ask the user to run, in the lockfile account: " + StartCommand(profile)}
	return &Error{Kind: KindNoConsole, Msg: string(answers.CodeUnknownProfile), Refusal: &info}
}

// closed turns any error of a call into an *Error built from the closed
// list. The console's own text is never kept: its code picks the message.
func closed(profile string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, client.ErrNoConsole) {
		return unknownProfile(profile)
	}
	if errors.Is(err, client.ErrConsoleClosed) {
		return &Error{Kind: KindOther, Msg: rpc.MsgClosed}
	}
	var re *ipc.RPCError
	if !errors.As(err, &re) {
		return &Error{Kind: KindOther, Msg: rpc.MsgInternal}
	}
	switch re.Code {
	case ipc.CodeRefused:
		return refusedError(re)
	case ipc.CodeDenied:
		return &Error{Kind: KindDenied, Msg: string(answers.CodeApprovalDenied)}
	case ipc.CodeTimeout:
		return &Error{Kind: KindDenied, Msg: string(answers.CodeApprovalTimeout)}
	case ipc.CodeNoSuchPlan:
		return &Error{Kind: KindRefused, Msg: rpc.MsgNoSuchPlan}
	case ipc.CodeConnLost:
		return &Error{Kind: KindOther, Msg: rpc.MsgConnLost}
	case ipc.CodePolicyPending:
		return &Error{Kind: KindOther, Msg: rpc.MsgPending}
	case ipc.CodeInvalidParams:
		return &Error{Kind: KindOther, Msg: rpc.MsgParams}
	}
	return &Error{Kind: KindOther, Msg: rpc.MsgInternal}
}

// refusedError keeps a refusal only when its message is a closed code and
// its data that code's registered refusal; the hint is the registered one.
func refusedError(re *ipc.RPCError) error {
	c := answers.Code(re.Message)
	if !c.Valid() || !product.Mapped(c) || re.Data == nil || re.Data.Code != product.Code(c) {
		return &Error{Kind: KindOther, Msg: rpc.MsgInternal}
	}
	rc := product.Code(c)
	return &Error{Kind: KindRefused, Msg: string(c), Refusal: &refusal.Info{Code: rc, Retry: rc.Retry(), Hint: rc.Hint()}}
}

func call(ctx context.Context, profile, method string, params, result any) error {
	c, err := dial(profile)
	if err != nil {
		return closed(profile, err)
	}
	defer c.Close()
	return closed(profile, c.Call(ctx, method, params, result))
}

// errBadResult is a successful result with a value outside its shape: it
// is not forwarded.
func errBadResult() error { return &Error{Kind: KindOther, Msg: rpc.MsgInternal} }

// Plugins lists the plugins profile's console may run.
func Plugins(ctx context.Context, profile string) ([]rpc.PluginInfo, error) {
	var out []rpc.PluginInfo
	if err := call(ctx, profile, rpc.MethodPlugins, nil, &out); err != nil {
		return nil, err
	}
	for _, p := range out {
		if !validPluginInfo(p) {
			return nil, errBadResult()
		}
	}
	return mcpkit.NonNil(out), nil
}

// planOK checks a plan result against the request it answers.
func planOK(p rpc.PlanParams, out rpc.PlanResult) bool {
	return validPlanID(out.PlanID) && out.Answers == len(p.Plugins)
}

// Plan asks the human to approve a plan.
func Plan(ctx context.Context, profile string, p rpc.PlanParams) (rpc.PlanResult, error) {
	var out rpc.PlanResult
	if err := call(ctx, profile, rpc.MethodPlan, p, &out); err != nil {
		return rpc.PlanResult{}, err
	}
	if !planOK(p, out) {
		return rpc.PlanResult{}, errBadResult()
	}
	return out, nil
}

// PlanAndRun plans, then runs the plan once approved, on one connection.
func PlanAndRun(ctx context.Context, profile string, p rpc.PlanParams) (answers.Result, error) {
	c, err := dial(profile)
	if err != nil {
		return answers.Result{}, closed(profile, err)
	}
	defer c.Close()
	var plan rpc.PlanResult
	if err := c.Call(ctx, rpc.MethodPlan, p, &plan); err != nil {
		return answers.Result{}, closed(profile, err)
	}
	if !planOK(p, plan) {
		return answers.Result{}, errBadResult()
	}
	var out answers.Result
	if err := c.Call(ctx, rpc.MethodRun, rpc.RunParams{PlanID: plan.PlanID}, &out); err != nil {
		return answers.Result{}, closed(profile, err)
	}
	if !validResult(out) || len(out.Answers) != len(p.Plugins) {
		return answers.Result{}, errBadResult()
	}
	return result(out), nil
}

// Run runs an approved plan.
func Run(ctx context.Context, profile, planID string) (answers.Result, error) {
	var out answers.Result
	if err := call(ctx, profile, rpc.MethodRun, rpc.RunParams{PlanID: planID}, &out); err != nil {
		return answers.Result{}, err
	}
	if !validResult(out) {
		return answers.Result{}, errBadResult()
	}
	return result(out), nil
}

// Answers returns the answers already given on a handle.
func Answers(ctx context.Context, profile, handle string) (answers.Result, error) {
	var out answers.Result
	if err := call(ctx, profile, rpc.MethodAnswers, rpc.HandleParams{Handle: handle}, &out); err != nil {
		return answers.Result{}, err
	}
	if !validResult(out) || out.Handle != handle {
		return answers.Result{}, errBadResult()
	}
	return result(out), nil
}

// Release deletes a fetched file now.
func Release(ctx context.Context, profile, handle string) (rpc.Released, error) {
	var out rpc.Released
	err := call(ctx, profile, rpc.MethodRelease, rpc.HandleParams{Handle: handle}, &out)
	return out, err
}

// ReleasedText is a release's outcome as text.
func ReleasedText(handle string, r rpc.Released) string {
	if r.Released {
		return "released " + handle
	}
	return "not released " + handle
}

// result keeps only closed values: an answer is 1, 0 or null with
// plugin_error, a warning is the unofficial marker.
func result(r answers.Result) answers.Result {
	out := answers.Result{Handle: r.Handle, Answers: make([]answers.Answer, 0, len(r.Answers))}
	for _, a := range r.Answers {
		b := answers.Answer{Plugin: a.Plugin, Version: a.Version, Official: a.Official}
		if a.Result != nil && (*a.Result == 0 || *a.Result == 1) && a.Error == "" {
			v := *a.Result
			b.Result = &v
		} else {
			b.Error = string(answers.CodePluginError)
		}
		if !a.Official {
			b.Warning = answers.WarningUnofficial
		}
		out.Answers = append(out.Answers, b)
	}
	return out
}
