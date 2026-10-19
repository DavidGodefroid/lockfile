package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/DavidGodefroid/lockkit/refusal"
	"github.com/DavidGodefroid/lockkit/render"
	"github.com/DavidGodefroid/lockkit/sanitize"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/clientcmd"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
)

func init() {
	commands["status"] = command{help: "state of the running consoles", run: runStatus}
	commands["sources"] = command{help: "profiles served by a running console", run: runSources}
	commands["plugins"] = command{help: "plugins a console may run: question, meaning of 1 and 0", run: runPlugins}
	commands["plan"] = command{help: "ask the human to approve questions about a file (--run: answer at once)", run: runPlan}
	commands["run"] = command{help: "run an approved plan: 1, 0 or no answer per plugin", run: runRun}
	commands["answers"] = command{help: "answers already given on a handle", run: runAnswers}
	commands["release"] = command{help: "delete a fetched file now", run: runRelease}
}

// outCaps bounds what a command prints.
var outCaps = render.Caps{MaxCellChars: 512, MaxOutputBytes: 64 << 10}

// agentFlags are the flags every agent command takes.
type agentFlags struct {
	fs      *flag.FlagSet
	profile *string
	json    *bool
}

func newAgentFlags(name string) agentFlags {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return agentFlags{fs: fs, profile: fs.String("profile", "", "profile of the console"), json: fs.Bool("json", false, "JSON output")}
}

// parse parses args, flags and positional arguments in any order.
func (a agentFlags) parse(args []string) ([]string, error) {
	var pos []string
	for {
		if err := a.fs.Parse(args); err != nil {
			return nil, err
		}
		if a.fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, a.fs.Arg(0))
		args = a.fs.Args()[1:]
	}
}

func usageErr(e env, line string) int {
	fmt.Fprintln(e.stderr, "usage: lockfile "+line)
	return product.ExitUsage
}

func agentContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// emit prints v: JSON, or the human lines of text.
func emit(e env, asJSON bool, v any, text func() []string) int {
	if asJSON {
		if err := render.Value(e.stdout, v, outCaps); err != nil {
			fmt.Fprintln(e.stderr, "lockfile: output too large")
			return product.ExitError
		}
		return product.ExitOK
	}
	for _, l := range text() {
		fmt.Fprintln(e.stdout, sanitize.Terminal(l, false))
	}
	return product.ExitOK
}

// fail prints err and returns its exit code. Its text is a closed code or
// a fixed message (clientcmd); JSON output goes to stdout.
func fail(e env, asJSON bool, err error) int {
	var ce *clientcmd.Error
	if !errors.As(err, &ce) {
		ce = &clientcmd.Error{Kind: clientcmd.KindOther, Msg: rpc.MsgInternal}
		if errors.Is(err, context.Canceled) {
			ce.Msg = "interrupted"
		}
	}
	if asJSON {
		_ = render.Value(e.stdout, struct {
			Error   string        `json:"error"`
			Refusal *refusal.Info `json:"refusal,omitempty"`
		}{ce.Msg, ce.Refusal}, outCaps)
	} else {
		fmt.Fprintln(e.stderr, "lockfile: "+sanitize.Terminal(ce.Msg, false))
		if r := ce.Refusal; r != nil {
			fmt.Fprintf(e.stderr, "  refusal %s, retry: %s\n  %s\n", r.Code, r.Retry, sanitize.Terminal(r.Hint, false))
		}
	}
	switch ce.Kind {
	case clientcmd.KindRefused:
		return product.ExitRefused
	case clientcmd.KindDenied:
		return product.ExitDenied
	}
	return product.ExitError
}

func runStatus(e env, args []string) int {
	f := newAgentFlags("status")
	if pos, err := f.parse(args); err != nil || len(pos) != 0 {
		return usageErr(e, "status [--profile P] [--json]")
	}
	ctx, stop := agentContext()
	defer stop()
	out, ok, err := clientcmd.Statuses(ctx, *f.profile)
	if err != nil {
		return fail(e, *f.json, err)
	}
	if c := emit(e, *f.json, out, func() []string { return clientcmd.StatusLines(out) }); c != product.ExitOK || !ok {
		return product.ExitError
	}
	return product.ExitOK
}

func runSources(e env, args []string) int {
	f := newAgentFlags("sources")
	if pos, err := f.parse(args); err != nil || len(pos) != 0 || *f.profile != "" {
		return usageErr(e, "sources [--json]")
	}
	ctx, stop := agentContext()
	defer stop()
	src, err := clientcmd.Sources(ctx)
	if err != nil {
		return fail(e, *f.json, err)
	}
	return emit(e, *f.json, src, func() []string {
		if len(src) == 0 {
			return []string{"no lockfile console is running"}
		}
		var lines []string
		for _, s := range src {
			switch {
			case s.Busy:
				lines = append(lines, s.Profile+"  (busy: waiting for the human)")
			case s.Production:
				lines = append(lines, s.Profile+"  PRODUCTION")
			default:
				lines = append(lines, s.Profile)
			}
		}
		return lines
	})
}

