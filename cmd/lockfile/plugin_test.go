package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/sysconf"
	"github.com/DavidGodefroid/lockkit/update"

	"github.com/DavidGodefroid/lockfile/index"
	"github.com/DavidGodefroid/lockfile/internal/plugin"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/verify"
)

type okVerifier struct{ roots []verify.TrustRoot }

func (v *okVerifier) VerifyBlob(_ context.Context, _, bundle string, root verify.TrustRoot) error {
	v.roots = append(v.roots, root)
	_, err := os.Stat(bundle)
	return err
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func pluginManifest(version, question string) string {
	return fmt.Sprintf("name = \"pdf.parses\"\nversion = %q\nprotocol = 1\nquestion = %q\none = \"it parses\"\nzero = \"it does not\"\n"+
		"accepts = [\"application/pdf\"]\nlimits = { cpu_s = 5, memory = \"64MiB\", timeout_s = 5 }\n", version, question)
}

// fakeIndex is the index the last fakeMarket serves; tests may change it.
var fakeIndex *index.Index

// fakeMarket serves a signed-looking index of pdf.parses 1.0.0, 1.0.1 (same
// question) and 1.1.0 (another question), for this platform.
func fakeMarket(t *testing.T, name string) *httptest.Server {
	t.Helper()
	files := map[string][]byte{"/index.json.sigstore.json": []byte("{}")}
	idx := &index.Index{}
	fakeIndex = idx
	t.Cleanup(func() { fakeIndex = nil })
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.json" {
			b, _ := json.Marshal(idx)
			w.Write(b)
			return
		}
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	p := index.Plugin{Name: "pdf.parses"}
	for _, v := range []struct{ version, question string }{
		{"1.0.0", "Does the file parse as a PDF?"},
		{"1.0.1", "Does the file parse as a PDF?"},
		{"1.1.0", "Does the file parse as a PDF 2.0?"},
	} {
		man := []byte(pluginManifest(v.version, v.question))
		bin := []byte("\x7fELF synthetic " + v.version)
		d := "/pdf.parses-" + v.version
		files[d+"/plugin.toml"], files[d+"/plugin.toml.sigstore.json"] = man, []byte("{}")
		files[d+"/bin"], files[d+"/bin.sigstore.json"] = bin, []byte("{}")
		p.Versions = append(p.Versions, index.Version{
			Version: v.version, ManifestURL: srv.URL + d + "/plugin.toml", ManifestSHA256: sum(man),
			ManifestBundleURL: srv.URL + d + "/plugin.toml.sigstore.json",
			Artifacts:         []index.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, URL: srv.URL + d + "/bin", SHA256: sum(bin), BundleURL: srv.URL + d + "/bin.sigstore.json"}},
		})
	}
	*idx = index.Index{Schema: 1, Marketplace: name, Generated: time.Now().UTC(), Plugins: []index.Plugin{p}}
	return srv
}

// humanEnv points the human commands at temp paths, a fake marketplace
// and a verifier that accepts every bundle.
func humanEnv(t *testing.T, market string) (*humanHooks, *httptest.Server) {
	t.Helper()
	srv := fakeMarket(t, market)
	state := t.TempDir()
	// Installed plugins are read-only: open them up before TempDir removes them.
	t.Cleanup(func() { _ = plugin.Store{Dir: state}.RemoveAll() })
	h := &humanHooks{
		configPath: filepath.Join(t.TempDir(), "config.toml"), stateDir: state,
		sys: func() (*sysconf.Config, error) { return nil, nil }, http: srv.Client(), verifier: &okVerifier{},
		officialIndex: srv.URL + "/index.json",
	}
	testHumanHooks = h
	t.Cleanup(func() { testHumanHooks = nil })
	return h, srv
}

func installedVersion(t *testing.T, h *humanHooks, name string) string {
	t.Helper()
	s := plugin.Store{Dir: filepath.Join(h.stateDir, product.P.Name)}
	in, err := s.Installed()
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range in {
		if x.Name == name {
			return x.Version
		}
	}
	return ""
}

func TestPluginInstallFromFakeMarketplace(t *testing.T) {
	h, _ := humanEnv(t, "official")
	code, out, errText := runArgs(t, "", "plugin", "install", "pdf.parses@1.0.0")
	if code != 0 || !strings.Contains(out, "? Does the file parse as a PDF?") || !strings.Contains(out, "1 = it parses") {
		t.Fatalf("exit %d: %s %s", code, out, errText)
	}
	if v := installedVersion(t, h, "pdf.parses"); v != "1.0.0" {
		t.Fatalf("installed %q", v)
	}
	if entries, _ := os.ReadDir(filepath.Join(h.stateDir, product.P.Name)); len(entries) == 0 {
		t.Fatal("nothing in the store")
	} else {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".staging-") {
				t.Fatalf("staging dir left: %s", e.Name())
			}
		}
	}
	if code, _, errText := runArgs(t, "", "plugin", "install", "pdf.parses@1.0.1"); code != product.ExitError || !strings.Contains(errText, "plugin update") {
		t.Fatalf("second install exit %d: %s", code, errText)
	}
	code, out, _ = runArgs(t, "", "plugin", "list")
	if code != 0 || !strings.Contains(out, "pdf.parses@1.0.0  (official)") {
		t.Fatalf("list %d: %s", code, out)
	}
	if code, out, _ := runArgs(t, "", "plugin", "verify"); code != 0 || !strings.Contains(out, "ok pdf.parses@1.0.0") {
		t.Fatalf("verify %d: %s", code, out)
	}
	// Tamper with the binary: verify says so, exit 2.
	bin := filepath.Join(h.stateDir, product.P.Name, "plugins", "pdf.parses", "1.0.0", "plugin")
	if err := os.Chmod(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("evil"), 0o700); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runArgs(t, "", "plugin", "verify", "pdf.parses"); code != product.ExitRefused || !strings.Contains(out, "TAMPERED") {
		t.Fatalf("tampered verify %d: %s", code, out)
	}
	if code, _, _ := runArgs(t, "", "plugin", "remove", "pdf.parses"); code != 0 || installedVersion(t, h, "pdf.parses") != "" {
		t.Fatalf("remove %d", code)
	}
}

