package marketplace

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/update"

	"github.com/DavidGodefroid/lockfile/internal/verify"
)

// keyCosign is a cosign that appends, for each run, the base name of the
// verified blob and the content of the file passed after --key.
func keyCosign(t *testing.T) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "runs")
	bin = filepath.Join(dir, "cosign")
	script := "#!/bin/sh\nkey=\nwhile [ $# -gt 1 ]; do if [ \"$1\" = --key ]; then key=$(cat \"$2\" | tr '\\n' ' '); fi; shift; done\n" +
		"printf '%s|%s\\n' \"$(basename \"$1\")\" \"$key\" >> " + log + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

// TestKeyRootedMarketplaceInstalls installs from an unofficial marketplace
// rooted in a public key through the real verifier: the index, the
// manifest and the binary are each checked with cosign --key and that key.
func TestKeyRootedMarketplaceInstalls(t *testing.T) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	key := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))

	m := newMarket(t, "acme")
	bin, log := keyCosign(t)
	in, _ := m.installer(t)
	in.Verifier = verify.NewLockkit(update.Options{Cosign: bin})
	mp := Marketplace{Name: "acme", IndexURL: m.srv.URL + "/index.json", Root: verify.TrustRoot{PublicKey: key}}
	e, err := in.Install(context.Background(), mp, "pdf.parses", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if e.Official || e.Name != "acme/pdf.parses" {
		t.Fatalf("entry %+v", e)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	runs := strings.Split(strings.TrimSpace(string(raw)), "\n")
	flat := strings.TrimSpace(strings.ReplaceAll(key, "\n", " "))
	want := []string{"index.json", "plugin.toml", "artifact"}
	if len(runs) != len(want) {
		t.Fatalf("cosign runs = %q", runs)
	}
	for i, r := range runs {
		blob, got, _ := strings.Cut(r, "|")
		if blob != want[i] || strings.TrimSpace(got) != flat {
			t.Errorf("run %d: blob %s, key %q; want %s with the marketplace's key", i, blob, got, want[i])
		}
	}
}
