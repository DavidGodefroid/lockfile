package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/term"

	"github.com/DavidGodefroid/lockkit/config"
	"github.com/DavidGodefroid/lockkit/kit"
	"github.com/DavidGodefroid/lockkit/sysconf"
	"github.com/DavidGodefroid/lockkit/ui"
	"github.com/DavidGodefroid/lockkit/update"

	"github.com/DavidGodefroid/lockfile/internal/marketplace"
	"github.com/DavidGodefroid/lockfile/internal/plugin"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/verify"
)

// humanHooks replace the account, paths, network and signature checks of
// the human commands in tests.
type humanHooks struct {
	configPath, stateDir string
	sys                  func() (*sysconf.Config, error)
	euid                 func() int
	http                 *http.Client
	verifier             verify.Verifier
	// officialIndex replaces the official marketplace's index URL.
	officialIndex string
	// terminal replaces the terminal check of typed confirmations.
	terminal func(io.Reader) bool
}

var testHumanHooks *humanHooks

func hooks() humanHooks {
	if testHumanHooks != nil {
		return *testHumanHooks
	}
	return humanHooks{}
}

// configPath is the lockfile account's config file.
func configPath() (string, error) {
	if h := hooks(); h.configPath != "" {
		return h.configPath, nil
	}
	return config.UserConfigPath(product.P)
}

// stateHome is <state base>/lockfile, where plugins, the approved policies
// and the audit live.
func stateHome() (base, home string, err error) {
	base = hooks().stateDir
	if base == "" {
		if base, err = kit.StateBase(); err != nil {
			return "", "", err
		}
	}
	return base, filepath.Join(base, product.P.Name), nil
}

func pluginStore() (plugin.Store, error) {
	_, home, err := stateHome()
	if err != nil {
		return plugin.Store{}, err
	}
	return plugin.Store{Dir: home}, nil
}

// serviceAccount refuses a command that changes the console's state when,
// in a separated setup, it does not run in the lockfile account.
func serviceAccount() error {
	h := hooks()
	load, euid := h.sys, h.euid
	if load == nil {
		load = func() (*sysconf.Config, error) { return sysconf.Load(product.P) }
	}
	if euid == nil {
		euid = os.Geteuid
	}
	sys, err := load()
	if err != nil || sys == nil {
		return err
	}
	want, err := sys.ServiceUID()
	if err != nil {
		return err
	}
	if euid() != want {
		return fmt.Errorf("run this in the %s account (sudo -u %s -i), where the console keeps its plugins and config", sys.ServiceUser, sys.ServiceUser)
	}
	return nil
}

func verifier() verify.Verifier {
	if v := hooks().verifier; v != nil {
		return v
	}
	return verify.NewLockkit(update.Options{})
}

func installer(store plugin.Store) marketplace.Installer {
	return marketplace.Installer{HTTP: hooks().http, Verifier: verifier(), Store: store, OS: runtime.GOOS, Arch: runtime.GOARCH}
}

func officialMarketplace() marketplace.Marketplace {
	m := marketplace.Official()
	if u := hooks().officialIndex; u != "" {
		m.IndexURL = u
	}
	return m
}

// isTerminal reports whether r is a terminal. Tests that set humanHooks
// without terminal act as one.
func isTerminal(r io.Reader) bool {
	if testHumanHooks != nil {
		return testHumanHooks.terminal == nil || testHumanHooks.terminal(r)
	}
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// confirm asks the human to type want; "y" accepts y or yes. A name to
// type must come from a terminal, never from a pipe.
func confirm(e env, in *bufio.Reader, prompt, want string) bool {
	if want != "y" && !isTerminal(e.stdin) {
		fmt.Fprintln(e.stderr, "lockfile: type the name in a terminal; a typed confirmation is never read from a pipe")
		return false
	}
	fmt.Fprint(e.stdout, prompt)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(e.stdout)
		return false
	}
	line = strings.TrimSpace(line)
	if want == "y" {
		return strings.EqualFold(line, "y") || strings.EqualFold(line, "yes")
	}
	return line == want
}

// failHuman prints a human command's error.
func failHuman(e env, cmd string, err error) int {
	fmt.Fprintf(e.stderr, "lockfile %s: %v\n", cmd, err)
	if errors.Is(err, verify.ErrCosignMissing) {
		fmt.Fprintln(e.stderr, "  install cosign (https://docs.sigstore.dev/cosign/system_config/installation/): signatures are never skipped")
	}
	return product.ExitError
}

func red(s string) string    { return ui.Red + s + ui.Reset }
func yellow(s string) string { return ui.Yellow + s + ui.Reset }
