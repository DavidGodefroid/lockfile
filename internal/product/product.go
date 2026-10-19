// Package product holds lockfile's names for lockkit, its refusal codes and
// its command exit codes.
package product

import "github.com/DavidGodefroid/lockkit/kit"

// Version is the release version, set at build time with
// -ldflags "-X github.com/DavidGodefroid/lockfile/internal/product.Version=vX.Y.Z".
var Version = "dev"

// P is lockfile for lockkit: account, clients group, directories, sockets.
var P = kit.Product{
	Name:            "lockfile",
	Tagline:         "Your AI asks about the file. It never reads it.",
	Account:         "lockfile",
	ClientsGroup:    "lockfile-clients",
	KeychainService: "lockfile",
	EnvPrefix:       "LOCKFILE_",
	Version:         Version,
}

// Exit codes of the commands. They depend on the outcome only, never on an
// answer (docs/security-model.md).
const (
	ExitOK      = 0  // answered, or done
	ExitError   = 1  // no console running, or any other error
	ExitRefused = 2  // a refusal (E_* code, unknown_profile included)
	ExitDenied  = 3  // denied by the human, or no answer in time
	ExitUsage   = 64 // bad command line
)
