package marketplace

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A replayed older signed index (which could un-yank a version) is refused
// once a newer one was seen; the same or a newer one is accepted.
func TestIndexRollbackRefused(t *testing.T) {
	m := newMarket(t, "official")
	in, _ := m.installer(t)
	in.StateDir = t.TempDir()
	mp := m.marketplace("official", true)
	newer := m.idx.Generated
	if _, err := in.Index(context.Background(), mp); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Index(context.Background(), mp); err != nil {
		t.Fatalf("same index again: %v", err)
	}
	m.idx.Generated = newer.Add(-time.Hour)
	if _, err := in.Index(context.Background(), mp); !errors.Is(err, ErrRollback) {
		t.Fatalf("older index: err = %v", err)
	}
	if _, err := in.Install(context.Background(), mp, "pdf.parses", "1.0.0"); !errors.Is(err, ErrRollback) {
		t.Fatalf("install from an older index: err = %v", err)
	}
	m.idx.Generated = newer.Add(time.Hour)
	if _, err := in.Index(context.Background(), mp); err != nil {
		t.Fatalf("newer index: %v", err)
	}
	// Another marketplace has its own history.
	other := newMarket(t, "acme")
	other.idx.Generated = newer.Add(-24 * time.Hour)
	oin, _ := other.installer(t)
	oin.StateDir = in.StateDir
	if _, err := oin.Index(context.Background(), other.marketplace("acme", false)); err != nil {
		t.Fatalf("other marketplace: %v", err)
	}
}
