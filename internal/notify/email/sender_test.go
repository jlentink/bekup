package email

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/netguard"
)

// testPKI returns a CA (PEM) and a server certificate for 127.0.0.1 and
// localhost signed by it.
func testPKI(t *testing.T) (caPEM []byte, cert tls.Certificate) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "bekup test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "smtp"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// fakeSMTP is a scripted single-connection ESMTP server.
type fakeSMTP struct {
	ln       net.Listener
	starttls bool        // advertise STARTTLS
	implicit bool        // TLS from the first byte
	tlsCfg   *tls.Config // server TLS config
	auth     string      // AUTH mechanisms to advertise ("" none)

	mu       sync.Mutex
	commands []string
	data     string
	authResp string
}

func startFake(t *testing.T, f *fakeSMTP) int {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
	t.Cleanup(func() { discard(ln.Close()) })
	go f.serve(t.Context())
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("not a TCP listener")
	}
	return addr.Port
}

func (f *fakeSMTP) record(c string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, c)
}

func (f *fakeSMTP) saw(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.commands {
		if strings.HasPrefix(strings.ToUpper(c), prefix) {
			return true
		}
	}
	return false
}

func (f *fakeSMTP) serve(ctx context.Context) {
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer func() { discard(conn.Close()) }()
	discard(conn.SetDeadline(time.Now().Add(10 * time.Second)))
	isTLS := false
	if f.implicit {
		tc := tls.Server(conn, f.tlsCfg)
		if tc.HandshakeContext(ctx) != nil {
			return
		}
		conn, isTLS = tc, true
	}
	r, w := bufio.NewReader(conn), conn
	write := func(s string) {
		_, err := io.WriteString(w, s+"\r\n")
		discard(err)
	}
	write("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.record(line)
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			ext := []string{"fake"}
			if f.starttls && !isTLS {
				ext = append(ext, "STARTTLS")
			}
			if f.auth != "" {
				ext = append(ext, "AUTH "+f.auth)
			}
			for i, e := range ext {
				sep := "-"
				if i == len(ext)-1 {
					sep = " "
				}
				write("250" + sep + e)
			}
		case cmd == "STARTTLS":
			write("220 go ahead")
			tc := tls.Server(conn, f.tlsCfg)
			if tc.HandshakeContext(ctx) != nil {
				return
			}
			conn, isTLS = tc, true
			r, w = bufio.NewReader(conn), conn
		case strings.HasPrefix(cmd, "AUTH PLAIN "):
			b, err := base64.StdEncoding.DecodeString(line[len("AUTH PLAIN "):])
			discard(err)
			f.mu.Lock()
			f.authResp = string(b)
			f.mu.Unlock()
			if string(b) == "\x00bekup\x00relay-password" {
				write("235 ok")
			} else {
				write("535 bad credentials")
			}
		case strings.HasPrefix(cmd, "MAIL FROM:"), strings.HasPrefix(cmd, "RCPT TO:"):
			if strings.Contains(cmd, "REJECT") {
				write("550 no such user")
				continue
			}
			write("250 ok")
		case cmd == "DATA":
			write("354 send")
			var sb strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				sb.WriteString(l)
			}
			f.mu.Lock()
			f.data = sb.String()
			f.mu.Unlock()
			write("250 queued")
		case cmd == "QUIT":
			write("221 bye")
			return
		default:
			write("502 unknown")
		}
	}
}

func testSender(t *testing.T, allow bool) *Sender {
	t.Helper()
	opts := netguard.Options{LoopbackAllowable: true}
	if allow {
		opts.AllowCIDRs = []string{"127.0.0.1/32"}
	}
	g, err := netguard.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return &Sender{o: SenderOptions{Guard: g, HeloName: "bekup.test", Logger: slog.New(slog.DiscardHandler), Now: time.Now}}
}

func testConfig(port int, mode Mode, ca []byte) *Config {
	return &Config{
		Enabled: true, Host: "127.0.0.1", Port: port, Mode: mode, FromAddress: "bekup@example.com",
		CABundlePEM: string(ca), ConnectTimeoutSeconds: 5, SendTimeoutSeconds: 10,
	}
}

func codeOf(t *testing.T, err error) Code {
	t.Helper()
	var se *SendError
	if !errors.As(err, &se) {
		t.Fatalf("want *SendError, got %v", err)
	}
	return se.Code
}

