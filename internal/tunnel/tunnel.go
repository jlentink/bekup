package tunnel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/jlentink/bekup/internal/sshpolicy"
)

// Tunnel is an authenticated SSH connection to a bastion that forwards to
// exactly one target. It is safe for concurrent use.
type Tunnel struct {
	s      *settings
	client *ssh.Client

	done      chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	err       error
	wg        sync.WaitGroup
}

// clientConn is the result of a successful handshake.
type clientConn struct {
	conn  ssh.Conn
	chans <-chan ssh.NewChannel
	reqs  <-chan *ssh.Request
}

func (c *clientConn) Close() error { return c.conn.Close() }

// Dial validates cfg, connects to the bastion through cfg.Dialer, verifies
// the pinned host key and authenticates. It always destroys cfg.Auth.
//
// A host key other than the pinned one, or a server that does not offer the
// pinned key type, yields a *HostKeyMismatchError (errors.Is
// ErrHostKeyMismatch): treat it as a security event.
func Dial(ctx context.Context, cfg *Config) (*Tunnel, error) {
	defer cfg.Auth.destroy()
	s, err := cfg.validate()
	if err != nil {
		return nil, err
	}

	method, key, err := authMethod(&cfg.Auth)
	if err != nil {
		return nil, err
	}
	defer key.Destroy()

	clientCfg, check := sshpolicy.ClientConfig(s.user, []ssh.AuthMethod{method}, s.pin, s.addr)
	cc, err := handshake(ctx, "dial", s.addr, s.dialer, s.dialTO, clientCfg)
	if err != nil {
		if v := check.Violation(err); v != nil {
			if errors.Is(v, sshpolicy.ErrHostKeyMismatch) {
				return nil, newError(ErrHostKeyMismatch, "dial", s.addr, v)
			}
			return nil, newError(ErrUnsupportedHostKey, "dial", s.addr, v)
		}
		return nil, err
	}

	t := &Tunnel{
		s:      s,
		client: ssh.NewClient(cc.conn, cc.chans, cc.reqs),
		done:   make(chan struct{}),
	}
	t.wg.Add(2)
	go t.waitLoop()
	go t.keepaliveLoop()
	return t, nil
}

// authMethod builds the SSH auth method. key is the parsed private key
// (destroy it after the handshake) or nil for password authentication.
func authMethod(a *Auth) (ssh.AuthMethod, *sshpolicy.ClientKey, error) {
	if a.Password.Len() > 0 {
		pw := a.Password
		// x/crypto/ssh needs a string; see the limitation on Auth.
		return ssh.PasswordCallback(func() (string, error) {
			if pw.Len() == 0 {
				return "", errors.New("password unavailable")
			}
			return string(pw.Bytes()), nil
		}), nil, nil
	}
	key, err := sshpolicy.ParseClientKey(a.PrivateKey.Bytes(), a.Passphrase.Bytes())
	if err != nil {
		// Static reasons only; never the parser error.
		return nil, nil, invalid("auth: " + err.Error())
	}
	return ssh.PublicKeys(key.Signer), key, nil
}

// handshake dials addr and runs the SSH handshake within timeout (and
// ctx). Errors are normalized *Error values (the SSH protocol error is
// attached as cause), or ctx.Err() if ctx was canceled.
func handshake(ctx context.Context, op, addr string, dialer Dialer, timeout time.Duration, cfg *ssh.ClientConfig) (*clientConn, error) {
	hctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := dialer.DialContext(hctx, "tcp", addr)
	if err != nil {
		if cerr := canceled(ctx); cerr != nil {
			return nil, cerr
		}
		return nil, newError(classifyNetErr(hctx, err, ErrUnreachable), op, addr, err)
	}
	cc, err := sshpolicy.Handshake(hctx, conn, addr, cfg)
	if err != nil {
		discardErr(conn.Close())
		if cerr := canceled(ctx); cerr != nil {
			return nil, cerr
		}
		if hctx.Err() != nil && errors.Is(err, hctx.Err()) {
			return nil, newError(ErrTimeout, op, addr, nil)
		}
		return nil, newError(classifyHandshake(hctx, err), op, addr, err)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		discardErr(cc.Conn.Close())
		return nil, newError(ErrHandshake, op, addr, err)
	}
	return &clientConn{conn: cc.Conn, chans: cc.Chans, reqs: cc.Reqs}, nil
}

