package main

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// dials counts the connections a test listener accepts, command by
// command, without waiting on the scheduler. A client's connect returns
// once the connection is queued on the listener, and the listener's queue
// is first in, first out: after a command returns, a marker connection
// (bound to a path of its own, so the listener knows it) is accepted after
// every connection the command made. count dials that marker and reads how
// many connections came before it.
type dials struct {
	marker string
	path   string
	counts chan int
	first  chan struct{}
}

// serveDials accepts the connections of ln, hands each one but the
// markers to serve (which owns it), and counts them for dials.count.
func serveDials(t *testing.T, ln net.Listener, serve func(net.Conn)) *dials {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lfmark")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	d := &dials{
		marker: filepath.Join(dir, "m"),
		path:   ln.Addr().String(),
		counts: make(chan int),
		first:  make(chan struct{}),
	}
	var once sync.Once
	go func() {
		n := 0
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			if c.RemoteAddr().String() == d.marker {
				c.Close()
				d.counts <- n
				n = 0
				continue
			}
			n++
			once.Do(func() { close(d.first) })
			serve(c)
		}
	}()
	return d
}

// count returns how many connections were accepted since the last count
// (or since the listener started).
func (d *dials) count(t *testing.T) int {
	t.Helper()
	_ = os.Remove(d.marker)
	c, err := net.DialUnix("unix", &net.UnixAddr{Name: d.marker, Net: "unix"}, &net.UnixAddr{Name: d.path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case n := <-d.counts:
		return n
	case <-time.After(10 * time.Second):
		t.Fatal("the listener did not accept the marker connection")
		return 0
	}
}
