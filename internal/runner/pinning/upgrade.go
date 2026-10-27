package pinning

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/jlentink/bekup/internal/engine"
)

// Errors of the TLS upgrade. Messages are static; the RPC layer maps them
// to normalized runnerpb reasons.
var (
	// ErrNoTLS: the server refused TLS (PostgreSQL 'N', MySQL greeting
	// without the SSL capability).
	ErrNoTLS = errors.New("pinning: the server does not offer TLS")
	// ErrProtocol: the server's answer is not the expected protocol
	// (malformed or oversized greeting, unexpected byte, wrong ALPN).
	ErrProtocol = errors.New("pinning: unexpected answer from the server")
	// ErrServerRejected: the server answered the greeting with an error
	// packet (e.g. host not allowed, too many connections).
	ErrServerRejected = errors.New("pinning: the server rejected the connection")
	// ErrHandshake: the TLS handshake failed (not a pin decision).
	ErrHandshake = errors.New("pinning: TLS handshake failed")
	// ErrUnreachable: the TCP connection could not be opened.
	ErrUnreachable = errors.New("pinning: target unreachable")
)

const (
	// pgSSLRequestCode is the PostgreSQL SSLRequest code (1234 << 16 | 5679).
	pgSSLRequestCode = 80877103
	// pgALPN is the ALPN protocol of PostgreSQL direct TLS (PG 17+).
	pgALPN = "postgresql"

	// MySQL capability flags.
	capLongPassword      = 0x00000001
	capProtocol41        = 0x00000200
	capSSL               = 0x00000800
	capTransactions      = 0x00002000
	capSecureConnection  = 0x00008000
	capPluginAuth        = 0x00080000
	mysqlHandshakeV10    = 0x0a
	mysqlErrPacket       = 0xff
	mysqlCharsetUTF8MB4  = 45
	mysqlSSLRequestBytes = 32
	// maxGreetingBytes bounds the MySQL/MariaDB server greeting (real
	// greetings are about 80-130 bytes).
	maxGreetingBytes = 1024
	// maxServerVersionBytes bounds the NUL-terminated version string.
	maxServerVersionBytes = 255

	// DefaultTimeout bounds the TCP dial, the upgrade and the handshake.
	DefaultTimeout = 15 * time.Second
)

// startTLS performs the protocol-specific TLS upgrade on conn and the TLS
// handshake with cfg. It closes conn on failure. A handshake error is
// returned as is (wrapped in ErrHandshake) so callers can inspect it.
func startTLS(ctx context.Context, conn net.Conn, kind engine.Kind, up Upgrade, cfg *tls.Config) (*tls.Conn, error) {
	if dl, ok := ctx.Deadline(); ok {
		// SSH tunnel channels do not support deadlines; the AfterFunc
		// below bounds them through ctx instead.
		discardErr(conn.SetDeadline(dl))
	}
	// Close the connection when ctx ends during a blocking read (the
	// bound for connections without deadline support).
	stop := context.AfterFunc(ctx, func() { discardErr(conn.Close()) })
	defer stop()

	cfg = cfg.Clone()
	switch kind {
	case engine.KindPostgres:
		if up == UpgradeDirect {
			cfg.NextProtos = []string{pgALPN}
		} else if err := pgSSLRequest(conn); err != nil {
			discardErr(conn.Close())
			return nil, err
		}
	case engine.KindMySQL, engine.KindMariaDB:
		if up == UpgradeDirect {
			discardErr(conn.Close())
			return nil, fmt.Errorf("%w: direct TLS is PostgreSQL only", ErrProtocol)
		}
		if err := mysqlSSLRequest(conn); err != nil {
			discardErr(conn.Close())
			return nil, err
		}
	default:
		discardErr(conn.Close())
		return nil, fmt.Errorf("%w: unknown engine", ErrProtocol)
	}
	tc := tls.Client(conn, cfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		discardErr(tc.Close())
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		return nil, fmt.Errorf("%w: %w", ErrHandshake, err)
	}
	if up == UpgradeDirect && tc.ConnectionState().NegotiatedProtocol != pgALPN {
		// PostgreSQL requires the ALPN for direct TLS; a server that did
		// not negotiate it is not a PostgreSQL direct-TLS endpoint.
		discardErr(tc.Close())
		return nil, fmt.Errorf("%w: ALPN not negotiated", ErrProtocol)
	}
	return tc, nil
}

