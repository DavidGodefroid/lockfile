package index

import (
	"errors"
	"testing"
)

const sample = `{
 "schema": 1,
 "marketplace": "official",
 "generated": "2026-10-19T12:00:00Z",
 "plugins": [
  {"name": "pdf.parses", "versions": [
   {"version": "1.0.0", "manifest_url": "https://example.org/m1", "manifest_sha256": "aa", "yanked": true,
    "artifacts": [{"os": "linux", "arch": "amd64", "url": "https://example.org/b1", "sha256": "bb", "bundle_url": "https://example.org/b1.sigstore.json"}]},
   {"version": "1.1.0", "manifest_url": "https://example.org/m2", "manifest_sha256": "cc", "yanked": false,
    "artifacts": [{"os": "linux", "arch": "arm64", "url": "https://example.org/b2", "sha256": "dd", "bundle_url": "https://example.org/b2.sigstore.json"}]}
  ]}
 ]
}`

func TestParseAndFind(t *testing.T) {
	i, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if i.Schema != 1 || i.Marketplace != "official" || i.Generated.Year() != 2026 {
		t.Fatalf("header %+v", i)
	}
	v, err := i.Find("pdf.parses", "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if v.ManifestSHA256 != "cc" || v.Yanked || len(v.Artifacts) != 1 || v.Artifacts[0].Arch != "arm64" || v.Artifacts[0].BundleURL == "" {
		t.Fatalf("version %+v", v)
	}
	if _, err := i.Find("pdf.parses", "9.9.9"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing version: %v", err)
	}
	if _, err := i.Find("pdf.nope", "1.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing plugin: %v", err)
	}
}

func TestFindYankedReturnsVersionWithYanked(t *testing.T) {
	i, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	v, err := i.Find("pdf.parses", "1.0.0")
	if err != nil || !v.Yanked {
		t.Fatalf("got %+v, %v", v, err)
	}
}

func TestParseRejectsUnknownSchema(t *testing.T) {
	if _, err := Parse([]byte(`{"schema": 2, "marketplace": "official", "plugins": []}`)); err == nil {
		t.Fatal("schema 2 accepted")
	}
	if _, err := Parse([]byte(`{"schema": 1, "marketplace": "official", "plugins": [], "extra": 1}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
}
