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

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// A conn deadline that fires just before ctx's own timer is ctx's deadline
// (the race that made "silent server times out" flaky under load).
func TestDeadlineErr(t *testing.T) {
	past, cancelPast := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelPast()
	future, cancelFuture := context.WithTimeout(context.Background(), time.Hour)
	defer cancelFuture()
	protocol := errors.New("ssh: handshake failed: protocol")
	for _, tc := range []struct {
		name     string
		ctx      context.Context
		err      error
		deadline bool
	}{
		{"timeout after deadline", past, &net.OpError{Op: "read", Err: timeoutError{}}, true},
		{"timeout before deadline", future, &net.OpError{Op: "read", Err: timeoutError{}}, false},
		{"timeout without deadline", context.Background(), &net.OpError{Op: "read", Err: timeoutError{}}, false},
		{"protocol error after deadline", past, protocol, true}, // ctx.Err() wins once ctx ended
		{"protocol error", future, protocol, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := deadlineErr(tc.ctx, tc.err)
			if errors.Is(got, context.DeadlineExceeded) != tc.deadline {
				t.Fatalf("deadlineErr = %v, want DeadlineExceeded=%v", got, tc.deadline)
			}
			if !tc.deadline && (!errors.Is(got, tc.err) || got.Error() != tc.err.Error()) {
				t.Fatalf("error changed: %v", got)
			}
		})
	}
}
