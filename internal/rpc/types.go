// Package rpc holds the wire types of lockfile's console methods, shared by
// the console's backend and its clients (CLI and MCP). Every result is
// built from the closed list of design §7b.
package rpc

// Method names. lockkit serves hello, status, change.request and logout.
const (
	MethodPlugins = "file.plugins"
	MethodPlan    = "file.plan"
	MethodRun     = "file.run"
	MethodAnswers = "file.answers"
	MethodRelease = "file.release"
)

// Fixed texts of the errors that are not refusals: none carries anything
// the agent did not send. Clients print these, never the console's text.
const (
	MsgPlanParams = "invalid params: give exactly one of key and handle, 1 or more distinct plugins, an intent of at most 200 bytes"
	MsgParams     = "invalid params"
	MsgNoSuchPlan = "unknown, used or expired plan, or the policy changed since it was approved: plan again"
	MsgConnLost   = "the console lost its connection to the source; the human must restart or reconnect it"
	MsgPending    = "a policy change awaits the human in the console"
	MsgClosed     = "the console closed the connection"
	MsgInternal   = "console error"
)

// MaxIntent bounds the agent's one-line reason shown on the approval
// screen, in bytes.
const MaxIntent = 200

// PlanParams asks the human to approve a plan: a fetch of Key, or a
// follow-up on Handle (exactly one), and the plugins to run on it.
type PlanParams struct {
	Key     string   `json:"key,omitempty"`
	Handle  string   `json:"handle,omitempty"`
	Plugins []string `json:"plugins"`
	Intent  string   `json:"intent,omitempty"`
}

// PlanResult names an approved plan, run once with file.run.
type PlanResult struct {
	PlanID string `json:"plan_id"`
	// Answers is how many answers the plan reveals at most.
	Answers int `json:"answers"`
}

// RunParams runs an approved plan.
type RunParams struct {
	PlanID string `json:"plan_id"`
}

// HandleParams names a fetched file.
type HandleParams struct {
	Handle string `json:"handle"`
}

// Released answers file.release.
type Released struct {
	Released bool `json:"released"`
}

// PluginInfo is an installed, approved plugin as its signed manifest
// describes it.
type PluginInfo struct {
	Plugin      string `json:"plugin"`
	Version     string `json:"version"`
	Marketplace string `json:"marketplace"`
	Official    bool   `json:"official"`
	Warning     string `json:"warning,omitempty"`
	Question    string `json:"question"`
	One         string `json:"one"`
	Zero        string `json:"zero"`
}

// Status is lockfile's part of the status method.
type Status struct {
	Protocol int    `json:"protocol"`
	Sandbox  string `json:"sandbox"` // "ok" or "unavailable"
	// AnswerQuantumMS is the approved answer_quantum, in milliseconds (0:
	// unknown). Clients pace their run heartbeats on it.
	AnswerQuantumMS int64 `json:"answer_quantum_ms,omitempty"`
}

// MaxQuantumMS bounds the answer quantum a client accepts from a status.
const MaxQuantumMS = 3_600_000

// Source is a running console's profile.
type Source struct {
	Profile    string `json:"profile"`
	Production bool   `json:"production"`
	// Busy: the console is serving another request (a plan waiting for the
	// human, or a run until its quantum), so Production is not known yet.
	Busy bool `json:"busy,omitempty"`
}
