package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/testenv/consoletest"
	"time"
)

const (
	canary    = "glsa_CanarySecretToken123"
	urlCanary = "https://user:glsa_Canary@h"
	canaryHit = "glsa_Canary"
)

// TestErrorsDoNotEchoValues: a typed value (a pasted token, a URL with
// one) is never quoted back; the error names the flag or argument.
func TestErrorsDoNotEchoValues(t *testing.T) {
	humanEnv(t, "official")
	for _, v := range []string{canary, urlCanary} {
		if _, err := sinceTime(v, time.Now()); err == nil || strings.Contains(err.Error(), canaryHit) || !strings.Contains(err.Error(), "--since") {
			t.Errorf("--since %q: %v", v, err)
		}
		if _, err := findMarketplace(v); err == nil || strings.Contains(err.Error(), canaryHit) || !strings.Contains(err.Error(), "marketplace") {
			t.Errorf("--marketplace %q: %v", v, err)
		}
	}
	for _, args := range [][]string{
		{"plugin", "install", canary + "@1.0.0"},
		{"plugin", "install", urlCanary + "@1.0.0"},
		{"plugin", "install", "pdf.parses@" + canary},
		{"plugin", "update", canary + "@1.0.0"},
		{"plugin", "install", "--marketplace", canary, "pdf.parses@1.0.0"},
		{"plugin", "install", canary + "/pdf.parses@1.0.0"},
		{"plugin", "verify", canary},
		{"plugin", "remove", canary},
		{"marketplace", "remove", canary},
		{"audit", "--since", canary},
		{canary},
		{"console", "--profile", "dev", "--allowed-uids", canary},
		{"forget", "--profile", canary},
		{"forget", "--profile", canary + " x"},
		{"profile", "add", urlCanary, "--source", "local", "--path", "/srv"},
		{"profile", "add", canary + " x", "--source", "local", "--path", "/srv"},
		{"hub-key", "--instance", canary + " x"},
		{"hub-key", "--instance=" + urlCanary},
		{"hub-key", "--" + canary},
		{"hub-key", "--instance", "files", canary},
		{"init", canary + " x"},
		{"init", "--user", urlCanary},
	} {
		code, out, errText := runArgs(t, "", args...)
		if code == 0 || strings.Contains(out+errText, canaryHit) {
			t.Errorf("%v: exit %d: %s %s", args, code, out, errText)
		}
	}
}

// The hub flags reach lockkit, whose checks quote them: the console prints
// its errors without the typed values.
func TestConsoleHubErrorsDoNotEchoValues(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg, []byte("[profiles.samples]\nsource = \"local\"\npath = \"/tmp\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "hub.pub")
	if err := os.WriteFile(key, []byte(base64.StdEncoding.EncodeToString(make([]byte, 32))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	devices := filepath.Join(dir, "devices.json")
	if err := os.WriteFile(devices, []byte(`{"devices":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testConsoleHooks = nil })
	for _, hub := range [][]string{
		{"--hub", "http://user:" + canary + "@hub.example.org", "--instance", "files"},
		{"--hub", "https://" + canary + "%zz", "--instance", "files"},
		{"--hub", "https://hub.example.org", "--instance", canary + " x"},
		{"--hub", "https://hub.example.org", "--instance", "files", "--devices-file", devices, "--require-device", "read, " + canary},
	} {
		testConsoleHooks = &consoleHooks{io: consoletest.NewScriptIO(), isolation: sameUserIsolation(t), harden: func() error { return nil },
			stateDir: t.TempDir(), configPath: cfg, home: t.TempDir()}
		args := append([]string{"console", "--profile", "samples", "--hub-key-file", key}, hub...)
		code, out, errText := runArgs(t, "", args...)
		if code == product.ExitOK || strings.Contains(out+errText, canaryHit) || !strings.Contains(errText, "lockfile console: configuration error") {
			t.Errorf("%v: exit %d: %s %s", hub, code, out, errText)
		}
	}
}

func TestScrub(t *testing.T) {
	got := scrub(`bad "https://u:`+canary+`@h" and `+canary+`, risk "aaaa" in aaaa,bbbb; ab stays`, "https://u:"+canary+"@h", canary, "aaaa,bbbb", "ab")
	if want := `bad "<value>" and <value>, risk "<value>" in <value>; ab stays`; got != want {
		t.Errorf("scrub = %q, want %q", got, want)
	}
	if got := scrub(`x "a\"`+canary+`"`, `a"`+canary); strings.Contains(got, canaryHit) {
		t.Errorf("quoted form kept: %q", got)
	}
	if got := scrub(`unknown risk class "`+canary+`"`, "read, "+canary); strings.Contains(got, canaryHit) {
		t.Errorf("trimmed part kept: %q", got)
	}
	v := typedValues([]string{"--instance", "files", "--hub=" + urlCanary, "--" + canary, "x1"}, []string{"instance", "hub"}, func(s string) bool { return s == "files" })
	if len(v) != 3 || v[0] != urlCanary || v[1] != canary || v[2] != "x1" {
		t.Errorf("typedValues = %q", v)
	}
}
