package wiring

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitClaude(t *testing.T) {
	root := t.TempDir()
	if _, err := Wiring.Init(root, "claude"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if s := m.MCPServers["lockfile"]; s.Command != "lockfile" || len(s.Args) != 1 || s.Args[0] != "mcp" {
		t.Fatalf(".mcp.json: %s", b)
	}
	skill, err := os.ReadFile(filepath.Join(root, ".claude", "skills", "lockfile", "SKILL.md"))
	if err != nil || !strings.HasPrefix(string(skill), "---\nname: lockfile\n") {
		t.Fatalf("skill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".lockfile")); !os.IsNotExist(err) {
		t.Fatalf(".lockfile created: %v", err)
	}
}

func TestClaudeAllowTouchesNoData(t *testing.T) {
	for _, a := range ClaudeAllow {
		for _, w := range []string{"plan", "run", "answers", "release"} {
			if strings.Contains(a, "lockfile_"+w) || strings.Contains(a, "lockfile "+w) {
				t.Errorf("%s allows %s without a prompt", a, w)
			}
		}
	}
}

func TestTextsSayNullIsNotZero(t *testing.T) {
	for name, b := range map[string][]byte{"skill": Skill, "instructions": Instructions} {
		if !strings.Contains(string(b), "not** a 0") && !strings.Contains(string(b), "It is not a 0") {
			t.Errorf("%s does not say that null is not a 0", name)
		}
	}
}
