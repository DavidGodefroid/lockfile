package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DavidGodefroid/lockkit/config"
	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/verbs"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
)

func init() {
	commands["profile"] = command{human: true, help: "profile add [--non-interactive] [--json] [--profile NAME] --source local|s3|sftp …: add a profile to the workspace", run: runProfile}
}

const profileUsage = "profile add [--non-interactive] [--json] [--project DIR] [--profile NAME] [--production] [--credentials ask|vault [--vault-path P]] " +
	"--source local|s3|sftp [--path DIR] [--bucket B --region R --endpoint URL] [--host H --port N --user U --auth key|password --key FILE --known-hosts FILE --base-dir D] [--prefix P]"

// defaultProfile is the profile profile add writes without --profile, the
// one lox run starts a lockfile lock with when lockfamily.yaml names none.
const defaultProfile = "default"

// profileFlags are the string target options of profile add, by TOML key:
// the flag is the key with '-' for '_'. None carries a secret: the console
// asks for it at start or reads it from Vault.
var profileFlags = []string{"source", "bucket", "region", "endpoint", "prefix", "host", "user", "auth", "key", "known_hosts",
	"base_dir", "path", "credentials_ttl", "max_object_bytes", "handle_ttl", "answer_quantum"}

// flagOf is the flag of a TOML key.
func flagOf(key string) string { return strings.ReplaceAll(key, "_", "-") }

// profileAdd is one run of profile add: its flags and how it answers.
type profileAdd struct {
	e      env
	fs     *flag.FlagSet
	asJSON bool
	in     *bufio.Reader
}

// fail answers err: a verbs.Error on stdout with --json, else a line on
// stderr (with the usage for a usage error).
func (p *profileAdd) fail(code string, exit int, err error) int {
	if p.asJSON {
		if werr := verbs.WriteJSON(p.e.stdout, verbs.Error{Lock: product.P.Name, Code: code, Error: err.Error()}); werr != nil {
			return product.ExitError
		}
		return exit
	}
	fmt.Fprintln(p.e.stderr, "lockfile profile add:", err)
	if code == verbs.CodeUsage || code == verbs.CodeSecret {
		fmt.Fprintln(p.e.stderr, "usage: lockfile "+profileUsage)
	}
	return exit
}

func (p *profileAdd) usage(err error) int {
	return p.fail(verbs.ErrorCode(err, verbs.CodeUsage), product.ExitUsage, err)
}

// str is the value of the string flag of key.
func (p *profileAdd) str(key string) string { return p.fs.Lookup(flagOf(key)).Value.String() }

// ask prompts for one value on the terminal (the prompt on stderr: with
// --json, stdout carries the JSON line only).
func (p *profileAdd) ask(prompt string) (string, error) {
	fmt.Fprint(p.e.stderr, prompt+": ")
	line, err := p.in.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(p.e.stderr)
		return "", errors.New("no answer")
	}
	return strings.TrimSpace(line), nil
}

// missing are the TOML keys the source needs that the flags did not give;
// an s3 source needs region or endpoint, reported as region.
func (p *profileAdd) missing() []string {
	var need []string
	switch p.str("source") {
	case profile.SourceS3:
		need = []string{"bucket"}
		if p.str("region") == "" && p.str("endpoint") == "" {
			need = append(need, "region")
		}
	case profile.SourceSFTP:
		need = []string{"host", "user", "known_hosts", "auth"}
		if p.str("auth") == profile.AuthKey {
			need = append(need, "key")
		}
	case profile.SourceLocal:
		need = []string{"path"}
	}
	var out []string
	for _, k := range need {
		if p.str(k) == "" {
			out = append(out, k)
		}
	}
	return out
}

// prompts for each key: what profile add asks on the terminal.
var prompts = map[string]string{
	"source":      "source (local, s3 or sftp)",
	"bucket":      "S3 bucket",
	"region":      "S3 region (empty to give an endpoint instead)",
	"endpoint":    "S3 endpoint (https://...)",
	"host":        "SFTP host",
	"user":        "SFTP user",
	"known_hosts": "known_hosts file of the SFTP host (in the console's home)",
	"auth":        "SFTP authentication (key or password)",
	"key":         "private key file (in the console's home)",
	"path":        "absolute directory of the files",
}

