package validate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/pem"
	"net/netip"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/sshpolicy"
	"github.com/jlentink/bekup/internal/tunnel"
)

// Pin is a validated certificate pin.
type Pin struct {
	// Anchor is the parsed pinned certificate (DER in Anchor.Raw).
	Anchor *x509.Certificate
	// LeafSPKI is the expected leaf SPKI SHA-256; nil when the anchor is a
	// CA whose leaves may rotate.
	LeafSPKI []byte
}

// SSH is a validated SSH tunnel (credentials stay in the request).
type SSH struct {
	// Bastion is resolved and checked against the target allowlist.
	Bastion Endpoint
	User    string
	Pin     tunnel.HostKeyPin
	// KeyAuth is true for private-key authentication, false for a
	// password.
	KeyAuth bool
}

// Target is a validated runnerpb.Target.
type Target struct {
	Kind   engine.Kind
	Engine engine.Engine
	Entry  engine.Entry
	// Conn is what the native client is given (Host/Port are the real
	// target; inside the container Host maps to the per-run proxy).
	Conn engine.Target
	// Direct is set when the runner dials the target itself: resolved and
	// checked against the target allowlist.
	Direct *Endpoint
	// SSH is set when the target is reached through a bastion. The target
	// host is then resolved by the bastion (BehindBastion rules).
	SSH  *SSH
	Pins []Pin
}

// target validates a Target. field is the path prefix ("target.").
func (v *Validator) target(ctx context.Context, field string, t *runnerpb.Target) (*Target, error) {
	if t == nil {
		return nil, fieldError(field[:len(field)-1], "required")
	}
	k, eng, ent, err := v.entry(field, t.GetEngine(), t.GetVersionKey())
	if err != nil {
		return nil, err
	}
	out := &Target{Kind: k, Engine: eng, Entry: ent}
	if err := engine.ValidateHost(t.GetHost()); err != nil {
		return nil, fieldError(field+"host", "invalid host")
	}
	pt, err := port(field+"port", t.GetPort())
	if err != nil {
		return nil, err
	}
	if err := engine.ValidateUser(t.GetUser()); err != nil {
		return nil, fieldError(field+"user", "invalid user name")
	}
	if err := checkPassword(field+"password", t.GetPassword()); err != nil {
		return nil, err
	}
	mode, err := tlsMode(field+"tls_mode", t.GetTlsMode())
	if err != nil {
		return nil, err
	}
	out.Conn = engine.Target{Host: t.GetHost(), Port: pt, User: t.GetUser(), TLS: mode}

	pins, err := checkPins(field+"pins", mode, t.GetPins())
	if err != nil {
		return nil, err
	}
	out.Pins = pins

	if cc := t.GetClientCertificate(); cc != nil {
		if mode == engine.TLSDisabled {
			return nil, fieldError(field+"client_certificate", "not allowed without TLS")
		}
		if err := checkClientCertificate(field+"client_certificate.", cc); err != nil {
			return nil, err
		}
		out.Conn.ClientCert = true
	}
	if err := out.Conn.Validate(); err != nil {
		return nil, fieldError(field[:len(field)-1], "invalid connection settings")
	}

	if st := t.GetSshTunnel(); st != nil {
		s, err := v.sshTunnel(ctx, field+"ssh_tunnel.", st)
		if err != nil {
			return nil, err
		}
		if err := v.behindBastion(field+"host", t.GetHost(), s); err != nil {
			return nil, err
		}
		out.SSH = s
		return out, nil
	}
	addrs, err := v.guard.Resolve(ctx, t.GetHost())
	if err != nil {
		return nil, allowlistError(field+"host", err)
	}
	out.Direct = &Endpoint{Host: t.GetHost(), Port: pt, Addrs: addrs}
	return out, nil
}

// behindBastion checks a target host that the SSH bastion resolves (the
// runner cannot resolve it). deny_hosts and deny_cidrs apply; an IP
// literal must not be in an always-denied range other than loopback (a
// database listening on the bastion's own loopback is a common setup).
// The allow lists apply to the bastion, which is what the runner dials;
// a bastion listed in targets.bastion_targets may only forward to its
// listed targets (D-138).
func (v *Validator) behindBastion(field, host string, s *SSH) error {
	if err := v.guard.CheckBehindBastion(s.Bastion.Host, s.Bastion.Addrs, host); err != nil {
		return allowlistError(field, err)
	}
	if a, err := netip.ParseAddr(host); err == nil {
		ip := a.Unmap()
		c := netguard.Classify(ip)
		if c.Blocked && c.Hard && c.Reason != netguard.ReasonLoopback {
			return allowlistError(field, &behindBastionError{})
		}
		if err := v.guard.CheckDenyLists(ip, ""); err != nil {
			return allowlistError(field, err)
		}
		return nil
	}
	if err := v.guard.CheckDenyLists(netip.Addr{}, host); err != nil {
		return allowlistError(field, err)
	}
	return nil
}

