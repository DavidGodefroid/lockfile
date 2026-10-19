package backend

import (
	"fmt"

	"github.com/DavidGodefroid/lockkit/audit"
	"github.com/DavidGodefroid/lockkit/console"

	"github.com/DavidGodefroid/lockfile/pluginspec"
)

// screenPlugin is one plugin as the approval screen shows it: metadata
// from its signed manifest, never anything about the file.
type screenPlugin struct {
	Name, Version, Marketplace string
	Official                   bool
	Manifest                   pluginspec.Manifest
}

// planScreen is the approval screen of a plan (design §3 step 3, §7b): the
// agent's request as typed and the plugins' questions. No size, type, hash
// or path: no source was touched yet. lockkit escapes every line and adds
// the production banner and typed profile name.
func planScreen(key, handle string, plugins []screenPlugin, intent string, record func(audit.Base) audit.Record) console.Screen {
	n := len(plugins)
	s := console.Screen{Note: intent, Record: record}
	if handle != "" {
		s.Header = fmt.Sprintf("follow-up on %s · %d answers", handle, n)
		s.Body = append(s.Body, "handle "+handle+" (no new fetch)")
	} else {
		s.Header = fmt.Sprintf("file plan · %d answers", n)
		s.Body = append(s.Body, "key    "+key)
	}
	s.Body = append(s.Body, "")
	for _, p := range plugins {
		s.Body = append(s.Body, fmt.Sprintf("%s@%s  (%s)", p.Name, p.Version, p.Marketplace))
		s.Body = append(s.Body, fmt.Sprintf("  ? %s   1 = %s   0 = %s", p.Manifest.Question, p.Manifest.One, p.Manifest.Zero))
		if !p.Official {
			s.Warnings = append(s.Warnings, fmt.Sprintf("UNOFFICIAL MARKETPLACE %s: plugin %s", p.Marketplace, p.Name))
			s.NeverAuto = true
		}
	}
	s.Body = append(s.Body, "", fmt.Sprintf("this plan reveals at most %d answers about the file", n))
	return s
}