// fill asks for the missing keys on the terminal and sets their flags,
// then checks every flag again (a typed value is checked as a flag is).
func (p *profileAdd) fill(nonInteractive bool) error {
	for {
		var miss []string
		if p.str("source") == "" {
			miss = []string{"source"}
		} else {
			miss = p.missing()
		}
		if len(miss) == 0 {
			return verbs.RefuseSecrets(product.P, p.fs)
		}
		names := make([]string, len(miss))
		for i, k := range miss {
			names[i] = "--" + flagOf(k)
		}
		if p.str("source") == profile.SourceS3 && miss[len(miss)-1] == "region" {
			names[len(names)-1] = "--region or --endpoint"
		}
		if nonInteractive {
			return fmt.Errorf("missing %s", strings.Join(names, ", "))
		}
		if !isTerminal(p.e.stdin) {
			return fmt.Errorf("missing %s: give them, or run profile add in a terminal to be asked", strings.Join(names, ", "))
		}
		k := miss[0]
		v, err := p.ask(prompts[k])
		if err != nil {
			return fmt.Errorf("%s: %w", names[0], err)
		}
		if v == "" && k == "region" {
			if v, err = p.ask(prompts["endpoint"]); err != nil {
				return fmt.Errorf("--endpoint: %w", err)
			}
			k = "endpoint"
		}
		if v == "" {
			return fmt.Errorf("missing %s", names[0])
		}
		if err := p.fs.Set(flagOf(k), v); err != nil {
			return fmt.Errorf("--%s: invalid value", flagOf(k))
		}
		if err := verbs.RefuseSecrets(product.P, p.fs); err != nil {
			return err
		}
	}
}

// credentials is the credential source to write: "" for a local source
// (it has no secret); else --credentials ask|vault, or, asked on the
// terminal when the flags give none, ask, vault, or none for an SFTP key
// without a passphrase. No form asks for the secret itself.
func (p *profileAdd) credentials(source, vaultPath string, given, nonInteractive bool) (string, string, error) {
	cred := p.fs.Lookup(verbs.FlagCredentials).Value.String()
	if source == profile.SourceLocal {
		if given {
			return "", "", fmt.Errorf("a local source takes no --%s nor --%s: it has no secret", verbs.FlagCredentials, verbs.FlagVaultPath)
		}
		return "", "", nil
	}
	if given || nonInteractive || !isTerminal(p.e.stdin) {
		c, err := verbs.Credentials(cred, vaultPath)
		return c, vaultPath, err
	}
	keyAuth := source == profile.SourceSFTP && p.str("auth") == profile.AuthKey
	prompt := "credentials: ask (the console asks at start, masked) or vault"
	if keyAuth {
		prompt += ", or none (a key without a passphrase)"
	}
	c, err := p.ask(prompt + " [ask]")
	if err != nil {
		return "", "", fmt.Errorf("--%s: %w", verbs.FlagCredentials, err)
	}
	if c == console.CredentialsNone && keyAuth {
		return c, "", nil
	}
	if c == config.CredentialsVault {
		if vaultPath, err = p.ask("Vault path (such as secret/data/lockfile/NAME)"); err != nil {
			return "", "", fmt.Errorf("--%s: %w", verbs.FlagVaultPath, err)
		}
		if err := p.fs.Set(verbs.FlagVaultPath, vaultPath); err != nil {
			return "", "", fmt.Errorf("--%s: invalid value", verbs.FlagVaultPath)
		}
		if err := verbs.RefuseSecrets(product.P, p.fs); err != nil {
			return "", "", err
		}
	}
	c, err = verbs.Credentials(c, vaultPath)
	return c, vaultPath, err
}

