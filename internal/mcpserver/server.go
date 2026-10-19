// Package mcpserver is lockfile's MCP server: one tool per agent command,
// each a thin adapter over clientcmd. Its outputs are the CLI's: the closed
// values of design §7b, as text and as structured content.
package mcpserver

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DavidGodefroid/lockkit/mcpkit"
	"github.com/DavidGodefroid/lockkit/refusal"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/clientcmd"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
)

//go:embed instructions.md
var Instructions string

// Tool names.
const (
	ToolStatus  = "lockfile_status"
	ToolSources = "lockfile_sources"
	ToolPlugins = "lockfile_plugins"
	ToolPlan    = "lockfile_plan"
	ToolRun     = "lockfile_run"
	ToolAnswers = "lockfile_answers"
	ToolRelease = "lockfile_release"
)

// Names lists every tool.
func Names() []string {
	return []string{ToolStatus, ToolSources, ToolPlugins, ToolPlan, ToolRun, ToolAnswers, ToolRelease}
}

// RunningMessage is the one progress text of a run: constant, so that the
// notifications say nothing the response time does not (design §7b).
const RunningMessage = "running in the lockfile console"

// Inputs.

type ProfileIn struct {
	Profile string `json:"profile,omitempty" jsonschema:"profile of the console; may be left out when exactly one console runs"`
}

type NoIn struct{}

type PlanIn struct {
	Profile string   `json:"profile,omitempty" jsonschema:"profile of the console; may be left out when exactly one console runs"`
	Key     string   `json:"key,omitempty" jsonschema:"exact object key, as the user gave it (or handle, not both)"`
	Handle  string   `json:"handle,omitempty" jsonschema:"handle of a file already fetched, for follow-up questions (or key, not both)"`
	Plugins []string `json:"plugins" jsonschema:"plugins to run, from lockfile_plugins"`
	Intent  string   `json:"intent,omitempty" jsonschema:"one line telling the user why, at most 200 bytes"`
}

type RunIn struct {
	Profile string `json:"profile,omitempty" jsonschema:"profile of the console; may be left out when exactly one console runs"`
	PlanID  string `json:"plan_id" jsonschema:"plan id returned by lockfile_plan"`
}

type HandleIn struct {
	Profile string `json:"profile,omitempty" jsonschema:"profile of the console; may be left out when exactly one console runs"`
	Handle  string `json:"handle" jsonschema:"handle of a result"`
}

// Outputs. Each holds the refusal when the call was refused.

type StatusOut struct {
	Consoles []clientcmd.StatusEntry `json:"consoles"`
}

type SourcesOut struct {
	Sources []rpc.Source `json:"sources"`
}

type PluginsOut struct {
	Plugins []rpc.PluginInfo `json:"plugins"`
	Refusal *refusal.Info    `json:"refusal,omitempty"`
}

type PlanOut struct {
	PlanID  string        `json:"plan_id,omitempty"`
	Answers int           `json:"answers,omitempty"`
	Refusal *refusal.Info `json:"refusal,omitempty"`
}

// ResultOut is answers.Result plus the refusal.
type ResultOut struct {
	Handle  string           `json:"handle,omitempty"`
	Answers []answers.Answer `json:"answers"`
	Refusal *refusal.Info    `json:"refusal,omitempty"`
}

type ReleaseOut struct {
	Released bool          `json:"released"`
	Refusal  *refusal.Info `json:"refusal,omitempty"`
}