func TestPluginUpdateQuestionChangeNeedsTypedName(t *testing.T) {
	h, _ := humanEnv(t, "official")
	if code, _, errText := runArgs(t, "", "plugin", "install", "pdf.parses@1.0.0"); code != 0 {
		t.Fatal(errText)
	}
	// Same question: y is enough.
	if code, out, _ := runArgs(t, "y\n", "plugin", "update", "pdf.parses@1.0.1"); code != 0 || installedVersion(t, h, "pdf.parses") != "1.0.1" {
		t.Fatalf("update 1.0.1 exit %d: %s", code, out)
	}
	// Another question: y is not enough.
	code, out, _ := runArgs(t, "y\n", "plugin", "update", "pdf.parses@1.1.0")
	if code != product.ExitDenied || installedVersion(t, h, "pdf.parses") != "1.0.1" || !strings.Contains(out, `question: "Does the file parse as a PDF?" -> "Does the file parse as a PDF 2.0?"`) {
		t.Fatalf("update 1.1.0 with y: exit %d: %s", code, out)
	}
	if code, out, _ := runArgs(t, "pdf.parses\n", "plugin", "update", "pdf.parses@1.1.0"); code != 0 || installedVersion(t, h, "pdf.parses") != "1.1.0" {
		t.Fatalf("update 1.1.0 typed: exit %d: %s", code, out)
	}
}

// A version yanked after install: plugin verify reads the signed index,
// records it in plugins.lock, and the store refuses to open it.
func TestPluginYankedAfterInstall(t *testing.T) {
	h, _ := humanEnv(t, "official")
	if code, _, errText := runArgs(t, "", "plugin", "install", "pdf.parses@1.0.0"); code != 0 {
		t.Fatal(errText)
	}
	fakeIndex.Plugins[0].Versions[0].Yanked = true
	fakeIndex.Generated = fakeIndex.Generated.Add(time.Minute)
	code, out, _ := runArgs(t, "", "plugin", "verify")
	if code != product.ExitRefused || !strings.Contains(out, "YANKED pdf.parses@1.0.0") {
		t.Fatalf("verify %d: %s", code, out)
	}
	s := plugin.Store{Dir: filepath.Join(h.stateDir, product.P.Name)}
	if _, _, err := s.Open("pdf.parses"); !errors.Is(err, plugin.ErrYanked) {
		t.Fatalf("open after yank: %v", err)
	}
	if code, out, _ := runArgs(t, "", "plugin", "list"); code != 0 || !strings.Contains(out, "YANKED") {
		t.Fatalf("list %d: %s", code, out)
	}
	// Updating to a version that is not yanked clears it.
	if code, out, _ := runArgs(t, "y\n", "plugin", "update", "pdf.parses@1.0.1"); code != 0 {
		t.Fatalf("update %d: %s", code, out)
	}
	f, _, err := s.Open("pdf.parses")
	if err != nil {
		t.Fatalf("open after update: %v", err)
	}
	f.Close()
}

func TestPluginInstallUnofficial(t *testing.T) {
	h, srv := humanEnv(t, "acme")
	if err := os.WriteFile(h.configPath, []byte("[[marketplaces]]\nname = \"acme\"\nindex_url = \""+srv.URL+"/index.json\"\nidentity_regexp = \"^https://example\\\\.org/\"\noidc_issuer = \"https://issuer.example.org\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errText := runArgs(t, "", "plugin", "install", "acme/pdf.parses@1.0.0")
	if code != 0 || !strings.Contains(out, "UNOFFICIAL MARKETPLACE acme") || installedVersion(t, h, "acme/pdf.parses") != "1.0.0" {
		t.Fatalf("exit %d: %s %s", code, out, errText)
	}
	roots := h.verifier.(*okVerifier).roots
	if len(roots) == 0 || roots[0].IdentityRegexp != `^https://example\.org/` {
		t.Fatalf("roots %+v", roots)
	}
}

func TestNotServiceAccountRefused(t *testing.T) {
	h, _ := humanEnv(t, "official")
	h.sys = func() (*sysconf.Config, error) { return &sysconf.Config{ServiceUser: "root"}, nil }
	h.euid = func() int { return 4242 }
	for _, args := range [][]string{
		{"plugin", "install", "pdf.parses@1.0.0"},
		{"plugin", "remove", "pdf.parses"},
		{"marketplace", "add", "acme", "--index", "https://example.org/index.json", "--identity-regexp", "^https://example.org/"},
		{"marketplace", "remove", "acme"},
	} {
		code, _, errText := runArgs(t, "acme\n", args...)
		if code != product.ExitError || !strings.Contains(errText, "run this in the root account") {
			t.Fatalf("%v: exit %d: %s", args, code, errText)
		}
	}
}

func TestCosignMissingRefusesInstall(t *testing.T) {
	h, _ := humanEnv(t, "official")
	t.Setenv("PATH", t.TempDir())
	h.verifier = verify.NewLockkit(update.Options{})
	code, _, errText := runArgs(t, "", "plugin", "install", "pdf.parses@1.0.0")
	if code != product.ExitError || !strings.Contains(errText, "install cosign") || installedVersion(t, h, "pdf.parses") != "" {
		t.Fatalf("exit %d: %s", code, errText)
	}
}
