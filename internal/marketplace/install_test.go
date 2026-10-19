package marketplace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockfile/index"
	"github.com/DavidGodefroid/lockfile/internal/plugin"
	"github.com/DavidGodefroid/lockfile/internal/verify"
	"github.com/DavidGodefroid/lockfile/pluginspec"
)

const manifest = `name = "pdf.parses"
version = "1.0.0"
protocol = 1
question = "Does the file parse as a PDF without errors?"
one = "it parses"
zero = "it does not"
accepts = ["application/pdf"]
limits = { cpu_s = 5, memory = "64MiB", timeout_s = 5 }
`

var binary = []byte("\x7fELF synthetic plugin binary")

func hexsum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// fakeVerifier fails for blobs whose base name is in fail, and records
// what it was asked to verify.
type fakeVerifier struct {
	fail  map[string]bool
	calls []string
	roots []verify.TrustRoot
}

func (f *fakeVerifier) VerifyBlob(_ context.Context, blob, bundle string, root verify.TrustRoot) error {
	f.calls = append(f.calls, filepath.Base(blob))
	f.roots = append(f.roots, root)
	if _, err := os.Stat(bundle); err != nil {
		return err
	}
	if f.fail[filepath.Base(blob)] {
		return errors.New("bad signature")
	}
	return nil
}

type market struct {
	srv      *httptest.Server
	files    map[string][]byte
	idx      index.Index
	verifier *fakeVerifier
}

func newMarket(t *testing.T, name string) *market {
	t.Helper()
	m := &market{files: map[string][]byte{}, verifier: &fakeVerifier{fail: map[string]bool{}}}
	m.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.json" {
			b, _ := json.Marshal(m.idx)
			w.Write(b)
			return
		}
		b, ok := m.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(m.srv.Close)
	u := m.srv.URL
	m.files["/index.json.sigstore.json"] = []byte("{}")
	m.files["/pdf.parses-1.0.0/plugin.toml"] = []byte(manifest)
	m.files["/pdf.parses-1.0.0/pdf.parses-1.0.0-linux-amd64"] = binary
	m.files["/pdf.parses-1.0.0/pdf.parses-1.0.0-linux-amd64.sigstore.json"] = []byte("{}")
	m.idx = index.Index{Schema: 1, Marketplace: name, Generated: time.Now().UTC(), Plugins: []index.Plugin{{
		Name: "pdf.parses",
		Versions: []index.Version{{
			Version: "1.0.0", ManifestURL: u + "/pdf.parses-1.0.0/plugin.toml", ManifestSHA256: hexsum([]byte(manifest)),
			Artifacts: []index.Artifact{{
				OS: "linux", Arch: "amd64",
				URL:       u + "/pdf.parses-1.0.0/pdf.parses-1.0.0-linux-amd64",
				SHA256:    hexsum(binary),
				BundleURL: u + "/pdf.parses-1.0.0/pdf.parses-1.0.0-linux-amd64.sigstore.json",
			}},
		}},
	}}}
	return m
}

func (m *market) marketplace(name string, official bool) Marketplace {
	root := verify.TrustRoot{PublicKeyPath: "/keys/" + name + ".pub"}
	if official {
		root = verify.Official
	}
	return Marketplace{Name: name, IndexURL: m.srv.URL + "/index.json", Root: root, Official: official}
}

func (m *market) installer(t *testing.T) (Installer, plugin.Store) {
	s := tempStore(t)
	return Installer{HTTP: m.srv.Client(), Verifier: m.verifier, Store: s, OS: "linux", Arch: "amd64"}, s
}

