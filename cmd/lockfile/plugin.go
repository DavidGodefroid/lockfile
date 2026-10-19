package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DavidGodefroid/lockkit/sanitize"

	"github.com/DavidGodefroid/lockfile/internal/marketplace"
	"github.com/DavidGodefroid/lockfile/internal/plugin"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
	"github.com/DavidGodefroid/lockfile/pluginspec"
)

func init() {
	commands["plugin"] = command{human: true, help: "install|update|remove|list|verify plugins (lockfile account)", run: runPlugin}
}

const pluginUsage = "plugin install NAME@X.Y.Z [--marketplace M] | update NAME@X.Y.Z | remove NAME | list | verify [NAME]"

func runPlugin(e env, args []string) int {
	if len(args) == 0 {
		return usageErr(e, pluginUsage)
	}
	switch args[0] {
	case "install":
		return pluginInstall(e, args[1:], false)
	case "update":
		return pluginInstall(e, args[1:], true)
	case "remove":
		return pluginRemove(e, args[1:])
	case "list":
		return pluginList(e, args[1:])
	case "verify":
		return pluginVerify(e, args[1:])
	}
	return usageErr(e, pluginUsage)
}

// findMarketplace is the official marketplace ("" or "official") or a
// configured unofficial one.
func findMarketplace(name string) (marketplace.Marketplace, error) {
	if name == "" || name == plugin.OfficialMarketplace {
		return officialMarketplace(), nil
	}
	path, err := configPath()
	if err != nil {
		return marketplace.Marketplace{}, err
	}
	f, err := profile.ReadFile(path)
	if err != nil {
		return marketplace.Marketplace{}, err
	}
	for _, t := range f.Marketplaces {
		if t.Name == name {
			return t.Marketplace(), nil
		}
	}
	return marketplace.Marketplace{}, fmt.Errorf("marketplace %s is not configured (lockfile marketplace add)", name)
}

// staged downloads and verifies a plugin version into a staging store and
// returns its lock entry, binary and manifest.
func staged(ctx context.Context, home string, m marketplace.Marketplace, name, version string) (plugin.LockEntry, []byte, []byte, pluginspec.Manifest, error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return plugin.LockEntry{}, nil, nil, pluginspec.Manifest{}, err
	}
	dir, err := os.MkdirTemp(home, ".staging-")
	if err != nil {
		return plugin.LockEntry{}, nil, nil, pluginspec.Manifest{}, err
	}
	stage := plugin.Store{Dir: dir}
	defer stage.RemoveAll()
	ent, err := installer(stage).Install(ctx, m, name, version)
	if err != nil {
		return plugin.LockEntry{}, nil, nil, pluginspec.Manifest{}, err
	}
	f, in, err := stage.Open(ent.Name)
	if err != nil {
		return plugin.LockEntry{}, nil, nil, pluginspec.Manifest{}, err
	}
	defer f.Close()
	bin, err := io.ReadAll(f)
	if err != nil {
		return plugin.LockEntry{}, nil, nil, pluginspec.Manifest{}, err
	}
	man, err := os.ReadFile(filepath.Join(filepath.Dir(in.Binary), "plugin.toml"))
	if err != nil {
		return plugin.LockEntry{}, nil, nil, pluginspec.Manifest{}, err
	}
	return ent, bin, man, in.Manifest, nil
}

