package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func open(t *testing.T, dir string, max int64, ttl time.Duration, c *clock) *Manager {
	t.Helper()
	cfg := Config{Dir: dir, MaxBytes: max, TTL: ttl}
	if c != nil {
		cfg.Now = c.now
	}
	m, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range des {
		names = append(names, d.Name())
	}
	return names
}

func TestModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "work")
	m := open(t, dir, 1<<20, time.Hour, nil)
	defer m.Close()
	h, info, err := m.Create(strings.NewReader("invoice for john.pdf"), 1<<10)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^f_[0-9A-Za-z]{10}$`).MatchString(string(h)) {
		t.Fatalf("handle %q", h)
	}
	sum := sha256.Sum256([]byte("invoice for john.pdf"))
	if info.Size != 20 || info.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("info %+v", info)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("root mode %o", fi.Mode().Perm())
	}
	subs := entries(t, dir)
	if len(subs) != 1 || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(subs[0]) || strings.Contains(subs[0], string(h)) {
		t.Fatalf("handle dir %v", subs)
	}
	hd := filepath.Join(dir, subs[0])
	if fi, _ := os.Stat(hd); fi.Mode().Perm() != 0o700 {
		t.Fatalf("handle dir mode %o", fi.Mode().Perm())
	}
	files := entries(t, hd)
	if len(files) != 1 || strings.Contains(files[0], "john") || strings.Contains(files[0], string(h)) {
		t.Fatalf("file names %v", files)
	}
	if fi, _ := os.Stat(filepath.Join(hd, files[0])); fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %o", fi.Mode().Perm())
	}
	// Info carries no path, ever.
	typ := reflect.TypeOf(Info{})
	if typ.NumField() != 2 || typ.Field(0).Name != "Size" || typ.Field(1).Name != "SHA256" {
		t.Fatalf("Info fields changed: %v", typ)
	}

	f, info2, err := m.OpenFile(h)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	if string(b) != "invoice for john.pdf" || info2 != info {
		t.Fatalf("read %q %+v", b, info2)
	}
}

func TestTooLarge(t *testing.T) {
	dir := t.TempDir()
	m := open(t, dir, 1<<20, time.Hour, nil)
	defer m.Close()
	_, _, err := m.Create(strings.NewReader(strings.Repeat("x", 2048)), 1024)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err %v", err)
	}
	if e := entries(t, dir); len(e) != 0 {
		t.Fatalf("left behind %v", e)
	}
	// Exactly max is fine.
	if _, _, err := m.Create(strings.NewReader(strings.Repeat("x", 1024)), 1024); err != nil {
		t.Fatal(err)
	}
}

func TestBudget(t *testing.T) {
	m := open(t, t.TempDir(), 100, time.Hour, nil)
	defer m.Close()
	h, _, err := m.Create(strings.NewReader(strings.Repeat("a", 60)), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Create(strings.NewReader(strings.Repeat("b", 60)), 1000); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over budget: %v", err)
	}
	if err := m.Release(h); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Create(strings.NewReader(strings.Repeat("b", 60)), 1000); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

func TestTTLExpiry(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	m := open(t, dir, 1<<20, 30*time.Minute, c)
	defer m.Close()
	h, _, err := m.Create(strings.NewReader("x"), 10)
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(29 * time.Minute)
	if f, _, err := m.OpenFile(h); err != nil {
		t.Fatalf("before ttl: %v", err)
	} else {
		f.Close()
	}
	c.t = c.t.Add(2 * time.Minute)
	if _, _, err := m.OpenFile(h); !errors.Is(err, ErrExpired) {
		t.Fatalf("after ttl: %v", err)
	}
	m.Expire()
	if e := entries(t, dir); len(e) != 0 {
		t.Fatalf("Expire left %v", e)
	}
	if _, _, err := m.OpenFile(h); !errors.Is(err, ErrExpired) {
		t.Fatalf("after Expire: %v", err)
	}
}

func TestSweepOrphans(t *testing.T) {
	dir := t.TempDir()
	m1 := open(t, dir, 1<<20, time.Hour, nil)
	if _, _, err := m1.Create(strings.NewReader("left by a killed console"), 100); err != nil {
		t.Fatal(err)
	}
	// m1 is never closed, as if the console was killed.
	if e := entries(t, dir); len(e) != 1 {
		t.Fatalf("setup %v", e)
	}
	m2 := open(t, dir, 1<<20, time.Hour, nil)
	defer m2.Close()
	if e := entries(t, dir); len(e) != 0 {
		t.Fatalf("orphans not swept: %v", e)
	}
}

// A work_dir pointed by mistake at a directory with other content keeps that
// content: only entries the workspace creates (32 hex characters) go.
func TestSweepOnlyOwnEntries(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "audit.log"), []byte("keep"), 0o600)
	os.MkdirAll(filepath.Join(dir, "plugins", "pdf.x"), 0o700)
	os.MkdirAll(filepath.Join(dir, strings.Repeat("ab", 16)), 0o700)
	os.MkdirAll(filepath.Join(dir, strings.Repeat("AB", 16)), 0o700)
	m := open(t, dir, 1<<20, time.Hour, nil)
	if _, _, err := m.Create(strings.NewReader("x"), 10); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(entries(t, dir), " ")
	if got != strings.Repeat("AB", 16)+" audit.log plugins" {
		t.Fatalf("left %q", got)
	}
}

func TestReleaseUnknownHandle(t *testing.T) {
	m := open(t, t.TempDir(), 1<<20, time.Hour, nil)
	defer m.Close()
	if err := m.Release("f_0000000000"); !errors.Is(err, ErrUnknownHandle) {
		t.Fatalf("err %v", err)
	}
	if _, _, err := m.OpenFile("f_0000000000"); !errors.Is(err, ErrUnknownHandle) {
		t.Fatalf("OpenFile err %v", err)
	}
}

func TestCloseRemovesAll(t *testing.T) {
	dir := t.TempDir()
	m := open(t, dir, 1<<20, time.Hour, nil)
	for i := 0; i < 3; i++ {
		if _, _, err := m.Create(strings.NewReader("x"), 10); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if e := entries(t, dir); len(e) != 0 {
		t.Fatalf("Close left %v", e)
	}
}