// resolve picks the profile of a command.
func resolve(e env, f agentFlags) (string, int, bool) {
	p, err := clientcmd.Resolve("", *f.profile)
	if err != nil {
		return "", fail(e, *f.json, err), false
	}
	return p, 0, true
}

func runPlugins(e env, args []string) int {
	f := newAgentFlags("plugins")
	if pos, err := f.parse(args); err != nil || len(pos) != 0 {
		return usageErr(e, "plugins [--profile P] [--json]")
	}
	profile, code, ok := resolve(e, f)
	if !ok {
		return code
	}
	ctx, stop := agentContext()
	defer stop()
	list, err := clientcmd.Plugins(ctx, profile)
	if err != nil {
		return fail(e, *f.json, err)
	}
	return emit(e, *f.json, list, func() []string {
		if len(list) == 0 {
			return []string{"no plugin is installed for profile " + profile + ": ask the user to install one"}
		}
		var lines []string
		for _, p := range list {
			head := fmt.Sprintf("%s@%s  (%s)", p.Plugin, p.Version, p.Marketplace)
			if !p.Official {
				head += "  UNOFFICIAL MARKETPLACE"
			}
			lines = append(lines, head, "  ? "+p.Question, "  1 = "+p.One, "  0 = "+p.Zero)
		}
		return lines
	})
}

// multi is a repeatable string flag.
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func runPlan(e env, args []string) int {
	const use = "plan [--profile P] (--key KEY | --handle HANDLE) --plugin NAME... [--intent TEXT] [--run] [--json]"
	f := newAgentFlags("plan")
	key := f.fs.String("key", "", "exact object key")
	handle := f.fs.String("handle", "", "handle of a file already fetched")
	intent := f.fs.String("intent", "", "one line shown to the human")
	andRun := f.fs.Bool("run", false, "run the plan once approved")
	var plugins multi
	f.fs.Var(&plugins, "plugin", "plugin to run (repeat)")
	if pos, err := f.parse(args); err != nil || len(pos) != 0 || (*key == "") == (*handle == "") || len(plugins) == 0 {
		return usageErr(e, use)
	}
	profile, code, ok := resolve(e, f)
	if !ok {
		return code
	}
	ctx, stop := agentContext()
	defer stop()
	p := rpc.PlanParams{Key: *key, Handle: *handle, Plugins: plugins, Intent: *intent}
	if *andRun {
		res, err := clientcmd.PlanAndRun(ctx, profile, p)
		if err != nil {
			return fail(e, *f.json, err)
		}
		return emit(e, *f.json, res, func() []string { return resultLines(res) })
	}
	plan, err := clientcmd.Plan(ctx, profile, p)
	if err != nil {
		return fail(e, *f.json, err)
	}
	return emit(e, *f.json, plan, func() []string {
		return []string{
			fmt.Sprintf("plan %s approved: it reveals at most %d answers", plan.PlanID, plan.Answers),
			"run it once: lockfile run " + plan.PlanID + " --profile " + profile,
		}
	})
}

// resultLines is a result as text: one line per answer.
func resultLines(r answers.Result) []string {
	lines := []string{"handle " + r.Handle}
	for _, a := range r.Answers {
		v := "no answer (" + string(answers.CodePluginError) + ")"
		if a.Result != nil {
			v = fmt.Sprint(*a.Result)
		}
		l := fmt.Sprintf("%s@%s: %s", a.Plugin, a.Version, v)
		if !a.Official {
			l += "  (unofficial marketplace)"
		}
		lines = append(lines, l)
	}
	return lines
}

// runOne is the body of the commands that take one id and give a result.
func runOne(e env, args []string, name, use string, do func(ctx context.Context, profile, id string) (any, []string, error)) int {
	f := newAgentFlags(name)
	pos, err := f.parse(args)
	if err != nil || len(pos) != 1 || pos[0] == "" {
		return usageErr(e, use)
	}
	profile, code, ok := resolve(e, f)
	if !ok {
		return code
	}
	ctx, stop := agentContext()
	defer stop()
	v, lines, err := do(ctx, profile, pos[0])
	if err != nil {
		return fail(e, *f.json, err)
	}
	return emit(e, *f.json, v, func() []string { return lines })
}

func runRun(e env, args []string) int {
	return runOne(e, args, "run", "run PLAN_ID [--profile P] [--json]", func(ctx context.Context, profile, id string) (any, []string, error) {
		r, err := clientcmd.Run(ctx, profile, id)
		return r, resultLines(r), err
	})
}

func runAnswers(e env, args []string) int {
	return runOne(e, args, "answers", "answers HANDLE [--profile P] [--json]", func(ctx context.Context, profile, id string) (any, []string, error) {
		r, err := clientcmd.Answers(ctx, profile, id)
		return r, resultLines(r), err
	})
}

func runRelease(e env, args []string) int {
	return runOne(e, args, "release", "release HANDLE [--profile P] [--json]", func(ctx context.Context, profile, id string) (any, []string, error) {
		r, err := clientcmd.Release(ctx, profile, id)
		return r, []string{"released " + id}, err
	})
}
