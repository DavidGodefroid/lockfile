package clientcmd

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
	"github.com/DavidGodefroid/lockfile/pluginspec"
)

// The shapes of the values a console sends back. A successful result is
// forwarded only when each of its strings has its shape; anything else is
// "console error", as for an unrecognised error.
var (
	planIDRe      = regexp.MustCompile(`^pl_[0-9A-Za-z]{10}$`)
	handleRe      = regexp.MustCompile(`^f_[0-9A-Za-z]{10}$`)
	marketplaceRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// maxText bounds a manifest text (question, meaning of 1 and of 0).
const maxText = 1024

func validPlanID(s string) bool { return planIDRe.MatchString(s) }
func validHandle(s string) bool { return handleRe.MatchString(s) }

// validPlugin is a plugin name as the store qualifies it: bare, or
// "marketplace/name".
func validPlugin(s string) bool {
	if m, n, ok := strings.Cut(s, "/"); ok {
		return marketplaceRe.MatchString(m) && pluginspec.ValidName(n)
	}
	return pluginspec.ValidName(s)
}

// validText is a manifest text: valid UTF-8, not blank, bounded, without
// control characters.
func validText(s string) bool {
	if s == "" || len(s) > maxText || !utf8.ValidString(s) || strings.TrimSpace(s) == "" {
		return false
	}
	return !strings.ContainsFunc(s, unicode.IsControl)
}

func validPluginInfo(p rpc.PluginInfo) bool {
	return validPlugin(p.Plugin) && pluginspec.ValidVersion(p.Version) && marketplaceRe.MatchString(p.Marketplace) &&
		validText(p.Question) && validText(p.One) && validText(p.Zero) &&
		(p.Warning == "" || p.Warning == answers.WarningUnofficial)
}

func validStatus(s rpc.Status) bool {
	return s.Sandbox == "ok" || s.Sandbox == "unavailable"
}

// validResult checks the handle and each answer's plugin and version; the
// values themselves are closed by result.
func validResult(r answers.Result) bool {
	if !validHandle(r.Handle) {
		return false
	}
	for _, a := range r.Answers {
		if !validPlugin(a.Plugin) || !pluginspec.ValidVersion(a.Version) {
			return false
		}
	}
	return true
}
