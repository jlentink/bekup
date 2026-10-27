package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"
)

// Per-run proxy limits.
const (
	// proxyMaxConns bounds the concurrent connections of one run (the
	// native tools open one or a few).
	proxyMaxConns = 16
	// listenRetryFor is how long the proxy retries binding while the
	// address of a just-joined network is not configured yet.
	listenRetryFor = 5 * time.Second
)

// dialFunc opens one upstream connection to the run's single target.
type dialFunc func(ctx context.Context) (net.Conn, error)

// proxy is the per-run TCP proxy (D-029, D-132): it listens on the
// runner's address in the per-run network, accepts connections only from
// the run's dump container and forwards each to the one target, directly
// through the target allowlist dialer or through the SSH tunnel. TLS stays
// end to end between the client and the database; the proxy never sees
// plaintext of a TLS connection.
type proxy struct {
	ln      net.Listener
	allowed netip.Addr
	dial    dialFunc
	log     *slog.Logger
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	once    sync.Once
	sem     chan struct{}
	// rejected counts connections from other addresses (tests, logs).
	mu       sync.Mutex
	rejected int
}

// startProxy serves ln until Close; it owns ln.
func startProxy(ctx context.Context, ln net.Listener, allowed netip.Addr, dial dialFunc, log *slog.Logger) *proxy {
	pctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p := &proxy{ln: ln, allowed: allowed.Unmap(), dial: dial, log: log, cancel: cancel, sem: make(chan struct{}, proxyMaxConns)}
	p.wg.Add(1)
	go p.serve(pctx)
	return p
}

// listenRetry listens on addr, retrying briefly while the address of a
// just-joined network is not configured yet.
func listenRetry(ctx context.Context, addr netip.AddrPort, retryFor time.Duration) (net.Listener, error) {
	lc := net.ListenConfig{}
	deadline := time.Now().Add(retryFor)
	for {
		ln, err := lc.Listen(ctx, "tcp4", addr.String())
		if err == nil {
			return ln, nil
		}
		if !errors.Is(err, syscall.EADDRNOTAVAIL) || time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: proxy listen: %w", ErrUpstream, err)
		}
		t := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

// Addr returns the listen address.
func (p *proxy) Addr() netip.AddrPort {
	ap, err := netip.ParseAddrPort(p.ln.Addr().String())
	if err != nil {
		return netip.AddrPort{}
	}
	return ap
}

func (p *proxy) serve(ctx context.Context) {
	defer p.wg.Done()
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return // closed
		}
		if !p.permit(c) {
			discardErr(c.Close())
			continue
		}
		select {
		case p.sem <- struct{}{}:
		default:
			p.log.WarnContext(ctx, "run_proxy_connection_limit")
			discardErr(c.Close())
			continue
		}
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer func() { <-p.sem }()
			p.handle(ctx, c)
		}()
	}
}

// permit accepts only the dump container's address.
func (p *proxy) permit(c net.Conn) bool {
	ap, err := netip.ParseAddrPort(c.RemoteAddr().String())
	if err == nil && ap.Addr().Unmap() == p.allowed {
		return true
	}
	p.mu.Lock()
	p.rejected++
	p.mu.Unlock()
	p.log.Warn("run_proxy_rejected_peer", slog.String("peer", c.RemoteAddr().String()))
	return false
}

// Rejected returns the number of refused peers.
func (p *proxy) Rejected() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rejected
}

func (p *proxy) handle(ctx context.Context, local net.Conn) {
	defer func() { discardErr(local.Close()) }()
	remote, err := p.dial(ctx)
	if err != nil {
		if ctx.Err() == nil {
			p.log.WarnContext(ctx, "run_proxy_upstream_failed", slog.String("error", err.Error()))
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

type closeWriter interface{ CloseWrite() error }

// copyHalf copies src to dst and half-closes dst.
func copyHalf(dst, src net.Conn) {
	_, err := io.Copy(dst, src)
	discardErr(err)
	if cw, ok := dst.(closeWriter); ok {
		discardErr(cw.CloseWrite())
	} else {
		discardErr(dst.Close())
	}
}

// Close stops accepting, closes every connection and waits for the
// handlers. It is idempotent.
func (p *proxy) Close() {
	p.once.Do(func() {
		discardErr(p.ln.Close())
		p.cancel()
		p.wg.Wait()
	})
}
