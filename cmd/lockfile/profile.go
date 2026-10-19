package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
)

func init() {
	commands["profile"] = command{human: true, help: "profile add NAME --source s3|sftp|local … (lockfile account)", run: runProfile}
}

const profileUsage = "profile add NAME --source s3|sftp|local [--bucket B --region R --endpoint URL] [--host H --port N --user U --auth key|password --key FILE --known-hosts FILE --base-dir D] [--path DIR] [--prefix P] [--credentials ask|keychain|vault|none --vault-path P] [--production]"

// profileFlags are the string options of profile add, by TOML key.
var profileFlags = []string{"source", "bucket", "region", "endpoint", "prefix", "host", "user", "auth", "key", "known_hosts",
	"base_dir", "path", "credentials", "vault_path", "credentials_ttl", "max_object_bytes", "handle_ttl", "answer_quantum"}

func runProfile(e env, args []string) int {
	if len(args) == 0 || args[0] != "add" {
		return usageErr(e, profileUsage)
	}
	fs := flag.NewFlagSet("profile add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	strs := map[string]*string{}
	for _, k := range profileFlags {
		strs[k] = fs.String(strings.ReplaceAll(k, "_", "-"), "", k)
	}
	port := fs.Int("port", 0, "sftp port")
	production := fs.Bool("production", false, "production profile (the profile name is typed for every approval)")
	var pos []string
	rest := args[1:]
	for {
		if err := fs.Parse(rest); err != nil {
			return usageErr(e, profileUsage)
		}
		if fs.NArg() == 0 {
			break
		}
		pos, rest = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
	if len(pos) != 1 || *strs["source"] == "" {
		return usageErr(e, profileUsage)
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[strings.ReplaceAll(f.Name, "-", "_")] = true })
	var keys []string
	values := map[string]any{}
	for _, k := range profileFlags {
		if set[k] {
			keys, values[k] = append(keys, k), *strs[k]
		}
	}
	if set["port"] {
		keys, values["port"] = append(keys, "port"), *port
	}
	if *production {
		keys, values["production"] = append(keys, "production"), true
	}
	if err := serviceAccount(); err != nil {
		return failHuman(e, "profile add", err)
	}
	path, err := configPath()
	if err != nil {
		return failHuman(e, "profile add", err)
	}
	if err := profile.AppendProfile(path, pos[0], keys, values); err != nil {
		return failHuman(e, "profile add", err)
	}
	fmt.Fprintf(e.stdout, "added profile %s to %s\nstart it: lockfile console --profile %s\n", pos[0], path, pos[0])
	return product.ExitOK
}
