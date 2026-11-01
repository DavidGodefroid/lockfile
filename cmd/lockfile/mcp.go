package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/DavidGodefroid/lockkit/agentwire"
	"github.com/DavidGodefroid/lockkit/client"

	"github.com/DavidGodefroid/lockfile/internal/mcpserver"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/wiring"
)

func init() {
	commands["mcp"] = command{help: "MCP server on stdin/stdout (started by the agent)", run: runMCP}
	commands["init"] = command{human: true, help: "wire an agent to this project: init claude|codex|cursor|gemini (--user: for your account)", run: runInit}
}

func runMCP(e env, args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	const use = "mcp [--profile P] [--instance NAME]"
	profile := fs.String("profile", "", "pin the server to one profile")
	instance := fs.String(client.FlagInstance, "", "lock instance, one among several of this type")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return usageErr(e, use)
	}
	if !setInstance(e, *instance, use) {
		return product.ExitUsage
	}
	ctx, stop := agentContext()
	defer stop()
	if err := mcpserver.Serve(ctx, *profile, e.stdin, e.stdout); err != nil {
		fmt.Fprintln(e.stderr, "lockfile mcp: the MCP session failed")
		return product.ExitError
	}
	return product.ExitOK
}

// runInit is `lockfile init <agent>`, the launcher verb of lockkit's
// GUIDE.md §14: it writes the agent's files into the project of the
// current directory (or, with --user, the account's), never asks, and is
// idempotent.
func runInit(e env, args []string) int {
	const use = "init claude|codex|cursor|gemini [--user]"
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	user := fs.Bool("user", false, "wire the agent for the whole account instead of this project")
	var agents []string
	for {
		if err := fs.Parse(args); err != nil {
			return usageErr(e, use)
		}
		if fs.NArg() == 0 {
			break
		}
		agents = append(agents, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(agents) != 1 {
		return usageErr(e, use)
	}
	agent := agents[0]
	// lockkit's error quotes an unknown agent: refuse it here first.
	if !slices.Contains(agentwire.Agents(), agent) {
		fmt.Fprintln(e.stderr, "lockfile init: unknown agent (want one of "+strings.Join(agentwire.Agents(), ", ")+")")
		return product.ExitUsage
	}
	var res agentwire.Result
	var err error
	if *user {
		var env agentwire.Env
		if env, err = agentwire.SystemEnv(); err == nil {
			res, err = wiring.Wiring.InitUser(env, agent)
		}
	} else {
		cwd, cerr := os.Getwd()
		if cerr != nil {
			fmt.Fprintln(e.stderr, "lockfile init:", cerr)
			return product.ExitError
		}
		res, err = wiring.Wiring.Init(agentwire.ProjectRoot(product.P, cwd), agent)
	}
	for _, a := range res.Actions {
		line := fmt.Sprintf("  %-9s %s", a.Status, a.Path)
		if a.Detail != "" {
			line += "  (" + a.Detail + ")"
		}
		fmt.Fprintln(e.stdout, line)
	}
	if res.Notes != "" {
		fmt.Fprintln(e.stdout, strings.TrimRight(res.Notes, "\n"))
	}
	if err != nil {
		fmt.Fprintln(e.stderr, "lockfile init:", err)
		return product.ExitError
	}
	return product.ExitOK
}
