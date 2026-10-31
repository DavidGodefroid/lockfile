package marketplace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	canary    = "glsa_CanarySecretToken123"
	canaryHit = "glsa_Canary"
)

// TestErrorsDoNotEchoValues: a download error names the file it was
// fetching, never the URL (an index_url typed in the config may carry a
// token in its userinfo, path or query), and an invalid plugin name or
// version is not quoted.
func TestErrorsDoNotEchoValues(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/big") {
			w.Write([]byte(strings.Repeat("x", 2048)))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	tok := "user:" + canary + "@"
	host := strings.TrimPrefix(srv.URL, "https://")
	dir := t.TempDir()
	for _, tc := range []struct {
		name, url string
		client    *http.Client
		kind      string
	}{
		{"plain http", "http://" + tok + "plugins.example.org/index.json", srv.Client(), "non-https"},
		{"not a URL", "https://" + canary + "%zz", srv.Client(), "non-https"},
		{"status", "https://" + tok + host + "/" + canary + "/index.json?token=" + canary, srv.Client(), "HTTP 404"},
		{"too large", "https://" + tok + host + "/" + canary + "/big?token=" + canary, srv.Client(), "larger than"},
		{"untrusted certificate", "https://" + tok + host + "/" + canary + "?token=" + canary, nil, "TLS certificate not trusted"},
		{"connection refused", "https://" + tok + "127.0.0.1:1/" + canary + "?token=" + canary, srv.Client(), "connection refused"},
		{"unknown host", "https://" + tok + canary + ".invalid/index.json?token=" + canary, srv.Client(), ""},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, err := Installer{HTTP: tc.client}.fetch(ctx, "marketplace acme: index", tc.url, filepath.Join(dir, "f"), 1<<10)
		cancel()
		if err == nil {
			t.Errorf("%s: no error", tc.name)
			continue
		}
		if strings.Contains(err.Error(), canaryHit) {
			t.Errorf("%s: the error quotes the URL: %v", tc.name, err)
		}
		if kind := tc.kind; !strings.Contains(err.Error(), kind) && (runtime.GOOS != "windows" || kind != "connection refused") {
			t.Errorf("%s: want %q in %v", tc.name, tc.kind, err)
		}
		if !strings.Contains(err.Error(), "marketplace acme: index") {
			t.Errorf("%s: the error does not name the download: %v", tc.name, err)
		}
	}

	m := Marketplace{Name: "acme", IndexURL: "https://" + tok + host + "/index.json"}
	for _, nv := range [][2]string{{canary, "1.0.0"}, {"https://" + tok + "h", "1.0.0"}, {"acme.ok", canary}} {
		_, err := Installer{}.Install(context.Background(), m, nv[0], nv[1])
		if err == nil || strings.Contains(err.Error(), canaryHit) {
			t.Errorf("Install(%q, %q) = %v", nv[0], nv[1], err)
		}
	}
	if _, err := (Installer{}).Install(context.Background(), m, canary, "1.0.0"); err == nil || !strings.Contains(err.Error(), "plugin name") {
		t.Errorf("invalid name: %v", err)
	}
	if _, err := (Installer{}).Install(context.Background(), m, "acme.ok", canary); err == nil || !strings.Contains(err.Error(), "version") {
		t.Errorf("invalid version: %v", err)
	}
}

// A cancelled download stays context.Canceled, and a redirect to http
// ErrInsecureRedirect, without the URL.
func TestFetchErrorKindsMatch(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(plain.Close)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/"+canary, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	i := Installer{HTTP: srv.Client()}
	path := filepath.Join(t.TempDir(), "f")
	_, err := i.fetch(context.Background(), "x", srv.URL+"/"+canary, path, 1<<10)
	if !errors.Is(err, ErrInsecureRedirect) || strings.Contains(err.Error(), canaryHit) {
		t.Errorf("redirect: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = i.fetch(ctx, "x", srv.URL+"/"+canary, path, 1<<10)
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), canaryHit) {
		t.Errorf("cancelled: %v", err)
	}
}
