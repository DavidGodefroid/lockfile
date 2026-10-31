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
		parts = append(parts, strings.Split(v, ",")...)
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

// typedValues are the values in args: the arguments that are not flags,
// and the VALUE of -flag=VALUE.
func typedValues(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		} else if _, v, ok := strings.Cut(a, "="); ok {
			out = append(out, v)
		}
	}
	return out
}