// runProfile is `lockfile profile add` (docs/usage.md#profiles), the
// launcher verb of lockkit's GUIDE.md §14: it adds one profile to the
// workspace's .lockfile/config.toml, never rewrites one, and never takes,
// asks for or writes a secret.
func runProfile(e env, args []string) int {
	p := &profileAdd{e: e, in: bufio.NewReader(e.stdin)}
	if len(args) == 0 || args[0] != "add" {
		p.asJSON = verbs.WantsJSON(args)
		return p.usage(errors.New("the only profile command is profile add"))
	}
	args = args[1:]
	p.asJSON = verbs.WantsJSON(args)
	fs := flag.NewFlagSet("profile add", flag.ContinueOnError)
	p.fs = fs
	nonInteractive := fs.Bool(verbs.FlagNonInteractive, false, "never ask: a missing value is a usage error")
	asJSON := fs.Bool(verbs.FlagJSON, false, "one line of JSON on stdout")
	project := fs.String(verbs.FlagProject, "", "the workspace (default: the one of the current directory)")
	name := fs.String(verbs.FlagProfile, "", "the profile name (default: "+defaultProfile+")")
	production := fs.Bool(verbs.FlagProduction, false, "production profile (the profile name is typed for every approval)")
	fs.String(verbs.FlagCredentials, "", "ask or vault")
	vaultPath := fs.String(verbs.FlagVaultPath, "", "the Vault path, with --credentials vault")
	for _, k := range profileFlags {
		fs.String(flagOf(k), "", k)
	}
	port := fs.Int("port", 0, "sftp port")
	if err := verbs.Parse(product.P, fs, args); err != nil {
		return p.usage(err)
	}
	p.asJSON = *asJSON
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	// Where the profile goes, before anything is asked.
	var path string
	if set[verbs.FlagProject] {
		ws, err := projectDir(*project)
		if err != nil {
			return p.usage(err)
		}
		if path, err = workspaceConfig(ws); err != nil {
			return p.fail(verbs.CodeFailed, product.ExitError, err)
		}
	} else {
		err := configAccount()
		if err == nil {
			path, err = configPath()
		}
		if err != nil {
			return p.fail(verbs.CodeFailed, product.ExitError, err)
		}
	}

	if err := p.fill(*nonInteractive); err != nil {
		return p.usage(err)
	}
	source := p.str("source")
	switch source {
	case profile.SourceLocal, profile.SourceS3, profile.SourceSFTP:
	default:
		return p.usage(errors.New("--source: want local, s3 or sftp"))
	}
	cred, vault, err := p.credentials(source, *vaultPath, set[verbs.FlagCredentials] || set[verbs.FlagVaultPath], *nonInteractive)
	if err != nil {
		return p.usage(err)
	}

	var keys []string
	values := map[string]any{}
	for _, k := range profileFlags {
		if v := p.str(k); v != "" {
			keys, values[k] = append(keys, k), v
		}
	}
	if set["port"] {
		keys, values["port"] = append(keys, "port"), *port
	}
	if cred != "" {
		keys, values["credentials"] = append(keys, "credentials"), cred
	}
	if cred == config.CredentialsVault {
		keys, values["vault_path"] = append(keys, "vault_path"), vault
	}
	if *production {
		keys, values["production"] = append(keys, "production"), true
	}
	pname := *name
	if pname == "" {
		pname = defaultProfile
	}

	switch err := profile.AppendProfile(path, pname, keys, values); {
	case errors.Is(err, profile.ErrProfileExists):
		return p.fail(verbs.CodeExists, product.ExitError, err)
	case errors.Is(err, profile.ErrInvalidProfile):
		return p.usage(err)
	case err != nil:
		return p.fail(verbs.CodeFailed, product.ExitError, err)
	}

	hint := e.stdout
	if p.asJSON {
		if err := verbs.WriteJSON(e.stdout, verbs.Profile{Lock: product.P.Name, Profile: pname, Config: path, Production: *production, Credentials: cred}); err != nil {
			return product.ExitError
		}
		hint = e.stderr
	} else {
		fmt.Fprintf(hint, "added profile %s to %s\n", pname, path)
	}
	if os.Getenv(workspaceEnv) != "" || set[verbs.FlagProject] {
		fmt.Fprintf(hint, "lox run reviews it and copies it to the console: lox add lockfile INSTANCE --profile %s, then lox run\n", pname)
	} else {
		fmt.Fprintf(hint, "start it with its hub: lox add lockfile INSTANCE --profile %s, then lox run\n", pname)
	}
	return product.ExitOK
}

// projectDir is the workspace --project names, absolute: its profile
// lives in its .lockfile/config.toml.
func projectDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("--%s: not a directory", verbs.FlagProject)
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("--%s: not a directory", verbs.FlagProject)
	}
	return abs, nil
}