// checkPassword enforces the size limit and the characters the password
// files can hold (NUL, CR and LF are rejected, D-123).
func checkPassword(field string, pw []byte) error {
	if len(pw) > MaxPasswordBytes {
		return fieldError(field, "too long")
	}
	if bytes.ContainsAny(pw, "\x00\r\n") {
		return fieldError(field, "contains NUL, CR or LF")
	}
	return nil
}

func tlsMode(field string, m runnerpb.TlsMode) (engine.TLSMode, error) {
	switch m {
	case runnerpb.TlsMode_TLS_MODE_PINNED:
		return engine.TLSPinned, nil
	case runnerpb.TlsMode_TLS_MODE_UNVERIFIED:
		return engine.TLSUnverified, nil
	case runnerpb.TlsMode_TLS_MODE_DISABLED:
		return engine.TLSDisabled, nil
	default:
		return 0, fieldError(field, "unknown or unspecified TLS mode")
	}
}

// publicAnchor is IsPublicAnchor (replaced by tests).
var publicAnchor = IsPublicAnchor

// checkPins: pinned mode needs 1..MaxPins distinct, parseable anchors; the
// other modes take none. A leaf anchor (not a CA) must carry its own SPKI
// hash as leaf_spki_sha256; a CA anchor may omit it.
func checkPins(field string, mode engine.TLSMode, pins []*runnerpb.CertificatePin) ([]Pin, error) {
	if mode != engine.TLSPinned {
		if len(pins) != 0 {
			return nil, fieldError(field, "only allowed in pinned TLS mode")
		}
		return nil, nil
	}
	if len(pins) == 0 {
		return nil, fieldError(field, "pinned TLS mode needs a certificate pin")
	}
	if len(pins) > MaxPins {
		return nil, fieldError(field, "at most two pins (current and staged)")
	}
	out := make([]Pin, 0, len(pins))
	for _, p := range pins {
		if p == nil {
			return nil, fieldError(field, "empty pin")
		}
		der := p.GetAnchorDer()
		if len(der) == 0 || len(der) > MaxAnchorBytes {
			return nil, fieldError(field+".anchor_der", "missing or too large")
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fieldError(field+".anchor_der", "not a DER certificate")
		}
		if publicAnchor(cert) {
			return nil, fieldError(field+".anchor_der", "is or chains to a public CA; pin a self-signed leaf or a private CA")
		}
		leaf := p.GetLeafSpkiSha256()
		switch {
		case len(leaf) != 0 && len(leaf) != sha256.Size:
			return nil, fieldError(field+".leaf_spki_sha256", "must be 32 bytes")
		case !cert.IsCA && len(leaf) == 0:
			return nil, fieldError(field+".leaf_spki_sha256", "required when the anchor is not a CA")
		case !cert.IsCA:
			sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
			if subtle.ConstantTimeCompare(sum[:], leaf) != 1 {
				return nil, fieldError(field+".leaf_spki_sha256", "does not match the leaf anchor")
			}
		}
		for _, o := range out {
			if bytes.Equal(o.Anchor.Raw, cert.Raw) && bytes.Equal(o.LeafSPKI, leaf) {
				return nil, fieldError(field, "duplicate pin")
			}
		}
		var l []byte
		if len(leaf) > 0 {
			l = bytes.Clone(leaf)
		}
		out = append(out, Pin{Anchor: cert, LeafSPKI: l})
	}
	return out, nil
}

// pemBlocks decodes data into PEM blocks; only whitespace may surround or
// separate them, and no block may carry headers (encrypted PEM). The
// caller zeroes block bytes that are secret.
func pemBlocks(data []byte, limit int) ([]*pem.Block, bool) {
	var blocks []*pem.Block
	rest := data
	for {
		rest = bytes.TrimLeft(rest, " \t\r\n")
		if len(rest) == 0 {
			break
		}
		if len(blocks) == limit {
			return blocks, false
		}
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			return blocks, false
		}
		blocks = append(blocks, b)
		if len(b.Headers) != 0 {
			return blocks, false
		}
	}
	return blocks, len(blocks) > 0
}

func zeroBlocks(blocks []*pem.Block) {
	for _, b := range blocks {
		crypto.Zero(b.Bytes)
	}
}

