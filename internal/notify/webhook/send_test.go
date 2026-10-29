package webhook

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/netguard"
)

// loopbackGuard allowlists 127.0.0.1 for the httptest servers, like a dev
// instance with a loopback allowlist entry.
func loopbackGuard(t *testing.T) *netguard.Guard {
	t.Helper()
	return mustGuard(t, netguard.Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.1/32"}})
}

func tlsConfigFor(srv *httptest.Server) *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}

func newSender(t *testing.T, g *netguard.Guard, srv *httptest.Server, mod func(*SenderOptions)) *Sender {
	t.Helper()
	opts := SenderOptions{Version: "1.2.3", Timeout: 5 * time.Second}
	if srv != nil && srv.TLS != nil {
		opts.TLSConfig = tlsConfigFor(srv)
	}
	if mod != nil {
		mod(&opts)
	}
	s, err := NewSender(g, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.CloseIdleConnections)
	return s
}

func portOf(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}

func delivery(u string) Delivery {
	return Delivery{
		URL:        u,
		HMACKey:    []byte(vectorKey),
		EventID:    "evt_01",
		EventType:  "backup.failed",
		DeliveryID: "dlv_01",
		Payload:    []byte(`{"id":"evt_01","type":"backup.failed","data":{"job":"nightly"}}`),
	}
}

type captured struct {
	method string
	header http.Header
	body   []byte
}

func TestSendSuccessHeadersAndSignature(t *testing.T) {
	var (
		mu   sync.Mutex
		got  []captured
		code = http.StatusAccepted
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		got = append(got, captured{r.Method, r.Header.Clone(), b})
		mu.Unlock()
		w.WriteHeader(code)
		if _, err := io.WriteString(w, "ignored response content"); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)

	now := time.Unix(vectorTimestamp, 0)
	s := newSender(t, loopbackGuard(t), srv, func(o *SenderOptions) { o.Now = func() time.Time { return now } })
	d := delivery(srv.URL + "/hook?k=v")
	res := s.Send(context.Background(), d)
	if res.Outcome != OutcomeSuccess || res.Code != CodeOK || res.StatusCode != http.StatusAccepted || res.Err != nil {
		t.Fatalf("Send = %+v", res)
	}
	if res.DeliveryID != "dlv_01" || res.Timestamp != vectorTimestamp {
		t.Fatalf("result ids = %+v", res)
	}
	if len(got) != 1 {
		t.Fatalf("%d requests", len(got))
	}
	c := got[0]
	want := map[string]string{
		"Content-Type":  "application/json",
		"User-Agent":    "bekup/1.2.3",
		HeaderEvent:     "backup.failed",
		HeaderDelivery:  "dlv_01",
		HeaderTimestamp: "1767225600",
		HeaderSignature: Sign([]byte(vectorKey), vectorTimestamp, "dlv_01", "backup.failed", d.Payload),
	}
	if c.method != http.MethodPost {
		t.Fatalf("method %s", c.method)
	}
	for k, v := range want {
		if c.header.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, c.header.Get(k), v)
		}
	}
	if !bytes.Equal(c.body, d.Payload) {
		t.Fatalf("body = %s", c.body)
	}
	if err := Verify([]byte(vectorKey), c.header.Get(HeaderSignature), c.header.Get(HeaderTimestamp),
		c.header.Get(HeaderDelivery), c.header.Get(HeaderEvent), c.body, now.Add(time.Minute), 0); err != nil {
		t.Fatalf("receiver verification: %v", err)
	}

	// An empty DeliveryID gets a fresh random one per call.
	d.DeliveryID = ""
	r1, r2 := s.Send(context.Background(), d), s.Send(context.Background(), d)
	if len(r1.DeliveryID) != 32 || r1.DeliveryID == r2.DeliveryID {
		t.Fatalf("generated delivery ids %q %q", r1.DeliveryID, r2.DeliveryID)
	}
	if got[2].header.Get(HeaderDelivery) != r2.DeliveryID {
		t.Fatal("header does not carry the generated id")
	}
}

func TestSendStatusClassification(t *testing.T) {
	var status atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status.Load() == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "42")
		}
		w.WriteHeader(int(status.Load()))
	}))
	t.Cleanup(srv.Close)
	s := newSender(t, loopbackGuard(t), srv, nil)
	tests := []struct {
		status  int
		outcome Outcome
		after   time.Duration
	}{
		{200, OutcomeSuccess, 0},
		{400, OutcomePermanent, 0},
		{404, OutcomePermanent, 0},
		{408, OutcomeRetryable, 0},
		{429, OutcomeRetryable, 42 * time.Second},
		{500, OutcomeRetryable, 0},
		{503, OutcomeRetryable, 0},
	}
	for _, tt := range tests {
		status.Store(int64(tt.status))
		res := s.Send(context.Background(), delivery(srv.URL))
		if res.Outcome != tt.outcome || res.StatusCode != tt.status || res.RetryAfter != tt.after {
			t.Errorf("%d: %+v", tt.status, res)
		}
	}
}

