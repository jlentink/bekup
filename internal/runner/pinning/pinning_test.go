package pinning

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/validate"
)

// world is a set of certificates for the tests: CA A with two different
// leaves, CA B (another CA, with a public-looking name) and a self-signed
// leaf.
type world struct {
	caA, caB, root, inter *testCA
	leafA, leafA2, leafB  *testCA
	leafInter, selfLeaf   *testCA
	wrongName, expired    *testCA
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{}
	sans := leafOpts{dns: []string{"db.test"}, ips: []net.IP{net.ParseIP("127.0.0.1")}}
	w.caA = newCA(t, "bekup test CA A", nil)
	// A CA with a name like a public root: not in the pin, so it must
	// never be trusted (no system roots anywhere).
	w.caB = newCA(t, "ISRG Root X1", nil)
	w.root = newCA(t, "bekup test root", nil)
	w.inter = newCA(t, "bekup test intermediate", w.root)
	w.leafA = newLeaf(t, w.caA, sans)
	w.leafA2 = newLeaf(t, w.caA, sans)
	w.leafB = newLeaf(t, w.caB, sans)
	w.leafInter = newLeaf(t, w.inter, sans)
	w.selfLeaf = newLeaf(t, nil, sans)
	w.wrongName = newLeaf(t, w.caA, leafOpts{dns: []string{"other.test"}})
	w.expired = newLeaf(t, w.caA, leafOpts{dns: []string{"db.test"}, notAfter: time.Now().Add(-time.Minute)})
	return w
}

