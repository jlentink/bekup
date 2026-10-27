package tunnel

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/jlentink/bekup/internal/crypto"
)

const (
	testTargetHost = "db.internal"
	testTargetPort = 5432
	testUser       = "tunnel"
	testPassword   = "correct horse battery staple"
)

// testServer is an in-process SSH server: it authenticates one client key
// and one password, and answers direct-tcpip requests for the test target
// with an echo service.
type testServer struct {
	t         *testing.T
	ln        net.Listener
	config    *ssh.ServerConfig
	hostKeys  []ssh.Signer
	silent    atomic.Bool // ignore global requests (no keepalive replies)
	authCalls atomic.Int32

	mu    sync.Mutex
	conns []net.Conn
	wg    sync.WaitGroup
}

type serverOpts struct {
	hostKeys     []ssh.Signer
	clientKey    ssh.PublicKey
	kexes        []string
	silentGlobal bool
}

func newTestServer(t *testing.T, o serverOpts) *testServer {
	t.Helper()
	s := &testServer{t: t, hostKeys: o.hostKeys}
	s.silent.Store(o.silentGlobal)
	s.config = &ssh.ServerConfig{
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			s.authCalls.Add(1)
			if o.clientKey != nil && c.User() == testUser &&
				bytes.Equal(key.Marshal(), o.clientKey.Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("denied")
		},
		PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			s.authCalls.Add(1)
			if c.User() == testUser && string(pw) == testPassword {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("denied")
		},
	}
	if o.kexes != nil {
		s.config.KeyExchanges = o.kexes
	}
	for _, k := range o.hostKeys {
		s.config.AddHostKey(k)
	}
	s.ln = mustListen(t)
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(s.close)
	return s
}

func (s *testServer) host() string { return "127.0.0.1" }

func (s *testServer) port() int { return tcpPort(s.t, s.ln) }

func (s *testServer) serve() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, c)
		s.mu.Unlock()
		s.wg.Add(1)
		go s.handle(c)
	}
}

// dropAll closes every client connection (simulates a lost bastion).
func (s *testServer) dropAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		discardErr(c.Close())
	}
}

func (s *testServer) close() {
	discardErr(s.ln.Close())
	s.dropAll()
	s.wg.Wait()
}

func (s *testServer) handle(c net.Conn) {
	defer s.wg.Done()
	defer func() { discardErr(c.Close()) }()
	sc, chans, reqs, err := ssh.NewServerConn(c, s.config)
	if err != nil {
		return
	}
	defer func() { discardErr(sc.Close()) }()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for r := range reqs {
			if s.silent.Load() {
				continue
			}
			if r.WantReply {
				discardErr(r.Reply(false, nil))
			}
		}
	}()
	for nc := range chans {
		if nc.ChannelType() != "direct-tcpip" {
			discardErr(nc.Reject(ssh.UnknownChannelType, "unsupported"))
			continue
		}
		var p struct {
			Host     string
			Port     uint32
			OrigHost string
			OrigPort uint32
		}
		if err := ssh.Unmarshal(nc.ExtraData(), &p); err != nil ||
			p.Host != testTargetHost || p.Port != testTargetPort {
			discardErr(nc.Reject(ssh.Prohibited, "target not allowed"))
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go ssh.DiscardRequests(creqs)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { discardErr(ch.Close()) }()
			discardCopy(io.Copy(ch, ch))
			discardErr(ch.CloseWrite())
		}()
	}
}

// keys

func ed25519Signer(t testing.TB) (ssh.Signer, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s, priv
}

func ecdsaSigner(t testing.TB) ssh.Signer {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func rsaSigner(t testing.TB, bits int) ssh.Signer {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func authorizedLine(k ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

func pemKey(t testing.TB, priv any, passphrase string) *crypto.Secret {
	t.Helper()
	var (
		block *pem.Block
		err   error
	)
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	return crypto.NewSecret(pem.EncodeToMemory(block))
}

func secretOf(s string) *crypto.Secret { return crypto.NewSecret([]byte(s)) }

// countingDialer records the addresses it dialed.
type countingDialer struct {
	mu    sync.Mutex
	addrs []string
	d     net.Dialer
}

func (c *countingDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	c.mu.Lock()
	c.addrs = append(c.addrs, addr)
	c.mu.Unlock()
	return c.d.DialContext(ctx, network, addr)
}

func (c *countingDialer) dialed() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.addrs...)
}

// baseConfig returns a config for s with key authentication.
func baseConfig(t testing.TB, s *testServer, clientPriv ed25519.PrivateKey) *Config {
	t.Helper()
	var auth Auth
	if len(clientPriv) > 0 {
		auth.PrivateKey = pemKey(t, clientPriv, "")
	}
	return &Config{
		Host:       s.host(),
		Port:       s.port(),
		User:       testUser,
		Auth:       auth,
		HostKeyPin: HostKeyPin{AuthorizedKey: authorizedLine(s.hostKeys[0].PublicKey())},
		TargetHost: testTargetHost,
		TargetPort: testTargetPort,
		Dialer:     &countingDialer{},
	}
}

func serverAddr(s *testServer) string {
	return net.JoinHostPort(s.host(), strconv.Itoa(s.port()))
}

// listenTCP listens on a random loopback port.
func listenTCP(t testing.TB) (net.Listener, error) {
	t.Helper()
	var lc net.ListenConfig
	return lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
}

func mustListen(t testing.TB) net.Listener {
	t.Helper()
	ln, err := listenTCP(t)
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

// dialTCP connects to addr on the loopback interface.
func dialTCP(t testing.TB, addr string) (net.Conn, error) {
	t.Helper()
	var d net.Dialer
	return d.DialContext(context.Background(), "tcp", addr)
}

func tcpPort(t testing.TB, ln net.Listener) int {
	t.Helper()
	a, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("not a TCP listener")
	}
	return a.Port
}