// checkClientCertificate: cert_pem is 1..8 CERTIFICATE blocks that parse;
// key_pem is exactly one unencrypted private key block. The key itself is
// not parsed (that would leave unzeroable copies of the key in memory); a
// key that does not match the certificate fails in the native client.
func checkClientCertificate(field string, cc *runnerpb.ClientCertificate) error {
	cert, key := cc.GetCertPem(), cc.GetKeyPem()
	if len(cert) == 0 || len(cert) > MaxClientCertBytes {
		return fieldError(field+"cert_pem", "missing or too large")
	}
	if len(key) == 0 || len(key) > MaxClientKeyBytes {
		return fieldError(field+"key_pem", "missing or too large")
	}
	blocks, ok := pemBlocks(cert, maxClientChain)
	if !ok {
		return fieldError(field+"cert_pem", "must be PEM certificates")
	}
	for _, b := range blocks {
		if b.Type != "CERTIFICATE" {
			return fieldError(field+"cert_pem", "must be PEM certificates")
		}
		if _, err := x509.ParseCertificate(b.Bytes); err != nil {
			return fieldError(field+"cert_pem", "certificate does not parse")
		}
	}
	kb, ok := pemBlocks(key, 1)
	defer zeroBlocks(kb)
	if !ok || len(kb) != 1 {
		return fieldError(field+"key_pem", "must be one unencrypted PEM private key")
	}
	switch kb[0].Type {
	case "PRIVATE KEY", "EC PRIVATE KEY", "RSA PRIVATE KEY":
	default:
		return fieldError(field+"key_pem", "must be one unencrypted PEM private key")
	}
	if len(kb[0].Bytes) == 0 {
		return fieldError(field+"key_pem", "empty key")
	}
	return nil
}

// sshTunnel validates an SshTunnel; the bastion goes through the target
// allowlist.
func (v *Validator) sshTunnel(ctx context.Context, field string, st *runnerpb.SshTunnel) (*SSH, error) {
	user := st.GetUser()
	if err := sshpolicy.ValidateUser(user); err != nil {
		return nil, fieldError(field+"user", err.Error())
	}
	pw, key, pass := st.GetPassword(), st.GetPrivateKeyPem(), st.GetPassphrase()
	switch {
	case len(pw) > 0 && len(key) > 0:
		return nil, fieldError(field[:len(field)-1], "set either a password or a private key")
	case len(pw) == 0 && len(key) == 0:
		return nil, fieldError(field[:len(field)-1], "a password or a private key is required")
	case len(pass) > 0 && len(key) == 0:
		return nil, fieldError(field+"passphrase", "requires a private key")
	case len(pw) > MaxPasswordBytes:
		return nil, fieldError(field+"password", "too long")
	case len(pass) > MaxPasswordBytes:
		return nil, fieldError(field+"passphrase", "too long")
	case len(key) > MaxSSHKeyBytes:
		return nil, fieldError(field+"private_key_pem", "too large")
	}
	if len(key) > 0 {
		if err := checkSSHKey(key); err != nil {
			return nil, fieldError(field+"private_key_pem", "must be one PEM private key")
		}
	}
	hp := st.GetHostKeyPin()
	if hp == nil {
		return nil, fieldError(field+"host_key_pin", "required")
	}
	if len(hp.GetType()) > 64 || len(hp.GetFingerprintSha256()) > 128 || len(hp.GetAuthorizedKey()) > MaxAuthorizedKeyBytes {
		return nil, fieldError(field+"host_key_pin", "too large")
	}
	pin := tunnel.HostKeyPin{Type: hp.GetType(), FingerprintSHA256: hp.GetFingerprintSha256(), AuthorizedKey: hp.GetAuthorizedKey()}
	if err := pin.Validate(); err != nil {
		return nil, fieldError(field+"host_key_pin", "invalid or unsupported host key pin")
	}
	ep, err := v.endpoint(ctx, field, st.GetHost(), st.GetPort())
	if err != nil {
		return nil, err
	}
	return &SSH{Bastion: ep, User: user, Pin: pin, KeyAuth: len(key) > 0}, nil
}

type plainError string

func (e plainError) Error() string { return string(e) }

// checkSSHKey: exactly one PEM block of a private-key type. Legacy
// encrypted PEM (headers) is allowed here because x/crypto/ssh decrypts it
// with the passphrase; the key is not parsed (no unzeroable copies).
func checkSSHKey(key []byte) error {
	rest := bytes.TrimSpace(key)
	b, rest := pem.Decode(rest)
	if b == nil {
		return plainError("no PEM block")
	}
	defer crypto.Zero(b.Bytes)
	if len(bytes.TrimSpace(rest)) != 0 {
		return plainError("trailing data")
	}
	switch b.Type {
	case "OPENSSH PRIVATE KEY", "PRIVATE KEY", "EC PRIVATE KEY", "RSA PRIVATE KEY":
	default:
		return plainError("not a private key")
	}
	if len(b.Bytes) == 0 {
		return plainError("empty key")
	}
	return nil
}

// behindBastionError marks an always-denied literal behind a bastion.
type behindBastionError struct{}

func (*behindBastionError) Error() string { return "always-denied address behind the bastion" }
func (*behindBastionError) Unwrap() error { return targets.ErrDenied }
