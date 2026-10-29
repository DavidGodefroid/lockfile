//go:build unix

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockfile/internal/product"
)

// migrate-ids refuses arguments and any account but root, and is not in
// the help.
func TestMigrateIDsRefusals(t *testing.T) {
	old := geteuid
	t.Cleanup(func() { geteuid = old })
	geteuid = func() int { return 10031 }
	var out, errb bytes.Buffer
	e := env{stdout: &out, stderr: &errb}
	if got := run(e, []string{"migrate-ids", "/etc"}); got != product.ExitUsage {
		t.Fatalf("migrate-ids /etc = %d, want %d", got, product.ExitUsage)
	}
	errb.Reset()
	if got := run(e, []string{"migrate-ids"}); got != product.ExitError || !strings.Contains(errb.String(), "runs as root") {
		t.Fatalf("migrate-ids as 10031 = %d %q", got, errb.String())
	}
	if strings.Contains(usage(), "migrate-ids") {
		t.Fatal("migrate-ids is in the help")
	}
}
