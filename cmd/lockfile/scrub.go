package main

import (
	"slices"
	"strconv"
	"strings"
)

// minScrub is the shortest value scrub replaces: a shorter one is no
// secret, and replacing it would garble the message.
const minScrub = 4

// scrub replaces in msg every value typed on the command line (as is, as
// %q quotes it, and each comma-separated part) with <value>: for errors of
// lockkit that quote a flag value, which may be a token or a URL holding
// one.
func scrub(msg string, values ...string) string {
	var parts []string
	for _, v := range values {
		parts = append(parts, v)
		for _, p := range strings.Split(v, ",") {
			parts = append(parts, p, strings.TrimSpace(p))
		}
	}
	// Longest first, so that a part never splits a whole value.
	slices.SortFunc(parts, func(a, b string) int { return len(b) - len(a) })
	for _, v := range parts {
		if len(v) < minScrub {
			continue
		}
		q := strconv.Quote(v)
		msg = strings.ReplaceAll(msg, q[1:len(q)-1], "<value>")
		msg = strings.ReplaceAll(msg, v, "<value>")
	}
	return msg
}

// typedValues are the values in args that an error may quote: the
// arguments that are not flags, the VALUE of -flag=VALUE, and the name of
// a flag not in known (the flag package quotes an unknown flag), except
// those keep accepts (a valid name, quoted on purpose).
func typedValues(args []string, known []string, keep func(string) bool) []string {
	var out []string
	add := func(v string) {
		if keep == nil || !keep(v) {
			out = append(out, v)
		}
	}
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			add(a)
			continue
		}
		name, v, ok := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !slices.Contains(known, name) {
			out = append(out, name)
		}
		if ok {
			add(v)
		}
	}
	return out
}
