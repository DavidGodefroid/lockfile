package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockfile/internal/product"
)

func tarGz(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tw.Write(body)
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

// releaseServer serves a latest release v9.9.9 for linux/amd64 whose
// archive holds newBinary; checksums.txt lists sum (the archive's real
// sum when "").
func releaseServer(t *testing.T, newBinary []byte, sumOverride string) *httptest.Server {
	t.Helper()
	archive := tarGz(t, "lockfile", newBinary)
	name := "lockfile_9.9.9_linux_amd64.tar.gz"
	s := sum(archive)
	if sumOverride != "" {
		s = sumOverride
	}
	files := map[string][]byte{
		"/checksums.txt":               []byte(s + "  " + name + "\n"),
		"/checksums.txt.sigstore.json": []byte("{}"),
		"/" + name:                     archive,
	}
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			var assets []map[string]string
			for p := range files {
				assets = append(assets, map[string]string{"name": strings.TrimPrefix(p, "/"), "browser_download_url": srv.URL + p})
			}
			b, _ := json.Marshal(map[string]any{"tag_name": "v9.9.9", "assets": assets})
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
	return srv
}

// fakeCosignBin is a cosign that records its argv and exits with code.
func fakeCosignBin(t *testing.T, code int) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "argv")
	bin = filepath.Join(dir, "cosign")
	script := fmt.Sprintf("#!/bin/sh\nfor a in \"$@\"; do printf '%%s\\n' \"$a\"; done > %s\nexit %d\n", log, code)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func updateEnv(t *testing.T, srv *httptest.Server, cosign string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "lockfile")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	testUpdateHooks = &updateHooks{latest: srv.URL + "/latest", http: srv.Client(), cosign: cosign, target: target, goos: "linux", goarch: "amd64",
		root: func(string) error { t.Fatal("sudo for a binary this account owns"); return nil }}
	t.Cleanup(func() { testUpdateHooks = nil })
	return target
}

func TestUpdateVerifiesBeforeReplace(t *testing.T) {
	srv := releaseServer(t, []byte("new binary"), "")
	bad, _ := fakeCosignBin(t, 1)
	target := updateEnv(t, srv, bad)
	code, _, errText := runArgs(t, "", "update", "--yes")
	if b, _ := os.ReadFile(target); code != product.ExitError || string(b) != "old binary" || !strings.Contains(errText, "not signed by lockfile's release workflow") {
		t.Fatalf("bad signature: exit %d, binary %q: %s", code, b, errText)
	}
	good, log := fakeCosignBin(t, 0)
	testUpdateHooks.cosign = good
	if code, out, errText := runArgs(t, "", "update", "--yes"); code != 0 {
		t.Fatalf("good: exit %d: %s %s", code, out, errText)
	}
	if b, _ := os.ReadFile(target); string(b) != "new binary" {
		t.Fatalf("binary %q", b)
	}
	argv, _ := os.ReadFile(log)
	if !strings.Contains(string(argv), `^https://github\.com/DavidGodefroid/lockfile/\.github/workflows/release\.yml@refs/tags/v[0-9]`) {
		t.Fatalf("identity not checked:\n%s", argv)
	}
}

func TestUpdateChecksumMismatch(t *testing.T) {
	srv := releaseServer(t, []byte("new binary"), strings.Repeat("0", 64))
	good, _ := fakeCosignBin(t, 0)
	target := updateEnv(t, srv, good)
	code, _, errText := runArgs(t, "", "update", "--yes")
	if b, _ := os.ReadFile(target); code != product.ExitError || string(b) != "old binary" || !strings.Contains(errText, "does not match its signed checksum") {
		t.Fatalf("exit %d, binary %q: %s", code, b, errText)
	}
}

func TestUpdateCosignMissing(t *testing.T) {
	srv := releaseServer(t, []byte("new binary"), "")
	target := updateEnv(t, srv, "")
	t.Setenv("PATH", t.TempDir())
	code, _, errText := runArgs(t, "", "update", "--yes")
	if b, _ := os.ReadFile(target); code != product.ExitError || string(b) != "old binary" || !strings.Contains(errText, "cosign") {
		t.Fatalf("exit %d, binary %q: %s", code, b, errText)
	}
}

func TestUpdateCheckOnly(t *testing.T) {
	srv := releaseServer(t, []byte("new binary"), "")
	target := updateEnv(t, srv, "")
	code, out, _ := runArgs(t, "", "update", "--check")
	if b, _ := os.ReadFile(target); code != 0 || !strings.Contains(out, "v9.9.9 is available") || string(b) != "old binary" {
		t.Fatalf("exit %d: %s", code, out)
	}
}
