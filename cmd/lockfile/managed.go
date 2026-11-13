package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/managed"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
)

func init() {
	commands["managed"] = command{human: true, help: "managed show | managed check [--profile NAME]: the company's managed configuration, and whether the profiles are within it", run: runManaged}
}

// noManaged is printed when the machine has no managed file.
const noManaged = "no managed configuration"

// runManaged is `lockfile managed show|check`. Its output and exit codes
// are a contract: lox runs `managed check` before it starts the lock
// (lockhub#179). Exit 3 (ExitManaged) is a refusal by the managed file:
// a file that does not load, or a profile beyond it.
func runManaged(e env, args []string) int {
	const usage = "usage: lockfile managed show | lockfile managed check [--profile NAME]"
	if len(args) == 0 {
		fmt.Fprintln(e.stderr, usage)
		return product.ExitUsage
	}
	switch args[0] {
	case "show":
		if len(args) != 1 {
			fmt.Fprintln(e.stderr, usage)
			return product.ExitUsage
		}
		return managedShow(e)
	case "check":
		fs := flag.NewFlagSet("managed check", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		name := fs.String("profile", "", "the profile to check (default: every one)")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
			fmt.Fprintln(e.stderr, usage)
			return product.ExitUsage
		}
		return managedCheck(e, *name)
	}
	fmt.Fprintln(e.stderr, usage)
	return product.ExitUsage
}

// managedShow prints the managed file: where, whose, its fingerprint and
// signature, lockfile's bounds and defaults, the managed profiles.
func managedShow(e env) int {
	m, err := managed.Load(context.Background())
	if err != nil {
		fmt.Fprintln(e.stderr, "lockfile managed:", err)
		return product.ExitManaged
	}
	if m == nil {
		fmt.Fprintln(e.stdout, noManaged)
		return product.ExitOK
	}
	// lockfile's own section must load too.
	if _, err := profile.ParseManaged(nil, "managed", m); err != nil {
		fmt.Fprintln(e.stderr, "lockfile managed:", err)
		return product.ExitManaged
	}
	org := m.Organisation
	if org == "" {
		org = "-"
	}
	fmt.Fprintln(e.stdout, m.Describe())
	fmt.Fprintln(e.stdout, "path:", m.Path)
	fmt.Fprintln(e.stdout, "organisation:", org)
	fmt.Fprintln(e.stdout, "fingerprint: sha256:"+m.Fingerprint)
	fmt.Fprintln(e.stdout, "signed:", yesNo(m.Signed))
	for _, kind := range []string{managed.KindBounds, managed.KindDefaults} {
		var t map[string]any
		ok, err := m.Decode(&t, profile.ManagedTool, kind)
		if err != nil {
			fmt.Fprintln(e.stderr, "lockfile managed:", err)
			return product.ExitManaged
		}
		if !ok || len(t) == 0 {
			fmt.Fprintf(e.stdout, "%s: none\n", kind)
			continue
		}
		fmt.Fprintf(e.stdout, "%s:\n", kind)
		for _, k := range slices.Sorted(maps.Keys(t)) {
			fmt.Fprintf(e.stdout, "  %s = %s\n", k, tomlValue(t[k]))
		}
	}
	if m.Family.Vault != nil && m.Family.Vault.Required {
		fmt.Fprintln(e.stdout, "family: vault.required = true (credentials = vault)")
	}
	names := m.Profiles(profile.ManagedTool)
	if len(names) == 0 {
		fmt.Fprintln(e.stdout, "profiles: none")
	} else {
		fmt.Fprintln(e.stdout, "profiles:", strings.Join(names, ", "))
	}
	return product.ExitOK
}

// tomlValue writes a decoded TOML value back as TOML.
func tomlValue(v any) string {
	switch x := v.(type) {
	case string:
		return fmt.Sprintf("%q", x)
	case []any:
		parts := make([]string, len(x))
		for i, el := range x {
			parts[i] = tomlValue(el)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return fmt.Sprint(v)
}

// managedCheck loads the config file and the managed file as the console
// does, and checks every profile (or the one named) against the bounds.
// It opens no connection and reads no credential.
func managedCheck(e env, name string) int {
	path, err := configPath()
	if err != nil {
		fmt.Fprintln(e.stderr, "lockfile managed:", err)
		return product.ExitError
	}
	f, err := profile.ReadFile(path)
	switch {
	case errors.Is(err, console.ErrManaged):
		fmt.Fprintln(e.stdout, "blocked by company policy: "+err.Error())
		return product.ExitManaged
	case err != nil:
		fmt.Fprintln(e.stderr, "lockfile managed:", err)
		return product.ExitError
	}
	if f.Managed == nil {
		fmt.Fprintln(e.stdout, noManaged)
		return product.ExitOK
	}
	names := slices.Sorted(maps.Keys(f.Profiles))
	if name != "" {
		if _, ok := f.Profiles[name]; !ok {
			fmt.Fprintln(e.stderr, "lockfile managed:", profile.ErrNoProfile)
			return product.ExitError
		}
		names = []string{name}
	}
	refused := false
	for _, n := range names {
		for _, line := range profile.FileViolations(f, n) {
			fmt.Fprintln(e.stdout, line)
			refused = true
		}
	}
	if refused {
		return product.ExitManaged
	}
	fmt.Fprintf(e.stdout, "%d profile(s) within the company policy (%s)\n", len(names), f.Managed.Describe())
	return product.ExitOK
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
