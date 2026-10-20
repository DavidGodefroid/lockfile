package plugin

import (
	"errors"
	"testing"
)

func TestMarkYankedRefusesOpen(t *testing.T) {
	s := tempStore(t)
	install(t, s, "pdf.parses", OfficialMarketplace, true, []byte("bin-a"))
	install(t, s, "acme/pdf.parses", "acme", false, []byte("bin-b"))

	var asked []string
	changed, err := s.MarkYanked(OfficialMarketplace, func(bare, version string) (bool, bool) {
		asked = append(asked, bare+"@"+version)
		return true, true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0].Name != "pdf.parses" || !changed[0].Yanked {
		t.Fatalf("changed = %+v", changed)
	}
	if len(asked) != 1 || asked[0] != "pdf.parses@1.0.0" {
		t.Fatalf("asked %v: another marketplace's plugin was looked up", asked)
	}
	if _, _, err := s.Open("pdf.parses"); !errors.Is(err, ErrYanked) {
		t.Fatalf("open yanked: err = %v", err)
	}
	f, _, err := s.Open("acme/pdf.parses")
	if err != nil {
		t.Fatalf("other marketplace: %v", err)
	}
	f.Close()

	// A version the index does not list keeps its status.
	if changed, err := s.MarkYanked(OfficialMarketplace, func(string, string) (bool, bool) { return false, false }); err != nil || len(changed) != 0 {
		t.Fatalf("unknown version: %+v %v", changed, err)
	}
	if _, _, err := s.Open("pdf.parses"); !errors.Is(err, ErrYanked) {
		t.Fatalf("still yanked: err = %v", err)
	}
	// A newer verified index that lists it as not yanked lifts it.
	if _, err := s.MarkYanked(OfficialMarketplace, func(string, string) (bool, bool) { return false, true }); err != nil {
		t.Fatal(err)
	}
	f, _, err = s.Open("pdf.parses")
	if err != nil {
		t.Fatalf("un-yanked: %v", err)
	}
	f.Close()
}
