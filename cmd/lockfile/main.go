// Command lockfile lets an AI agent ask yes/no questions about a file it
// never reads: a separate account fetches the file and runs signed plugins
// on it in a sandbox, and the agent gets 1 or 0 per plugin.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/sandbox"
)

// env is what a command reads and writes; tests replace it.
type env struct {
	stdin          io.Reader
	stdout, stderr io.Writer
}

// command is one subcommand: who runs it, its one-line help, its body.
type command struct {
	human bool // run by the human (lockfile account or install), not the agent
	help  string
	run   func(e env, args []string) int
}

// commands is filled by the files of this package, one per command group.
var commands = map[string]command{}

func main() {
	// A plugin launch re-executes this binary as the sandbox trampoline: it
	// must take over before anything else runs.
	sandbox.Main()
	os.Exit(run(env{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}, os.Args[1:]))
}

func run(e env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(e.stderr, usage())
		return product.ExitUsage
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(e.stdout, usage())
		return product.ExitOK
	case "version", "--version":
		fmt.Fprintln(e.stdout, "lockfile", product.Version)
		return product.ExitOK
	case "install", "update":
		return refuseHostMode(e, args[0])
	}
	c, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(e.stderr, "lockfile: unknown command (lockfile help lists them)\n\n%s", usage())
		return product.ExitUsage
	}
	return c.run(e, args[1:])
}

// refuseHostMode prints hostModeRefusal(cmd) and returns ExitError.
func refuseHostMode(e env, cmd string) int {
	fmt.Fprintln(e.stderr, hostModeRefusal(cmd))
	return product.ExitError
}

// hostModeRefusal is printed for the commands of the former host install
// (install, update). lockfile runs in Docker: the launcher on the host
// (scripts/lockfile) runs every command in the image and updates itself and
// the image.
func hostModeRefusal(cmd string) string {
	return "lockfile " + cmd + ": the host install is gone: lockfile runs in Docker. " +
		"Install the launcher with scripts/install.sh; `lockfile update` on the host updates it and its image; " +
		"see docs/docker.md"
}

func usage() string {
	var human, agent []string
	for name, c := range commands {
		if c.help == "" {
			continue // not for the help
		}
		line := fmt.Sprintf("  %-12s %s", name, c.help)
		if c.human {
			human = append(human, line)
		} else {
			agent = append(agent, line)
		}
	}
	sort.Strings(human)
	sort.Strings(agent)
	var b strings.Builder
	b.WriteString(product.P.Tagline + "\n\nUsage: lockfile <command> [options]\n")
	if len(agent) > 0 {
		b.WriteString("\nAgent commands (--json for JSON output):\n" + strings.Join(agent, "\n") + "\n")
	}
	if len(human) > 0 {
		b.WriteString("\nHuman commands:\n" + strings.Join(human, "\n") + "\n")
	}
	b.WriteString("\n  version      print the version\n")
	return b.String()
}
