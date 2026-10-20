package answers

// Code is an agent-visible error or refusal code. The list is closed
// (design §7b): nothing else about a request or a file reaches the agent.
type Code string

const (
	CodePluginError        Code = "plugin_error"
	CodeFetchFailed        Code = "fetch_failed"
	CodeKeyOutsidePrefix   Code = "key_outside_prefix"
	CodeUnknownProfile     Code = "unknown_profile"
	CodeTooManyPlugins     Code = "too_many_plugins"
	CodePluginNotInstalled Code = "plugin_not_installed"
	CodePluginTampered     Code = "plugin_tampered"
	CodePluginYanked       Code = "plugin_yanked"
	CodeSandboxUnavailable Code = "sandbox_unavailable"
	CodeProcNotHidden      Code = "proc_not_hidden"
	CodeApprovalDenied     Code = "approval_denied"
	CodeApprovalTimeout    Code = "approval_timeout"
	CodeHandleExpired      Code = "handle_expired"
)

// Codes is the closed list.
var Codes = []Code{
	CodePluginError, CodeFetchFailed, CodeKeyOutsidePrefix, CodeUnknownProfile, CodeTooManyPlugins,
	CodePluginNotInstalled, CodePluginTampered, CodePluginYanked, CodeSandboxUnavailable,
	CodeProcNotHidden,
	CodeApprovalDenied, CodeApprovalTimeout, CodeHandleExpired,
}

// Valid reports whether c is in the closed list.
func (c Code) Valid() bool {
	for _, x := range Codes {
		if c == x {
			return true
		}
	}
	return false
}
