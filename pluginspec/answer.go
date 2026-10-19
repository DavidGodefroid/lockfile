package pluginspec

// AgentErrorCode is the only error an agent sees for a plugin-caused
// failure, whatever the cause.
const AgentErrorCode = "plugin_error"

// FailureCause is why a plugin gave no answer. It is for the audit only.
type FailureCause string

const (
	CauseNone          FailureCause = ""
	CauseExitCode      FailureCause = "exit_code"
	CauseSignal        FailureCause = "signal"
	CauseTimeout       FailureCause = "timeout"
	CauseInvalidOutput FailureCause = "invalid_output"
	CauseOutputTooLong FailureCause = "output_too_long"
	CauseSandbox       FailureCause = "sandbox"
)

// Outcome is the result of one plugin run: an answer, or no answer and a
// cause. Answer is nil exactly when Cause is not CauseNone.
type Outcome struct {
	Answer *bool
	Cause  FailureCause
}

// Failed builds an outcome without an answer.
func Failed(c FailureCause) Outcome { return Outcome{Cause: c} }

// ParseAnswer applies the contract to what a plugin process produced:
// stdout must be exactly "1" or "0", optionally followed by one "\n", and
// the process must have exited with status 0. truncated reports that the
// plugin wrote more than the read cap.
func ParseAnswer(stdout []byte, truncated bool, exitCode int, signaled bool) Outcome {
	switch {
	case signaled:
		return Failed(CauseSignal)
	case exitCode != 0:
		return Failed(CauseExitCode)
	case truncated:
		return Failed(CauseOutputTooLong)
	}
	var v bool
	switch string(stdout) {
	case "1", "1\n":
		v = true
	case "0", "0\n":
		v = false
	default:
		return Failed(CauseInvalidOutput)
	}
	return Outcome{Answer: &v}
}
