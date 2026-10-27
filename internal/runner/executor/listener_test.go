package executor

import (
	"log/slog"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestPeerFilter(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var deny atomic.Bool
	deny.Store(true)
	f := FilterListener(ln, func(a netip.Addr) bool { return deny.Load() && a == netip.MustParseAddr("127.0.0.1") }, slog.New(slog.DiscardHandler))
	defer func() { discardErr(f.Close()) }()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := f.Accept()
		if err == nil {
			accepted <- c
		}
		close(accepted)
	}()
	var d net.Dialer
	c, err := d.DialContext(t.Context(), "tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	discardErr(c.SetReadDeadline(time.Now().Add(2 * time.Second)))
	if n, err := c.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatalf("denied peer: read %d %v", n, err)
	}
	discardErr(c.Close())
	select {
	case <-accepted:
		t.Fatal("denied connection was handed to the server")
	case <-time.After(100 * time.Millisecond):
	}
	deny.Store(false)
	c2, err := d.DialContext(t.Context(), "tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { discardErr(c2.Close()) }()
	select {
	case sc := <-accepted:
		if sc == nil {
			t.Fatal("accept failed")
		}
		discardErr(sc.Close())
	case <-time.After(2 * time.Second):
		t.Fatal("permitted connection not accepted")
	}
}
