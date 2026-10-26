package certs

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

var allowServe = []string{"spiffe://bekup/serve"}

// hsResult is the outcome of a test handshake.
type hsResult struct {
	server, client error
	peer           string
}

// handshake runs a TLS handshake plus a 1-byte exchange over net.Pipe. In
// TLS 1.3 the client may finish its handshake before the server rejects its
// certificate, so the client also reads the server's reply.
func handshake(t *testing.T, server, client *tls.Config) hsResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	sc, cc := net.Pipe()
	deadline, _ := ctx.Deadline()
	if err := errors.Join(sc.SetDeadline(deadline), cc.SetDeadline(deadline)); err != nil {
		t.Fatal(err)
	}

	done := make(chan hsResult, 1)
	go func() {
		s := tls.Server(sc, server)
		r := hsResult{server: s.HandshakeContext(ctx)}
		if r.server == nil {
			r.peer, r.server = PeerIdentity(s.ConnectionState())
		}
		if r.server == nil {
			_, r.server = s.Write([]byte{1})
		}
		r.server = errors.Join(r.server, ignoreClosed(sc.Close()))
		done <- r
	}()

	c := tls.Client(cc, client)
	clientErr := c.HandshakeContext(ctx)
	if clientErr == nil {
		buf := make([]byte, 1)
		_, clientErr = c.Read(buf)
	}
	if err := ignoreClosed(cc.Close()); err != nil {
		t.Error(err)
	}
	r := <-done
	r.client = clientErr
	return r
}

func ignoreClosed(err error) error {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}

func handshakeFiles(t *testing.T, sCert, sKey, sCA, cCert, cKey, cCA string) error {
	t.Helper()
	srv, err := ServerTLSConfig(sCert, sKey, sCA, allowServe)
	if err != nil {
		t.Fatal(err)
	}
	cli, err := ClientTLSConfig(cCert, cKey, cCA, "runner")
	if err != nil {
		t.Fatal(err)
	}
	r := handshake(t, srv, cli)
	return errors.Join(r.server, r.client)
}

func serverCfg(t *testing.T, l Layout, allowed []string) *tls.Config {
	t.Helper()
	cfg, err := ServerTLSConfig(l.CertFile(ComponentRunner), l.KeyFile(ComponentRunner), l.BundleFile(ComponentRunner), allowed)
	if err != nil {
		t.Fatalf("ServerTLSConfig: %v", err)
	}
	return cfg
}

func clientCfg(t *testing.T, l Layout, component, serverName string) *tls.Config {
	t.Helper()
	cfg, err := ClientTLSConfig(l.CertFile(component), l.KeyFile(component), l.BundleFile(component), serverName)
	if err != nil {
		t.Fatalf("ClientTLSConfig: %v", err)
	}
	return cfg
}

