package connections

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/pinning"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Certificate pinning (D-034, D-133): the runner captures the chain
// without trusting it or sending credentials; an admin compares it out of
// band and confirms a pin (step-up, audited). Nothing is trusted
// automatically.

// maxAnchorDER bounds a pinned certificate (the runner's MaxAnchorBytes).
const maxAnchorDER = 16 << 10

// ChainCertificate is one certificate of a captured chain (public data).
type ChainCertificate struct {
	PEM         string
	Subject     string
	Issuer      string
	DNSNames    []string
	IPAddresses []string
	NotBefore   time.Time
	NotAfter    time.Time
	IsCA        bool
	SelfSigned  bool
	// SPKISHA256 is the SHA-256 of the SubjectPublicKeyInfo.
	SPKISHA256 []byte
	// Fingerprint is the SHA-256 of the DER (hex).
	Fingerprint string
}

// CertificateChain is the result of FetchCertificate.
type CertificateChain struct {
	// Chain is leaf first, as presented by the server.
	Chain []ChainCertificate
	// SuggestedAnchorIndex is the certificate to pin (the self-signed leaf
	// or root CA); -1 when the server presented none (supply the root CA
	// as PEM).
	SuggestedAnchorIndex int
	// LeafSPKISHA256 is the leaf's SPKI SHA-256 (confirm it with the pin).
	LeafSPKISHA256 []byte
}

// capture runs FetchServerCertificate for row (through its tunnel, if any).
func (s *Service) capture(ctx context.Context, actor Actor, row *sqlc.Connection) (*runnerpb.FetchServerCertificateResponse, error) {
	eng, err := engineOf(engine.Kind(row.Engine))
	if err != nil {
		return nil, err
	}
	creds, err := openCredentials(s.o.Keyring, row.ID, row.SecretEnc)
	if err != nil {
		return nil, err
	}
	tun, err := sshTunnel(row, creds)
	creds.Zero()
	if err != nil {
		return nil, err
	}
	req := &runnerpb.FetchServerCertificateRequest{
		Ctx: runContext(), Engine: eng, Host: row.Host, Port: uint32(row.Port), SshTunnel: tun, //nolint:gosec // G115: validated port
	}
	defer runnerpb.ZeroSecrets(req)
	resp, err := s.o.Runner.FetchServerCertificate(ctx, req)
	if err != nil {
		return nil, s.runnerError(ctx, actor, row, "fetch_certificate", err)
	}
	if len(resp.GetChain()) == 0 {
		return nil, &TargetError{Code: CodeRunnerFailed, Message: "the server presented no certificate"}
	}
	return resp, nil
}

// FetchCertificate captures the server's certificate chain through the
// runner. It stores and trusts nothing.
func (s *Service) FetchCertificate(ctx context.Context, actor Actor, id uuid.UUID) (CertificateChain, error) {
	ctx, row, done, err := s.begin(ctx, actor, id)
	if err != nil {
		return CertificateChain{}, err
	}
	defer done()
	resp, err := s.capture(ctx, actor, &row)
	if err != nil {
		return CertificateChain{}, err
	}
	out := CertificateChain{SuggestedAnchorIndex: int(resp.GetSuggestedAnchorIndex()), LeafSPKISHA256: resp.GetLeafSpkiSha256()}
	for _, pc := range resp.GetChain() {
		cert, err := x509.ParseCertificate(pc.GetDer())
		if err != nil {
			return CertificateChain{}, &TargetError{Code: CodeRunnerFailed, Message: "the server presented a certificate that does not parse"}
		}
		out.Chain = append(out.Chain, chainCertificate(cert))
	}
	if out.SuggestedAnchorIndex < -1 || out.SuggestedAnchorIndex >= len(out.Chain) {
		out.SuggestedAnchorIndex = -1
	}
	return out, nil
}

