package tunnel

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func echoRoundTrip(t *testing.T, c net.Conn, msg string) {
	t.Helper()
	// SSH channel connections do not support deadlines; TCP ones do.
	discardErr(c.SetDeadline(time.Now().Add(5 * time.Second)))
	if _, err := io.WriteString(c, msg); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != msg {
		t.Fatalf("echo = %q, want %q", buf, msg)
	}
}

func mustDial(t *testing.T, cfg *Config) *Tunnel {
	t.Helper()
	tun, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { discardErr(tun.Close()) })
	return tun
}

func TestDialKeyAuth(t *testing.T) {
	host, _ := ed25519Signer(t)
	client, clientPriv := ed25519Signer(t)
	s := newTestServer(t, serverOpts{hostKeys: []ssh.Signer{host}, clientKey: client.PublicKey()})
	cfg := baseConfig(t, s, clientPriv)
	key := cfg.Auth.PrivateKey
	tun := mustDial(t, cfg)

	if key.Bytes() != nil {
		t.Error("Dial did not destroy the private key")
	}
	d, ok := cfg.Dialer.(*countingDialer)
	if !ok {
		t.Fatal("unexpected dialer type")
	}
	if got := d.dialed(); len(got) != 1 || got[0] != serverAddr(s) {
		t.Errorf("dialer used for %v, want [%s]", got, serverAddr(s))
	}
	c, err := tun.DialTarget(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { discardErr(c.Close()) }()
	echoRoundTrip(t, c, "hello through the bastion")
}

func TestDialEncryptedKey(t *testing.T) {
	host, _ := ed25519Signer(t)
	client, clientPriv := ed25519Signer(t)
	s := newTestServer(t, serverOpts{hostKeys: []ssh.Signer{host}, clientKey: client.PublicKey()})

	cfg := baseConfig(t, s, clientPriv)
	cfg.Auth.PrivateKey = pemKey(t, clientPriv, "pass phrase")
	cfg.Auth.Passphrase = secretOf("pass phrase")
	pp := cfg.Auth.Passphrase
	mustDial(t, cfg)
	if pp.Bytes() != nil {
		t.Error("Dial did not destroy the passphrase")
	}

	for name, pass := range map[string]string{"missing": "", "wrong": "nope"} {
		t.Run(name, func(t *testing.T) {
			cfg := baseConfig(t, s, clientPriv)
			cfg.Auth.PrivateKey = pemKey(t, clientPriv, "pass phrase")
			if pass != "" {
				cfg.Auth.Passphrase = secretOf(pass)
			}
			_, err := Dial(context.Background(), cfg)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("err = %v, want ErrInvalidConfig", err)
			}
			if strings.Contains(err.Error(), "pass phrase") || strings.Contains(err.Error(), "nope") {
				t.Fatal("error leaks the passphrase")
			}
		})
	}
}

