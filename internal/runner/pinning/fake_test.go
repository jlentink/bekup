package pinning

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/engine"
)

// record is what a fake server observed on one connection.
type record struct {
	// pre is every byte the client sent before the TLS handshake.
	pre []byte
	// handshake is the server-side TLS handshake result.
	handshake error
	// app is application data the client sent over TLS (must stay empty:
	// the capture and pre-flight paths never send anything).
	app []byte
}

type fakeServer struct {
	ln      net.Listener
	handle  func(net.Conn, *record)
	mu      sync.Mutex
	records []*record
	wg      sync.WaitGroup
}

func startFake(t *testing.T, handle func(net.Conn, *record)) *fakeServer {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeServer{ln: ln, handle: handle}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			r := &record{}
			f.mu.Lock()
			f.records = append(f.records, r)
			f.mu.Unlock()
			f.wg.Add(1)
			go func() {
				defer f.wg.Done()
				defer func() { discardErr(c.Close()) }()
				discardErr(c.SetDeadline(time.Now().Add(10 * time.Second)))
				f.handle(c, r)
			}()
		}
	}()
	t.Cleanup(func() {
		discardErr(ln.Close())
		f.wg.Wait()
	})
	return f
}

func (f *fakeServer) dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", f.ln.Addr().String())
}

func (f *fakeServer) target(kind engine.Kind, host string) Target {
	return Target{Kind: kind, Host: host, Dial: f.dial}
}

// recordsDone waits for the handlers and returns the records.
func (f *fakeServer) recordsDone(t *testing.T) []*record {
	t.Helper()
	discardErr(f.ln.Close())
	f.wg.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.records
}

// serveTLS runs the server handshake and records any application data.
func serveTLS(c net.Conn, cert tls.Certificate, alpn []string, r *record) {
	ts := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: alpn, MinVersion: tls.VersionTLS12})
	r.handshake = ts.HandshakeContext(context.Background())
	if r.handshake != nil {
		return
	}
	discardErr(ts.SetReadDeadline(time.Now().Add(2 * time.Second)))
	b, err := io.ReadAll(io.LimitReader(ts, 1<<16))
	r.app = b
	_ = err
}

// pgHandler answers the SSLRequest with reply (+extra) and then speaks
// TLS when reply is 'S'.
func pgHandler(cert tls.Certificate, reply byte, extra []byte) func(net.Conn, *record) {
	return func(c net.Conn, r *record) {
		buf := make([]byte, 8)
		if _, err := io.ReadFull(c, buf); err != nil {
			return
		}
		r.pre = buf
		if _, err := c.Write(append([]byte{reply}, extra...)); err != nil {
			return
		}
		if reply == 'S' {
			serveTLS(c, cert, nil, r)
		}
	}
}

// directHandler is a PG 17+ direct-TLS-only endpoint: anything that is
// not a TLS ClientHello breaks the handshake and closes the connection.
func directHandler(cert tls.Certificate) func(net.Conn, *record) {
	return func(c net.Conn, r *record) { serveTLS(c, cert, []string{pgALPN}, r) }
}

// mysqlGreetingPayload builds a HandshakeV10 payload.
func mysqlGreetingPayload(caps uint32) []byte {
	p := []byte{mysqlHandshakeV10}
	p = append(p, "8.4.6-bekup-test\x00"...)
	p = append(p, 1, 0, 0, 0)    // connection id
	p = append(p, "abcdefgh"...) // auth-plugin-data-part-1
	p = append(p, 0)             // filler
	p = binary.LittleEndian.AppendUint16(p, uint16(caps&0xffff))
	p = append(p, 0xff, 2, 0) // charset, status
	p = binary.LittleEndian.AppendUint16(p, uint16(caps>>16))
	p = append(p, 21)                  // auth data length
	p = append(p, make([]byte, 10)...) // reserved
	p = append(p, "ijklmnopqrst"...)
	p = append(p, 0)
	p = append(p, "caching_sha2_password"...)
	return append(p, 0)
}

func mysqlPacket(seq byte, payload []byte) []byte {
	n := uint32(len(payload)) //nolint:gosec // G115: test payloads are tiny
	return append([]byte{byte(n & 0xff), byte(n >> 8 & 0xff), byte(n >> 16 & 0xff), seq}, payload...)
}

const serverCaps = capLongPassword | capProtocol41 | capSSL | capTransactions | capSecureConnection | capPluginAuth | 0x00100000

// mysqlHandler sends greeting, reads the SSL request (recorded in pre)
// and speaks TLS.
func mysqlHandler(cert tls.Certificate, greeting []byte) func(net.Conn, *record) {
	return func(c net.Conn, r *record) {
		if _, err := c.Write(greeting); err != nil {
			return
		}
		buf := make([]byte, 4+mysqlSSLRequestBytes)
		if _, err := io.ReadFull(c, buf); err != nil {
			r.pre = buf
			return
		}
		r.pre = buf
		serveTLS(c, cert, nil, r)
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
