package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/DavidGodefroid/lockkit/audit"
	"github.com/DavidGodefroid/lockkit/sanitize"
	"github.com/DavidGodefroid/lockkit/secrets"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
)

func init() {
	commands["audit"] = command{human: true, help: "read the console's audit log: causes, sizes, hashes (lockfile account) [--since 24h] [--json]", run: runAudit}
	commands["forget"] = command{human: true, help: "forget --profile P: delete the profile's keychain secret (lockfile account)", run: runForget}
}

// sinceTime reads --since: a duration back from now, or a date.
func sinceTime(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d), nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("--since %q: want a duration (24h) or a date (2026-10-19)", s)
}

// runAudit prints the audit log: the one place for file metadata (docs/security-model.md,
// "Data at rest"), read by the human in the console account only.
func runAudit(e env, args []string) int {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	since := fs.String("since", "", "only records after this duration back (24h) or date")
	asJSON := fs.Bool("json", false, "the records as JSON lines")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return usageErr(e, "audit [--since 24h|DATE] [--json]")
	}
	from, err := sinceTime(*since, time.Now())
	if err != nil {
		return usageErr(e, "audit [--since 24h|DATE] [--json]")
	}
	if err := serviceAccount(); err != nil {
		return failHuman(e, "audit", err)
	}
	base, _, err := stateHome()
	if err != nil {
		return failHuman(e, "audit", err)
	}
	log, err := audit.Open(product.P, base)
	if err != nil {
		return failHuman(e, "audit", err)
	}
	f, err := os.Open(log.Path())
	if err != nil {
		return failHuman(e, "audit", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var rec map[string]any
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		if ts, _ := rec["ts"].(string); !from.IsZero() {
			t, err := time.Parse(time.RFC3339Nano, ts)
			if err != nil || t.Before(from) {
				continue
			}
		}
		if *asJSON {
			fmt.Fprintln(e.stdout, sc.Text())
			continue
		}
		fmt.Fprintln(e.stdout, auditLine(rec))
	}
	if err := sc.Err(); err != nil {
		return failHuman(e, "audit", err)
	}
	return product.ExitOK
}

// auditLine is a record as one line: time, event, then key=value.
func auditLine(rec map[string]any) string {
	var keys []string
	for k := range rec {
		if k != "ts" && k != "event" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	parts := []string{fmt.Sprint(rec["ts"]), fmt.Sprint(rec["event"])}
	for _, k := range keys {
		v, _ := json.Marshal(rec[k])
		parts = append(parts, k+"="+string(v))
	}
	return sanitize.Terminal(strings.Join(parts, "  "), false)
}

func runForget(e env, args []string) int {
	fs := flag.NewFlagSet("forget", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("profile", "", "profile whose keychain secret to delete")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *name == "" {
		return usageErr(e, "forget --profile P")
	}
	if err := serviceAccount(); err != nil {
		return failHuman(e, "forget", err)
	}
	path, err := configPath()
	if err != nil {
		return failHuman(e, "forget", err)
	}
	f, err := profile.ReadFile(path)
	if err != nil {
		return failHuman(e, "forget", err)
	}
	p, ok := f.Profiles[*name]
	if !ok {
		return failHuman(e, "forget", fmt.Errorf("no profile %q", *name))
	}
	cp := p.Console()
	if err := secrets.NewKeychain(product.P.KeychainService).Delete(cp.Name, cp.Host, cp.Port); err != nil {
		return failHuman(e, "forget", err)
	}
	fmt.Fprintf(e.stdout, "the keychain secret of profile %s is deleted\n", *name)
	return product.ExitOK
}