func TestDialRSAClientKeyAndHostKey(t *testing.T) {
	host := rsaSigner(t, 2048)
	clientPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	clientPub, err := ssh.NewPublicKey(&clientPriv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, serverOpts{hostKeys: []ssh.Signer{host}, clientKey: clientPub})
	cfg := baseConfig(t, s, nil)
	cfg.Auth = Auth{PrivateKey: pemKey(t, clientPriv, "")}
	cfg.HostKeyPin = HostKeyPin{Type: ssh.KeyAlgoRSA, FingerprintSHA256: ssh.FingerprintSHA256(host.PublicKey())}
	tun := mustDial(t, cfg)
	c, err := tun.DialTarget(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { discardErr(c.Close()) }()
	echoRoundTrip(t, c, "rsa")
}

func TestDialSmallRSAHostKeyRejected(t *testing.T) {
	host := rsaSigner(t, 1024)
	client, clientPriv := ed25519Signer(t)
	s := newTestServer(t, serverOpts{hostKeys: []ssh.Signer{host}, clientKey: client.PublicKey()})
	cfg := baseConfig(t, s, clientPriv)
	cfg.HostKeyPin = HostKeyPin{Type: ssh.KeyAlgoRSA, FingerprintSHA256: ssh.FingerprintSHA256(host.PublicKey())}
	_, err := Dial(context.Background(), cfg)
	if !errors.Is(err, ErrUnsupportedHostKey) {
		t.Fatalf("err = %v, want ErrUnsupportedHostKey", err)
	}
	if n := s.authCalls.Load(); n != 0 {
		t.Errorf("%d authentication attempts reached the server", n)
	}
}

func TestDialPasswordAuth(t *testing.T) {
	host, _ := ed25519Signer(t)
	s := newTestServer(t, serverOpts{hostKeys: []ssh.Signer{host}})
	cfg := baseConfig(t, s, nil)
	cfg.Auth = Auth{Password: secretOf(testPassword)}
	pw := cfg.Auth.Password
	tun := mustDial(t, cfg)
	if pw.Bytes() != nil {
		t.Error("Dial did not destroy the password")
	}
	c, err := tun.DialTarget(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { discardErr(c.Close()) }()
	echoRoundTrip(t, c, "password")
}

func TestDialAuthFailed(t *testing.T) {
	host, _ := ed25519Signer(t)
	client, _ := ed25519Signer(t)
	_, otherPriv := ed25519Signer(t)
	s := newTestServer(t, serverOpts{hostKeys: []ssh.Signer{host}, clientKey: client.PublicKey()})

	cases := map[string]Auth{
		"wrong key":      {PrivateKey: pemKey(t, otherPriv, "")},
		"wrong password": {Password: secretOf("wrong password")},
	}
	for name, auth := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := baseConfig(t, s, otherPriv)
			cfg.Auth = auth
			_, err := Dial(context.Background(), cfg)
			if !errors.Is(err, ErrAuthFailed) {
				t.Fatalf("err = %v, want ErrAuthFailed", err)
			}
			if strings.Contains(err.Error(), "wrong password") {
				t.Fatal("error leaks the password")
			}
		})
	}
}

func TestDialHostKeyMismatch(t *testing.T) {
	host, _ := ed25519Signer(t)
	other, _ := ed25519Signer(t)
	client, clientPriv := ed25519Signer(t)
	s := newTestServer(t, serverOpts{hostKeys: []ssh.Signer{host}, clientKey: client.PublicKey()})

	t.Run("different key", func(t *testing.T) {
		cfg := baseConfig(t, s, clientPriv)
		cfg.HostKeyPin = HostKeyPin{AuthorizedKey: authorizedLine(other.PublicKey())}
		key := cfg.Auth.PrivateKey
		_, err := Dial(context.Background(), cfg)
		var m *HostKeyMismatchError
		if !errors.As(err, &m) || !errors.Is(err, ErrHostKeyMismatch) {
			t.Fatalf("err = %v, want HostKeyMismatchError", err)
		}
		if m.PresentedFingerprint != ssh.FingerprintSHA256(host.PublicKey()) ||
			m.PinnedFingerprint != ssh.FingerprintSHA256(other.PublicKey()) {
			t.Errorf("fingerprints = %+v", m)
		}
		if key.Bytes() != nil {
			t.Error("secrets not destroyed on failure")
		}
	})

	t.Run("pinned type not offered", func(t *testing.T) {
		ec := ecdsaSigner(t)
		s2 := newTestServer(t, serverOpts{hostKeys: []ssh.Signer{ec}, clientKey: client.PublicKey()})
		cfg := baseConfig(t, s2, clientPriv)
		cfg.HostKeyPin = HostKeyPin{AuthorizedKey: authorizedLine(host.PublicKey())}
		_, err := Dial(context.Background(), cfg)
		var m *HostKeyMismatchError
		if !errors.As(err, &m) {
			t.Fatalf("err = %v, want HostKeyMismatchError", err)
		}
		if m.PresentedType != "" || m.PinnedType != ssh.KeyAlgoED25519 {
			t.Errorf("mismatch = %+v", m)
		}
		if n := s2.authCalls.Load(); n != 0 {
			t.Errorf("%d authentication attempts reached the server", n)
		}
	})

	t.Run("server with several keys uses the pinned type", func(t *testing.T) {
		ec := ecdsaSigner(t)
		s3 := newTestServer(t, serverOpts{hostKeys: []ssh.Signer{ec, host}, clientKey: client.PublicKey()})
		cfg := baseConfig(t, s3, clientPriv)
		cfg.HostKeyPin = HostKeyPin{AuthorizedKey: authorizedLine(host.PublicKey())}
		mustDial(t, cfg)
	})
}

