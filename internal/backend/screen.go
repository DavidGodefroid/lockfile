package backend

import (
	"fmt"

	"github.com/DavidGodefroid/lockkit/audit"
	"github.com/DavidGodefroid/lockkit/console"

	"github.com/DavidGodefroid/lockfile/internal/rpc"
	"github.com/DavidGodefroid/lockfile/pluginspec"
)

// screenPlugin is one plugin as the approval screen shows it: metadata
// from its signed manifest, never anything about the file.
type screenPlugin struct {
	Name, Version, Marketplace string
	Official                   bool
	Manifest                   pluginspec.Manifest
}

// planScreen is the approval screen of a plan (docs/usage.md, "The console";
// docs/security-model.md): the key the console will fetch and the plugins' questions. No size, type,
// hash or path: no source was touched yet. lockkit escapes every line and
// adds the production banner and typed profile name.
//
// key is the canonical key (percent-decoded and cleaned, what is fetched);
// typed is the agent's text, shown too when it differs, so the human never
// approves "invoices/%73alaries.pdf" without seeing "invoices/salaries.pdf".
//
// A plan binding plugin inputs to placeholders shows each question with
// its placeholders, the values the human will type on the approval and
// those this console holds already (when typed, on how many files
// replayed): names only, never a value. Each (plugin, placeholder) pair
// counts as one answer.
func planScreen(key, typed, handle string, plugins []screenPlugin, intent string, record func(audit.Base) audit.Record, bindings map[string]string, vt *vault) console.Screen {
	names := make([]string, 0, len(plugins))
	for _, p := range plugins {
		names = append(names, p.Name)
	}
	n := rpc.PlanCost(names, bindings)
	s := console.Screen{Note: intent, Record: record}
	if handle != "" {
		s.Header = fmt.Sprintf("follow-up on %s · %d answers", handle, n)
		s.Body = append(s.Body, "handle "+handle+" (no new fetch)")
	} else {
		s.Header = fmt.Sprintf("file plan · %d answers", n)
		s.Body = append(s.Body, "key    "+key)
		if typed != key {
			s.Body = append(s.Body, "typed  "+typed)
		}
	}
	s.Body = append(s.Body, "")
	for _, p := range plugins {
		s.Body = append(s.Body, fmt.Sprintf("%s@%s  (%s)", p.Name, p.Version, p.Marketplace))
		s.Body = append(s.Body, fmt.Sprintf("  ? %s   1 = %s   0 = %s", boundQuestion(p, bindings), p.Manifest.One, p.Manifest.Zero))
		if !p.Official {
			s.Warnings = append(s.Warnings, fmt.Sprintf("UNOFFICIAL MARKETPLACE %s: plugin %s", p.Marketplace, p.Name))
			s.NeverAuto = true
		}
	}
	if len(bindings) > 0 {
		s.Body = append(s.Body, "")
		s.Body = append(s.Body, inputLines(rpc.InputNames(bindings), vt)...)
		// Only a human types a value: never an auto rule's.
		s.NeverAuto = true
	}
	s.Body = append(s.Body, "", fmt.Sprintf("this plan reveals at most %d answers about the file", n))
	return s
}
