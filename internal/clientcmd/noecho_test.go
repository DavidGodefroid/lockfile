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
	if h := unknownProfile("other").Refusal.Hint; h != UnknownProfileHint("other") || !strings.Contains(h, "lockfile profile add other,") {
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
	h := UnknownProfileHint("other")
	if h != "use a profile listed by lockfile sources, or ask the user to add profile other in the lockfile account (lockfile profile add other, then lox add lockfile) and to run `lox up`" {
		t.Errorf("unknown profile: %q", h)
	}
	for _, s := range []string{NoConsoleText, h} {
		if strings.Contains(s, "lockfile console --") || strings.Contains(s, "--profile") {
			t.Errorf("start hint names the console command: %q", s)
		}
	}
}
