package workspace

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gatedReader yields head, then blocks until gate is closed, then EOF.
type gatedReader struct {
	head    io.Reader
	started chan struct{}
	gate    chan struct{}
	once    bool
}

func (g *gatedReader) Read(p []byte) (int, error) {
	n, err := g.head.Read(p)
	if n > 0 {
		return n, nil
	}
	if err == io.EOF {
		if !g.once {
			g.once = true
			close(g.started)
		}
		<-g.gate
	}
	return 0, err
}

// A fetch in progress holds its share of the budget: a concurrent fetch
// cannot use the same bytes, so k fetches never take k × the budget.
func TestCreateReservesBudget(t *testing.T) {
	m := open(t, filepath.Join(t.TempDir(), "work"), 100, time.Hour, nil)
	defer m.Close()
	slow := &gatedReader{head: strings.NewReader(strings.Repeat("a", 60)), started: make(chan struct{}), gate: make(chan struct{})}
	type result struct {
		h   Handle
		err error
	}
	done := make(chan result, 1)
	go func() {
		h, _, err := m.Create(slow, 1000)
		done <- result{h, err}
	}()
	<-slow.started
	if _, _, err := m.Create(strings.NewReader(strings.Repeat("b", 60)), 1000); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("concurrent fetch over the reserved budget: err = %v", err)
	}
	close(slow.gate)
	r := <-done
	if r.err != nil {
		t.Fatalf("first fetch: %v", r.err)
	}
	// The unused part of the reservation is refunded: 40 bytes are left.
	if _, _, err := m.Create(strings.NewReader(strings.Repeat("c", 40)), 1000); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if _, _, err := m.Create(strings.NewReader("d"), 1000); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("budget exhausted: err = %v", err)
	}
	if err := m.Release(r.h); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Create(strings.NewReader(strings.Repeat("e", 60)), 1000); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

func TestCreateHandleCap(t *testing.T) {
	m, err := Open(Config{Dir: filepath.Join(t.TempDir(), "work"), MaxHandles: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	h1, _, err := m.Create(strings.NewReader("x"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Create(strings.NewReader("y"), 10); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Create(strings.NewReader("z"), 10); !errors.Is(err, ErrTooManyHandles) {
		t.Fatalf("third handle: err = %v", err)
	}
	if err := m.Release(h1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Create(strings.NewReader("z"), 10); err != nil {
		t.Fatalf("after release: %v", err)
	}
}
