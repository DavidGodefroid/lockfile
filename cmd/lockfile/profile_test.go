package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/verbs"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
)

// noTerminal makes the human commands see no terminal on stdin.
func noTerminal(h *humanHooks) { h.terminal = func(io.Reader) bool { return false } }

// jsonLine is the one line of JSON out holds, decoded into v.
func jsonLine(t *testing.T, out string, v any) {
	t.Helper()
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("want exactly one line on stdout: %q", out)
	}
	if err := json.Unmarshal([]byte(out), v); err != nil {
		t.Fatalf("%q: %v", out, err)
	}
}

func TestProfileAddValidates(t *testing.T) {
	h, _ := humanEnv(t, "official")
	if err := os.WriteFile(h.configPath, []byte(humanConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errText := runArgs(t, "", "profile", "add", "--profile", "docs", "--source", "s3", "--bucket", "documents", "--region", "eu-west-1",
		"--prefix", "invoices/", "--credentials", "vault", "--vault-path", "secret/data/lockfile/docs", "--production"); code != 0 {
		t.Fatalf("s3: exit %d: %s %s", code, out, errText)
	}
	before, _ := os.ReadFile(h.configPath)
	for name, tc := range map[string]struct {
		args []string
		code int
	}{
		"remote http endpoint": {[]string{"--source", "s3", "--bucket", "b", "--endpoint", "http://minio.example.org"}, product.ExitUsage},
		"unknown source":       {[]string{"--source", "ftp"}, product.ExitUsage},
		"sftp without hosts":   {[]string{"--source", "sftp", "--host", "h.example.org", "--user", "u", "--auth", "password"}, product.ExitUsage},
		"none on s3":           {[]string{"--source", "s3", "--bucket", "b", "--region", "r", "--credentials", "none"}, product.ExitUsage},
		"keychain":             {[]string{"--source", "s3", "--bucket", "b", "--region", "r", "--credentials", "keychain"}, product.ExitUsage},
		"production name":      {[]string{"--source", "local", "--path", "/srv/x", "--production", "--profile", "y"}, product.ExitUsage},
		"bad name":             {[]string{"--source", "local", "--path", "/srv/x", "--profile", "a b"}, product.ExitUsage},
		"duplicate":            {[]string{"--profile", "docs", "--source", "local", "--path", "/srv/x"}, product.ExitError},
	} {
		code, _, errText := runArgs(t, "", append([]string{"profile", "add", "--non-interactive"}, tc.args...)...)
		if code != tc.code {
			t.Errorf("%s: exit %d, want %d: %s", name, code, tc.code, errText)
		}
	}
	after, _ := os.ReadFile(h.configPath)
	if string(after) != string(before) || !strings.HasPrefix(string(after), humanConfig) {
		t.Fatalf("file changed by refused adds:\n%s", after)
	}
	f, err := profile.ReadFile(h.configPath)
	if err != nil || !f.Profiles["docs"].Production || f.Profiles["docs"].Prefix != "invoices/" || f.Profiles["docs"].Credentials != "vault" {
		t.Fatalf("%+v %v", f, err)
	}
}

// --json: one verbs.Profile line on success, the local source without
// credentials; the default profile name when --profile is absent.
func TestProfileAddJSON(t *testing.T) {
	h, _ := humanEnv(t, "official")
	noTerminal(h)
	code, out, errText := runArgs(t, "", "profile", "add", "--non-interactive", "--json", "--source", "local", "--path", "/srv/lockfile/samples")
	if code != product.ExitOK {
		t.Fatalf("exit %d: %s %s", code, out, errText)
	}
	want := `{"lock":"lockfile","profile":"default","config":"` + h.configPath + `","production":false}` + "\n"
	if out != want {
		t.Fatalf("got %q\nwant %q", out, want)
	}
	code, out, errText = runArgs(t, "", "profile", "add", "--non-interactive", "--json", "--profile", "docs", "--production", "--source", "s3",
		"--bucket", "documents", "--region", "eu-west-1")
	if code != product.ExitOK {
		t.Fatalf("exit %d: %s %s", code, out, errText)
	}
	var p verbs.Profile
	jsonLine(t, out, &p)
	if p != (verbs.Profile{Lock: "lockfile", Profile: "docs", Config: h.configPath, Production: true, Credentials: "ask"}) {
		t.Fatalf("%+v", p)
	}
	code, out, _ = runArgs(t, "", "profile", "add", "--non-interactive", "--json", "--profile", "vaulted", "--source", "sftp", "--host", "files.example.org",
		"--port", "2222", "--user", "reader", "--auth", "key", "--key", "/home/lockfile/.ssh/id_ed25519", "--known-hosts", "/home/lockfile/.ssh/known_hosts",
		"--credentials", "vault", "--vault-path", "secret/data/lockfile/vaulted")
	jsonLine(t, out, &p)
	if code != product.ExitOK || p.Credentials != "vault" || p.Profile != "vaulted" {
		t.Fatalf("exit %d: %+v", code, p)
	}
	f, err := profile.ReadFile(h.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if d := f.Profiles["default"]; d.LocalPath != "/srv/lockfile/samples" || d.Production {
		t.Errorf("default: %+v", d)
	}
	if v := f.Profiles["vaulted"]; v.SFTP.Port != 2222 || v.VaultPath != "secret/data/lockfile/vaulted" || v.SFTPAuth != "key" {
		t.Errorf("vaulted: %+v", v)
	}
}

// Every refusal of --json is one verbs.Error line on stdout, with the code
// lox init acts on, and nothing written.
func TestProfileAddJSONErrors(t *testing.T) {
	h, _ := humanEnv(t, "official")
	noTerminal(h)
	if err := os.WriteFile(h.configPath, []byte("[profiles.docs]\nsource = \"local\"\npath = \"/srv/docs\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(h.configPath)
	const secret = "hunter2-Secret"
	for name, tc := range map[string]struct {
		args []string
		code string
		exit int
		want string // in the error
	}{
		"exists":                {[]string{"--profile", "docs", "--source", "local", "--path", "/srv/other"}, verbs.CodeExists, product.ExitError, "already in the config file"},
		"missing source":        {[]string{"--profile", "x"}, verbs.CodeUsage, product.ExitUsage, "missing --source"},
		"missing s3 values":     {[]string{"--source", "s3"}, verbs.CodeUsage, product.ExitUsage, "missing --bucket, --region or --endpoint"},
		"missing sftp key":      {[]string{"--source", "sftp", "--host", "h", "--user", "u", "--known-hosts", "/k", "--auth", "key"}, verbs.CodeUsage, product.ExitUsage, "missing --key"},
		"missing path":          {[]string{"--source", "local"}, verbs.CodeUsage, product.ExitUsage, "missing --path"},
		"unknown source":        {[]string{"--source", "ftp"}, verbs.CodeUsage, product.ExitUsage, "--source"},
		"local credentials":     {[]string{"--source", "local", "--path", "/srv/x", "--credentials", "ask"}, verbs.CodeUsage, product.ExitUsage, "a local source takes no --credentials"},
		"local vault path":      {[]string{"--source", "local", "--path", "/srv/x", "--vault-path", "secret/x"}, verbs.CodeUsage, product.ExitUsage, "a local source takes no --credentials"},
		"vault without path":    {[]string{"--source", "s3", "--bucket", "b", "--region", "r", "--credentials", "vault"}, verbs.CodeUsage, product.ExitUsage, "--vault-path"},
		"keychain":              {[]string{"--source", "s3", "--bucket", "b", "--region", "r", "--credentials", "keychain"}, verbs.CodeSecret, product.ExitUsage, "--credentials"},
		"credentials secret":    {[]string{"--source", "s3", "--bucket", "b", "--region", "r", "--credentials", secret}, verbs.CodeSecret, product.ExitUsage, "--credentials"},
		"password flag":         {[]string{"--source", "sftp", "--password", secret}, verbs.CodeSecret, product.ExitUsage, "unknown flag"},
		"token flag":            {[]string{"--source", "s3", "--vault-token=" + secret}, verbs.CodeSecret, product.ExitUsage, "unknown flag"},
		"embedded password":     {[]string{"--source", "s3", "--bucket", "b", "--endpoint", "https://u:" + secret + "@s3.example.org"}, verbs.CodeSecret, product.ExitUsage, "--endpoint"},
		"positional":            {[]string{"docs", "--source", "local", "--path", "/srv/x"}, verbs.CodeUsage, product.ExitUsage, "positional"},
		"bad port":              {[]string{"--source", "sftp", "--port", secret}, verbs.CodeUsage, product.ExitUsage, "--port: invalid value"},
		"invalid profile value": {[]string{"--source", "s3", "--bucket", "b", "--endpoint", "http://s3.example.org"}, verbs.CodeUsage, product.ExitUsage, "endpoint"},
		"project not a dir":     {[]string{"--project", "/nonexistent/x", "--source", "local", "--path", "/srv/x"}, verbs.CodeUsage, product.ExitUsage, "--project"},
	} {
		args := append([]string{"profile", "add", "--non-interactive", "--json"}, tc.args...)
		code, out, errText := runArgs(t, "", args...)
		if code != tc.exit {
			t.Errorf("%s: exit %d, want %d", name, code, tc.exit)
		}
		var e verbs.Error
		jsonLine(t, out, &e)
		if e.Lock != "lockfile" || e.Code != tc.code || !strings.Contains(e.Error, tc.want) {
			t.Errorf("%s: %+v, want code %s and %q", name, e, tc.code, tc.want)
		}
		if strings.Contains(out+errText, secret) {
			t.Errorf("%s: the secret is quoted: %s %s", name, out, errText)
		}
		if errText != "" {
			t.Errorf("%s: stderr %q", name, errText)
		}
	}
	// An argument that does not parse still answers in JSON.
	code, out, _ := runArgs(t, "", "profile", "add", "--json", "--bogus")
	var e verbs.Error
	jsonLine(t, out, &e)
	if code != product.ExitUsage || e.Code != verbs.CodeUsage {
		t.Fatalf("exit %d: %+v", code, e)
	}
	if after, _ := os.ReadFile(h.configPath); string(after) != string(before) {
		t.Fatalf("file changed by refused adds:\n%s", after)
	}
}

// Without --non-interactive, on a terminal, profile add asks for what the
// flags did not give (on stderr) and checks the answers as flags; without
// a terminal it refuses rather than wait.
func TestProfileAddInteractive(t *testing.T) {
	h, _ := humanEnv(t, "official")
	in := "sftp\nfiles.example.org\nreader\n/home/lockfile/.ssh/known_hosts\nkey\n/home/lockfile/.ssh/id_ed25519\nnone\n"
	code, out, errText := runArgs(t, in, "profile", "add", "--profile", "partner")
	if code != product.ExitOK {
		t.Fatalf("exit %d: %s %s", code, out, errText)
	}
	for _, p := range []string{"source (local, s3 or sftp): ", "SFTP host: ", "SFTP authentication (key or password): ", "or none (a key without a passphrase)"} {
		if !strings.Contains(errText, p) {
			t.Errorf("no prompt %q in %q", p, errText)
		}
	}
	if strings.Contains(out, "SFTP host") || !strings.Contains(out, "added profile partner to "+h.configPath) {
		t.Errorf("stdout: %q", out)
	}
	f, err := profile.ReadFile(h.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if p := f.Profiles["partner"]; p.SFTP.Host != "files.example.org" || p.SFTPKey != "/home/lockfile/.ssh/id_ed25519" || p.Credentials != "none" {
		t.Fatalf("%+v", p)
	}

	// The flags given are not asked again; vault asks for its path; with
	// --json the JSON line stays alone on stdout.
	code, out, errText = runArgs(t, "\nhttps://s3.example.org\nvault\nsecret/data/lockfile/docs\n", "profile", "add", "--json", "--profile", "docs", "--source", "s3", "--bucket", "documents")
	if code != product.ExitOK {
		t.Fatalf("exit %d: %s %s", code, out, errText)
	}
	var p verbs.Profile
	jsonLine(t, out, &p)
	if p.Credentials != "vault" || strings.Contains(errText, "S3 bucket") || !strings.Contains(errText, "S3 region") || !strings.Contains(errText, "S3 endpoint") {
		t.Fatalf("%+v\n%s", p, errText)
	}
	if f, err := profile.ReadFile(h.configPath); err != nil || f.Profiles["docs"].VaultPath != "secret/data/lockfile/docs" {
		t.Fatalf("%v", err)
	}

	// A typed value is checked as a flag is: a password in it is refused,
	// never quoted.
	before, _ := os.ReadFile(h.configPath)
	code, out, errText = runArgs(t, "s3\nreader:hunter2-Secret@bucket\n", "profile", "add", "--profile", "leak")
	if code != product.ExitUsage || strings.Contains(out+errText, "hunter2") || !strings.Contains(errText, "--bucket") {
		t.Fatalf("exit %d: %s %s", code, out, errText)
	}
	// An answer that is not a credential source offered is refused.
	code, _, errText = runArgs(t, "keychain\n", "profile", "add", "--profile", "kc", "--source", "s3", "--bucket", "b", "--region", "r")
	if code != product.ExitUsage || !strings.Contains(errText, "--credentials") {
		t.Fatalf("keychain: exit %d: %s", code, errText)
	}
	// No answer (end of input).
	if code, _, _ := runArgs(t, "", "profile", "add", "--profile", "eof"); code != product.ExitUsage {
		t.Fatalf("eof: exit %d", code)
	}

	// No terminal: refused, nothing asked.
	noTerminal(h)
	code, _, errText = runArgs(t, "local\n/srv/x\n", "profile", "add", "--profile", "piped")
	if code != product.ExitUsage || !strings.Contains(errText, "missing --source") || !strings.Contains(errText, "terminal") || strings.Contains(errText, "source (local") {
		t.Fatalf("exit %d: %s", code, errText)
	}
	// ... but nothing to ask: written, credentials ask by default.
	if code, _, errText := runArgs(t, "", "profile", "add", "--profile", "complete", "--source", "s3", "--bucket", "b", "--region", "r"); code != product.ExitOK {
		t.Fatalf("complete: exit %d: %s", code, errText)
	}
	after, _ := os.ReadFile(h.configPath)
	if !strings.HasPrefix(string(after), string(before)) {
		t.Fatal("a refused add changed the file")
	}
	if f, err := profile.ReadFile(h.configPath); err != nil || f.Profiles["complete"].Credentials != "ask" || len(f.Profiles) != 3 {
		t.Fatalf("%+v %v", f, err)
	}
}

// --project writes that workspace's .lockfile/config.toml, as the
// workspace mode does, without the lockfile account.
func TestProfileAddProject(t *testing.T) {
	workspaceHooks(t)
	t.Setenv(workspaceEnv, "")
	ws := t.TempDir()
	code, out, errText := runArgs(t, "", "profile", "add", "--non-interactive", "--json", "--project", ws, "--profile", "samples", "--source", "local", "--path", "/srv/x")
	if code != product.ExitOK {
		t.Fatalf("exit %d: %s %s", code, out, errText)
	}
	file := filepath.Join(ws, ".lockfile", "config.toml")
	var p verbs.Profile
	jsonLine(t, out, &p)
	if p.Config != file || !strings.Contains(errText, "lox add lockfile INSTANCE --profile samples") {
		t.Fatalf("%+v %s", p, errText)
	}
	if f, err := profile.ReadFile(file); err != nil || f.Profiles["samples"].LocalPath != "/srv/x" {
		t.Fatalf("%+v %v", f, err)
	}
	if fi, err := os.Stat(filepath.Dir(file)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("%v %v", fi, err)
	}
}

// The old forms are gone: a positional profile name, and any verb but add.
func TestProfileAddOldFormsRefused(t *testing.T) {
	h, _ := humanEnv(t, "official")
	for _, args := range [][]string{
		{"profile", "add", "docs", "--source", "local", "--path", "/srv/x"},
		{"profile", "list"},
		{"profile"},
	} {
		if code, _, errText := runArgs(t, "", args...); code != product.ExitUsage || !strings.Contains(errText, "usage: lockfile profile add") {
			t.Errorf("%v: exit %d: %s", args, code, errText)
		}
	}
	if _, err := os.Stat(h.configPath); !os.IsNotExist(err) {
		t.Fatalf("written: %v", err)
	}
}