func TestSendRedirectNotFollowed(t *testing.T) {
	var targetHits atomic.Int32
	mux := http.NewServeMux()
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/target", func(http.ResponseWriter, *http.Request) { targetHits.Add(1) })
	mux.HandleFunc("/internal", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	mux.HandleFunc("/same", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/target", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/nolocation", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMultipleChoices)
	})
	s := newSender(t, loopbackGuard(t), srv, nil)
	for _, path := range []string{"/internal", "/same", "/nolocation"} {
		res := s.Send(context.Background(), delivery(srv.URL+path))
		if res.Outcome != OutcomePermanent || res.Code != CodeRedirect || !errors.Is(res.Err, ErrRedirect) {
			t.Errorf("%s: %+v", path, res)
		}
	}
	if targetHits.Load() != 0 {
		t.Fatal("redirect target was requested")
	}
}

func TestSendTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	s := newSender(t, loopbackGuard(t), srv, func(o *SenderOptions) { o.Timeout = 300 * time.Millisecond })
	start := time.Now()
	res := s.Send(context.Background(), delivery(srv.URL))
	if res.Outcome != OutcomeRetryable || res.Code != CodeUnreachable {
		t.Fatalf("slow server: %+v", res)
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("timeout not enforced: %s", el)
	}
}

func TestSendCallerCanceled(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	s := newSender(t, loopbackGuard(t), srv, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if res := s.Send(ctx, delivery(srv.URL)); res.Outcome != OutcomeAborted || res.Code != CodeCanceled {
		t.Fatalf("canceled: %+v", res)
	}
	if res := s.Send(ctx, delivery(srv.URL)); res.Outcome != OutcomeAborted {
		t.Fatalf("already canceled: %+v", res)
	}
}

func TestSendHugeResponseNotRead(t *testing.T) {
	const total = 256 << 20
	written := make(chan int64, 1)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		chunk := make([]byte, 32<<10)
		var n int64
		for n < total {
			m, err := w.Write(chunk)
			n += int64(m)
			if err != nil {
				break
			}
		}
		written <- n
	}))
	t.Cleanup(srv.Close)
	s := newSender(t, loopbackGuard(t), srv, nil)
	start := time.Now()
	res := s.Send(context.Background(), delivery(srv.URL))
	if res.Outcome != OutcomeSuccess {
		t.Fatalf("huge body: %+v", res)
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("Send took %s", el)
	}
	select {
	case n := <-written:
		if n >= 64<<20 {
			t.Fatalf("server wrote %d bytes: the body was read", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server kept writing: the connection was not closed")
	}
}

func TestSendBlockedAddresses(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	port := portOf(t, srv)

	// Production guard: loopback is hard-denied.
	s := newSender(t, mustGuard(t, netguard.Options{}), srv, nil)
	for _, u := range []string{
		srv.URL,
		"https://[::ffff:127.0.0.1]:" + port,
		"https://169.254.169.254/latest",
		"https://10.0.0.1:" + port,
	} {
		res := s.Send(context.Background(), delivery(u))
		if res.Outcome != OutcomePermanent || res.Code != CodeNotAllowed {
			t.Errorf("%s: %+v", u, res)
		}
		if res.Err != nil && (strings.Contains(res.Err.Error(), "127.0.0.1") || strings.Contains(res.Err.Error(), port)) {
			t.Errorf("error leaks the destination: %v", res.Err)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("blocked server was reached")
	}
}

// TestSendDNSRebinding: the name passes the input-time check (the fake
// resolver answers a public address) but resolves to loopback when the
// sender dials; the dial-time guard refuses it.
func TestSendDNSRebinding(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	port := portOf(t, srv)

	g := mustGuard(t, netguard.Options{})
	raw := "https://localhost:" + port + "/hook"
	first := fakeResolver{guard: g, addrs: map[string][]string{"localhost": {"93.184.216.34"}}}
	if _, err := ValidateURL(context.Background(), raw, first, URLOptions{}); err != nil {
		t.Fatalf("input-time check: %v", err)
	}
	// Sanity: the real resolution of the name is loopback.
	if addrs, err := net.DefaultResolver.LookupNetIP(context.Background(), "ip", "localhost"); err != nil || len(addrs) == 0 || !addrs[0].Unmap().IsLoopback() {
		t.Skipf("localhost does not resolve to loopback here: %v %v", addrs, err)
	}
	res := newSender(t, g, srv, nil).Send(context.Background(), delivery(raw))
	if res.Outcome != OutcomePermanent || res.Code != CodeNotAllowed {
		t.Fatalf("rebinding: %+v", res)
	}
	if hits.Load() != 0 {
		t.Fatal("rebinding target was reached")
	}
}

func TestSendTLSError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)
	// System roots do not trust the httptest certificate.
	s := newSender(t, loopbackGuard(t), nil, nil)
	res := s.Send(context.Background(), delivery(srv.URL))
	if res.Outcome != OutcomePermanent || res.Code != CodeTLS {
		t.Fatalf("untrusted cert: %+v", res)
	}
}