// New returns the server; pinned pins it to one profile (`lockfile mcp
// --profile P`).
func New(pinned string) *mcp.Server {
	s := mcpkit.New(product.P, product.Version, Instructions)
	resolve := func(arg string) (string, error) { return clientcmd.Resolve(pinned, arg) }

	mcp.AddTool(s, &mcp.Tool{Name: ToolStatus, Description: "State of the running lockfile consoles (or of one profile's): production, auto mode, security level, plugin protocol, sandbox."},
		func(ctx context.Context, req *mcp.CallToolRequest, in ProfileIn) (*mcp.CallToolResult, StatusOut, error) {
			p, err := clientcmd.StatusProfile(pinned, in.Profile)
			if err != nil {
				return toolErr[StatusOut](err)
			}
			out, _, err := clientcmd.Statuses(ctx, p)
			if err != nil {
				return toolErr[StatusOut](err)
			}
			return mcpkit.Text(strings.Join(clientcmd.StatusLines(out), "\n")), StatusOut{Consoles: mcpkit.NonNil(out)}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: ToolSources, Description: "Profiles served by a running lockfile console, with their production flag. There is no way to list keys: ask the user for the exact key."},
		func(ctx context.Context, req *mcp.CallToolRequest, in NoIn) (*mcp.CallToolResult, SourcesOut, error) {
			src, err := clientcmd.Sources(ctx)
			if err != nil {
				return toolErr[SourcesOut](err)
			}
			var lines []string
			for _, x := range src {
				l := x.Profile
				switch {
				case x.Busy:
					l += " (busy: serving another request)"
				case x.Production:
					l += " (production)"
				}
				lines = append(lines, l)
			}
			if len(lines) == 0 {
				lines = []string{clientcmd.NoConsoleText}
			}
			return mcpkit.Text(strings.Join(lines, "\n")), SourcesOut{Sources: mcpkit.NonNil(src)}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: ToolPlugins, Description: "Plugins the console may run on a file: each answers one fixed yes/no question, with the meaning of 1 and of 0."},
		func(ctx context.Context, req *mcp.CallToolRequest, in ProfileIn) (*mcp.CallToolResult, PluginsOut, error) {
			profile, err := resolve(in.Profile)
			if err != nil {
				return refusedOr(err, PluginsOut{Plugins: []rpc.PluginInfo{}})
			}
			list, err := clientcmd.Plugins(ctx, profile)
			if err != nil {
				return refusedOr(err, PluginsOut{Plugins: []rpc.PluginInfo{}})
			}
			lines := []string{mcpkit.Untrusted("plugin descriptions", "the plugin manifests")}
			for _, p := range list {
				head := fmt.Sprintf("%s@%s (%s)", p.Plugin, p.Version, p.Marketplace)
				if !p.Official {
					head += " UNOFFICIAL MARKETPLACE"
				}
				lines = append(lines, head, "  ? "+p.Question, "  1 = "+p.One, "  0 = "+p.Zero)
			}
			return mcpkit.Text(strings.Join(lines, "\n")), PluginsOut{Plugins: list}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: ToolPlan, Description: "Ask the user to approve questions about one file: the exact key (or the handle of a file already fetched) and the plugins to run. Waits for the user's decision; returns a plan id for lockfile_run. Never call it in a loop."},
		func(ctx context.Context, req *mcp.CallToolRequest, in PlanIn) (*mcp.CallToolResult, PlanOut, error) {
			profile, err := resolve(in.Profile)
			if err != nil {
				return refusedOr(err, PlanOut{})
			}
			stop := mcpkit.Progress(ctx, req, 0, mcpkit.WaitingMessage(product.P))
			plan, err := clientcmd.Plan(ctx, profile, rpc.PlanParams{Key: in.Key, Handle: in.Handle, Plugins: in.Plugins, Intent: in.Intent})
			stop()
			if err != nil {
				return refusedOr(err, PlanOut{})
			}
			return mcpkit.Text(fmt.Sprintf("plan %s approved: it reveals at most %d answers; run it once with lockfile_run", plan.PlanID, plan.Answers)),
				PlanOut{PlanID: plan.PlanID, Answers: plan.Answers}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: ToolRun, Description: "Run an approved plan once: 1, 0 or no answer (null, plugin_error: not a 0) per plugin, and the handle for follow-ups."},
		func(ctx context.Context, req *mcp.CallToolRequest, in RunIn) (*mcp.CallToolResult, ResultOut, error) {
			profile, err := resolve(in.Profile)
			if err != nil {
				return refusedOr(err, ResultOut{Answers: []answers.Answer{}})
			}
			stop := mcpkit.Progress(ctx, req, 0, func(time.Duration) string { return RunningMessage })
			res, err := clientcmd.Run(ctx, profile, in.PlanID)
			stop()
			return resultOut(res, err)
		})

	mcp.AddTool(s, &mcp.Tool{Name: ToolAnswers, Description: "Answers already given on a handle (no new run, no approval)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in HandleIn) (*mcp.CallToolResult, ResultOut, error) {
			profile, err := resolve(in.Profile)
			if err != nil {
				return refusedOr(err, ResultOut{Answers: []answers.Answer{}})
			}
			return resultOut(clientcmd.Answers(ctx, profile, in.Handle))
		})

	mcp.AddTool(s, &mcp.Tool{Name: ToolRelease, Description: "Delete a fetched file now (it is deleted anyway when the handle expires)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in HandleIn) (*mcp.CallToolResult, ReleaseOut, error) {
			profile, err := resolve(in.Profile)
			if err != nil {
				return refusedOr(err, ReleaseOut{})
			}
			r, err := clientcmd.Release(ctx, profile, in.Handle)
			if err != nil {
				return refusedOr(err, ReleaseOut{})
			}
			return mcpkit.Text("released " + in.Handle), ReleaseOut{Released: r.Released}, nil
		})
	return s
}

// resultOut is a result, or its refusal.
func resultOut(res answers.Result, err error) (*mcp.CallToolResult, ResultOut, error) {
	if err != nil {
		return refusedOr(err, ResultOut{Answers: []answers.Answer{}})
	}
	lines := []string{mcpkit.Untrusted("answers", "lockfile plugins"), "handle " + res.Handle}
	for _, a := range res.Answers {
		v := "null (plugin_error: no answer, not a 0)"
		if a.Result != nil {
			v = fmt.Sprint(*a.Result)
		}
		l := fmt.Sprintf("%s@%s: %s", a.Plugin, a.Version, v)
		if !a.Official {
			l += " (unofficial marketplace)"
		}
		lines = append(lines, l)
	}
	return mcpkit.Text(strings.Join(lines, "\n")), ResultOut{Handle: res.Handle, Answers: mcpkit.NonNil(res.Answers)}, nil
}

// refusedOr returns a refusal as a result with its structured form in out,
// any other error as a tool error carrying its closed text.
func refusedOr[T any](err error, out T) (*mcp.CallToolResult, T, error) {
	var tool error = err
	if ce, ok := err.(*clientcmd.Error); ok {
		tool = ce.Tool()
	}
	if res, info, ok := mcpkit.Refused(tool); ok {
		setRefusal(&out, info)
		return res, out, nil
	}
	return toolErr[T](err)
}

func setRefusal(out any, info *refusal.Info) {
	switch o := out.(type) {
	case *PluginsOut:
		o.Refusal = info
	case *PlanOut:
		o.Refusal = info
	case *ResultOut:
		o.Refusal = info
	case *ReleaseOut:
		o.Refusal = info
	}
}

// toolErr is an error the model reads: a closed text, never another one.
func toolErr[T any](err error) (*mcp.CallToolResult, T, error) {
	var zero T
	if ce, ok := err.(*clientcmd.Error); ok {
		return nil, zero, ce
	}
	return nil, zero, &clientcmd.Error{Msg: rpc.MsgInternal}
}

// Serve runs the server on the agent's stdin and stdout.
func Serve(ctx context.Context, pinned string, r io.Reader, w io.Writer) error {
	return mcpkit.Serve(ctx, New(pinned), r, w)
}