func pluginInstall(e env, args []string, update bool) int {
	cmd := "plugin install"
	if update {
		cmd = "plugin update"
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	market := fs.String("marketplace", "", "unofficial marketplace (lockfile marketplace list)")
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return usageErr(e, pluginUsage)
		}
		if fs.NArg() == 0 {
			break
		}
		pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
	if len(pos) != 1 {
		return usageErr(e, pluginUsage)
	}
	name, version, ok := strings.Cut(pos[0], "@")
	if !ok || name == "" || version == "" {
		return usageErr(e, pluginUsage)
	}
	// A qualified name carries its marketplace.
	if mk, bare, ok := strings.Cut(name, "/"); ok {
		if *market != "" && *market != mk {
			return usageErr(e, pluginUsage)
		}
		*market, name = mk, bare
	}
	if err := serviceAccount(); err != nil {
		return failHuman(e, cmd, err)
	}
	store, err := pluginStore()
	if err != nil {
		return failHuman(e, cmd, err)
	}
	m, err := findMarketplace(*market)
	if err != nil {
		return failHuman(e, cmd, err)
	}
	qualified := plugin.Qualify(m.Name, name, m.Official)
	var old *plugin.Installed
	installed, err := store.Installed()
	if err != nil {
		return failHuman(e, cmd, err)
	}
	for i := range installed {
		if installed[i].Name == qualified {
			old = &installed[i]
		}
	}
	switch {
	case update && old == nil:
		return failHuman(e, cmd, fmt.Errorf("%s is not installed (lockfile plugin install)", qualified))
	case !update && old != nil:
		return failHuman(e, cmd, fmt.Errorf("%s@%s is installed: use lockfile plugin update", qualified, old.Version))
	}
	ctx, stop := agentContext()
	defer stop()
	ent, bin, man, mf, err := staged(ctx, store.Dir, m, name, version)
	if errors.Is(err, marketplace.ErrYanked) {
		return failHuman(e, cmd, fmt.Errorf("%s@%s was withdrawn by its marketplace (yanked)", qualified, version))
	}
	if err != nil {
		return failHuman(e, cmd, err)
	}
	if !m.Official {
		fmt.Fprintln(e.stdout, red("UNOFFICIAL MARKETPLACE "+m.Name+": this plugin was not reviewed by the official marketplace"))
	}
	if update {
		questionChanged, lines := marketplace.DiffManifests(old.Manifest, mf)
		fmt.Fprintf(e.stdout, "%s %s -> %s\n", qualified, old.Version, ent.Version)
		for _, l := range lines {
			fmt.Fprintln(e.stdout, "  "+sanitize.Terminal(l, false))
		}
		in := bufio.NewReader(e.stdin)
		if questionChanged {
			fmt.Fprintln(e.stdout, red("the question or the meaning of 1/0 changed: what an answer reveals changes"))
			if !confirm(e, in, fmt.Sprintf("Type the plugin name %q to update: ", qualified), qualified) {
				fmt.Fprintln(e.stdout, "not updated")
				return product.ExitDenied
			}
		} else if !confirm(e, in, "Update? [y/N] ", "y") {
			fmt.Fprintln(e.stdout, "not updated")
			return product.ExitDenied
		}
	}
	if err := store.Install(ent, bin, man); err != nil {
		return failHuman(e, cmd, err)
	}
	fmt.Fprintf(e.stdout, "installed %s@%s (%s)\n", ent.Name, ent.Version, ent.Marketplace)
	fmt.Fprintln(e.stdout, "  ? "+sanitize.Terminal(mf.Question, false))
	fmt.Fprintln(e.stdout, "  1 = "+sanitize.Terminal(mf.One, false))
	fmt.Fprintln(e.stdout, "  0 = "+sanitize.Terminal(mf.Zero, false))
	fmt.Fprintln(e.stdout, "A running console asks you to accept it (:review) before an agent can use it.")
	return product.ExitOK
}

func pluginRemove(e env, args []string) int {
	if len(args) != 1 {
		return usageErr(e, pluginUsage)
	}
	if err := serviceAccount(); err != nil {
		return failHuman(e, "plugin remove", err)
	}
	store, err := pluginStore()
	if err != nil {
		return failHuman(e, "plugin remove", err)
	}
	if err := store.Remove(args[0]); err != nil {
		return failHuman(e, "plugin remove", err)
	}
	fmt.Fprintln(e.stdout, "removed "+args[0])
	return product.ExitOK
}

func pluginList(e env, args []string) int {
	if len(args) != 0 {
		return usageErr(e, pluginUsage)
	}
	if err := serviceAccount(); err != nil {
		return failHuman(e, "plugin list", err)
	}
	store, err := pluginStore()
	if err != nil {
		return failHuman(e, "plugin list", err)
	}
	installed, err := store.Installed()
	if err != nil {
		return failHuman(e, "plugin list", err)
	}
	if len(installed) == 0 {
		fmt.Fprintln(e.stdout, "no plugin installed (lockfile plugin install NAME@X.Y.Z)")
	}
	for _, in := range installed {
		head := fmt.Sprintf("%s@%s  (%s)", in.Name, in.Version, in.Marketplace)
		if !in.Official {
			head += "  " + red("UNOFFICIAL")
		}
		if in.Tampered {
			fmt.Fprintln(e.stdout, head+"  "+red("TAMPERED: lockfile plugin verify"))
			continue
		}
		fmt.Fprintln(e.stdout, head)
		fmt.Fprintln(e.stdout, "  ? "+sanitize.Terminal(in.Manifest.Question, false))
	}
	return product.ExitOK
}

func pluginVerify(e env, args []string) int {
	if len(args) > 1 {
		return usageErr(e, pluginUsage)
	}
	// From another account the store is empty: "ok" would be false assurance.
	if err := serviceAccount(); err != nil {
		return failHuman(e, "plugin verify", err)
	}
	store, err := pluginStore()
	if err != nil {
		return failHuman(e, "plugin verify", err)
	}
	installed, err := store.Installed()
	if err != nil {
		return failHuman(e, "plugin verify", err)
	}
	bad, seen := 0, false
	for _, in := range installed {
		if len(args) == 1 && in.Name != args[0] {
			continue
		}
		seen = true
		f, _, err := store.Open(in.Name)
		if err != nil {
			bad++
			fmt.Fprintln(e.stdout, red("TAMPERED")+" "+in.Name+"@"+in.Version+": reinstall it (lockfile plugin remove, then install)")
			continue
		}
		f.Close()
		fmt.Fprintln(e.stdout, "ok "+in.Name+"@"+in.Version)
	}
	if len(args) == 1 && !seen {
		return failHuman(e, "plugin verify", fmt.Errorf("%s is not installed", args[0]))
	}
	if bad > 0 {
		return product.ExitRefused
	}
	return product.ExitOK
}