func TestDialWeakKexRejected(t *testing.T) {
	host, _ := ed25519Signer(t)
	client, clientPriv := ed25519Signer(t)
	s := newTestServer(t, serverOpts{
		hostKeys: []ssh.Signer{host}, clientKey: client.PublicKey(),
		kexes: []string{ssh.KeyExchangeECDHP256},
	})
	_, err := Dial(context.Background(), baseConfig(t, s, clientPriv))
	if !errors.Is(err, ErrHandshake) {
		t.Fatalf("err = %v, want ErrHandshake", err)
	}
}

func TestDialUnreachable(t *testing.T) {
	ln, err := listenTCP(t)
	if err != nil {
		t.Fatal(err)
	}
	port := tcpPort(t, ln)
	discardErr(ln.Close())
	host, _ := ed25519Signer(t)
	_, clientPriv := ed25519Signer(t)
	cfg := &Config{
		Host: "127.0.0.1", Port: port, User: testUser,
		Auth:       Auth{PrivateKey: pemKey(t, clientPriv, "")},
		HostKeyPin: HostKeyPin{AuthorizedKey: authorizedLine(host.PublicKey())},
		TargetHost: testTargetHost, TargetPort: testTargetPort,
		Dialer: &countingDialer{},
	}
	_, err = Dial(context.Background(), cfg)
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
}

