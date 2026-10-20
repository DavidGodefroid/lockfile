package marketplace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// ErrRollback refuses a signed index older than one already seen.
var ErrRollback = errors.New("marketplace: index older than the last one seen")

const freshFile = "marketplaces.state.json"

// checkFresh refuses an index of marketplace generated before the last one
// verified for it, then records generated as the last one. The state file
// lives in dir (the plugin store, in the lockfile account).
func checkFresh(dir, marketplace string, generated time.Time) error {
	path := filepath.Join(dir, freshFile)
	seen := map[string]time.Time{}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(b, &seen); err != nil {
			return fmt.Errorf("marketplace: %s: %w", path, err)
		}
	}
	last, ok := seen[marketplace]
	if ok && generated.Before(last) {
		return fmt.Errorf("%w: %s index generated %s, last seen %s (a replayed index; nothing changed)",
			ErrRollback, marketplace, generated.UTC().Format(time.RFC3339), last.UTC().Format(time.RFC3339))
	}
	if ok && generated.Equal(last) {
		return nil
	}
	seen[marketplace] = generated.UTC()
	out, err := json.Marshal(seen)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".marketplaces.state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
