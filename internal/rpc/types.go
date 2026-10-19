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
}

// Source is a running console's profile.
type Source struct {
	Profile    string `json:"profile"`
	Production bool   `json:"production"`
}
