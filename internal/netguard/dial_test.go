package netguard

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// listen starts a TCP listener on 127.0.0.1 that accepts and closes
// connections, and returns its port.
func listen(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeLog(t, ln) })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			if err := c.Close(); err != nil {
				return
			}
		}
	}()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func closeLog(t *testing.T, c io.Closer) {
	t.Helper()
	if err := c.Close(); err != nil {
		t.Logf("close: %v", err)
	}
}

func get(t *testing.T, c *http.Client, url string) error {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer closeLog(t, resp.Body)
	return nil
}

// fakeLookup returns the answers in order, one per call (the last repeats).
func fakeLookup(answers ...[]string) (lookupFunc, *atomic.Int32) {
	var calls atomic.Int32
	return func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		i := int(calls.Add(1)) - 1
		if i >= len(answers) {
			i = len(answers) - 1
		}
		out := make([]netip.Addr, 0, len(answers[i]))
		for _, s := range answers[i] {
			out = append(out, netip.MustParseAddr(s))
		}
		return out, nil
	}, &calls
}

func TestDNSRebinding(t *testing.T) {
	port := listen(t)
	g := mustGuard(t, Options{})
	lookup, calls := fakeLookup([]string{"93.184.216.34"}, []string{"127.0.0.1"})
	g.lookup = lookup

	ctx := context.Background()
	if _, err := g.ResolveAndCheck(ctx, "rebind.example"); err != nil {
		t.Fatalf("validation-time check refused the public answer: %v", err)
	}
	_, err := g.DialContext(ctx, "tcp", net.JoinHostPort("rebind.example", port))
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("dial after rebinding = %v, want ErrBlockedAddress", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("lookup calls = %d, want 2 (no caching)", calls.Load())
	}
	if !errors.Is(Normalize(err), ErrNotAllowed) {
		t.Fatalf("Normalize = %v", Normalize(err))
	}
}

func TestResolveAndCheckMixedAnswers(t *testing.T) {
	g := mustGuard(t, Options{})
	g.lookup, _ = fakeLookup([]string{"93.184.216.34", "10.0.0.5"})
	_, err := g.ResolveAndCheck(context.Background(), "mixed.example")
	var be *BlockedAddressError
	if !errors.As(err, &be) || be.IP != netip.MustParseAddr("10.0.0.5") || be.Reason != ReasonPrivate {
		t.Fatalf("ResolveAndCheck = %v, want private 10.0.0.5", err)
	}
	for _, h := range []string{"[::1]", "169.254.169.254", "::ffff:127.0.0.1"} {
		if _, err := g.ResolveAndCheck(context.Background(), h); !errors.Is(err, ErrBlockedAddress) {
			t.Fatalf("ResolveAndCheck(%s) = %v", h, err)
		}
	}
	// The shared host-name rule (internal/hostname) applies before any
	// lookup; a trailing dot and upper case are normalized first.
	for _, h := range []string{"bad host!", "a_b.example", "1.2.3", "a..b", "-x.example"} {
		if _, err := g.ResolveAndCheck(context.Background(), h); err == nil {
			t.Fatalf("invalid host %q accepted", h)
		}
	}
	g.lookup, _ = fakeLookup([]string{"93.184.216.34"})
	if _, err := g.ResolveAndCheck(context.Background(), "Public.Example."); err != nil {
		t.Fatalf("normalized host refused: %v", err)
	}
}

func TestControlHookIsAuthoritative(t *testing.T) {
	port := listen(t)
	g := mustGuard(t, Options{})
	// The raw dialer resolves by itself; Control still refuses loopback.
	for _, addr := range []string{"127.0.0.1:" + port, "localhost:" + port} {
		_, err := g.Dialer().DialContext(context.Background(), "tcp", addr)
		if !errors.Is(err, ErrBlockedAddress) {
			t.Fatalf("Dialer().Dial(%s) = %v, want ErrBlockedAddress", addr, err)
		}
	}
	// Control on the internal dialer, bypassing the pre-check.
	_, err := g.dialer("").DialContext(context.Background(), "tcp", "127.0.0.1:"+port)
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("dialer().Dial = %v, want ErrBlockedAddress", err)
	}
}