func chainCertificate(c *x509.Certificate) ChainCertificate {
	spki := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	cc := ChainCertificate{
		PEM:     string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})),
		Subject: c.Subject.String(), Issuer: c.Issuer.String(), DNSNames: c.DNSNames,
		NotBefore: c.NotBefore, NotAfter: c.NotAfter, IsCA: c.IsCA, SelfSigned: selfSigned(c),
		SPKISHA256: spki[:], Fingerprint: certFingerprint(c.Raw),
	}
	for _, ip := range c.IPAddresses {
		cc.IPAddresses = append(cc.IPAddresses, ip.String())
	}
	return cc
}

// selfSigned reports a certificate signed by its own key with matching
// subject and issuer (a leaf or a root CA).
func selfSigned(c *x509.Certificate) bool {
	return bytes.Equal(c.RawSubject, c.RawIssuer) && c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil
}

// parseSPKI accepts a SHA-256 as standard/URL base64 or hex.
func parseSPKI(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if len(s) == 64 {
		if b, err := hex.DecodeString(s); err == nil {
			return b, nil
		}
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == sha256.Size {
			return b, nil
		}
	}
	return nil, fieldErr("leafSpkiSha256", "must be a SHA-256 in base64 or hex")
}

// parseAnchorPEM parses exactly one PEM certificate.
func parseAnchorPEM(s string) (*x509.Certificate, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxAnchorPEMBytes {
		return nil, fieldErr("certificatePem", "one PEM certificate is required")
	}
	blocks, ok := pemBlocks([]byte(s), 1)
	if !ok || len(blocks) != 1 || blocks[0].Type != "CERTIFICATE" {
		return nil, fieldErr("certificatePem", "exactly one PEM certificate is required")
	}
	c, err := x509.ParseCertificate(blocks[0].Bytes)
	if err != nil {
		return nil, fieldErr("certificatePem", "the certificate does not parse")
	}
	return c, nil
}

// pinChoice is a validated pin ready to store.
type pinChoice struct {
	anchor   *x509.Certificate
	leafSPKI []byte // nil: CA pin with leaf rotation allowed
	notAfter time.Time
	source   string // "captured" or "pem"
}

// publicAnchor reports whether an anchor is, or chains to, a CA of the
// system trust store (pinning.IsPublicAnchor; replaced in tests). D-139: a
// pin anchor must be a self-signed leaf or a private CA; a public CA would
// accept every certificate it issues for the host name. The runner refuses
// such pins in every request too.
var publicAnchor = pinning.IsPublicAnchor

// verifyChain checks that the captured chain (leaf first) verifies with
// anchor as the only root, for host, at now: the check the runner's
// pre-flight applies before every connection (D-133).
func verifyChain(chain []*x509.Certificate, anchor *x509.Certificate, host string, now time.Time) error {
	roots := x509.NewCertPool()
	roots.AddCert(anchor)
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	_, err := chain[0].Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: inter, DNSName: host, CurrentTime: now,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err == nil {
		return nil
	}
	var (
		hostErr x509.HostnameError
		invErr  x509.CertificateInvalidError
	)
	switch {
	case errors.As(err, &hostErr):
		return fieldErr("certIndex", "the server's certificate is not valid for the host name %q (it would fail the runner's check)", host)
	case errors.As(err, &invErr) && invErr.Reason == x509.Expired:
		return fieldErr("certIndex", "a certificate of the server's chain is expired or not yet valid")
	default:
		return fieldErr("certIndex", "the server's certificate chain does not verify with the chosen anchor (pin the self-signed server certificate or the root CA that issued it)")
	}
}

