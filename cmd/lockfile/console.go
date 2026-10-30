package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/DavidGodefroid/lockkit/config"
	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/hubclient"

	"github.com/DavidGodefroid/lockfile/internal/backend"
	"github.com/DavidGodefroid/lockfile/internal/product"
)

func init() {
	commands["console"] = command{human: true, help: "serve a profile to agents (run in the lockfile account) [--hub URL [--hub-plain-http] --hub-key-file F --instance N]", run: runConsole}
	commands["hub-key"] = command{human: true, help: "hub-key --instance NAME: print the instance key for lockhub to pin (lockfile account)", run: runHubKey}
}

// consoleHooks replace the real terminal, isolation and paths in tests.
type consoleHooks struct {
	io         console.IO
	isolation  *console.IsolationEnv
	harden     func() error
	stateDir   string
	configPath string
	backend    func(*backend.Options)
	ready      func(socket string)
	// home holds the hub instance key ("" is the account's home).
	home string
}

var testConsoleHooks *consoleHooks

func runConsole(e env, args []string) int {
	fs := flag.NewFlagSet("console", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profile := fs.String("profile", "", "profile to serve")
	auto := fs.Bool("auto", false, "approve requests without a prompt (never on production, never for unofficial plugins)")
	var hub console.HubOptions
	hub.Register(fs)
	if err := fs.Parse(args); err != nil || *profile == "" || fs.NArg() != 0 {
		fmt.Fprintln(e.stderr, "usage: lockfile console --profile NAME [--auto | --hub URL [--hub-plain-http] --hub-key-file FILE --instance NAME]")
		return product.ExitUsage
	}
	h := consoleHooks{}
	if testConsoleHooks != nil {
		h = *testConsoleHooks
	}
	if h.configPath == "" {
		p, err := config.UserConfigPath(product.P)
		if err != nil {
			fmt.Fprintln(e.stderr, "lockfile console:", err)
			return product.ExitError
		}
		h.configPath = p
	}
	opts := backend.Options{ConfigPath: h.configPath, Profile: *profile, StateDir: h.stateDir}
	if h.backend != nil {
		h.backend(&opts)
	}
	b, err := backend.New(opts)
	if err != nil {
		fmt.Fprintln(e.stderr, "lockfile console:", err)
		return product.ExitError
	}
	// Every exit path removes the fetched files.
	defer b.Close()

	cio := h.io
	var tty *os.File
	if cio == nil {
		term := console.NewTerminal(os.Stdin, e.stdout)
		if err := term.Interactive(); err != nil {
			fmt.Fprintln(e.stderr, "lockfile console: warning:", err, "(the mode keys and the status line are off)")
		}
		defer term.Restore()
		cio, tty = term, os.Stdin
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = console.Run(ctx, product.P, b, console.Options{
		Profile: *profile, IO: cio, TTY: tty, Auto: *auto, StateDir: opts.StateDir,
		// Runs are released at the profile's answer quantum by the backend;
		// the other answers say nothing about a file.
		Quantum:   -1,
		Sanitizer: b.Sanitizer(),
		Isolation: h.isolation, Harden: h.harden, Ready: h.ready,
		Hub: withHome(hub, h.home),
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(e.stderr, "lockfile console:", err)
		return product.ExitError
	}
	return product.ExitOK
}

func withHome(o console.HubOptions, home string) console.HubOptions {
	if o.Enabled() {
		o.Home = home
	}
	return o
}

// runHubKey is `lockfile hub-key --instance NAME`: the instance's public
// key, made on the first call in the account's home, for `lock up`.
func runHubKey(e env, args []string) int {
	home := ""
	if testConsoleHooks != nil {
		home = testConsoleHooks.home
	}
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			fmt.Fprintln(e.stderr, "lockfile hub-key:", err)
			return product.ExitError
		}
	}
	if err := hubclient.HubKeyCommand(e.stdout, home, args); err != nil {
		fmt.Fprintln(e.stderr, "lockfile hub-key:", err)
		return product.ExitUsage
	}
	return product.ExitOK
}
