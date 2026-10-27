package sshpolicy

import (
	"context"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// ClientConfig returns the client configuration for a pinned server: the
// strong algorithms, the pin callback and only the pinned type's host-key
// algorithms. The PinCheck interprets a failed handshake.
func ClientConfig(user string, auth []ssh.AuthMethod, pin NormalizedPin, addr string) (*ssh.ClientConfig, *PinCheck) {
	cb, pc := NewPinCallback(pin, addr)
	return &ssh.ClientConfig{
		Config:            Algorithms(),
		User:              user,
		Auth:              auth,
		HostKeyCallback:   cb,
		HostKeyAlgorithms: pin.Algorithms(),
	}, pc
}

// Conn is an established SSH client connection.
type Conn struct {
	Conn  ssh.Conn
	Chans <-chan ssh.NewChannel
	Reqs  <-chan *ssh.Request
}

// Handshake runs the SSH client handshake on conn, bounded by ctx: conn's
// deadline is set to ctx's deadline, and conn is closed when ctx ends
// before the handshake finished. On success the deadline is left in place
// for the caller's own setup (clear it with conn.SetDeadline(time.Time{})).
//
// When ctx ended during the handshake the error is ctx.Err() (so callers
// can tell a timeout or cancellation from a protocol failure); otherwise
// it is x/crypto/ssh's error. Handshake does not close conn on failure.
func Handshake(ctx context.Context, conn net.Conn, addr string, cfg *ssh.ClientConfig) (*Conn, error) {
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	stop := context.AfterFunc(ctx, func() { discard(conn.Close()) })
	sc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	aborted := !stop()
	if aborted {
		if err == nil {
			discard(sc.Close())
		}
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return &Conn{Conn: sc, Chans: chans, Reqs: reqs}, nil
}

// DefaultKeepaliveMisses is the number of keepalive intervals without a
// reply after which a connection is considered dead.
const DefaultKeepaliveMisses = 3

// Requester sends global requests (an *ssh.Client or ssh.Conn).
type Requester interface {
	SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error)
}

// Keepalive sends keepalive@openssh.com requests every interval (one in
// flight at a time) until done is closed. It returns true when no reply
// arrived for DefaultKeepaliveMisses intervals (the caller then closes the
// connection, which also ends the outstanding request) and false when done
// was closed. Request goroutines are tracked in wg; each ends when its
// reply arrives or the connection closes.
func Keepalive(done <-chan struct{}, conn Requester, interval time.Duration, wg *sync.WaitGroup) bool {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	replies := make(chan struct{}, 1)
	pending := false
	var sentAt time.Time
	for {
		select {
		case <-done:
			return false
		case <-replies:
			pending = false
		case now := <-ticker.C:
			if pending {
				if now.Sub(sentAt) >= DefaultKeepaliveMisses*interval {
					return true
				}
				continue
			}
			pending = true
			sentAt = now
			wg.Add(1)
			go func() {
				defer wg.Done()
				// Any reply (including a failure) proves the server is alive.
				if _, _, err := conn.SendRequest("keepalive@openssh.com", true, nil); err == nil {
					select {
					case replies <- struct{}{}:
					default:
					}
				}
			}()
		}
	}
}

func discard(error) {}