func TestPreflight(t *testing.T) {
	w := newWorld(t)
	now := time.Now()
	pinA := pinOf(w.caA.cert, w.leafA.cert)
	pinB := pinOf(w.caB.cert, w.leafB.cert)
	tests := []struct {
		name    string
		cert    tls.Certificate
		host    string
		pins    []validate.Pin
		now     time.Time
		reason  string // "" = success
		index   int
		warning string
	}{
		{name: "pinned leaf from pinned CA", cert: tlsCert(w.leafA), host: "db.test", pins: []validate.Pin{pinA}},
		{name: "IP literal SAN", cert: tlsCert(w.leafA), host: "127.0.0.1", pins: []validate.Pin{pinA}},
		{name: "other CA fails", cert: tlsCert(w.leafB), host: "db.test", pins: []validate.Pin{pinA}, reason: ReasonUntrusted},
		{name: "other leaf same CA fails on SPKI", cert: tlsCert(w.leafA2), host: "db.test", pins: []validate.Pin{pinA}, reason: ReasonLeafSPKI},
		{name: "CA pin without leaf SPKI accepts other leaf", cert: tlsCert(w.leafA2), host: "db.test", pins: []validate.Pin{{Anchor: w.caA.cert}}},
		{name: "wrong host name", cert: tlsCert(w.wrongName), host: "db.test", pins: []validate.Pin{pinOf(w.caA.cert, w.wrongName.cert)}, reason: ReasonHostname},
		{name: "wrong IP", cert: tlsCert(w.leafA), host: "127.0.0.2", pins: []validate.Pin{pinA}, reason: ReasonHostname},
		{name: "expired", cert: tlsCert(w.expired), host: "db.test", pins: []validate.Pin{pinOf(w.caA.cert, w.expired.cert)}, reason: ReasonExpired},
		{name: "staged backup pin", cert: tlsCert(w.leafB), host: "db.test", pins: []validate.Pin{pinA, pinB}, index: 1, warning: WarningStagedPinInUse},
		{name: "current pin preferred", cert: tlsCert(w.leafA), host: "db.test", pins: []validate.Pin{pinA, pinB}},
		{name: "self-signed leaf", cert: tlsCert(w.selfLeaf), host: "db.test", pins: []validate.Pin{pinOf(w.selfLeaf.cert, w.selfLeaf.cert)}},
		{name: "intermediate chain to pinned root", cert: tlsCert(w.leafInter, w.inter.cert), host: "db.test", pins: []validate.Pin{pinOf(w.root.cert, w.leafInter.cert)}},
		{name: "missing intermediate", cert: tlsCert(w.leafInter), host: "db.test", pins: []validate.Pin{pinOf(w.root.cert, w.leafInter.cert)}, reason: ReasonUntrusted},
		{name: "cross pin: CA of one pin, leaf of the other", cert: tlsCert(w.leafA2), host: "db.test", pins: []validate.Pin{pinA, {Anchor: w.caB.cert, LeafSPKI: spkiOf(w.leafA2.cert)}}, reason: ReasonLeafSPKI},
		{name: "expires within 30 days", cert: tlsCert(w.leafA), host: "db.test", pins: []validate.Pin{pinA}, now: w.leafA.cert.NotAfter.Add(-20 * 24 * time.Hour), warning: WarningExpires30d},
		{name: "expires within 7 days", cert: tlsCert(w.leafA), host: "db.test", pins: []validate.Pin{pinA}, now: w.leafA.cert.NotAfter.Add(-2 * 24 * time.Hour), warning: WarningExpires7d},
	}
	for _, kind := range []engine.Kind{engine.KindPostgres, engine.KindMySQL, engine.KindMariaDB} {
		for _, tt := range tests {
			t.Run(string(kind)+"/"+tt.name, func(t *testing.T) {
				h := pgHandler(tt.cert, 'S', nil)
				if kind != engine.KindPostgres {
					h = mysqlHandler(tt.cert, mysqlPacket(0, mysqlGreetingPayload(serverCaps)))
				}
				f := startFake(t, h)
				clock := now
				if !tt.now.IsZero() {
					clock = tt.now
				}
				c := New(Options{Now: func() time.Time { return clock }})
				rep, err := c.Preflight(context.Background(), f.target(kind, tt.host), tt.pins)
				if tt.reason != "" {
					var me *MismatchError
					if !errors.As(err, &me) || !errors.Is(err, ErrPinMismatch) || me.Reason != tt.reason {
						t.Fatalf("err = %v, want mismatch %s", err, tt.reason)
					}
				} else {
					if err != nil {
						t.Fatalf("preflight: %v", err)
					}
					if rep.PinIndex != tt.index {
						t.Fatalf("pin index = %d, want %d", rep.PinIndex, tt.index)
					}
					if tt.warning != "" && !contains(rep.Warnings, tt.warning) {
						t.Fatalf("warnings = %v, want %s", rep.Warnings, tt.warning)
					}
					if tt.warning == "" && len(rep.Warnings) != 0 {
						t.Fatalf("unexpected warnings %v", rep.Warnings)
					}
					if rep.LeafNotAfter.IsZero() || rep.NotAfter.After(rep.LeafNotAfter) {
						t.Fatalf("report expiry %+v", rep)
					}
				}
				for _, r := range f.recordsDone(t) {
					if len(r.app) != 0 {
						t.Fatalf("client sent application data: %q", r.app)
					}
				}
			})
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestUpgradeErrors(t *testing.T) {
	w := newWorld(t)
	cert := tlsCert(w.leafA)
	pins := []validate.Pin{pinOf(w.caA.cert, w.leafA.cert)}
	greeting := mysqlGreetingPayload(serverCaps)
	big := make([]byte, 8)
	tests := []struct {
		name   string
		kind   engine.Kind
		handle func(net.Conn, *record)
		want   error
	}{
		{"pg N", engine.KindPostgres, pgHandler(cert, 'N', nil), ErrNoTLS},
		{"pg E", engine.KindPostgres, pgHandler(cert, 'E', nil), ErrServerRejected},
		{"pg garbage", engine.KindPostgres, pgHandler(cert, 'X', nil), ErrProtocol},
		// Bytes after 'S' are plaintext injection: they go to the TLS layer
		// and break the handshake.
		{"pg S with trailing bytes", engine.KindPostgres, pgHandler(cert, 'S', []byte("injected")), ErrHandshake},
		{"mysql without SSL", engine.KindMySQL, mysqlHandler(cert, mysqlPacket(0, mysqlGreetingPayload(serverCaps&^capSSL))), ErrNoTLS},
		{"mysql without protocol 41", engine.KindMySQL, mysqlHandler(cert, mysqlPacket(0, mysqlGreetingPayload(serverCaps&^capProtocol41))), ErrProtocol},
		{"mysql error packet", engine.KindMySQL, mysqlHandler(cert, mysqlPacket(0, []byte{0xff, 0x6a, 0x04, 'H', 'o', 's', 't'})), ErrServerRejected},
		{"mysql wrong protocol version", engine.KindMariaDB, mysqlHandler(cert, mysqlPacket(0, append([]byte{9}, greeting[1:]...))), ErrProtocol},
		{"mysql wrong sequence", engine.KindMySQL, mysqlHandler(cert, mysqlPacket(3, greeting)), ErrProtocol},
		{"mysql truncated", engine.KindMySQL, mysqlHandler(cert, mysqlPacket(0, greeting[:20])), ErrProtocol},
		{"mysql no NUL in version", engine.KindMySQL, mysqlHandler(cert, mysqlPacket(0, append([]byte{10}, bytes.Repeat([]byte{'a'}, 300)...))), ErrProtocol},
		{"mysql empty packet", engine.KindMySQL, mysqlHandler(cert, []byte{0, 0, 0, 0}), ErrProtocol},
		// An oversized length is refused from the header alone: the
		// client never tries to read (or allocate) 16 MiB.
		{"mysql oversized packet", engine.KindMySQL, mysqlHandler(cert, append([]byte{0xff, 0xff, 0xff, 0}, big...)), ErrProtocol},
		{"mysql closes", engine.KindMySQL, func(net.Conn, *record) {}, ErrProtocol},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := startFake(t, tt.handle)
			c := New(Options{})
			tgt := f.target(tt.kind, "db.test")
			tgt.Upgrade = UpgradeInProtocol
			_, err := c.Preflight(context.Background(), tgt, pins)
			if !errors.Is(err, tt.want) {
				t.Fatalf("preflight err = %v, want %v", err, tt.want)
			}
			if _, err := c.Fetch(context.Background(), tgt); !errors.Is(err, tt.want) {
				t.Fatalf("fetch err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestReadMySQLPacketBounded(t *testing.T) {
	// The reader must not consume more than the header for an oversized
	// packet.
	hdr := make([]byte, 4, 4+1<<16)
	hdr[2] = 0x01 // 65536 > 1024
	r := bytes.NewReader(append(hdr, make([]byte, 1<<16)...))
	if _, _, err := readMySQLPacket(r, maxGreetingBytes); !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v", err)
	}
	if consumed := int64(len(hdr)+1<<16) - int64(r.Len()); consumed != 4 {
		t.Fatalf("consumed %d bytes, want 4", consumed)
	}
}

func FuzzParseMySQLGreeting(f *testing.F) {
	f.Add(byte(0), mysqlGreetingPayload(serverCaps))
	f.Add(byte(0), []byte{0xff, 1, 2})
	f.Add(byte(1), []byte{10, 0})
	f.Fuzz(func(t *testing.T, seq byte, p []byte) {
		g, err := parseMySQLGreeting(seq, p)
		if err == nil && (seq != 0 || len(p) < 16 || p[0] != mysqlHandshakeV10) {
			t.Fatalf("accepted invalid greeting %x: %+v", p, g)
		}
	})
}

func TestPGDirectTLS(t *testing.T) {
	w := newWorld(t)
	cert := tlsCert(w.leafA)
	pins := []validate.Pin{pinOf(w.caA.cert, w.leafA.cert)}
	c := New(Options{})

	t.Run("explicit", func(t *testing.T) {
		f := startFake(t, directHandler(cert))
		tgt := f.target(engine.KindPostgres, "db.test")
		tgt.Upgrade = UpgradeDirect
		if _, err := c.Preflight(context.Background(), tgt, pins); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("auto falls back", func(t *testing.T) {
		f := startFake(t, directHandler(cert))
		if _, err := c.Preflight(context.Background(), f.target(engine.KindPostgres, "db.test"), pins); err != nil {
			t.Fatal(err)
		}
		if n := len(f.recordsDone(t)); n != 2 {
			t.Fatalf("connections = %d, want 2 (SSLRequest then direct)", n)
		}
	})
	t.Run("N is final", func(t *testing.T) {
		f := startFake(t, pgHandler(cert, 'N', nil))
		if _, err := c.Preflight(context.Background(), f.target(engine.KindPostgres, "db.test"), pins); !errors.Is(err, ErrNoTLS) {
			t.Fatalf("err = %v", err)
		}
		if n := len(f.recordsDone(t)); n != 1 {
			t.Fatalf("connections = %d, want 1", n)
		}
	})
	t.Run("no ALPN", func(t *testing.T) {
		f := startFake(t, func(c net.Conn, r *record) { serveTLS(c, cert, nil, r) })
		tgt := f.target(engine.KindPostgres, "db.test")
		tgt.Upgrade = UpgradeDirect
		if _, err := c.Preflight(context.Background(), tgt, pins); !errors.Is(err, ErrProtocol) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("mysql direct refused", func(t *testing.T) {
		f := startFake(t, directHandler(cert))
		tgt := f.target(engine.KindMySQL, "db.test")
		tgt.Upgrade = UpgradeDirect
		if _, err := c.Preflight(context.Background(), tgt, pins); !errors.Is(err, ErrProtocol) {
			t.Fatalf("err = %v", err)
		}
	})
}

// TestFetchNeverAuthenticates asserts the capture path: the only bytes the
// client sends before TLS are the 8-byte SSLRequest / the 32-byte SSL
// request (no user name, no password, no database), the TLS handshake is
// aborted by the client (the server sees a failed handshake), and no
// application data (startup message / handshake response) ever follows.
func TestFetchNeverAuthenticates(t *testing.T) {
	w := newWorld(t)
	c := New(Options{})
	for _, kind := range []engine.Kind{engine.KindPostgres, engine.KindMySQL, engine.KindMariaDB} {
		t.Run(string(kind), func(t *testing.T) {
			cert := tlsCert(w.leafInter, w.inter.cert, w.root.cert)
			h := pgHandler(cert, 'S', nil)
			if kind != engine.KindPostgres {
				h = mysqlHandler(cert, mysqlPacket(0, mysqlGreetingPayload(serverCaps)))
			}
			f := startFake(t, h)
			chain, err := c.Fetch(context.Background(), f.target(kind, "db.test"))
			if err != nil {
				t.Fatal(err)
			}
			if len(chain) != 3 || !chain[0].Equal(w.leafInter.cert) {
				t.Fatalf("chain = %d certs", len(chain))
			}
			recs := f.recordsDone(t)
			if len(recs) != 1 {
				t.Fatalf("connections = %d", len(recs))
			}
			r := recs[0]
			if r.handshake == nil {
				t.Fatal("the capture handshake completed; it must be aborted by the client")
			}
			if len(r.app) != 0 {
				t.Fatalf("application data sent: %q", r.app)
			}
			if kind == engine.KindPostgres {
				want := []byte{0, 0, 0, 8, 0x04, 0xd2, 0x16, 0x2f}
				if !bytes.Equal(r.pre, want) {
					t.Fatalf("pre-TLS bytes = %x, want only the SSLRequest", r.pre)
				}
				return
			}
			if len(r.pre) != 36 || r.pre[0] != 32 || r.pre[3] != 1 {
				t.Fatalf("SSL request header = %x", r.pre[:4])
			}
			caps := binary.LittleEndian.Uint32(r.pre[4:8])
			if caps&capSSL == 0 || caps&capProtocol41 == 0 {
				t.Fatalf("caps = %#x", caps)
			}
			if !bytes.Equal(r.pre[13:], make([]byte, 23)) {
				t.Fatalf("SSL request carries data after the charset: %x", r.pre[13:])
			}
		})
	}
}

func TestDescribe(t *testing.T) {
	w := newWorld(t)
	tests := []struct {
		name  string
		chain []*x509.Certificate
		want  int32
	}{
		{"self-signed leaf", []*x509.Certificate{w.selfLeaf.cert}, 0},
		{"leaf + root", []*x509.Certificate{w.leafA.cert, w.caA.cert}, 1},
		{"leaf + intermediate + root", []*x509.Certificate{w.leafInter.cert, w.inter.cert, w.root.cert}, 2},
		{"root sent before intermediate", []*x509.Certificate{w.leafInter.cert, w.root.cert, w.inter.cert}, 1},
		{"leaf + intermediate only", []*x509.Certificate{w.leafInter.cert, w.inter.cert}, -1},
		{"leaf only", []*x509.Certificate{w.leafA.cert}, -1},
		{"unrelated CA", []*x509.Certificate{w.leafA.cert, w.caB.cert}, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Describe(tt.chain)
			if r.GetSuggestedAnchorIndex() != tt.want {
				t.Fatalf("suggested = %d, want %d", r.GetSuggestedAnchorIndex(), tt.want)
			}
			if !bytes.Equal(r.GetLeafSpkiSha256(), spkiOf(tt.chain[0])) {
				t.Fatal("leaf spki")
			}
			for i, pc := range r.GetChain() {
				c := tt.chain[i]
				blk, _ := pem.Decode([]byte(pc.GetPem()))
				if blk == nil || !bytes.Equal(blk.Bytes, c.Raw) || !bytes.Equal(pc.GetDer(), c.Raw) {
					t.Fatal("pem/der")
				}
				if pc.GetSpkiSha256Hex() == "" || pc.GetSpkiSha256Base64() == "" || pc.GetIsCa() != c.IsCA {
					t.Fatalf("fields %+v", pc)
				}
			}
		})
	}
	r := Describe(nil)
	if r.GetSuggestedAnchorIndex() != -1 || len(r.GetChain()) != 0 {
		t.Fatal("empty chain")
	}
}

// TestSuggestionIsAcceptedByValidator checks that a pin built from the
// suggestion passes the runner's request validation (anchor + leaf SPKI).
func TestSuggestionIsAcceptedByPreflight(t *testing.T) {
	w := newWorld(t)
	for _, cert := range []tls.Certificate{tlsCert(w.selfLeaf), tlsCert(w.leafInter, w.inter.cert, w.root.cert)} {
		f := startFake(t, pgHandler(cert, 'S', nil))
		c := New(Options{})
		chain, err := c.Fetch(context.Background(), f.target(engine.KindPostgres, "db.test"))
		if err != nil {
			t.Fatal(err)
		}
		d := Describe(chain)
		anchor, err := x509.ParseCertificate(d.GetChain()[d.GetSuggestedAnchorIndex()].GetDer())
		if err != nil {
			t.Fatal(err)
		}
		pins := []validate.Pin{{Anchor: anchor, LeafSPKI: d.GetLeafSpkiSha256()}}
		if _, err := c.Preflight(context.Background(), f.target(engine.KindPostgres, "db.test"), pins); err != nil {
			t.Fatalf("preflight with suggested pin: %v", err)
		}
	}
}

func TestTrustAnchor(t *testing.T) {
	w := newWorld(t)
	c := New(Options{})
	pa := pinOf(w.caA.cert, w.leafA.cert)
	pa2 := pinOf(w.caA.cert, w.leafA2.cert)
	pb := pinOf(w.caB.cert, w.leafB.cert)
	out, err := c.TrustAnchor([]validate.Pin{pa, pb})
	if err != nil {
		t.Fatal(err)
	}
	var got [][]byte
	for rest := out; ; {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			if len(bytes.TrimSpace(rest)) != 0 {
				t.Fatal("trailing data")
			}
			break
		}
		if b.Type != "CERTIFICATE" || len(b.Headers) != 0 {
			t.Fatalf("block %s", b.Type)
		}
		got = append(got, b.Bytes)
	}
	if len(got) != 2 || !bytes.Equal(got[0], w.caA.cert.Raw) || !bytes.Equal(got[1], w.caB.cert.Raw) {
		t.Fatalf("anchors = %d", len(got))
	}
	out, err = c.TrustAnchor([]validate.Pin{pa, pa2})
	if err != nil || bytes.Count(out, []byte("BEGIN CERTIFICATE")) != 1 {
		t.Fatalf("dedup: %v", err)
	}
	if _, err := c.TrustAnchor(nil); err == nil {
		t.Fatal("no pins accepted")
	}
	if _, err := FailClosed().TrustAnchor([]validate.Pin{pa}); !errors.Is(err, ErrNoVerifier) {
		t.Fatal("fail-closed")
	}
	if _, err := FailClosed().Preflight(context.Background(), Target{}, []validate.Pin{pa}); !errors.Is(err, ErrNoVerifier) {
		t.Fatal("fail-closed")
	}
}

func TestClientTLSConfigNeverUsesSystemRoots(t *testing.T) {
	w := newWorld(t)
	if _, err := ClientTLSConfig("db.test", nil, nil); err == nil {
		t.Fatal("empty pins accepted (RootCAs nil would mean system roots)")
	}
	cfg, err := ClientTLSConfig("db.test", []validate.Pin{pinOf(w.caA.cert, w.leafA.cert)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RootCAs == nil || cfg.InsecureSkipVerify || cfg.MinVersion < tls.VersionTLS12 || cfg.VerifyConnection == nil {
		t.Fatalf("unsafe config")
	}
	// A real TLS client with this config refuses the other CA's leaf and
	// the other leaf of the pinned CA.
	for _, tc := range []struct {
		cert tls.Certificate
		ok   bool
	}{{tlsCert(w.leafA), true}, {tlsCert(w.leafB), false}, {tlsCert(w.leafA2), false}} {
		f := startFake(t, func(c net.Conn, r *record) { serveTLS(c, tc.cert, nil, r) })
		conn, err := f.dial(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		cl := tls.Client(conn, cfg)
		err = cl.HandshakeContext(context.Background())
		discardErr(cl.Close())
		if (err == nil) != tc.ok {
			t.Fatalf("handshake err = %v, want ok=%v", err, tc.ok)
		}
	}
}

func TestPreflightTimeout(t *testing.T) {
	// A server that never answers: the pre-flight ends at the timeout.
	f := startFake(t, func(net.Conn, *record) { time.Sleep(3 * time.Second) })
	w := newWorld(t)
	c := New(Options{Timeout: 300 * time.Millisecond})
	start := time.Now()
	_, err := c.Preflight(context.Background(), f.target(engine.KindMySQL, "db.test"), []validate.Pin{pinOf(w.caA.cert, w.leafA.cert)})
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
	if !errors.Is(err, context.DeadlineExceeded) && !isTimeout(err) && !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v", err)
	}
}

// TestIsPublicAnchor: private test CAs and their leaves are not public
// anchors (the chain logic is tested in validate).
func TestIsPublicAnchor(t *testing.T) {
	w := newWorld(t)
	if IsPublicAnchor(w.caA.cert) || IsPublicAnchor(w.leafA.cert) || IsPublicAnchor(nil) {
		t.Fatal("private anchor reported as public")
	}
}