// silentListener accepts TCP connections but never speaks SSH.
func silentListener(t *testing.T) (port int, stop func()) {
	t.Helper()
	ln, err := listenTCP(t)
	if err != nil {
		t.Fatal(err)
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		conns []net.Conn
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	return tcpPort(t, ln), func() {
		discardErr(ln.Close())
		wg.Wait()
		mu.Lock()
		for _, c := range conns {
			discardErr(c.Close())
		}
		mu.Unlock()
	}
}

func TestDialHandshakeTimeoutAndCancel(t *testing.T) {
	port, stop := silentListener(t)
	defer stop()
	host, _ := ed25519Signer(t)
	_, clientPriv := ed25519Signer(t)
	mk := func() *Config {
		return &Config{
			Host: "127.0.0.1", Port: port, User: testUser,
			Auth:       Auth{PrivateKey: pemKey(t, clientPriv, "")},
			HostKeyPin: HostKeyPin{AuthorizedKey: authorizedLine(host.PublicKey())},
			TargetHost: testTargetHost, TargetPort: testTargetPort,
			Dialer: &countingDialer{},
		}
	}

	t.Run("timeout", func(t *testing.T) {
		cfg := mk()
		cfg.DialTimeout = 200 * time.Millisecond
		start := time.Now()
		_, err := Dial(context.Background(), cfg)
		if !errors.Is(err, ErrTimeout) {
			t.Fatalf("err = %v, want ErrTimeout", err)
		}
		if time.Since(start) > 3*time.Second {
			t.Error("timeout not honored")
		}
	})

	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(100*time.Millisecond, cancel)
		_, err := Dial(ctx, mk())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

func TestDialTargetRefused(t *testing.T) {
	host, _ := ed25519Signer(t)
	client, clientPriv := ed25519Signer(t)
	s := newTestServer(t, serverOpts{hostKeys: []ssh.Signer{host}, clientKey: client.PublicKey()})
	cfg := baseConfig(t, s, clientPriv)
	cfg.TargetPort = 3306
	tun := mustDial(t, cfg)
	_, err := tun.DialTarget(context.Background())
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
}

func TestForward(t *testing.T) {
	host, _ := ed25519Signer(t)
	client, clientPriv := ed25519Signer(t)
	s := newTestServer(t, serverOpts{hostKeys: []ssh.Signer{host}, clientKey: client.PublicKey()})
	cfg := baseConfig(t, s, clientPriv)
	cfg.MaxConns = 2
	tun := mustDial(t, cfg)

	ln, err := listenTCP(t)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tun.Forward(ctx, ln) }()

	var wg sync.WaitGroup
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := dialTCP(t, ln.Addr().String())
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { discardErr(c.Close()) }()
			msg := strings.Repeat("x", 1+i*1000)
			if _, err := io.WriteString(c, msg); err != nil {
				t.Error(err)
				return
			}
			if tc, ok := c.(*net.TCPConn); ok {
				discardErr(tc.CloseWrite())
			}
			discardErr(c.SetReadDeadline(time.Now().Add(5 * time.Second)))
			got, err := io.ReadAll(c)
			if err != nil || !bytes.Equal(got, []byte(msg)) {
				t.Errorf("forwarded echo: len %d err %v", len(got), err)
			}
		}()
	}
	wg.Wait()

	// An idle connection is closed on cancel.
	idle, err := dialTCP(t, ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { discardErr(idle.Close()) }()
	echoRoundTrip(t, idle, "idle")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Forward = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Forward did not return after cancel")
	}
	if _, err := dialTCP(t, ln.Addr().String()); err == nil {
		t.Error("listener still open after Forward returned")
	}
}

func TestTunnelLost(t *testing.T) {
	host, _ := ed25519Signer(t)
	client, clientPriv := ed25519Signer(t)
	s := newTestServer(t, serverOpts{hostKeys: []ssh.Signer{host}, clientKey: client.PublicKey()})
	tun := mustDial(t, baseConfig(t, s, clientPriv))

	ln, err := listenTCP(t)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- tun.Forward(context.Background(), ln) }()

	s.dropAll()
	select {
	case <-tun.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("tunnel did not notice the lost connection")
	}
	if !errors.Is(tun.Err(), ErrClosed) {
		t.Errorf("Err = %v, want ErrClosed", tun.Err())
	}
	if _, err := tun.DialTarget(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("DialTarget = %v, want ErrClosed", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("Forward = %v, want ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Forward did not return after the tunnel was lost")
	}
}

func TestKeepaliveTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("takes a few seconds")
	}
	host, _ := ed25519Signer(t)
	client, clientPriv := ed25519Signer(t)
	s := newTestServer(t, serverOpts{
		hostKeys: []ssh.Signer{host}, clientKey: client.PublicKey(), silentGlobal: true,
	})
	cfg := baseConfig(t, s, clientPriv)
	cfg.KeepaliveInterval = MinKeepaliveInterval
	tun := mustDial(t, cfg)
	select {
	case <-tun.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("dead server not detected")
	}
	var e *Error
	if !errors.As(tun.Err(), &e) || !errors.Is(e.Kind, ErrTimeout) || e.Op != "keepalive" {
		t.Fatalf("Err = %v, want keepalive timeout", tun.Err())
	}
}

