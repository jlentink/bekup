package sshpolicy

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// ignore drops a teardown error in tests.
func ignore(error) {}

// pair returns a connected loopback TCP pair (net.Pipe is unbuffered, so
// both SSH version banners would block each other).
func pair(t *testing.T) (client, server net.Conn) {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { ignore(ln.Close()) }()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()
	var d net.Dialer
	client, err = d.DialContext(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server = <-accepted
	if server == nil {
		t.Fatal("accept failed")
	}
	t.Cleanup(func() { ignore(client.Close()); ignore(server.Close()) })
	return client, server
}

// serve runs an SSH server handshake on conn with host key hk.
func serve(t *testing.T, conn net.Conn, hk ssh.Signer) {
	t.Helper()
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(hk)
	go func() {
		sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		go func() {
			for c := range chans {
				ignore(c.Reject(ssh.Prohibited, "no"))
			}
		}()
		ignore(sc.Wait())
	}()
}

func TestHandshake(t *testing.T) {
	host, _ := ed25519Signer(t)
	other, _ := ed25519Signer(t)
	pin, err := Pin{AuthorizedKey: authorizedLine(host.PublicKey())}.Normalize()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("pinned key", func(t *testing.T) {
		c, s := pair(t)
		serve(t, s, host)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cfg, check := ClientConfig("bekup", nil, pin, "pipe:22")
		cc, err := Handshake(ctx, c, "pipe:22", cfg)
		if err != nil {
			t.Fatalf("handshake: %v (violation %v)", err, check.Violation(err))
		}
		ignore(cc.Conn.Close())
	})

	t.Run("other key", func(t *testing.T) {
		c, s := pair(t)
		serve(t, s, other)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cfg, check := ClientConfig("bekup", nil, pin, "pipe:22")
		_, err := Handshake(ctx, c, "pipe:22", cfg)
		if err == nil {
			t.Fatal("handshake with another key succeeded")
		}
		if v := check.Violation(err); !errors.Is(v, ErrHostKeyMismatch) {
			t.Fatalf("violation = %v", v)
		}
		ignore(c.Close())
	})

	t.Run("silent server times out", func(t *testing.T) {
		c, _ := pair(t)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		cfg, _ := ClientConfig("bekup", nil, pin, "pipe:22")
		if _, err := Handshake(ctx, c, "pipe:22", cfg); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
	})
}

type fakeRequester struct {
	reply bool
	calls atomic.Int32
	block chan struct{}
}

func (f *fakeRequester) SendRequest(string, bool, []byte) (bool, []byte, error) {
	f.calls.Add(1)
	if !f.reply {
		<-f.block
		return false, nil, errors.New("closed")
	}
	return true, nil, nil
}

func TestKeepalive(t *testing.T) {
	t.Run("dead server", func(t *testing.T) {
		f := &fakeRequester{block: make(chan struct{})}
		var wg sync.WaitGroup
		done := make(chan struct{})
		start := time.Now()
		if !Keepalive(done, f, 10*time.Millisecond, &wg) {
			t.Fatal("Keepalive returned false")
		}
		if time.Since(start) < DefaultKeepaliveMisses*10*time.Millisecond {
			t.Fatal("gave up too early")
		}
		if n := f.calls.Load(); n != 1 {
			t.Errorf("%d requests in flight, want 1", n)
		}
		close(f.block)
		wg.Wait()
	})
	t.Run("live server", func(t *testing.T) {
		f := &fakeRequester{reply: true}
		var wg sync.WaitGroup
		done := make(chan struct{})
		res := make(chan bool, 1)
		go func() { res <- Keepalive(done, f, 5*time.Millisecond, &wg) }()
		time.Sleep(60 * time.Millisecond)
		close(done)
		if <-res {
			t.Fatal("live server reported dead")
		}
		wg.Wait()
		if f.calls.Load() < 3 {
			t.Errorf("only %d keepalives", f.calls.Load())
		}
	})
}
