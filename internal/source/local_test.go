package source

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockfile/internal/workspace"
)

func TestLocalStatOpen(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "invoices"), 0o700)
	os.WriteFile(filepath.Join(root, "invoices", "a.pdf"), []byte("%PDF-1.7 synthetic"), 0o600)
	s, err := NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fi, err := s.Stat(ctx, "invoices/a.pdf")
	if err != nil || fi.Size != 18 {
		t.Fatalf("Stat = %+v, %v", fi, err)
	}
	rc, err := s.Open(ctx, "invoices/a.pdf")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "%PDF-1.7 synthetic" {
		t.Fatalf("read %q", b)
	}
	if _, err := s.Stat(ctx, "invoices"); err == nil {
		t.Fatal("Stat of a directory succeeded")
	}
	if _, err := s.Stat(ctx, "../etc/passwd"); !errors.Is(err, ErrKeyOutsidePrefix) {
		t.Fatalf("escape: %v", err)
	}
}

func TestLocalSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "link.pdf")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.pdf"), []byte("secret"), 0o600)
	os.Symlink(outside, filepath.Join(root, "dirlink"))
	s, err := NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, k := range []string{"link.pdf", "dirlink/secret.pdf"} {
		if _, err := s.Stat(ctx, k); err == nil {
			t.Errorf("Stat(%q) followed a symlink out of the root", k)
		}
		if rc, err := s.Open(ctx, k); err == nil {
			rc.Close()
			t.Errorf("Open(%q) followed a symlink out of the root", k)
		}
	}
}

// fake is a Source whose Stat and Open are scripted.
type fake struct {
	size    int64
	body    []byte
	statErr error
	openErr error
}

func (f fake) Stat(context.Context, string) (ObjectInfo, error) {
	return ObjectInfo{Size: f.size}, f.statErr
}

func (f fake) Open(context.Context, string) (io.ReadCloser, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	return io.NopCloser(bytes.NewReader(f.body)), nil
}

func ws(t *testing.T) (*workspace.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	m, err := workspace.Open(workspace.Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m, dir
}

func TestFetchOK(t *testing.T) {
	m, _ := ws(t)
	h, info, err := Fetch(context.Background(), fake{size: 5, body: []byte("hello")}, "a.pdf", 1024, m)
	if err != nil || info.Size != 5 || h == "" {
		t.Fatalf("Fetch = %q %+v %v", h, info, err)
	}
}

func TestOversizedStream(t *testing.T) {
	m, dir := ws(t)
	_, _, err := Fetch(context.Background(), fake{size: 10, body: bytes.Repeat([]byte("x"), 1<<20)}, "a.pdf", 1024, m)
	var fe *FetchError
	if !errors.As(err, &fe) || !errors.Is(err, workspace.ErrTooLarge) {
		t.Fatalf("err %v", err)
	}
	if des, _ := os.ReadDir(dir); len(des) != 0 {
		t.Fatalf("left %v", des)
	}
}

func TestFetchRefusesBigStat(t *testing.T) {
	m, _ := ws(t)
	opened := false
	_, _, err := Fetch(context.Background(), openSpy{fake{size: 4096}, &opened}, "a.pdf", 1024, m)
	if !errors.Is(err, workspace.ErrTooLarge) || opened {
		t.Fatalf("err %v opened %v", err, opened)
	}
}

type openSpy struct {
	fake
	opened *bool
}

func (o openSpy) Open(ctx context.Context, k string) (io.ReadCloser, error) {
	*o.opened = true
	return o.fake.Open(ctx, k)
}

func TestFetchErrorText(t *testing.T) {
	m, _ := ws(t)
	root := t.TempDir()
	local, _ := NewLocal(root)
	for name, s := range map[string]Source{
		"not found": local,
		"too large": fake{size: 1 << 30},
		"denied":    fake{statErr: errors.New("/srv/x/john.pdf: permission denied")},
		"open fail": fake{size: 1, openErr: errors.New("connection reset by sftp.example.org")},
	} {
		_, _, err := Fetch(context.Background(), s, "invoices/john.pdf", 1024, m)
		if err == nil || err.Error() != "fetch_failed" {
			t.Errorf("%s: err %v", name, err)
		}
		var fe *FetchError
		if !errors.As(err, &fe) || fe.Cause == nil {
			t.Errorf("%s: no cause kept for the audit", name)
		}
		if strings.Contains(err.Error(), "john") {
			t.Errorf("%s: leaked", name)
		}
	}
}