func TestKeepaliveAnsweredKeepsTunnel(t *testing.T) {
	if testing.Short() {
		t.Skip("takes a few seconds")
	}
	host, _ := ed25519Signer(t)
	client, clientPriv := ed25519Signer(t)
	s := newTestServer(t, serverOpts{hostKeys: []ssh.Signer{host}, clientKey: client.PublicKey()})
	cfg := baseConfig(t, s, clientPriv)
	cfg.KeepaliveInterval = MinKeepaliveInterval
	tun := mustDial(t, cfg)
	select {
	case <-tun.Done():
		t.Fatalf("tunnel closed: %v", tun.Err())
	case <-time.After(4500 * time.Millisecond):
	}
	if err := tun.Close(); err != nil {
		t.Fatal(err)
	}
	if tun.Err() != nil {
		t.Errorf("Err after Close = %v, want nil", tun.Err())
	}
}

func TestFetchHostKey(t *testing.T) {
	host, _ := ed25519Signer(t)
	s := newTestServer(t, serverOpts{hostKeys: []ssh.Signer{ecdsaSigner(t), host}})
	d := &countingDialer{}
	info, err := FetchHostKey(context.Background(), s.host(), s.port(), d)
	if err != nil {
		t.Fatal(err)
	}
	if info.Type != ssh.KeyAlgoED25519 || info.FingerprintSHA256 != ssh.FingerprintSHA256(host.PublicKey()) {
		t.Errorf("info = %+v", info)
	}
	if info.AuthorizedKey != authorizedLine(host.PublicKey()) {
		t.Errorf("authorized key = %q", info.AuthorizedKey)
	}
	if n := s.authCalls.Load(); n != 0 {
		t.Errorf("%d authentication attempts reached the server", n)
	}
	if len(d.dialed()) != 1 {
		t.Errorf("dialer not used: %v", d.dialed())
	}
	if _, err := info.Pin().Normalize(); err != nil {
		t.Errorf("Pin() does not normalize: %v", err)
	}

	t.Run("small rsa", func(t *testing.T) {
		s := newTestServer(t, serverOpts{hostKeys: []ssh.Signer{rsaSigner(t, 1024)}})
		_, err := FetchHostKey(context.Background(), s.host(), s.port(), d)
		if !errors.Is(err, ErrUnsupportedHostKey) {
			t.Fatalf("err = %v, want ErrUnsupportedHostKey", err)
		}
	})
	t.Run("invalid input", func(t *testing.T) {
		for _, tc := range []struct {
			host   string
			port   int
			dialer Dialer
		}{
			{"", 22, d}, {"bad host", 22, d}, {"h", 70000, d}, {"h", 22, nil},
		} {
			if _, err := FetchHostKey(context.Background(), tc.host, tc.port, tc.dialer); !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("%+v: err = %v, want ErrInvalidConfig", tc, err)
			}
		}
	})
	t.Run("timeout", func(t *testing.T) {
		port, stop := silentListener(t)
		defer stop()
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if _, err := FetchHostKey(ctx, "127.0.0.1", port, d); !errors.Is(err, ErrTimeout) {
			t.Fatalf("err = %v, want ErrTimeout", err)
		}
	})
}

