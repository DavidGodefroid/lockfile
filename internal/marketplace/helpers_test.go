package marketplace

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/DavidGodefroid/lockfile/internal/plugin"
)

// tempStore is a store in a temporary directory that the test can remove
// at the end although installed plugin directories are read-only (0500).
func tempStore(t *testing.T) plugin.Store {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(p, 0o700)
			}
			return nil
		})
	})
	return plugin.Store{Dir: dir}
}