// choosePin validates in into a pin. With CertIndex the chain is captured
// again through the runner, the captured leaf must present the leaf SPKI
// the admin compared, the anchor is taken from that capture, and the
// captured chain must verify with the anchor as the only root for the
// connection's host (Code M9, D-140). An out-of-band CertificatePEM is not
// checked against a capture (it may be staged before the server presents
// it). Either way the anchor must not be a public CA (D-139).
func (s *Service) choosePin(ctx context.Context, actor Actor, row *sqlc.Connection, in PinInput) (*pinChoice, error) {
	if (in.CertIndex == nil) == (strings.TrimSpace(in.CertificatePEM) == "") {
		return nil, fieldErr("certIndex", "set exactly one of certIndex or certificatePem")
	}
	leaf, err := parseSPKI(in.LeafSPKI)
	if err != nil {
		return nil, err
	}
	pc := &pinChoice{}
	var chain []*x509.Certificate
	if in.CertIndex != nil {
		if leaf == nil {
			return nil, fieldErr("leafSpkiSha256", "the leaf SPKI SHA-256 you compared is required with certIndex")
		}
		resp, err := s.capture(ctx, actor, row)
		if err != nil {
			return nil, err
		}
		for _, c := range resp.GetChain() {
			cert, err := x509.ParseCertificate(c.GetDer())
			if err != nil {
				return nil, &TargetError{Code: CodeRunnerFailed, Message: "the server presented a certificate that does not parse"}
			}
			chain = append(chain, cert)
		}
		idx := *in.CertIndex
		if idx < 0 || idx >= len(chain) {
			return nil, fieldErr("certIndex", "the server presented %d certificate(s); certIndex is out of range", len(chain))
		}
		leafSum := sha256.Sum256(chain[0].RawSubjectPublicKeyInfo)
		if !bytes.Equal(leafSum[:], leaf) {
			return nil, fieldErr("leafSpkiSha256", "the server now presents another leaf key than the one you confirmed; fetch the certificate again and compare it")
		}
		pc.anchor = chain[idx]
		pc.source = "captured"
	} else {
		if pc.anchor, err = parseAnchorPEM(in.CertificatePEM); err != nil {
			return nil, err
		}
		pc.source = "pem"
	}
	a := pc.anchor
	now := s.o.Now()
	switch {
	case len(a.Raw) > maxAnchorDER:
		return nil, fieldErr("certificatePem", "the certificate is too large")
	case !selfSigned(a):
		return nil, fieldErr("certIndex", "the pinned certificate must be self-signed (a self-signed server certificate or the root CA); database clients cannot use an intermediate CA as their only anchor")
	case now.Before(a.NotBefore) || !now.Before(a.NotAfter):
		return nil, fieldErr("certIndex", "the certificate is not valid now (valid %s to %s)", a.NotBefore.UTC().Format(time.RFC3339), a.NotAfter.UTC().Format(time.RFC3339))
	}
	own := sha256.Sum256(a.RawSubjectPublicKeyInfo)
	switch {
	case !a.IsCA && in.AllowLeafRotation:
		return nil, fieldErr("allowLeafRotation", "only a CA pin can allow leaf rotation")
	case !a.IsCA && leaf == nil:
		return nil, fieldErr("leafSpkiSha256", "the leaf SPKI SHA-256 is required")
	case !a.IsCA && !bytes.Equal(own[:], leaf):
		return nil, fieldErr("leafSpkiSha256", "does not match the pinned server certificate")
	case a.IsCA && in.AllowLeafRotation:
		pc.leafSPKI = nil
	case a.IsCA && leaf == nil:
		return nil, fieldErr("leafSpkiSha256", "the leaf SPKI SHA-256 is required (or allow leaf rotation for a CA pin)")
	default:
		pc.leafSPKI = leaf
	}
	if chain != nil {
		if err := verifyChain(chain, a, row.Host, now); err != nil {
			return nil, err
		}
	}
	if publicAnchor(a) {
		return nil, fieldErr("certIndex", "the anchor is a public CA trusted by the operating system; pin the server's self-signed certificate or a private CA instead (a public CA would accept any certificate it issues for this host name)")
	}
	pc.notAfter = a.NotAfter
	if chain != nil && pc.leafSPKI != nil && chain[0].NotAfter.Before(pc.notAfter) {
		pc.notAfter = chain[0].NotAfter
	}
	return pc, nil
}

func samePin(certPEM *string, spki []byte, pc *pinChoice) bool {
	if certPEM == nil {
		return false
	}
	b, _ := pem.Decode([]byte(*certPEM))
	return b != nil && bytes.Equal(b.Bytes, pc.anchor.Raw) && bytes.Equal(spki, pc.leafSPKI)
}

func anchorFingerprint(certPEM *string) string {
	if certPEM == nil {
		return ""
	}
	if b, _ := pem.Decode([]byte(*certPEM)); b != nil {
		return certFingerprint(b.Bytes)
	}
	return ""
}