func TestConfigValidation(t *testing.T) {
	host, _ := ed25519Signer(t)
	_, priv := ed25519Signer(t)
	pin := HostKeyPin{AuthorizedKey: authorizedLine(host.PublicKey())}
	valid := func() *Config {
		return &Config{
			Host: "bastion.example.com", User: testUser,
			Auth:       Auth{PrivateKey: pemKey(t, priv, "")},
			HostKeyPin: pin, TargetHost: "10.0.0.5", TargetPort: 5432,
			Dialer: &countingDialer{},
		}
	}
	if _, err := valid().validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := map[string]func(c *Config){
		"no dialer":          func(c *Config) { c.Dialer = nil },
		"no pin":             func(c *Config) { c.HostKeyPin = HostKeyPin{} },
		"no auth":            func(c *Config) { c.Auth = Auth{} },
		"key and password":   func(c *Config) { c.Auth.Password = secretOf("x") },
		"passphrase only":    func(c *Config) { c.Auth = Auth{Password: secretOf("x"), Passphrase: secretOf("y")} },
		"empty host":         func(c *Config) { c.Host = "" },
		"host with space":    func(c *Config) { c.Host = "a b" },
		"host with zone":     func(c *Config) { c.Host = "fe80::1%eth0" },
		"host bad label":     func(c *Config) { c.Host = "-a.example" },
		"host too long":      func(c *Config) { c.Host = strings.Repeat("a.", 130) + "a" },
		"bad port":           func(c *Config) { c.Port = 65536 },
		"negative port":      func(c *Config) { c.Port = -1 },
		"no target host":     func(c *Config) { c.TargetHost = "" },
		"no target port":     func(c *Config) { c.TargetPort = 0 },
		"empty user":         func(c *Config) { c.User = "" },
		"user with space":    func(c *Config) { c.User = "a b" },
		"user dash":          func(c *Config) { c.User = "-oProxyCommand" },
		"user too long":      func(c *Config) { c.User = strings.Repeat("u", 200) },
		"dial timeout big":   func(c *Config) { c.DialTimeout = time.Hour },
		"dial timeout neg":   func(c *Config) { c.DialTimeout = -time.Second },
		"keepalive small":    func(c *Config) { c.KeepaliveInterval = time.Millisecond },
		"max conns big":      func(c *Config) { c.MaxConns = 100000 },
		"max conns negative": func(c *Config) { c.MaxConns = -1 },
		"password too long":  func(c *Config) { c.Auth = Auth{Password: secretOf(strings.Repeat("p", 2000))} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := valid()
			mutate(c)
			key, pw := c.Auth.PrivateKey, c.Auth.Password
			_, err := Dial(context.Background(), c)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("err = %v, want ErrInvalidConfig", err)
			}
			if key.Bytes() != nil || pw.Bytes() != nil {
				t.Error("secrets not destroyed on validation failure")
			}
			if got := c.Dialer; got != nil {
				if d, ok := got.(*countingDialer); ok && len(d.dialed()) != 0 {
					t.Error("dialed despite invalid config")
				}
			}
		})
	}

	t.Run("defaults", func(t *testing.T) {
		s, err := valid().validate()
		if err != nil {
			t.Fatal(err)
		}
		if s.addr != "bastion.example.com:22" || s.dialTO != DefaultDialTimeout ||
			s.keepalive != DefaultKeepaliveInterval || s.maxConns != DefaultMaxConns || s.logger == nil {
			t.Errorf("defaults = %+v", s)
		}
	})
	t.Run("ipv6 address", func(t *testing.T) {
		c := valid()
		c.Host = "2001:db8::1"
		s, err := c.validate()
		if err != nil || s.addr != "[2001:db8::1]:22" {
			t.Fatalf("addr = %v, err = %v", s, err)
		}
	})
	t.Run("malformed key", func(t *testing.T) {
		c := valid()
		c.Auth = Auth{PrivateKey: secretOf("-----BEGIN NOPE-----")}
		if _, err := Dial(context.Background(), c); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestErrorMessages(t *testing.T) {
	e := newError(ErrAuthFailed, "dial", "h:22", errors.New("cause"))
	if e.Error() != "tunnel: dial h:22: ssh authentication failed: cause" {
		t.Errorf("Error() = %q", e.Error())
	}
	if !errors.Is(e, ErrAuthFailed) {
		t.Error("kind not matched")
	}
	m := &HostKeyMismatchError{Addr: "h:22", PinnedType: "ssh-ed25519", PinnedFingerprint: "SHA256:a"}
	if !strings.Contains(m.Error(), "does not offer") {
		t.Errorf("Error() = %q", m.Error())
	}
	m.PresentedType, m.PresentedFingerprint = "ssh-ed25519", "SHA256:b"
	if !strings.Contains(m.Error(), "presented ssh-ed25519 SHA256:b") {
		t.Errorf("Error() = %q", m.Error())
	}
}