func TestInstallHappyPath(t *testing.T) {
	m := newMarket(t, "official")
	in, s := m.installer(t)
	e, err := in.Install(context.Background(), m.marketplace("official", true), "pdf.parses", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != "pdf.parses" || !e.Official || e.Marketplace != "official" || e.BinarySHA256 != hexsum(binary) {
		t.Fatalf("entry %+v", e)
	}
	if got := strings.Join(m.verifier.calls, ","); got != "index.json,artifact" {
		t.Fatalf("verified %s", got)
	}
	for _, r := range m.verifier.roots {
		if r != verify.Official {
			t.Fatalf("root %+v", r)
		}
	}
	f, inst, err := s.Open("pdf.parses")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if inst.Manifest.Question != "Does the file parse as a PDF without errors?" {
		t.Fatalf("installed %+v", inst)
	}
}

func wantFail(t *testing.T, m *market, mp Marketplace, version string, is error) {
	t.Helper()
	in, s := m.installer(t)
	_, err := in.Install(context.Background(), mp, "pdf.parses", version)
	if err == nil {
		t.Fatal("install succeeded")
	}
	if is != nil && !errors.Is(err, is) {
		t.Fatalf("err %v, want %v", err, is)
	}
	if inst, _ := s.Installed(); len(inst) != 0 {
		t.Fatalf("installed anyway: %+v", inst)
	}
}

func TestIndexSignatureFails(t *testing.T) {
	m := newMarket(t, "official")
	m.verifier.fail["index.json"] = true
	wantFail(t, m, m.marketplace("official", true), "1.0.0", nil)
}

func TestArtifactSignatureFails(t *testing.T) {
	m := newMarket(t, "official")
	m.verifier.fail["artifact"] = true
	wantFail(t, m, m.marketplace("official", true), "1.0.0", nil)
}

func TestArtifactHashMismatch(t *testing.T) {
	m := newMarket(t, "official")
	m.files["/pdf.parses-1.0.0/pdf.parses-1.0.0-linux-amd64"] = []byte("swapped binary")
	wantFail(t, m, m.marketplace("official", true), "1.0.0", nil)
}

func TestManifestHashMismatch(t *testing.T) {
	m := newMarket(t, "official")
	m.files["/pdf.parses-1.0.0/plugin.toml"] = []byte(strings.Replace(manifest, "it parses", "it parses, honest", 1))
	wantFail(t, m, m.marketplace("official", true), "1.0.0", nil)
}

func TestYanked(t *testing.T) {
	m := newMarket(t, "official")
	m.idx.Plugins[0].Versions[0].Yanked = true
	wantFail(t, m, m.marketplace("official", true), "1.0.0", ErrYanked)
}

func TestExactVersionOnly(t *testing.T) {
	m := newMarket(t, "official")
	for _, v := range []string{"1", "latest", "1.0", "v1.0.0", "", "^1.0.0"} {
		in, _ := m.installer(t)
		if _, err := in.Install(context.Background(), m.marketplace("official", true), "pdf.parses", v); err == nil {
			t.Errorf("version %q accepted", v)
		}
	}
	if len(m.verifier.calls) != 0 {
		t.Fatalf("downloaded before checking the version: %v", m.verifier.calls)
	}
}

func TestNoArtifactForPlatform(t *testing.T) {
	m := newMarket(t, "official")
	in, _ := m.installer(t)
	in.Arch = "riscv64"
	if _, err := in.Install(context.Background(), m.marketplace("official", true), "pdf.parses", "1.0.0"); err == nil {
		t.Fatal("installed without an artifact for the platform")
	}
}

func TestHTTPSOnly(t *testing.T) {
	m := newMarket(t, "official")
	mp := m.marketplace("official", true)
	mp.IndexURL = strings.Replace(mp.IndexURL, "https://", "http://", 1)
	wantFail(t, m, mp, "1.0.0", nil)
}

func TestIndexOfAnotherMarketplace(t *testing.T) {
	m := newMarket(t, "acme")
	wantFail(t, m, m.marketplace("official", true), "1.0.0", nil)
}

func TestUnofficialEntryFlagged(t *testing.T) {
	m := newMarket(t, "acme")
	in, s := m.installer(t)
	e, err := in.Install(context.Background(), m.marketplace("acme", false), "pdf.parses", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if e.Official || e.Name != "acme/pdf.parses" || e.Marketplace != "acme" {
		t.Fatalf("entry %+v", e)
	}
	if m.verifier.roots[0].PublicKeyPath != "/keys/acme.pub" {
		t.Fatalf("verified with %+v", m.verifier.roots[0])
	}
	if _, _, err := s.Open("pdf.parses"); !errors.Is(err, plugin.ErrNotInstalled) {
		t.Fatalf("bare name resolved to an unofficial plugin: %v", err)
	}
}

func TestUnofficialCannotBeNamedOfficial(t *testing.T) {
	m := newMarket(t, "official")
	wantFail(t, m, m.marketplace("official", false), "1.0.0", nil)
}

func TestOfficialMarketplace(t *testing.T) {
	o := Official()
	if o.Name != "official" || !o.Official || o.Root != verify.Official ||
		o.IndexURL != "https://davidgodefroid.github.io/lockfile-marketplace/index.json" {
		t.Fatalf("Official() = %+v", o)
	}
}

func TestDiffManifestsQuestionChanged(t *testing.T) {
	old, err := pluginspec.ParseManifest([]byte(manifest))
	if err != nil {
		t.Fatal(err)
	}
	same := old
	same.Version = "1.0.1"
	changed, lines := DiffManifests(old, same)
	if changed || len(lines) != 1 || !strings.Contains(lines[0], "version") {
		t.Fatalf("version bump: %v %v", changed, lines)
	}
	for _, mut := range []func(*pluginspec.Manifest){
		func(m *pluginspec.Manifest) { m.Question = "Is the addressee a woman?" },
		func(m *pluginspec.Manifest) { m.One = "something else" },
		func(m *pluginspec.Manifest) { m.Zero = "something else" },
	} {
		n := old
		mut(&n)
		changed, lines := DiffManifests(old, n)
		if !changed || len(lines) != 1 {
			t.Errorf("question change not flagged: %v %v", changed, lines)
		}
	}
	n := old
	n.Limits.Timeout = 60 * time.Second
	n.Accepts = []string{"*/*"}
	if changed, lines := DiffManifests(old, n); changed || len(lines) != 2 {
		t.Fatalf("limits/accepts: %v %v", changed, lines)
	}
}
