package rpc

import (
	"regexp"
	"slices"
	"strings"
)

// placeholderRe is a placeholder as the agent writes it: ${name}.
var placeholderRe = regexp.MustCompile(`^\$\{([a-z0-9_]{1,32})\}$`)

// Placeholder returns the name of placeholder s ("${email}": "email"), and
// whether s is one. Anything else bound to an input is a literal.
func Placeholder(s string) (string, bool) {
	m := placeholderRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// Bindings turns PlanParams.Inputs into input key -> placeholder name; ok
// is false when a value is not a placeholder (a literal).
func Bindings(raw map[string]string) (map[string]string, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		name, ok := Placeholder(v)
		if !ok {
			return nil, false
		}
		out[k] = name
	}
	return out, true
}

// SplitInputKey splits "<plugin>.<input>" at its last dot: an input name
// has none, a plugin name always has one.
func SplitInputKey(k string) (plugin, input string, ok bool) {
	i := strings.LastIndexByte(k, '.')
	if i <= 0 || i == len(k)-1 {
		return "", "", false
	}
	return k[:i], k[i+1:], true
}

// PlanCost is how many answers a plan of plugins with bindings (input key
// to placeholder name) reveals at most: one per plugin and distinct
// placeholder it is bound to, at least one per plugin. Every plugin still
// answers one bit; a plugin asked about several typed values counts once
// per value.
func PlanCost(plugins []string, bindings map[string]string) int {
	n := 0
	for _, p := range plugins {
		var names []string
		for k, name := range bindings {
			if pl, _, ok := SplitInputKey(k); ok && pl == p && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		n += max(1, len(names))
	}
	return n
}

// InputNames is the sorted, distinct placeholder names of bindings.
func InputNames(bindings map[string]string) []string {
	out := make([]string, 0, len(bindings))
	for _, n := range bindings {
		out = append(out, n)
	}
	slices.Sort(out)
	return slices.Compact(out)
}
