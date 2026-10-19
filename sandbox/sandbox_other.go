//go:build !linux

package sandbox

import (
	"context"
	"os"

	"github.com/DavidGodefroid/lockfile/pluginspec"
)

// Run refuses: plugins run on Linux only.
func Run(context.Context, Spec) (pluginspec.Outcome, Report, error) {
	return pluginspec.Outcome{}, Report{}, ErrUnsupported
}

// Main exits a trampoline invocation; there is no sandbox here.
func Main() {
	if len(os.Args) >= 2 && os.Args[1] == trampolineArg {
		os.Exit(126)
	}
}

// Probe reports that this platform cannot sandbox plugins.
func Probe() (int, bool, error) { return 0, false, ErrUnsupported }