// pgSSLRequest sends the 8-byte SSLRequest and reads exactly one byte.
// Exactly one byte is read from the raw connection: anything the server
// sent after 'S' stays in the socket and is fed to the TLS layer, where it
// breaks the handshake (no plaintext injection, CVE-2021-23222).
func pgSSLRequest(conn net.Conn) error {
	var req [8]byte
	binary.BigEndian.PutUint32(req[0:4], 8)
	binary.BigEndian.PutUint32(req[4:8], pgSSLRequestCode)
	if _, err := conn.Write(req[:]); err != nil {
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	var resp [1]byte
	if _, err := io.ReadFull(conn, resp[:]); err != nil {
		return fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	switch resp[0] {
	case 'S':
		return nil
	case 'N':
		return ErrNoTLS
	case 'E':
		// Pre-7.0 servers or a server error before negotiation.
		return ErrServerRejected
	default:
		return ErrProtocol
	}
}

// mysqlGreeting is the parsed part of a Protocol::HandshakeV10 packet.
type mysqlGreeting struct {
	seq  byte
	caps uint32
}

// readMySQLPacket reads one packet with a payload of at most limit bytes.
func readMySQLPacket(r io.Reader, limit int) (seq byte, payload []byte, err error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	n := int(hdr[0]) | int(hdr[1])<<8 | int(hdr[2])<<16
	if n == 0 || n > limit {
		return 0, nil, fmt.Errorf("%w: packet length", ErrProtocol)
	}
	payload = make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	return hdr[3], payload, nil
}

// parseMySQLGreeting parses the fields of a HandshakeV10 payload that the
// SSL request needs.
func parseMySQLGreeting(seq byte, p []byte) (mysqlGreeting, error) {
	if len(p) == 0 {
		return mysqlGreeting{}, ErrProtocol
	}
	switch p[0] {
	case mysqlHandshakeV10:
	case mysqlErrPacket:
		return mysqlGreeting{}, ErrServerRejected
	default:
		return mysqlGreeting{}, fmt.Errorf("%w: protocol version", ErrProtocol)
	}
	if seq != 0 {
		return mysqlGreeting{}, fmt.Errorf("%w: sequence id", ErrProtocol)
	}
	rest := p[1:]
	nul := -1
	for i := 0; i < len(rest) && i <= maxServerVersionBytes; i++ {
		if rest[i] == 0 {
			nul = i
			break
		}
	}
	if nul < 0 {
		return mysqlGreeting{}, fmt.Errorf("%w: server version", ErrProtocol)
	}
	rest = rest[nul+1:]
	// connection id (4), auth-plugin-data-part-1 (8), filler (1),
	// capability flags lower (2).
	if len(rest) < 4+8+1+2 {
		return mysqlGreeting{}, fmt.Errorf("%w: truncated greeting", ErrProtocol)
	}
	if rest[12] != 0 {
		return mysqlGreeting{}, fmt.Errorf("%w: filler", ErrProtocol)
	}
	caps := uint32(binary.LittleEndian.Uint16(rest[13:15]))
	rest = rest[15:]
	// character set (1), status flags (2), capability flags upper (2).
	if len(rest) >= 5 {
		caps |= uint32(binary.LittleEndian.Uint16(rest[3:5])) << 16
	}
	return mysqlGreeting{seq: seq, caps: caps}, nil
}

// mysqlSSLRequest reads the server greeting and answers with the
// Protocol::SSLRequest packet (sequence id 1). The packet carries no user
// name, password or database: authentication only starts after it, and the
// capture path never gets there.
func mysqlSSLRequest(conn net.Conn) error {
	seq, payload, err := readMySQLPacket(conn, maxGreetingBytes)
	if err != nil {
		return err
	}
	g, err := parseMySQLGreeting(seq, payload)
	if err != nil {
		return err
	}
	if g.caps&capSSL == 0 {
		return ErrNoTLS
	}
	if g.caps&capProtocol41 == 0 {
		return fmt.Errorf("%w: protocol 4.1 not supported", ErrProtocol)
	}
	want := uint32(capLongPassword | capProtocol41 | capTransactions | capSecureConnection | capPluginAuth)
	caps := (want & g.caps) | capSSL | capProtocol41
	var pkt [4 + mysqlSSLRequestBytes]byte
	pkt[0] = mysqlSSLRequestBytes
	pkt[3] = g.seq + 1
	binary.LittleEndian.PutUint32(pkt[4:8], caps)
	// max packet size (4 bytes) stays 0 like go-sql-driver; the server
	// uses its default.
	pkt[12] = mysqlCharsetUTF8MB4
	// 23 bytes filler (zero).
	if _, err := conn.Write(pkt[:]); err != nil {
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	return nil
}

// discardErr ignores errors of best-effort cleanup.
func discardErr(error) {}
