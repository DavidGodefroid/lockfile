package source_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DavidGodefroid/lockfile/internal/source"
	"github.com/DavidGodefroid/lockfile/internal/testenv"
)

func TestSFTPSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{filepath.Join(base, "in"), outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p string) {
		if err := os.WriteFile(p, []byte("synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(base, "in", "a.pdf"))
	write(filepath.Join(outside, "secret.pdf"))
	if err := os.Symlink(filepath.Join(outside, "secret.pdf"), filepath.Join(base, "link.pdf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "dir")); err != nil {
		t.Fatal(err)
	}
	srv := testenv.NewSFTP(t, root, testenv.SFTPOptions{User: "reader", Password: "pw"})
	src, err := source.NewSFTP(source.SFTPConfig{Host: srv.Host, Port: srv.Port, User: srv.User, KnownHosts: srv.KnownHosts, BaseDir: base},
		source.SFTPCreds{Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rc, err := src.Open(ctx, "in/a.pdf")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "synthetic" {
		t.Fatalf("read %q", b)
	}
	if _, err := src.Stat(ctx, "in/a.pdf"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"link.pdf", "dir/secret.pdf"} {
		if _, err := src.Stat(ctx, key); err == nil {
			t.Errorf("stat %s: followed a symbolic link out of base_dir", key)
		}
		if rc, err := src.Open(ctx, key); err == nil {
			rc.Close()
			t.Errorf("open %s: followed a symbolic link out of base_dir", key)
		}
	}
}
