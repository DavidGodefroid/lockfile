package clientcmd

import (
	"strings"
	"testing"
)

// The unknown_profile hint quotes the profile only when it is a valid
// profile name.
func TestUnknownProfileHintDoesNotEcho(t *testing.T) {
	for _, p := range []string{"glsa_CanarySecretToken123 x", "https://user:glsa_Canary@h"} {
		if h := unknownProfile(p).Refusal.Hint; strings.Contains(h, "glsa_Canary") || h != UnknownProfileHint("NAME") {
			t.Errorf("%q: hint %q", p, h)
		}
	}
	if h := unknownProfile("other").Refusal.Hint; h != UnknownProfileHint("other") || !strings.Contains(h, "--profile other)") {
		t.Errorf("valid name: %q", h)
	}
}

// The start hints follow lockkit v0.9.1: a console is started by `lox up`
// only, never by `lockfile console`. An unknown profile is not fixed by
// `lox up` alone: its hint also lists the profiles and how to add one.
func TestStartHints(t *testing.T) {
	if StartCommand() != "lox up" {
		t.Errorf("start command %q", StartCommand())
	}
	if NoConsoleText != "no lockfile console is running: the console is started by `lox up` only: ask the user to run `lox up`" {
		t.Errorf("no console: %q", NoConsoleText)
	}
	if strings.Contains(NoConsoleText, "--profile") {
		t.Errorf("no console names a profile: %q", NoConsoleText)
	}
	h := UnknownProfileHint("other")
	if !strings.HasPrefix(h, "if profile other exists, its console is not running: ask the user to run `lox up`;") {
		t.Errorf("unknown profile does not lead with lox up: %q", h)
	}
	if h != "if profile other exists, its console is not running: ask the user to run `lox up`; otherwise use a profile listed by lockfile sources, or ask the user to set up profile other and declare it (lox add lockfile INSTANCE --profile other), then to run `lox up`" {
		t.Errorf("unknown profile: %q", h)
	}
	for _, s := range []string{NoConsoleText, h} {
		if strings.Contains(s, "lockfile console --") {
			t.Errorf("start hint names the console command: %q", s)
		}
	}
}
