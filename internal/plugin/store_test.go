package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func manifestFor(name, version string) []byte {
	return []byte(`name = "` + name + `"
version = "` + version + `"
protocol = 1
question = "Is it?"
one = "yes"
zero = "no"
accepts = ["application/pdf"]
limits = { cpu_s = 5, memory = "64MiB", timeout_s = 5 }
`)
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func entry(qualified, marketplace string, official bool, bin, man []byte) LockEntry {
	return LockEntry{Name: qualified, Version: "1.0.0", Marketplace: marketplace, Official: official,
		BinarySHA256: sum(bin), ManifestSHA256: sum(man)}
}

func install(t *testing.T, s Store, qualified, marketplace string, official bool, bin []byte) LockEntry {
	t.Helper()
	bare := qualified[strings.LastIndex(qualified, "/")+1:]
	man := manifestFor(bare, "1.0.0")
	e := entry(qualified, marketplace, official, bin, man)
	if err := s.Install(e, bin, man); err != nil {
		t.Fatal(err)
	}
	return e
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestInstallWritesModes(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	install(t, s, "pdf.parses", "official", true, []byte("binary"))
	inst, err := s.Installed()
	if err != nil || len(inst) != 1 {
		t.Fatalf("Installed = %v, %v", inst, err)
	}
	if m := mode(t, filepath.Dir(inst[0].Binary)); m != 0o500 {
		t.Errorf("version dir mode %o", m)
	}
	if m := mode(t, inst[0].Binary); m != 0o500 {
		t.Errorf("binary mode %o", m)
	}
	if m := mode(t, filepath.Join(s.Dir, "plugins.lock")); m != 0o600 {
		t.Errorf("lock mode %o", m)
	}
	lock, _ := os.ReadFile(filepath.Join(s.Dir, "plugins.lock"))
	if !strings.Contains(string(lock), `name = "pdf.parses"`) || !strings.Contains(string(lock), sum([]byte("binary"))) {
		t.Errorf("lock:\n%s", lock)
	}
	if inst[0].Manifest.Question != "Is it?" || !inst[0].Official {
		t.Errorf("installed %+v", inst[0])
	}
}

func TestInstallRejectsHashMismatch(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	man := manifestFor("pdf.parses", "1.0.0")
	e := entry("pdf.parses", "official", true, []byte("binary"), man)
	if err := s.Install(e, []byte("other"), man); err == nil {
		t.Fatal("binary hash mismatch accepted")
	}
	if err := s.Install(e, []byte("binary"), manifestFor("pdf.other", "1.0.0")); err == nil {
		t.Fatal("manifest hash mismatch accepted")
	}
	other := manifestFor("pdf.other", "1.0.0")
	e2 := entry("pdf.parses", "official", true, []byte("binary"), other)
	if err := s.Install(e2, []byte("binary"), other); err == nil {
		t.Fatal("manifest name differing from entry accepted")
	}
	if inst, _ := s.Installed(); len(inst) != 0 {
		t.Fatalf("something installed: %v", inst)
	}
}

func TestOpenReturnsVerifiedBinary(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	install(t, s, "pdf.parses", "official", true, []byte("binary"))
	f, inst, err := s.Open("pdf.parses")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	if string(b) != "binary" || inst.Manifest.Limits.Timeout == 0 {
		t.Fatalf("read %q, %+v", b, inst)
	}
	if _, _, err := s.Open("pdf.missing"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("missing: %v", err)
	}
}

func TestTampered(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	install(t, s, "pdf.parses", "official", true, []byte("binary"))
	inst, _ := s.Installed()
	bin := inst[0].Binary

	os.Chmod(filepath.Dir(bin), 0o700)
	os.Chmod(bin, 0o700)
	if err := os.WriteFile(bin, []byte("binarY"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Open("pdf.parses"); !errors.Is(err, ErrTampered) {
		t.Fatalf("flipped byte: %v", err)
	}
	os.WriteFile(bin, []byte("binary"), 0o700)
	if f, _, err := s.Open("pdf.parses"); err != nil {
		t.Fatalf("restored: %v", err)
	} else {
		f.Close()
	}

	lockPath := filepath.Join(s.Dir, "plugins.lock")
	lock, _ := os.ReadFile(lockPath)
	edited := strings.Replace(string(lock), sum([]byte("binary")), sum([]byte("evil")), 1)
	os.WriteFile(lockPath, []byte(edited), 0o600)
	if _, _, err := s.Open("pdf.parses"); !errors.Is(err, ErrTampered) {
		t.Fatalf("edited lock: %v", err)
	}
}

func TestTamperedManifest(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	install(t, s, "pdf.parses", "official", true, []byte("binary"))
	inst, _ := s.Installed()
	man := filepath.Join(filepath.Dir(inst[0].Binary), "plugin.toml")
	os.Chmod(filepath.Dir(man), 0o700)
	os.Chmod(man, 0o600)
	os.WriteFile(man, []byte(strings.Replace(string(manifestFor("pdf.parses", "1.0.0")), "Is it?", "Is it a woman?", 1)), 0o600)
	if _, _, err := s.Open("pdf.parses"); !errors.Is(err, ErrTampered) {
		t.Fatalf("edited manifest: %v", err)
	}
	inst, err := s.Installed()
	if err != nil || len(inst) != 1 || !inst[0].Tampered || inst[0].Manifest.Question != "" {
		t.Fatalf("Installed after manifest edit = %+v, %v", inst, err)
	}
}

func TestQualifiedNames(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	install(t, s, "pdf.x", "official", true, []byte("official-bin"))
	install(t, s, "acme/pdf.x", "acme", false, []byte("acme-bin"))
	f, inst, err := s.Open("pdf.x")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "official-bin" || !inst.Official {
		t.Fatalf("bare name resolved to %q %+v", b, inst.LockEntry)
	}
	f, inst, err = s.Open("acme/pdf.x")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(f)
	f.Close()
	if string(b) != "acme-bin" || inst.Official {
		t.Fatalf("qualified resolved to %q %+v", b, inst.LockEntry)
	}
	if all, _ := s.Installed(); len(all) != 2 {
		t.Fatalf("Installed = %d", len(all))
	}
	if err := s.Remove("acme/pdf.x"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Open("acme/pdf.x"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("after remove: %v", err)
	}
	if f, _, err := s.Open("pdf.x"); err != nil {
		t.Fatalf("official gone with remove: %v", err)
	} else {
		f.Close()
	}
}

func TestRejectsBadQualifiedName(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	bin := []byte("b")
	man := manifestFor("pdf.x", "1.0.0")
	for _, e := range []LockEntry{
		entry("acme/pdf.x", "official", true, bin, man), // official must be bare
		entry("pdf.x", "acme", false, bin, man),         // unofficial must be qualified
		entry("../pdf.x", "..", false, bin, man),
		entry("other/pdf.x", "acme", false, bin, man),
	} {
		if err := s.Install(e, bin, man); err == nil {
			t.Errorf("accepted %+v", e)
		}
	}
}
