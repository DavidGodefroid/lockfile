package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"

	"github.com/DavidGodefroid/lockkit/sanitize"

	"github.com/DavidGodefroid/lockfile/internal/marketplace"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
	"github.com/DavidGodefroid/lockfile/internal/verify"
)

func init() {
	commands["marketplace"] = command{human: true, help: "add|remove|list plugin marketplaces (lockfile account)", run: runMarketplace}
}

const marketplaceUsage = "marketplace add NAME --index URL --identity-regexp REGEXP --issuer URL | remove NAME | list"

func runMarketplace(e env, args []string) int {
	if len(args) == 0 {
		return usageErr(e, marketplaceUsage)
	}
	switch args[0] {
	case "add":
		return marketplaceAdd(e, args[1:])
	case "remove":
		return marketplaceRemove(e, args[1:])
	case "list":
		return marketplaceList(e, args[1:])
	}
	return usageErr(e, marketplaceUsage)
}

func marketplaceAdd(e env, args []string) int {
	fs := flag.NewFlagSet("marketplace add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	index := fs.String("index", "", "https URL of the signed index.json")
	identity := fs.String("identity-regexp", "", "certificate identity of the signing workflow, ^https://…")
	issuer := fs.String("issuer", "https://token.actions.githubusercontent.com", "OIDC issuer of the signing certificates")
	key := fs.String("public-key", "", "public key (refused: lockkit v0.2.1 checks keyless signatures only)")
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return usageErr(e, marketplaceUsage)
		}
		if fs.NArg() == 0 {
			break
		}
		pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
	if len(pos) != 1 || *index == "" || (*identity == "" && *key == "") {
		return usageErr(e, marketplaceUsage)
	}
	if err := serviceAccount(); err != nil {
		return failHuman(e, "marketplace add", err)
	}
	raw := profile.RawMarketplace{Name: pos[0], IndexURL: *index, IdentityRegexp: *identity, OIDCIssuer: *issuer, PublicKey: *key}
	t, err := profile.CheckMarketplace(raw)
	if err != nil {
		return failHuman(e, "marketplace add", err)
	}
	fmt.Fprintln(e.stdout, yellow("UNOFFICIAL MARKETPLACE "+t.Name))
	fmt.Fprintln(e.stdout, yellow("Its plugins were not reviewed by the official marketplace. They run on your files in the sandbox,"))
	fmt.Fprintln(e.stdout, yellow("and an agent reads their answers: a plugin can answer a question other than the one it shows."))
	fmt.Fprintln(e.stdout, "  index    "+sanitize.Terminal(t.IndexURL, false))
	fmt.Fprintln(e.stdout, "  signer   "+sanitize.Terminal(t.IdentityRegexp, false))
	fmt.Fprintln(e.stdout, "  issuer   "+sanitize.Terminal(t.OIDCIssuer, false))
	for _, w := range profile.SignerWarnings(t.IdentityRegexp) {
		fmt.Fprintln(e.stdout, red("WARNING "+w))
	}
	if !confirm(e, bufio.NewReader(e.stdin), fmt.Sprintf("Type the marketplace name %q to trust it: ", t.Name), t.Name) {
		fmt.Fprintln(e.stdout, "not added")
		return product.ExitDenied
	}
	path, err := configPath()
	if err != nil {
		return failHuman(e, "marketplace add", err)
	}
	if _, err := profile.AppendMarketplace(path, raw); err != nil {
		return failHuman(e, "marketplace add", err)
	}
	fmt.Fprintln(e.stdout, "added "+t.Name+"; a running console asks you to accept it (:review)")
	return product.ExitOK
}

func marketplaceRemove(e env, args []string) int {
	if len(args) != 1 {
		return usageErr(e, marketplaceUsage)
	}
	if err := serviceAccount(); err != nil {
		return failHuman(e, "marketplace remove", err)
	}
	path, err := configPath()
	if err != nil {
		return failHuman(e, "marketplace remove", err)
	}
	if err := profile.RemoveMarketplace(path, args[0]); err != nil {
		return failHuman(e, "marketplace remove", err)
	}
	fmt.Fprintln(e.stdout, "removed "+args[0]+" (its installed plugins stay until lockfile plugin remove)")
	return product.ExitOK
}

func marketplaceList(e env, args []string) int {
	if len(args) != 0 {
		return usageErr(e, marketplaceUsage)
	}
	if err := serviceAccount(); err != nil {
		return failHuman(e, "marketplace list", err)
	}
	fmt.Fprintf(e.stdout, "official  %s  (signer %s)\n", marketplace.OfficialIndexURL, verify.Official.IdentityRegexp)
	path, err := configPath()
	if err != nil {
		return failHuman(e, "marketplace list", err)
	}
	f, err := profile.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return product.ExitOK
	}
	if err != nil {
		return failHuman(e, "marketplace list", err)
	}
	for _, t := range f.Marketplaces {
		fmt.Fprintln(e.stdout, red("UNOFFICIAL")+" "+sanitize.Terminal(t.String(), false))
	}
	return product.ExitOK
}
