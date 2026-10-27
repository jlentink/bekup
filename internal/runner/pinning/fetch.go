package pinning

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// errCaptured aborts the capture handshake once the chain is recorded.
var errCaptured = errors.New("pinning: chain captured")

// Fetch captures t's certificate chain (leaf first) without trusting it.
//
// InsecureSkipVerify is set here because the chain is not trusted, only
// recorded for an admin to inspect and confirm (the confirmed pin is
// enforced by Preflight and the native client; D-133). It is one of the
// three InsecureSkipVerify uses in bekup, with dbprobe.UnverifiedTLS (TLS
// mode UNVERIFIED) and internal/db's tls_mode require (D-100, D-103).
// VerifyConnection records the chain and returns errCaptured, which aborts
// the handshake before it completes: the client never sends its Finished
// message, no application data is ever written, so no credentials can be
// sent and authentication never starts (TestFetchNeverAuthenticates).
func (c *Checker) Fetch(ctx context.Context, t Target) ([]*x509.Certificate, error) {
	if t.Dial == nil || !t.Kind.Valid() || t.Host == "" {
		return nil, errors.New("pinning: incomplete target")
	}
	var chain []*x509.Certificate
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: t.Host,
		// Capture only, see the function comment. The chain is never
		// used to protect data.
		InsecureSkipVerify: true, //nolint:gosec // G402: capture path only (D-133); the handshake is aborted in VerifyConnection, nothing is ever sent over it
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 || len(cs.PeerCertificates) > MaxChainCerts {
				return &MismatchError{Reason: ReasonNoCertificate}
			}
			chain = append([]*x509.Certificate(nil), cs.PeerCertificates...)
			return errCaptured
		},
	}
	tc, err := c.connect(ctx, t, cfg)
	if tc != nil {
		// Unreachable: VerifyConnection always fails the handshake.
		discardErr(tc.Close())
		return nil, errors.New("pinning: capture handshake completed unexpectedly")
	}
	if errors.Is(err, errCaptured) && len(chain) > 0 {
		return chain, nil
	}
	var me *MismatchError
	if errors.As(err, &me) {
		return nil, ErrProtocol
	}
	if err == nil {
		err = ErrHandshake
	}
	return nil, err
}

// Describe converts a captured chain into the RPC response: per
// certificate the subject, issuer, SANs, validity, SPKI SHA-256 (bytes,
// base64, hex) and PEM, plus the pin suggestion: a self-signed leaf is its
// own anchor; otherwise the self-signed root CA the presented chain leads
// to (-1 when the server did not present it). The leaf SPKI is always
// returned for the pre-flight check.
func Describe(chain []*x509.Certificate) *runnerpb.FetchServerCertificateResponse {
	out := &runnerpb.FetchServerCertificateResponse{SuggestedAnchorIndex: -1}
	for _, c := range chain {
		out.Chain = append(out.Chain, describeCert(c))
	}
	if len(chain) == 0 {
		return out
	}
	leafSPKI := sha256.Sum256(chain[0].RawSubjectPublicKeyInfo)
	out.LeafSpkiSha256 = leafSPKI[:]
	out.SuggestedAnchorIndex = suggestAnchor(chain)
	return out
}

func describeCert(c *x509.Certificate) *runnerpb.PeerCertificate {
	spki := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	ips := make([]string, 0, len(c.IPAddresses))
	for _, ip := range c.IPAddresses {
		ips = append(ips, ip.String())
	}
	return &runnerpb.PeerCertificate{
		Der:              c.Raw,
		Subject:          c.Subject.String(),
		Issuer:           c.Issuer.String(),
		DnsNames:         append([]string(nil), c.DNSNames...),
		IpAddresses:      ips,
		NotBefore:        timestamppb.New(c.NotBefore),
		NotAfter:         timestamppb.New(c.NotAfter),
		SpkiSha256:       spki[:],
		IsCa:             c.IsCA,
		SelfSigned:       selfSigned(c),
		Pem:              string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})),
		SpkiSha256Base64: base64.StdEncoding.EncodeToString(spki[:]),
		SpkiSha256Hex:    hex.EncodeToString(spki[:]),
	}
}

// selfSigned reports whether c is signed by its own key and names itself
// as the issuer.
func selfSigned(c *x509.Certificate) bool {
	if !bytes.Equal(c.RawIssuer, c.RawSubject) {
		return false
	}
	return c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil
}

// suggestAnchor follows the presented chain from the leaf through
// verified issuer signatures and returns the index of the self-signed
// certificate it ends at (the leaf itself when it is self-signed), or -1.
// Native clients (OpenSSL) need a self-signed trust anchor: an
// intermediate alone does not verify without a partial-chain flag.
func suggestAnchor(chain []*x509.Certificate) int32 {
	cur := 0
	used := map[int]bool{0: true}
	for steps := 0; steps < len(chain); steps++ {
		if selfSigned(chain[cur]) {
			if cur > 0 && !chain[cur].IsCA {
				return -1
			}
			return int32(cur)
		}
		next := -1
		for i := range chain {
			if used[i] || !chain[i].IsCA {
				continue
			}
			if bytes.Equal(chain[cur].RawIssuer, chain[i].RawSubject) && chain[cur].CheckSignatureFrom(chain[i]) == nil {
				next = i
				break
			}
		}
		if next < 0 {
			return -1
		}
		used[next] = true
		cur = next
	}
	return -1
}