// ConfirmPin pins the connection's certificate (current pin). Confirming
// the staged backup pin promotes it (the staged slot is cleared).
func (s *Service) ConfirmPin(ctx context.Context, actor Actor, id uuid.UUID, in PinInput) (Connection, error) {
	return s.setPin(ctx, actor, id, in, false)
}

// StageBackupPin stages a second pin for a planned certificate rotation;
// the runner accepts either pin (each with its own leaf binding). It needs
// a current pin.
func (s *Service) StageBackupPin(ctx context.Context, actor Actor, id uuid.UUID, in PinInput) (Connection, error) {
	return s.setPin(ctx, actor, id, in, true)
}

func (s *Service) setPin(ctx context.Context, actor Actor, id uuid.UUID, in PinInput, staged bool) (Connection, error) {
	cur, err := s.load(ctx, sqlc.New(s.o.DB), id, false)
	if err != nil {
		return Connection{}, err
	}
	if staged && cur.TlsPinCertPem == nil {
		return Connection{}, fieldErr("certIndex", "confirm a current pin before staging a backup pin")
	}
	var pc *pinChoice
	if in.CertIndex != nil {
		// A fresh capture (runner call): rate limited and bounded like the
		// other runner operations.
		release, err := s.acquire(actor)
		if err != nil {
			return Connection{}, err
		}
		cctx, cancel := context.WithTimeout(ctx, s.o.OperationTimeout)
		pc, err = s.choosePin(cctx, actor, &cur, in)
		cancel()
		release()
		if err != nil {
			return Connection{}, err
		}
	} else if pc, err = s.choosePin(ctx, actor, &cur, in); err != nil {
		return Connection{}, err
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pc.anchor.Raw}))
	var out sqlc.Connection
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		locked, err := s.load(ctx, q, id, true)
		if err != nil {
			return err
		}
		if locked.Host != cur.Host || locked.Port != cur.Port {
			return ErrConflict
		}
		details := audit.Details{
			"name": locked.Name, "host": locked.Host, "port": locked.Port, "source": pc.source,
			"subject": pc.anchor.Subject.String(), "issuer": pc.anchor.Issuer.String(), "is_ca": pc.anchor.IsCA,
			"anchor_sha256": certFingerprint(pc.anchor.Raw), "not_after": pc.notAfter.UTC().Format(time.RFC3339),
		}
		if pc.leafSPKI != nil {
			details["leaf_spki_sha256"] = hex.EncodeToString(pc.leafSPKI)
		} else {
			details["leaf_rotation_allowed"] = true
		}
		var action audit.Action
		if staged {
			if samePin(locked.TlsPinCertPem, locked.TlsPinSpki, pc) {
				return fieldErr("certIndex", "this is already the current pin")
			}
			if fp := anchorFingerprint(locked.TlsBackupPinCertPem); fp != "" {
				details["previous_anchor_sha256"] = fp
			}
			out, err = q.SetConnectionBackupPin(ctx, sqlc.SetConnectionBackupPinParams{
				ID: id, CertPem: &certPEM, Spki: pc.leafSPKI, NotAfter: &pc.notAfter, PinnedBy: &actor.UserID,
			})
			action = audit.ActionConnectionBackupPinStaged
		} else {
			promote := samePin(locked.TlsBackupPinCertPem, locked.TlsBackupPinSpki, pc)
			if fp := anchorFingerprint(locked.TlsPinCertPem); fp != "" {
				details["previous_anchor_sha256"] = fp
			}
			details["promoted_backup_pin"] = promote
			out, err = q.SetConnectionPin(ctx, sqlc.SetConnectionPinParams{
				ID: id, CertPem: &certPEM, Spki: pc.leafSPKI, NotAfter: &pc.notAfter, PinnedBy: &actor.UserID, ClearBackup: promote,
			})
			action = audit.ActionConnectionPinConfirmed
		}
		if err != nil {
			return fmt.Errorf("connections: store pin: %w", err)
		}
		return s.record(ctx, q, actor, action, id, details, nil)
	})
	if err != nil {
		return Connection{}, err
	}
	return s.view(&out), nil
}
