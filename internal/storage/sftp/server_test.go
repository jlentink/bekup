package sftp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/netguard"
)

const testUser = "bekup"
const testPassword = "unit-test-password-3f9a"

// testServer is an in-process SSH server with the pkg/sftp subsystem
// serving the local file system (no chroot: tests use absolute paths
// below t.TempDir()).
type testServer struct {
	addr      string
	host      string
	port      int
	hostKey   ssh.PublicKey
	clientPEM []byte

	mu    sync.Mutex
	conns []net.Conn
}

func startServer(t *testing.T) *testServer {
	t.Helper()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	kp, err := crypto.GenerateSSHKeypair("unit")
	if err != nil {
		t.Fatal(err)
	}
	clientPEM := append([]byte(nil), kp.PrivateKeyPEM.Bytes()...)
	kp.PrivateKeyPEM.Destroy()
	authorized, _, _, _, err := ssh.ParseAuthorizedKey([]byte(kp.AuthorizedKey))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if subtle.ConstantTimeCompare(key.Marshal(), authorized.Marshal()) == 1 {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("unknown key")
		},
		PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if subtle.ConstantTimeCompare(pw, []byte(testPassword)) == 1 {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("wrong password")
		},
	}
	cfg.AddHostKey(hostSigner)
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &testServer{
		addr: ln.Addr().String(), hostKey: hostSigner.PublicKey(), clientPEM: clientPEM,
	}
	host, port, err := net.SplitHostPort(srv.addr)
	if err != nil {
		t.Fatal(err)
	}
	srv.host = host
	if srv.port, err = strconv.Atoi(port); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		discard(ln.Close())
		srv.dropAll()
		wg.Wait()
	})
	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			srv.mu.Lock()
			srv.conns = append(srv.conns, c)
			srv.mu.Unlock()
			wg.Go(func() { serveConn(c, cfg, &wg) })
		}
	})
	return srv
}

// dropAll closes every server-side connection (simulates a dead link).
func (s *testServer) dropAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		discard(c.Close())
	}
	s.conns = nil
}

func serveConn(c net.Conn, cfg *ssh.ServerConfig, wg *sync.WaitGroup) {
	defer func() { discard(c.Close()) }()
	sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer func() { discard(sc.Close()) }()
	wg.Go(func() { ssh.DiscardRequests(reqs) })
	for nc := range chans {
		if nc.ChannelType() != "session" {
			discard(nc.Reject(ssh.UnknownChannelType, "unsupported"))
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			return
		}
		wg.Go(func() {
			for req := range creqs {
				ok := req.Type == "subsystem" && len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp"
				discard(req.Reply(ok, nil))
				if !ok {
					continue
				}
				wg.Go(func() {
					defer func() { discard(ch.Close()) }()
					s, err := pkgsftp.NewServer(ch)
					if err != nil {
						return
					}
					discard(s.Serve())
				})
			}
		})
	}
}

func (s *testServer) pin() HostKeyPin {
	return HostKeyPin{AuthorizedKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.hostKey)))}
}

func loopbackGuard(t *testing.T) *netguard.Guard {
	t.Helper()
	g, err := netguard.New(netguard.Options{
		LoopbackAllowable: true,
		AllowCIDRs:        []string{"127.0.0.1/32", "::1/128"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// keyConfig returns a key-auth config for base.
func (s *testServer) keyConfig(t *testing.T, base string) Config {
	t.Helper()
	return Config{
		Host:       s.host,
		Port:       s.port,
		User:       testUser,
		Auth:       Auth{PrivateKey: crypto.NewSecret(append([]byte(nil), s.clientPEM...))},
		HostKeyPin: s.pin(),
		BasePath:   base,
		Guard:      loopbackGuard(t),
	}
}

func mustOpen(t *testing.T, cfg Config) *Destination {
	t.Helper()
	d, err := Open(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { discard(d.Close()) })
	return d
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	return t.Context()
}
