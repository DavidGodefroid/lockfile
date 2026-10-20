package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/term"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/separate"
)

func init() {
	commands["install"] = command{human: true, help: "one-time setup separating the console account from the agent (sudo, Linux)", run: runInstall}
}

// installHooks replace the binary, the system, the terminal and sudo in
// tests: the script is captured, never run.
type installHooks struct {
	executable string
	goos       string
	tty        bool
	root       func(script string) error
}

var testInstallHooks *installHooks

// lockfile's socket directory is listable by the clients group (2750):
// `lockfile sources` lists the running consoles from it.
const socketMode = "2750"

// runInstall is `lockfile install`: it prints the root commands of the
// separated setup and runs them with sudo once the human confirms (or
// --yes). Linux only in v0.2: the plugin sandbox needs Landlock.
func runInstall(e env, args []string) int {
	const use = "install [--client USER] [--user lockfile] [--group lockfile-clients] [--print] [--trust-binary] [--yes]"
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	o := separate.Options{SocketMode: socketMode}
	fs.StringVar(&o.Client, "client", "", "the account the agent runs as (default: you)")
	fs.StringVar(&o.Service, "user", product.P.Account, "the console account to create")
	fs.StringVar(&o.Group, "group", product.P.ClientsGroup, "the group allowed to reach the console")
	printOnly := fs.Bool("print", false, "print the commands, do not run them")
	trust := fs.Bool("trust-binary", false, "copy this binary even when another account than root can change it")
	yes := fs.Bool("yes", false, "run the commands without asking")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return usageErr(e, use)
	}
	h := installHooks{}
	if testInstallHooks != nil {
		h = *testInstallHooks
	}
	goos := h.goos
	if goos == "" {
		goos = runtime.GOOS
	}
	if goos != "linux" {
		fmt.Fprintln(e.stderr, "lockfile install: the console install is Linux-only in v0.2 (the plugin sandbox needs Landlock and seccomp); on this system lockfile is a client of a Linux console")
		return product.ExitError
	}
	separate.Fill(product.P, &o)
	if err := separate.Check(o); err != nil {
		fmt.Fprintln(e.stderr, "lockfile install:", err)
		return product.ExitUsage
	}
	bin := h.executable
	if bin == "" {
		var err error
		if bin, err = os.Executable(); err == nil {
			bin, err = filepath.EvalSymlinks(bin)
		}
		if err != nil {
			fmt.Fprintln(e.stderr, "lockfile install:", err)
			return product.ExitError
		}
	}
	pin := ""
	if *trust {
		var err error
		if pin, err = separate.FileSum(bin); err != nil {
			fmt.Fprintln(e.stderr, "lockfile install:", err)
			return product.ExitError
		}
		fmt.Fprintf(e.stdout, "--trust-binary: copying %s (sha256 %s, checked again before it is installed)\n\n", bin, pin)
	} else {
		// A root-owned binary is pinned too: its directory may be the
		// agent's, which could swap it between this check and sudo.
		sum, msg, err := separate.SourceSum(product.P, bin)
		if err != nil {
			fmt.Fprintln(e.stderr, "lockfile install:", err)
			return product.ExitError
		}
		if msg != "" {
			fmt.Fprint(e.stderr, msg)
			return product.ExitError
		}
		pin = sum
	}
	script := separate.Script(product.P, goos, bin, o, pin)
	fmt.Fprintf(e.stdout, `lockfile install separates the console from the agent:
  - the console runs as %[1]q, in its own login session (use Wayland); it alone
    holds the source credentials, the plugins and the fetched files (home 0700);
  - the agent's account %[2]q joins group %[3]q, which may reach and list the
    consoles' sockets in /run/lockfile; the kernel checks every connection;
  - lockfile is copied to /usr/local/bin, owned by root, so that the agent cannot
    replace the binary the console runs.

These commands run as root:

    %[4]s
`, o.Service, o.Client, o.Group, strings.ReplaceAll(strings.TrimRight(script, "\n"), "\n", "\n    "))
	if *printOnly {
		return product.ExitOK
	}
	if !*yes {
		tty := h.tty || (testInstallHooks == nil && term.IsTerminal(int(os.Stdin.Fd())))
		if !tty {
			fmt.Fprintln(e.stderr, "lockfile install: confirm in a terminal, or run with --print or --yes")
			return product.ExitUsage
		}
		fmt.Fprint(e.stdout, "Run them now with sudo? [y/N] ")
		ans, _ := bufio.NewReader(e.stdin).ReadString('\n')
		if !strings.EqualFold(strings.TrimSpace(ans), "y") {
			fmt.Fprintln(e.stdout, "nothing done")
			return product.ExitOK
		}
	}
	run := h.root
	if run == nil {
		run = func(script string) error {
			cmd := exec.Command("sudo", "sh", "-c", script)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, e.stdout, e.stderr
			return cmd.Run()
		}
	}
	if err := run(script); err != nil {
		fmt.Fprintln(e.stderr, "lockfile install:", err)
		return product.ExitError
	}
	fmt.Fprintf(e.stdout, `
Done. Next:
  1. sudo passwd %[1]s            (a password for the console account)
  2. log out and in again as %[2]s (the new group applies at login)
  3. open a separate session as %[1]s (switch user, Wayland), then run there:
       lockfile profile add NAME --source s3|sftp|local …
       lockfile plugin install NAME@X.Y.Z
       lockfile console --profile NAME
  4. lockfile doctor              (from both accounts)
`, o.Service, o.Client)
	return product.ExitOK
}
