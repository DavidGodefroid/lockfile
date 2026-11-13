package testenv

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/DavidGodefroid/lockfile/internal/plugin"
)

// RepoRoot is the lockfile repository root.
func RepoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

var (
	buildMu    sync.Mutex
	buildCache = map[string][]byte{}
)

// BuildPlugin builds sandbox/testdata/plugins/<name> once per test binary.
func BuildPlugin(t testing.TB, name string) []byte {
	t.Helper()
	buildMu.Lock()
	defer buildMu.Unlock()
	if b, ok := buildCache[name]; ok {
		return b
	}
	dir, err := os.MkdirTemp("", "lockfile-plugin-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", out, "./sandbox/testdata/plugins/"+name)
	cmd.Dir = RepoRoot()
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	buildCache[name] = b
	return b
}

// TempStore is a plugin store the test can remove although installed
// plugin directories are read-only (0500).
func TempStore(t testing.TB) plugin.Store {
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

// Manifest is a valid manifest for a test plugin.
func Manifest(name, question string, timeoutS int) []byte {
	return fmt.Appendf(nil, `name = %q
version = "1.0.0"
protocol = 1
question = %q
one = "yes"
zero = "no"
accepts = ["*/*"]
limits = { cpu_s = 5, memory = "256MiB", timeout_s = %d }
`, name, question, timeoutS)
}

func hexsum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// Install installs bin as the plugin qualified (marketplace/name, or a
// bare name for the official marketplace) at version 1.0.0.
func Install(t testing.TB, s plugin.Store, qualified string, bin []byte, question string, timeoutS int) plugin.LockEntry {
	t.Helper()
	return InstallManifest(t, s, qualified, bin, func(bare string) []byte { return Manifest(bare, question, timeoutS) })
}

// InputsManifest is a valid protocol-2 manifest with one input, needle, of
// kind kind, named in its question, and detail on.
func InputsManifest(name, kind string) []byte {
	return fmt.Appendf(nil, `name = %q
version = "1.0.0"
protocol = 2
question = "Does the file contain ${needle}?"
one = "yes"
zero = "no"
accepts = ["*/*"]
inputs = [{ name = "needle", kind = %q, max_bytes = 64 }]
detail = true
limits = { cpu_s = 5, memory = "256MiB", timeout_s = 5 }
`, name, kind)
}

// InstallManifest is Install with the manifest man gives for the bare
// name.
func InstallManifest(t testing.TB, s plugin.Store, qualified string, bin []byte, man func(bare string) []byte) plugin.LockEntry {
	t.Helper()
	marketplace, official := plugin.OfficialMarketplace, true
	bare := qualified
	if i := strings.LastIndex(qualified, "/"); i >= 0 {
		marketplace, official, bare = qualified[:i], false, qualified[i+1:]
	}
	m := man(bare)
	e := plugin.LockEntry{Name: qualified, Version: "1.0.0", Marketplace: marketplace, Official: official,
		BinarySHA256: hexsum(bin), ManifestSHA256: hexsum(m)}
	if err := s.Install(e, bin, m); err != nil {
		t.Fatal(err)
	}
	return e
}
