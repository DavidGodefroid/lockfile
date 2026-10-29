//go:build unix

package main

import (
	"fmt"
	"os"

	"github.com/DavidGodefroid/lockfile/internal/idmigrate"
	"github.com/DavidGodefroid/lockfile/internal/product"
)

func init() {
	// Not in the help: the launcher runs it before the commands that mount
	// the volumes, as root with CAP_CHOWN and CAP_DAC_READ_SEARCH only. It
	// stays for one release (idmigrate).
	commands["migrate-ids"] = command{human: true, help: "", run: runMigrateIDs}
}

// geteuid is os.Geteuid; tests replace it.
var geteuid = os.Geteuid

func runMigrateIDs(e env, args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(e.stderr, "lockfile migrate-ids: no arguments")
		return product.ExitUsage
	}
	if geteuid() != 0 {
		fmt.Fprintln(e.stderr, "lockfile migrate-ids: runs as root in the image (the launcher runs it); nothing changed")
		return product.ExitError
	}
	n, err := idmigrate.Migrate(idmigrate.Dirs)
	if err != nil {
		fmt.Fprintf(e.stderr, "lockfile migrate-ids: %v (%d files moved to the new ids)\n", err, n)
		return product.ExitError
	}
	if n > 0 {
		fmt.Fprintf(e.stderr, "lockfile: %d files of the volumes moved to the account ids 10030-10032\n", n)
	}
	return product.ExitOK
}