// canceled returns ctx.Err() if ctx was canceled (not timed out).
func canceled(ctx context.Context) error {
	if err := ctx.Err(); err != nil && errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// waitLoop shuts the tunnel down when the SSH connection ends.
func (t *Tunnel) waitLoop() {
	defer t.wg.Done()
	err := t.client.Wait()
	t.shutdown(newError(ErrClosed, "dial", t.s.addr, waitCause(err)))
}

func waitCause(err error) error {
	if err == nil || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// keepaliveLoop sends keepalive@openssh.com requests (one in flight) and
// closes the tunnel after sshpolicy.DefaultKeepaliveMisses intervals
// without a reply.
func (t *Tunnel) keepaliveLoop() {
	defer t.wg.Done()
	if sshpolicy.Keepalive(t.done, t.client, t.s.keepalive, &t.wg) {
		t.s.logger.Warn("ssh_tunnel_keepalive_timeout", slog.String("addr", t.s.addr))
		t.shutdown(newError(ErrTimeout, "keepalive", t.s.addr, nil))
	}
}

// shutdown closes the tunnel once, recording why.
func (t *Tunnel) shutdown(cause error) {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.err = cause
		t.mu.Unlock()
		close(t.done)
		discardErr(t.client.Close())
	})
}

// Close closes the tunnel and waits for its background goroutines. It is
// safe to call more than once.
func (t *Tunnel) Close() error {
	t.shutdown(nil)
	t.wg.Wait()
	return nil
}

// Done is closed when the tunnel is closed or the connection is lost.
func (t *Tunnel) Done() <-chan struct{} { return t.done }

// Err returns why the tunnel ended: nil while it is open or after Close,
// otherwise an *Error (ErrClosed for a lost connection, ErrTimeout for a
// keepalive timeout).
func (t *Tunnel) Err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

func (t *Tunnel) closedErr(op string) error {
	if err := t.Err(); err != nil {
		return newError(ErrClosed, op, t.s.addr, err)
	}
	return newError(ErrClosed, op, t.s.addr, nil)
}

// DialTarget opens a connection to the configured target through the
// bastion.
func (t *Tunnel) DialTarget(ctx context.Context) (net.Conn, error) {
	const op = "dial_target"
	select {
	case <-t.done:
		return nil, t.closedErr(op)
	default:
	}
	conn, err := t.client.DialContext(ctx, "tcp", t.s.targetAddr)
	if err != nil {
		if cerr := canceled(ctx); cerr != nil {
			return nil, cerr
		}
		select {
		case <-t.done:
			return nil, t.closedErr(op)
		default:
		}
		return nil, newError(classifyNetErr(ctx, err, ErrUnreachable), op, t.s.targetAddr, err)
	}
	return conn, nil
}

// Forward accepts connections on ln and forwards each to the target, with
// at most MaxConns at a time. It closes ln and returns after all handlers
// finished: nil when ctx is done, an *Error with Kind ErrClosed when the
// tunnel ended or ln failed.
func (t *Tunnel) Forward(ctx context.Context, ln net.Listener) error {
	const op = "forward"
	defer func() { discardErr(ln.Close()) }()
	fctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var handlers sync.WaitGroup
	handlers.Add(1)
	go func() {
		defer handlers.Done()
		select {
		case <-t.done:
			cancel()
		case <-fctx.Done():
		}
	}()
	stopClose := context.AfterFunc(fctx, func() { discardErr(ln.Close()) })
	defer stopClose()

	sem := make(chan struct{}, t.s.maxConns)
	var acceptErr error
loop:
	for {
		select {
		case sem <- struct{}{}:
		case <-fctx.Done():
			break loop
		}
		c, err := ln.Accept()
		if err != nil {
			<-sem
			if fctx.Err() == nil {
				acceptErr = err
			}
			break
		}
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			defer func() { <-sem }()
			t.pipe(fctx, c)
		}()
	}
	cancel()
	handlers.Wait()

	select {
	case <-t.done:
		if ctx.Err() == nil {
			return t.closedErr(op)
		}
	default:
	}
	if acceptErr != nil {
		return newError(ErrClosed, op, ln.Addr().String(), acceptErr)
	}
	return nil
}

// closeWriter is implemented by TCP and SSH channel connections.
type closeWriter interface{ CloseWrite() error }

// pipe forwards one accepted connection to the target.
func (t *Tunnel) pipe(ctx context.Context, local net.Conn) {
	defer func() { discardErr(local.Close()) }()
	remote, err := t.DialTarget(ctx)
	if err != nil {
		if ctx.Err() == nil {
			t.s.logger.WarnContext(ctx, "ssh_tunnel_forward_failed",
				slog.String("addr", t.s.addr), slog.String("error", err.Error()))
		}
		return
	}
	defer func() { discardErr(remote.Close()) }()
	stop := context.AfterFunc(ctx, func() {
		discardErr(local.Close())
		discardErr(remote.Close())
	})
	defer stop()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		copyHalf(remote, local)
	}()
	copyHalf(local, remote)
	wg.Wait()
}

// copyHalf copies src to dst and then half-closes dst.
func copyHalf(dst, src net.Conn) {
	discardCopy(io.Copy(dst, src))
	if cw, ok := dst.(closeWriter); ok {
		discardErr(cw.CloseWrite())
	} else {
		discardErr(dst.Close())
	}
}