func TestSendSTARTTLSWithAuth(t *testing.T) {
	ca, cert := testPKI(t)
	f := &fakeSMTP{starttls: true, auth: "LOGIN PLAIN", tlsCfg: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	port := startFake(t, f)
	cfg := testConfig(port, ModeSTARTTLS, ca)
	cfg.Username = "bekup"
	s := testSender(t, true)
	pw := []byte("relay-password")
	_, err := s.send(t.Context(), cfg, pw, &Message{To: []string{"ops@example.com"}, Subject: "s", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !f.saw("STARTTLS") || !f.saw("AUTH PLAIN") || !f.saw("MAIL FROM:<BEKUP@EXAMPLE.COM>") {
		t.Fatalf("commands %q", f.commands)
	}
	// AUTH came only after STARTTLS.
	idx := func(prefix string) int {
		for i, c := range f.commands {
			if strings.HasPrefix(c, prefix) {
				return i
			}
		}
		return -1
	}
	if idx("STARTTLS") < 0 || idx("AUTH") < idx("STARTTLS") {
		t.Fatalf("AUTH before STARTTLS: %q", f.commands)
	}
	if !strings.Contains(f.data, "Subject: s\r\n") {
		t.Fatalf("data %q", f.data)
	}
}

func TestSendImplicitTLS(t *testing.T) {
	ca, cert := testPKI(t)
	f := &fakeSMTP{implicit: true, tlsCfg: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	port := startFake(t, f)
	if _, err := testSender(t, true).send(t.Context(), testConfig(port, ModeImplicitTLS, ca), nil,
		&Message{To: []string{"ops@example.com"}, Subject: "s", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
}

func TestSendRefusesMissingSTARTTLS(t *testing.T) {
	f := &fakeSMTP{auth: "PLAIN"}
	port := startFake(t, f)
	cfg := testConfig(port, ModeSTARTTLS, nil)
	cfg.Username = "bekup"
	_, err := testSender(t, true).send(t.Context(), cfg, []byte("relay-password"), &Message{To: []string{"ops@example.com"}, Subject: "s", Text: "x"})
	if codeOf(t, err) != CodeNoSTARTTLS {
		t.Fatalf("err %v", err)
	}
	if f.saw("AUTH") || f.saw("MAIL") {
		t.Fatalf("sent %q without TLS", f.commands)
	}
}

func TestSendUntrustedCertificate(t *testing.T) {
	_, cert := testPKI(t)
	otherCA, _ := testPKI(t)
	f := &fakeSMTP{starttls: true, tlsCfg: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	port := startFake(t, f)
	_, err := testSender(t, true).send(t.Context(), testConfig(port, ModeSTARTTLS, otherCA), nil, &Message{To: []string{"ops@example.com"}, Subject: "s", Text: "x"})
	if codeOf(t, err) != CodeTLS {
		t.Fatalf("err %v", err)
	}
	if f.saw("MAIL") {
		t.Fatal("MAIL after a failed handshake")
	}
}

func TestSendRefusesOldTLS(t *testing.T) {
	ca, cert := testPKI(t)
	f := &fakeSMTP{implicit: true, tlsCfg: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11}} //nolint:gosec // G402: an outdated server the client must refuse
	port := startFake(t, f)
	_, err := testSender(t, true).send(t.Context(), testConfig(port, ModeImplicitTLS, ca), nil, &Message{To: []string{"ops@example.com"}, Subject: "s", Text: "x"})
	if codeOf(t, err) != CodeTLS {
		t.Fatalf("err %v", err)
	}
}

// recordingSender is testSender with a log buffer.
func recordingSender(t *testing.T) (*Sender, *strings.Builder) {
	t.Helper()
	s := testSender(t, true)
	var buf strings.Builder
	s.o.Logger = slog.New(slog.NewTextHandler(&buf, nil))
	return s, &buf
}

func TestSendModes(t *testing.T) {
	ca, cert := testPKI(t)
	srvTLS := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	msg := &Message{To: []string{"ops@example.com"}, Subject: "s", Text: "x"}
	cases := []struct {
		name          string
		mode          Mode
		starttls      bool
		wantEncrypted bool
		wantSTARTTLS  bool
	}{
		{"auto with STARTTLS", ModeAuto, true, true, true},
		{"auto without STARTTLS", ModeAuto, false, false, false},
		{"none ignores STARTTLS", ModeNone, true, false, false},
		{"none plain server", ModeNone, false, false, false},
		{"starttls", ModeSTARTTLS, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSMTP{starttls: tc.starttls, auth: "PLAIN", tlsCfg: srvTLS}
			port := startFake(t, f)
			cfg := testConfig(port, tc.mode, ca)
			cfg.Username = "bekup"
			s, logs := recordingSender(t)
			d, err := s.send(t.Context(), cfg, []byte("relay-password"), msg)
			if err != nil {
				t.Fatal(err)
			}
			if d.Encrypted != tc.wantEncrypted || f.saw("STARTTLS") != tc.wantSTARTTLS {
				t.Fatalf("delivery %+v, commands %q", d, f.commands)
			}
			if tc.wantEncrypted && (d.TLSVersion == "" || !strings.HasPrefix(d.TLSVersion, "TLS 1.")) {
				t.Fatalf("tls version %q", d.TLSVersion)
			}
			if !tc.wantEncrypted && d.TLSVersion != "" {
				t.Fatalf("tls version without TLS: %q", d.TLSVersion)
			}
			// AUTH works in every case (over plain text in auto/none).
			if !f.saw("AUTH PLAIN") || !f.saw("MAIL") {
				t.Fatalf("commands %q", f.commands)
			}
			warned := strings.Contains(logs.String(), "smtp_unencrypted_session")
			if warned == tc.wantEncrypted {
				t.Fatalf("unencrypted warning = %v: %s", warned, logs.String())
			}
			if strings.Contains(logs.String(), "relay-password") {
				t.Fatal("password logged")
			}
		})
	}
}

func TestSendAutoNoFallbackAfterFailedSTARTTLS(t *testing.T) {
	_, cert := testPKI(t)
	otherCA, _ := testPKI(t)
	f := &fakeSMTP{starttls: true, auth: "PLAIN", tlsCfg: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	port := startFake(t, f)
	cfg := testConfig(port, ModeAuto, otherCA)
	cfg.Username = "bekup"
	d, err := testSender(t, true).send(t.Context(), cfg, []byte("relay-password"), &Message{To: []string{"ops@example.com"}, Subject: "s", Text: "x"})
	if codeOf(t, err) != CodeTLS || d.Encrypted {
		t.Fatalf("err %v, delivery %+v", err, d)
	}
	if f.saw("AUTH") || f.saw("MAIL") {
		t.Fatalf("fell back to plain text: %q", f.commands)
	}
}

func TestUnencryptedWarningOncePerEndpoint(t *testing.T) {
	s, logs := recordingSender(t)
	msg := &Message{To: []string{"ops@example.com"}, Subject: "s", Text: "x"}
	ports := make([]int, 0, 2)
	for range 2 {
		ports = append(ports, startFake(t, &fakeSMTP{}))
	}
	for _, p := range ports {
		if _, err := s.send(t.Context(), testConfig(p, ModeAuto, nil), nil, msg); err != nil {
			t.Fatal(err)
		}
	}
	// The same endpoint again: no second warning.
	s.warnUnencrypted(t.Context(), testConfig(ports[0], ModeNone, nil))
	if n := strings.Count(logs.String(), "smtp_unencrypted_session"); n != 2 {
		t.Fatalf("warnings %d: %s", n, logs.String())
	}
	// Bounded.
	for i := range maxWarnedEndpoints + 5 {
		s.warnUnencrypted(t.Context(), &Config{Host: "h", Port: i + 1})
	}
	if len(s.warned) > maxWarnedEndpoints {
		t.Fatalf("warned map grew to %d", len(s.warned))
	}
}

func TestSendBlockedByGuard(t *testing.T) {
	f := &fakeSMTP{}
	port := startFake(t, f)
	_, err := testSender(t, false).send(t.Context(), testConfig(port, ModeNone, nil), nil, &Message{To: []string{"ops@example.com"}, Subject: "s", Text: "x"})
	var se *SendError
	if !errors.As(err, &se) || se.Code != CodeNotAllowed || !strings.Contains(se.Detail, "127.0.0.1") {
		t.Fatalf("err %v", err)
	}
	if f.saw("EHLO") {
		t.Fatal("connected to a blocked address")
	}
}

func TestSendAuthFailedAndRejected(t *testing.T) {
	ca, cert := testPKI(t)
	f := &fakeSMTP{starttls: true, auth: "PLAIN", tlsCfg: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	port := startFake(t, f)
	cfg := testConfig(port, ModeSTARTTLS, ca)
	cfg.Username = "bekup"
	_, err := testSender(t, true).send(t.Context(), cfg, []byte("wrong"), &Message{To: []string{"ops@example.com"}, Subject: "s", Text: "x"})
	var se *SendError
	if !errors.As(err, &se) || se.Code != CodeAuthFailed || se.Detail != "SMTP reply 535" {
		t.Fatalf("err %v", err)
	}
	f2 := &fakeSMTP{}
	port = startFake(t, f2)
	_, err = testSender(t, true).send(t.Context(), testConfig(port, ModeNone, nil), nil, &Message{To: []string{"reject@example.com"}, Subject: "s", Text: "x"})
	if codeOf(t, err) != CodeRejected {
		t.Fatalf("err %v", err)
	}
}

func TestTLSConfig(t *testing.T) {
	ca, _ := testPKI(t)
	c, err := tlsConfigFor(&Config{Host: "SMTP.Example.com", CABundlePEM: string(ca)})
	if err != nil {
		t.Fatal(err)
	}
	if c.MinVersion != tls.VersionTLS12 || c.ServerName != "smtp.example.com" || c.InsecureSkipVerify || c.RootCAs == nil {
		t.Fatalf("%+v", c)
	}
	sys, err := tlsConfigFor(&Config{Host: "smtp.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if sys.RootCAs == nil || sys.MinVersion != tls.VersionTLS12 {
		t.Fatal("system roots not loaded")
	}
}

func TestAuthRefusesWithoutTLS(t *testing.T) {
	for _, a := range []credAuth{&plainAuth{host: "h", user: "u", pass: []byte("p")}, &loginAuth{host: "h", user: "u", pass: []byte("p")}} {
		if _, _, err := a.Start(&smtp.ServerInfo{Name: "h", TLS: false}); !errors.Is(err, errAuthNoTLS) {
			t.Fatalf("%T: %v", a, err)
		}
		if _, _, err := a.Start(&smtp.ServerInfo{Name: "other", TLS: true}); err == nil {
			t.Fatalf("%T: wrong host accepted", a)
		}
	}
	// allowPlain (auto/none only) permits AUTH without TLS, still only to
	// the configured host.
	for _, a := range []credAuth{&plainAuth{host: "h", user: "u", pass: []byte("p"), allowPlain: true}, &loginAuth{host: "h", user: "u", pass: []byte("p"), allowPlain: true}} {
		if _, _, err := a.Start(&smtp.ServerInfo{Name: "h", TLS: false}); err != nil {
			t.Fatalf("%T: %v", a, err)
		}
		if _, _, err := a.Start(&smtp.ServerInfo{Name: "other", TLS: false}); err == nil {
			t.Fatalf("%T: wrong host accepted", a)
		}
	}
}

func TestAuthPlainOnlyWhenModeAllows(t *testing.T) {
	// A server without TLS: starttls/implicit_tls never reach AUTH; the
	// auth step itself refuses for them (defense in depth).
	for _, m := range []Mode{ModeSTARTTLS, ModeImplicitTLS} {
		f := &fakeSMTP{auth: "PLAIN"}
		port := startFake(t, f)
		var d net.Dialer
		conn, err := d.DialContext(t.Context(), "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			t.Fatal(err)
		}
		c, err := smtp.NewClient(conn, "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Hello("bekup.test"); err != nil {
			t.Fatal(err)
		}
		cfg := testConfig(port, m, nil)
		cfg.Username = "bekup"
		if err := testSender(t, true).auth(t.Context(), c, cfg, []byte("relay-password")); codeOf(t, err) != CodeAuthUnsupported {
			t.Fatalf("%s: %v", m, err)
		}
		discard(c.Close())
		if f.saw("AUTH") {
			t.Fatalf("%s: AUTH sent without TLS", m)
		}
	}
	if !ModeAuto.MayBeUnencrypted() || !ModeNone.MayBeUnencrypted() || ModeSTARTTLS.MayBeUnencrypted() || ModeImplicitTLS.MayBeUnencrypted() {
		t.Fatal("MayBeUnencrypted")
	}
}

func TestClassifyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := classify(ctx, phaseDial, errors.New("x")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
