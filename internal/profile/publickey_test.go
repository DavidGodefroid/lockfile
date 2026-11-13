package profile

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"path/filepath"
	"strings"
	"testing"
)

func derOf(t *testing.T, pub any) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func testKeyValue(t *testing.T) (value string, der []byte) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der = derOf(t, &k.PublicKey)
	return base64.StdEncoding.EncodeToString(der), der
}

func keyMarketplace(extra string) string {
	return "[[marketplaces]]\nname = \"acme\"\nindex_url = \"https://plugins.example.org/index.json\"\n" + extra
}

func TestKeyMarketplaceAccepted(t *testing.T) {
	v, der := testKeyValue(t)
	f, err := Parse([]byte(keyMarketplace("public_key = \""+v+"\"\n")), "c")
	if err != nil {
		t.Fatal(err)
	}
	tr := f.Marketplaces[0]
	sum := sha256.Sum256(der)
	if tr.PublicKey != v || tr.KeySHA256 != hex.EncodeToString(sum[:]) || tr.IdentityRegexp != "" || tr.OIDCIssuer != "" {
		t.Fatalf("trust = %+v", tr)
	}
	m := tr.Marketplace()
	want := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	if m.Official || m.Root.PublicKey != want || m.Root.IdentityRegexp != "" || m.Root.Validate() != nil {
		t.Fatalf("root = %+v", m.Root)
	}
	// Outputs show the fingerprint, never the key.
	j, _ := json.Marshal(tr)
	for name, out := range map[string]string{"String": tr.String(), "JSON": string(j)} {
		if strings.Contains(out, v[20:40]) || !strings.Contains(out, tr.KeySHA256) {
			t.Errorf("%s = %s", name, out)
		}
	}
	if strings.Contains(string(j), "identity_regexp") {
		t.Errorf("JSON has an empty keyless identity: %s", j)
	}
}

func TestKeyMarketplaceTypes(t *testing.T) {
	ec384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	edPub, _, _ := ed25519.GenerateKey(rand.Reader)
	rsa2048, _ := rsa.GenerateKey(rand.Reader, 2048)
	rsa1024, _ := rsa.GenerateKey(rand.Reader, 1024)
	ec224, _ := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	for name, tc := range map[string]struct {
		pub any
		ok  bool
	}{
		"ecdsa p384": {&ec384.PublicKey, true}, "ed25519": {edPub, true}, "rsa 2048": {&rsa2048.PublicKey, true},
		"rsa 1024": {&rsa1024.PublicKey, false}, "ecdsa p224": {&ec224.PublicKey, false},
	} {
		v := base64.StdEncoding.EncodeToString(derOf(t, tc.pub))
		_, err := Parse([]byte(keyMarketplace("public_key = \""+v+"\"\n")), "c")
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", name, err)
		}
		if err != nil && strings.Contains(err.Error(), v[20:40]) {
			t.Errorf("%s: the error quotes the key: %v", name, err)
		}
	}
}

func TestKeyMarketplaceRefused(t *testing.T) {
	v, der := testKeyValue(t)
	pemText := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	for name, extra := range map[string]string{
		"key and identity": "public_key = \"" + v + "\"\nidentity_regexp = \"^https://x/\"\n",
		"key and issuer":   "public_key = \"" + v + "\"\noidc_issuer = \"https://x\"\n",
		"key and both":     "public_key = \"" + v + "\"\nidentity_regexp = \"^https://x/\"\noidc_issuer = \"https://x\"\n",
		"neither":          "",
		"pem text":         "public_key = '''" + pemText + "'''\n",
		"truncated":        "public_key = \"" + v[:len(v)-8] + "\"\n",
		"not a key":        "public_key = \"" + base64.StdEncoding.EncodeToString([]byte("not a key at all, just some bytes")) + "\"\n",
		"url-safe base64":  "public_key = \"" + strings.NewReplacer("+", "-", "/", "_").Replace(v) + "x\"\n",
	} {
		_, err := Parse([]byte(keyMarketplace(extra)), "c")
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), v[20:40]) {
			t.Errorf("%s: the error quotes the key: %v", name, err)
		}
	}
}

func TestPublicKeyValue(t *testing.T) {
	v, der := testKeyValue(t)
	pemText := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	got, err := PublicKeyValue(pemText)
	if err != nil || got != v {
		t.Fatalf("PublicKeyValue = %q, %v; want %q", got, err, v)
	}
	for name, in := range map[string][]byte{
		"empty":         nil,
		"private key":   pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
		"garbage after": append(append([]byte{}, pemText...), "trailing"...),
		"two blocks":    append(append([]byte{}, pemText...), pemText...),
		"bad der":       pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("not a key at all")}),
	} {
		_, err := PublicKeyValue(in)
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), v[20:40]) {
			t.Errorf("%s: the error quotes the key: %v", name, err)
		}
	}
}

func TestAppendKeyMarketplace(t *testing.T) {
	v, _ := testKeyValue(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	if _, err := AppendMarketplace(path, RawMarketplace{Name: "acme", IndexURL: "https://plugins.example.org/index.json", PublicKey: v}); err != nil {
		t.Fatal(err)
	}
	f, err := ReadFile(path)
	if err != nil || len(f.Marketplaces) != 1 || f.Marketplaces[0].PublicKey != v || f.Marketplaces[0].OIDCIssuer != "" {
		t.Fatalf("read back %+v %v", f, err)
	}
}
