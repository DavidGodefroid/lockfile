package marketplace

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DavidGodefroid/lockkit/update"

	"github.com/DavidGodefroid/lockfile/internal/verify"
)

// keyMarket serves the checked-in fixtures of testdata/key (gen.sh) as
// https://plugins.acme.example/. index replaces index.json and bundle its
// signature when set.
func keyMarket(t *testing.T, index, bundle []byte) (*http.Client, string) {
	t.Helper()
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", "key", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if index == nil {
		index = read("index.json")
	}
	if bundle == nil {
		bundle = read("index.json.sigstore.json")
	}
	files := map[string][]byte{"/index.json": index, "/index.json.sigstore.json": bundle}
	for _, f := range []string{"plugin.toml", "plugin.toml.sigstore.json", "binary", "binary.sigstore.json"} {
		files["/pdf.parses-1.0.0/"+f] = read(f)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok || r.Host != "plugins.acme.example" {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	c := srv.Client()
	tr := c.Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	tr.TLSClientConfig.ServerName = "example.com" // in httptest's certificate
	c.Transport = tr
	return c, "https://plugins.acme.example/index.json"
}

func keyPEM(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "key", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestKeyRootRealCosign installs from a marketplace signed with a test key
// and uploaded to Rekor, with the real cosign; a tampered index, another
// key and a bundle without a Rekor entry are refused. It needs network
// access to the sigstore TUF root.
func TestKeyRootRealCosign(t *testing.T) {
	if _, err := exec.LookPath("cosign"); err != nil {
		t.Skip("cosign is not in PATH")
	}
	ctx := context.Background()
	install := func(index, bundle []byte, key string) error {
		c, url := keyMarket(t, index, bundle)
		in := Installer{HTTP: c, Verifier: verify.NewLockkit(update.Options{}), Store: tempStore(t), OS: "linux", Arch: "amd64"}
		_, err := in.Install(ctx, Marketplace{Name: "acme", IndexURL: url, Root: verify.TrustRoot{PublicKey: key}}, "pdf.parses", "1.0.0")
		return err
	}
	if err := install(nil, nil, keyPEM(t, "cosign.pub")); err != nil {
		t.Fatalf("good marketplace: %v", err)
	}
	idx, _ := os.ReadFile(filepath.Join("testdata", "key", "index.json"))
	notlog, _ := os.ReadFile(filepath.Join("testdata", "key", "index.notlog.sigstore.json"))
	var ve *update.VerifyError
	for name, err := range map[string]error{
		"tampered index": install(append(append([]byte{}, idx...), '\n'), nil, keyPEM(t, "cosign.pub")),
		"other key":      install(nil, nil, keyPEM(t, "other.pub")),
		"no tlog entry":  install(nil, notlog, keyPEM(t, "cosign.pub")),
	} {
		if !errors.As(err, &ve) {
			t.Errorf("%s: err = %v, want *update.VerifyError", name, err)
		}
	}
}
