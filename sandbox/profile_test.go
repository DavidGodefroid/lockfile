package sandbox

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// The seccomp profile shipped for Docker (seccomp.json, Docker's default
// profile from moby/profiles seccomp/v0.2.4) must deny by default and allow,
// unconditionally, what the console and the trampoline call to set up the
// sandbox: Landlock, seccomp, no_new_privs, the fd sweep, the rlimits and
// the exec of the plugin. test/docker runs it for real.
func TestDockerSeccompProfile(t *testing.T) {
	raw, err := os.ReadFile("../seccomp.json")
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		DefaultAction string `json:"defaultAction"`
		Syscalls      []struct {
			Names    []string        `json:"names"`
			Action   string          `json:"action"`
			Args     json.RawMessage `json:"args"`
			Includes json.RawMessage `json:"includes"`
			Excludes json.RawMessage `json:"excludes"`
		} `json:"syscalls"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.DefaultAction != "SCMP_ACT_ERRNO" {
		t.Fatalf("defaultAction = %q, want SCMP_ACT_ERRNO (never allow by default)", p.DefaultAction)
	}
	empty := func(m json.RawMessage) bool {
		return len(m) == 0 || string(m) == "null" || string(m) == "{}" || string(m) == "[]"
	}
	allowed := map[string]bool{}
	for _, r := range p.Syscalls {
		if r.Action == "SCMP_ACT_ALLOW" && empty(r.Args) && empty(r.Includes) && empty(r.Excludes) {
			for _, n := range r.Names {
				allowed[n] = true
			}
		}
	}
	for _, n := range []string{
		"landlock_create_ruleset", "landlock_add_rule", "landlock_restrict_self",
		"seccomp", "prctl", "close_range", "execve", "prlimit64", "setrlimit", "getrlimit",
		"wait4", "waitid", "pipe2", "mmap", "getpid",
	} {
		if !allowed[n] {
			t.Errorf("%s is not allowed unconditionally: the sandbox cannot be set up under this profile", n)
		}
	}
	// Threads (and the trampoline's probing fork) use clone without
	// namespace flags: the default profile allows that much without
	// CAP_SYS_ADMIN, and no more.
	for _, r := range p.Syscalls {
		if r.Action == "SCMP_ACT_ALLOW" && slices.Contains(r.Names, "clone") && !empty(r.Args) && !empty(r.Excludes) {
			return
		}
	}
	t.Errorf("no rule allows clone without namespace flags")
}
