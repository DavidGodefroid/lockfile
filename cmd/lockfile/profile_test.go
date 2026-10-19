package main

import (
	"os"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
)

func TestProfileAddValidates(t *testing.T) {
	h, _ := humanEnv(t, "official")
	if err := os.WriteFile(h.configPath, []byte(humanConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errText := runArgs(t, "", "profile", "add", "docs", "--source", "s3", "--bucket", "documents", "--region", "eu-west-1",
		"--prefix", "invoices/", "--credentials", "keychain", "--production"); code != 0 {
		t.Fatalf("s3: exit %d: %s %s", code, out, errText)
	}
	before, _ := os.ReadFile(h.configPath)
	for name, args := range map[string][]string{
		"remote http endpoint": {"--source", "s3", "--bucket", "b", "--endpoint", "http://minio.example.org"},
		"unknown source":       {"--source", "ftp"},
		"sftp without hosts":   {"--source", "sftp", "--host", "h.example.org", "--user", "u", "--auth", "password"},
		"none on s3":           {"--source", "s3", "--bucket", "b", "--region", "r", "--credentials", "none"},
	} {
		code, _, errText := runArgs(t, "", append([]string{"profile", "add", "x"}, args...)...)
		if code != product.ExitError {
			t.Errorf("%s: exit %d: %s", name, code, errText)
		}
	}
	if code, _, errText := runArgs(t, "", "profile", "add", "docs", "--source", "local", "--path", "/srv/x"); code != product.ExitError || !strings.Contains(errText, "already exists") {
		t.Errorf("duplicate: exit %d: %s", code, errText)
	}
	after, _ := os.ReadFile(h.configPath)
	if string(after) != string(before) || !strings.HasPrefix(string(after), humanConfig) {
		t.Fatalf("file changed by refused adds:\n%s", after)
	}
	f, err := profile.ReadFile(h.configPath)
	if err != nil || !f.Profiles["docs"].Production || f.Profiles["docs"].Prefix != "invoices/" {
		t.Fatalf("%+v %v", f, err)
	}
}