func TestDialAllowlist(t *testing.T) {
	port := listen(t)
	ctx := context.Background()

	// Dev mode: loopback is soft, so a host entry can re-allow it.
	g := mustGuard(t, Options{LoopbackAllowable: true, AllowHosts: []string{"minio.lan"}})
	g.lookup, _ = fakeLookup([]string{"127.0.0.1"})
	conn, err := g.DialContext(ctx, "tcp", net.JoinHostPort("minio.lan", port))
	if err != nil {
		t.Fatalf("allowlisted host refused: %v", err)
	}
	closeLog(t, conn)
	if _, err := g.DialContext(ctx, "tcp", net.JoinHostPort("other.lan", port)); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("non-allowlisted host = %v", err)
	}
	// The raw Dialer does not know the host name: host entries do not apply.
	if _, err := g.Dialer().DialContext(ctx, "tcp", "127.0.0.1:"+port); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("raw dialer = %v", err)
	}

	// CIDR entries apply to both.
	g2 := mustGuard(t, Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.1/32"}})
	conn, err = g2.DialContext(ctx, "tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatalf("CIDR-allowlisted literal refused: %v", err)
	}
	closeLog(t, conn)
	conn, err = g2.Dialer().DialContext(ctx, "tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatalf("CIDR-allowlisted raw dial refused: %v", err)
	}
	closeLog(t, conn)
}

func TestDialSkipsDeniedAddresses(t *testing.T) {
	port := listen(t)
	g := mustGuard(t, Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.1/32"}})
	g.lookup, _ = fakeLookup([]string{"10.9.9.9", "169.254.169.254", "127.0.0.1"})
	conn, err := g.DialContext(context.Background(), "tcp", net.JoinHostPort("multi.example", port))
	if err != nil {
		t.Fatalf("dial = %v", err)
	}
	closeLog(t, conn)
}

func TestDialInputErrors(t *testing.T) {
	g := mustGuard(t, Options{})
	ctx := context.Background()
	for _, network := range []string{"udp", "unix", "ip", ""} {
		if _, err := g.DialContext(ctx, network, "8.8.8.8:53"); !errors.Is(err, ErrUnsupportedNetwork) {
			t.Fatalf("network %q = %v", network, err)
		}
	}
	for _, addr := range []string{"8.8.8.8", "8.8.8.8:0", "8.8.8.8:70000", ":443"} {
		if _, err := g.DialContext(ctx, "tcp", addr); err == nil {
			t.Fatalf("address %q accepted", addr)
		}
	}
	if _, err := g.DialContext(ctx, "tcp6", "8.8.8.8:53"); !errors.Is(err, ErrNoAddresses) {
		t.Fatalf("tcp6 to IPv4 literal = %v", err)
	}
	g.lookup, _ = fakeLookup([]string{})
	if _, err := g.DialContext(ctx, "tcp", "empty.example:443"); !errors.Is(err, ErrNoAddresses) {
		t.Fatalf("empty answer = %v", err)
	}
}

func TestHTTPRedirectNotFollowed(t *testing.T) {
	var targetHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/target", func(http.ResponseWriter, *http.Request) { targetHits.Add(1) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/target", http.StatusFound)
	})

	g := mustGuard(t, Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.1/32"}})
	c := g.NewHTTPClient(nil, 5*time.Second)
	err := get(t, c, srv.URL+"/redirect")
	if !errors.Is(err, ErrRedirect) {
		t.Fatalf("redirect = %v, want ErrRedirect", err)
	}
	if targetHits.Load() != 0 {
		t.Fatal("redirect target was requested")
	}
	if !errors.Is(Normalize(err), ErrRedirect) {
		t.Fatalf("Normalize = %v", Normalize(err))
	}
}

func TestHTTPBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("blocked server was reached")
	}))
	t.Cleanup(srv.Close)
	g := mustGuard(t, Options{})
	err := get(t, g.NewHTTPClient(nil, 5*time.Second), srv.URL)
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("GET loopback = %v, want ErrBlockedAddress", err)
	}
}

func TestHTTPTransportSettings(t *testing.T) {
	g := mustGuard(t, Options{})
	in := &tls.Config{MinVersion: tls.VersionTLS10, ServerName: "x"} //nolint:gosec // G402: testing that it is raised
	tr := g.NewHTTPTransport(in)
	if tr.Proxy != nil {
		t.Fatal("proxy set")
	}
	if tr.TLSClientConfig.MinVersion != tls.VersionTLS12 || in.MinVersion != tls.VersionTLS10 {
		t.Fatal("MinVersion not raised on a clone")
	}
	if g.NewHTTPTransport(nil).TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatal("default MinVersion")
	}
	if tr13 := g.NewHTTPTransport(&tls.Config{MinVersion: tls.VersionTLS13}); tr13.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Fatal("TLS 1.3 minimum lowered")
	}
}

func TestLimits(t *testing.T) {
	if b, err := ReadAllLimited(strings.NewReader("12345"), 5); err != nil || string(b) != "12345" {
		t.Fatalf("at limit: %q %v", b, err)
	}
	if _, err := ReadAllLimited(strings.NewReader("123456"), 5); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("over limit: %v", err)
	}
	b, err := io.ReadAll(LimitReader(strings.NewReader("12345"), 5))
	if err != nil || string(b) != "12345" {
		t.Fatalf("LimitReader at limit: %q %v", b, err)
	}
	var buf bytes.Buffer
	_, err = io.Copy(&buf, LimitReader(strings.NewReader("123456"), 5))
	if !errors.Is(err, ErrResponseTooLarge) || buf.Len() != 5 {
		t.Fatalf("LimitReader over limit: %d %v", buf.Len(), err)
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestNormalize(t *testing.T) {
	opErr := func(e error) error { return &net.OpError{Op: "dial", Net: "tcp", Err: e} }
	tests := []struct {
		name string
		in   error
		want error
	}{
		{"nil", nil, nil},
		{"canceled", fmt.Errorf("x: %w", context.Canceled), context.Canceled},
		{"blocked", opErr(&BlockedAddressError{IP: netip.MustParseAddr("10.0.0.1"), Reason: ReasonPrivate}), ErrNotAllowed},
		{"network", ErrUnsupportedNetwork, ErrNotAllowed},
		{"redirect", fmt.Errorf("get: %w", ErrRedirect), ErrRedirect},
		{"too large", ErrResponseTooLarge, ErrResponseTooLarge},
		{"refused", opErr(os.NewSyscallError("connect", syscall.ECONNREFUSED)), ErrUnreachable},
		{"reset", opErr(syscall.ECONNRESET), ErrUnreachable},
		{"host unreachable", opErr(syscall.EHOSTUNREACH), ErrUnreachable},
		{"deadline", context.DeadlineExceeded, ErrUnreachable},
		{"timeout", timeoutError{}, ErrUnreachable},
		{"dns", &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}, ErrUnreachable},
		{"no addrs", ErrNoAddresses, ErrUnreachable},
		{"unknown authority", fmt.Errorf("tls: %w", x509.UnknownAuthorityError{}), ErrTLS},
		{"hostname", x509.HostnameError{Host: "x"}, ErrTLS},
		{"cert verify", &tls.CertificateVerificationError{Err: errors.New("bad")}, ErrTLS},
		{"alert", opErr(tls.AlertError(40)), ErrTLS},
		{"record header", tls.RecordHeaderError{Msg: "not tls"}, ErrTLS},
		{"other", errors.New("boom"), ErrFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Normalize(tc.in); got != tc.want { //nolint:errorlint // sentinels are returned unwrapped
				t.Fatalf("Normalize(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