func TestSendRefusesOldTLS(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{MaxVersion: tls.VersionTLS11} //nolint:gosec // G402: the server under test offers only TLS 1.1
	srv.StartTLS()
	t.Cleanup(srv.Close)
	s := newSender(t, loopbackGuard(t), srv, nil)
	// The handshake fails; depending on how the server aborts it the
	// client sees an alert (tls) or a closed connection (unreachable).
	if res := s.Send(context.Background(), delivery(srv.URL)); res.Outcome == OutcomeSuccess || (res.Code != CodeTLS && res.Code != CodeUnreachable) {
		t.Fatalf("TLS 1.1 server: %+v", res)
	}
}

func TestSendHTTP(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	g := loopbackGuard(t)
	if res := newSender(t, g, nil, nil).Send(context.Background(), delivery(srv.URL)); res.Outcome != OutcomePermanent || !errors.Is(res.Err, ErrInsecureScheme) {
		t.Fatalf("http outside dev: %+v", res)
	}
	if hits.Load() != 0 {
		t.Fatal("http receiver reached outside dev mode")
	}
	dev := newSender(t, g, nil, func(o *SenderOptions) { o.AllowHTTP = true })
	if res := dev.Send(context.Background(), delivery(srv.URL)); res.Outcome != OutcomeSuccess {
		t.Fatalf("http in dev: %+v", res)
	}
}

func TestSendInvalidDeliveries(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	s := newSender(t, loopbackGuard(t), srv, nil)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*Delivery){
		"short key":        func(d *Delivery) { d.HMACKey = []byte("short") },
		"no key":           func(d *Delivery) { d.HMACKey = nil },
		"bad event type":   func(d *Delivery) { d.EventType = "backup failed\r\nX-Evil: 1" },
		"bad event id":     func(d *Delivery) { d.EventID = "" },
		"bad delivery id":  func(d *Delivery) { d.DeliveryID = "a\nb" },
		"not json":         func(d *Delivery) { d.Payload = []byte("not json") },
		"empty payload":    func(d *Delivery) { d.Payload = nil },
		"huge payload":     func(d *Delivery) { d.Payload = []byte(`"` + strings.Repeat("x", MaxPayloadBytes) + `"`) },
		"userinfo":         func(d *Delivery) { d.URL = "https://u:p@" + u.Host },
		"fragment":         func(d *Delivery) { d.URL = srv.URL + "#x" },
		"unparsable url":   func(d *Delivery) { d.URL = "https://[::1" },
		"non-https scheme": func(d *Delivery) { d.URL = "gopher://" + u.Host },
	}
	for name, mod := range tests {
		d := delivery(srv.URL)
		mod(&d)
		if res := s.Send(context.Background(), d); res.Outcome != OutcomePermanent || res.Code != CodeInvalid {
			t.Errorf("%s: %+v", name, res)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("an invalid delivery was sent")
	}
}

func TestNewSenderOptions(t *testing.T) {
	g := loopbackGuard(t)
	if _, err := NewSender(nil, SenderOptions{Version: "1"}); err == nil {
		t.Error("nil guard accepted")
	}
	for _, v := range []string{"", "1 2", "1\r\nX: y", strings.Repeat("1", 65)} {
		if _, err := NewSender(g, SenderOptions{Version: v}); err == nil {
			t.Errorf("version %q accepted", v)
		}
	}
	for _, d := range []time.Duration{-time.Second, MaxTimeout + time.Second} {
		if _, err := NewSender(g, SenderOptions{Version: "1", Timeout: d}); err == nil {
			t.Errorf("timeout %s accepted", d)
		}
	}
	if _, err := NewSender(g, SenderOptions{Version: "0.0.0-dev"}); err != nil {
		t.Errorf("dev version: %v", err)
	}
}

var _ Resolver = (*netguard.Guard)(nil)
