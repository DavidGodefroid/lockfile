package plugin

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// tempStore is a store in a temporary directory that the test can remove
// at the end although installed plugin directories are read-only (0500).
func tempStore(t *testing.T) Store {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() { makeWritable(dir) })
	return Store{Dir: dir}
}

func makeWritable(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
}
