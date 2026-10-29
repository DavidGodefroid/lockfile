package product

import (
	"fmt"

	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/ipc"
	"github.com/DavidGodefroid/lockkit/refusal"

	"github.com/DavidGodefroid/lockfile/internal/answers"
)

type refusalDef struct {
	code  refusal.Code
	retry refusal.Retry
	title string
	hint  string
}

// refusals maps every closed agent code that refuses a request to its
// lockkit refusal. plugin_error is per answer, and the approval outcomes
// use lockkit's own codes: none of them is here.
var refusals = map[answers.Code]refusalDef{
	answers.CodeKeyOutsidePrefix: {"E_KEY_OUTSIDE_PREFIX", refusal.RetryRewrite,
		"the key is outside the profile's prefix", "use the exact object key under the profile's prefix"},
	answers.CodeUnknownProfile: {"E_UNKNOWN_PROFILE", refusal.RetryRewrite,
		"no running console serves this profile", "use a profile listed by lockfile sources, or ask the user to start its console"},
	answers.CodeFetchFailed: {"E_FETCH_FAILED", refusal.RetryStop,
		"the file could not be fetched", "tell the user; the cause is in the lockfile audit, which only the human reads"},
	answers.CodeTooManyPlugins: {"E_TOO_MANY_PLUGINS", refusal.RetryRewrite,
		"too many answers for one file", "ask fewer questions; the profile caps the answers per fetched file"},
	answers.CodePluginNotInstalled: {"E_PLUGIN_NOT_INSTALLED", refusal.RetryAskUser,
		"a plugin is not installed", "use a plugin listed by lockfile plugins, or ask the user to install it"},
	answers.CodePluginTampered: {"E_PLUGIN_TAMPERED", refusal.RetryStop,
		"an installed plugin does not match its lock entry", "tell the user to run lockfile plugin verify"},
	answers.CodePluginYanked: {"E_PLUGIN_YANKED", refusal.RetryAskUser,
		"a plugin version was withdrawn by its marketplace", "ask the user to update the plugin"},
	answers.CodeSandboxUnavailable: {"E_SANDBOX_UNAVAILABLE", refusal.RetryStop,
		"the plugin sandbox is unavailable", "tell the user to run lockfile doctor"},
	answers.CodeProcNotHidden: {"E_PROC_NOT_HIDDEN", refusal.RetryStop,
		"other accounts can watch the plugins' processes in /proc", "tell the user to run lockfile doctor"},
	answers.CodeHandleExpired: {"E_HANDLE_EXPIRED", refusal.RetryRewrite,
		"the handle expired or was released", "plan again with the key"},
	answers.CodeStale: {"E_STALE", refusal.RetryRewrite,
		"the file changed since the plan was approved", "plan again with the key: the new content needs a new approval"},
}

func init() {
	for _, d := range refusals {
		refusal.Register(d.code, d.retry, d.title, d.hint)
	}
}

// Mapped reports whether c refuses a request (and so has a lockkit code).
func Mapped(c answers.Code) bool {
	_, ok := refusals[c]
	return ok
}

// Code is the lockkit refusal code of c. It panics on a code that does not
// refuse a request: a bug, never a runtime condition.
func Code(c answers.Code) refusal.Code {
	d, ok := refusals[c]
	if !ok {
		panic(fmt.Sprintf("product: %q is not a refusal code", c))
	}
	return d.code
}

// Refuse is the response refusing request id with c: the message is the
// closed code itself, the data its code, retry rule and static hint.
func Refuse(id int64, c answers.Code) ipc.Response {
	rc := Code(c)
	info := refusal.Info{Code: rc, Retry: rc.Retry(), Hint: rc.Hint()}
	r := console.ErrResp(id, ipc.CodeRefused, string(c))
	r.Error.Data = &info
	return r
}

// ApprovalRefusal replaces lockkit's refusal of an approval with a fixed
// message: the outcome only (docs/security-model.md), never the human's typed reason,
// which lockkit already keeps in the audit.
func ApprovalRefusal(id int64, r ipc.Response) ipc.Response {
	if r.Error != nil && r.Error.Code == ipc.CodeTimeout {
		return console.ErrResp(id, ipc.CodeTimeout, string(answers.CodeApprovalTimeout))
	}
	return console.ErrResp(id, ipc.CodeDenied, string(answers.CodeApprovalDenied))
}
