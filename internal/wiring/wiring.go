// Package wiring holds lockfile's agent texts (skill, instructions, Cursor
// rule) and their wiring through lockkit's agentwire.
package wiring

import (
	_ "embed"

	"github.com/DavidGodefroid/lockkit/agentwire"

	"github.com/DavidGodefroid/lockfile/internal/product"
)

var (
	//go:embed skill.md
	Skill []byte
	//go:embed agents.md
	Instructions []byte
	//go:embed cursor.mdc
	CursorFrontMatter []byte
)

// ClaudeAllow lists what Claude Code may call without a prompt: only calls
// that touch no file. Plans, runs and answers stay with the user.
var ClaudeAllow = []string{
	"mcp__lockfile__lockfile_status", "mcp__lockfile__lockfile_sources", "mcp__lockfile__lockfile_plugins",
	"Bash(lockfile status:*)", "Bash(lockfile sources:*)", "Bash(lockfile plugins:*)",
}

// Wiring wires the agents to lockfile. There is no project config:
// profiles live in the lockfile account.
var Wiring = agentwire.Wiring{Product: product.P, Assets: agentwire.Assets{
	MCPCommand:        []string{"lockfile", "mcp"},
	Skill:             Skill,
	Instructions:      Instructions,
	CursorFrontMatter: CursorFrontMatter,
	ClaudeAllow:       ClaudeAllow,
	ClaudeNote:        "Plans are approved in the lockfile console, not in Claude Code.",
}}