func TestMTLSHandshake(t *testing.T) {
	dir := initDir(t)
	other := initDir(t)
	l, o := Layout{Dir: dir}, Layout{Dir: other}

	t.Run("allowed client succeeds", func(t *testing.T) {
		r := handshake(t, serverCfg(t, l, allowServe), clientCfg(t, l, ComponentServe, "runner"))
		serr, cerr, peer := r.server, r.client, r.peer
		if serr != nil || cerr != nil {
			t.Fatalf("server=%v client=%v", serr, cerr)
		}
		if peer != "spiffe://bekup/serve" {
			t.Errorf("peer = %q", peer)
		}
	})

	t.Run("both clients allowed", func(t *testing.T) {
		srv := serverCfg(t, l, []string{"spiffe://bekup/serve", "spiffe://bekup/worker"})
		r := handshake(t, srv, clientCfg(t, l, ComponentWorker, "runner"))
		serr, cerr, peer := r.server, r.client, r.peer
		if serr != nil || cerr != nil || peer != "spiffe://bekup/worker" {
			t.Fatalf("server=%v client=%v peer=%q", serr, cerr, peer)
		}
	})

	t.Run("unknown SAN rejected", func(t *testing.T) {
		r := handshake(t, serverCfg(t, l, allowServe), clientCfg(t, l, ComponentWorker, "runner"))
		serr, cerr := r.server, r.client
		if !errors.Is(serr, ErrClientNotAllowed) || cerr == nil {
			t.Fatalf("server=%v client=%v", serr, cerr)
		}
	})

	t.Run("client cert from another CA rejected", func(t *testing.T) {
		cli := clientCfg(t, l, ComponentServe, "runner")
		// Trusts our runner, but presents a cert from the other CA.
		alien, err := ClientTLSConfig(o.CertFile(ComponentServe), o.KeyFile(ComponentServe), l.BundleFile(ComponentServe), "runner")
		if err != nil {
			t.Fatal(err)
		}
		cli.Certificates = alien.Certificates
		r := handshake(t, serverCfg(t, l, allowServe), cli)
		serr, cerr := r.server, r.client
		if serr == nil || cerr == nil {
			t.Fatalf("server=%v client=%v", serr, cerr)
		}
	})

	t.Run("server cert used as client cert rejected", func(t *testing.T) {
		cli := clientCfg(t, l, ComponentServe, "runner")
		srvAsClient := serverCfg(t, l, allowServe)
		cli.Certificates = srvAsClient.Certificates
		serr := handshake(t, serverCfg(t, l, allowServe), cli).server
		if serr == nil {
			t.Fatal("server accepted the runner's own (serverAuth) certificate as a client")
		}
	})

	t.Run("no client cert rejected", func(t *testing.T) {
		cli := clientCfg(t, l, ComponentServe, "runner")
		cli.Certificates = nil
		serr := handshake(t, serverCfg(t, l, allowServe), cli).server
		if serr == nil {
			t.Fatal("server accepted a client without certificate")
		}
	})

	t.Run("TLS 1.2 rejected", func(t *testing.T) {
		cli := clientCfg(t, l, ComponentServe, "runner")
		cli.MinVersion, cli.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
		r := handshake(t, serverCfg(t, l, allowServe), cli)
		serr, cerr := r.server, r.client
		if serr == nil || cerr == nil {
			t.Fatalf("server=%v client=%v", serr, cerr)
		}
	})

	t.Run("server cert from another CA rejected by client", func(t *testing.T) {
		srv := serverCfg(t, o, allowServe)
		r := handshake(t, srv, clientCfg(t, l, ComponentServe, "runner"))
		serr, cerr := r.server, r.client
		if cerr == nil || !strings.Contains(cerr.Error(), "certificate") {
			t.Fatalf("client=%v (server=%v)", cerr, serr)
		}
	})

	t.Run("wrong server name rejected by client", func(t *testing.T) {
		cerr := handshake(t, serverCfg(t, l, allowServe), clientCfg(t, l, ComponentServe, "db.example.com")).client
		if cerr == nil {
			t.Fatal("client accepted a server certificate for another name")
		}
	})
}

func TestTLSConfigValidation(t *testing.T) {
	dir := initDir(t)
	l := Layout{Dir: dir}
	sc, sk, sca := l.CertFile(ComponentRunner), l.KeyFile(ComponentRunner), l.BundleFile(ComponentRunner)

	for _, ids := range [][]string{nil, {"bekup-serve"}, {"spiffe://other/serve"}, {"spiffe://bekup/*"}} {
		if _, err := ServerTLSConfig(sc, sk, sca, ids); err == nil {
			t.Errorf("allowed IDs %v accepted", ids)
		}
	}
	if _, err := ClientTLSConfig(l.CertFile(ComponentServe), l.KeyFile(ComponentServe), l.BundleFile(ComponentServe), ""); err == nil {
		t.Error("empty server name accepted")
	}
	// A leaf (non-CA) as the trust anchor is refused.
	if _, err := ServerTLSConfig(sc, sk, l.CertFile(ComponentServe), allowServe); err == nil {
		t.Error("non-CA trust bundle accepted")
	}
	// Mismatched key.
	if _, err := ServerTLSConfig(sc, l.KeyFile(ComponentServe), sca, allowServe); err == nil {
		t.Error("mismatched key accepted")
	}
	// Unsafe key permissions are refused (secret-file checks).
	if err := os.Chmod(sk, 0o644); err != nil { // #nosec G302 -- deliberately unsafe for the test
		t.Fatal(err)
	}
	if _, err := ServerTLSConfig(sc, sk, sca, allowServe); err == nil {
		t.Error("world-readable key accepted")
	}
	// A group-writable CA bundle is refused.
	if err := os.Chmod(sk, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sca, 0o666); err != nil { // #nosec G302 -- deliberately unsafe for the test
		t.Fatal(err)
	}
	if _, err := ServerTLSConfig(sc, sk, sca, allowServe); err == nil {
		t.Error("world-writable CA bundle accepted")
	}

	cfg, err := ServerTLSConfig(sc, sk, l.CACert(), allowServe)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinVersion != tls.VersionTLS13 || cfg.ClientAuth != tls.RequireAndVerifyClientCert || !cfg.SessionTicketsDisabled {
		t.Errorf("server config: %+v", cfg)
	}
}
