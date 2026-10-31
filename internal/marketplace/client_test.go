package marketplace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// A redirect from https to plain http is not followed, an https one is,
// and every client has a timeout.
func TestFetchRedirectHTTPSOnly(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("served over plain http"))
	}))
	t.Cleanup(plain.Close)
	var tls *httptest.Server
	tls = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-http":
			http.Redirect(w, r, plain.URL+"/x", http.StatusFound)
		case "/to-https":
			http.Redirect(w, r, tls.URL+"/ok", http.StatusFound)
		case "/ok":
			w.Write([]byte("ok"))
		}
	}))
	t.Cleanup(tls.Close)
	i := Installer{HTTP: tls.Client()}
	dir := t.TempDir()
	if _, err := i.fetch(context.Background(), "test", tls.URL+"/to-http", filepath.Join(dir, "a"), 1<<10); !errors.Is(err, ErrInsecureRedirect) {
		t.Fatalf("redirect to http: err = %v", err)
	}
	if b, err := i.fetch(context.Background(), "test", tls.URL+"/to-https", filepath.Join(dir, "b"), 1<<10); err != nil || string(b) != "ok" {
		t.Fatalf("redirect to https: %q %v", b, err)
	}
	if c := SafeClient(nil); c.Timeout <= 0 || c.Timeout > ClientTimeout || c.CheckRedirect == nil {
		t.Fatalf("default client: timeout %v", c.Timeout)
	}
	if http.DefaultClient.CheckRedirect != nil || http.DefaultClient.Timeout != 0 {
		t.Fatal("SafeClient changed http.DefaultClient")
	}
}
