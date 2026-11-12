package destinations

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/i18n"
	"github.com/jlentink/bekup/internal/serviceutil"
	"github.com/jlentink/bekup/internal/storage/webdav"
)

// WebDAV certificate pinning (D-222). Like the SFTP host-key flow, the
// certificate is never trusted automatically: a test shows the SPKI
// fingerprint of the certificate the server presents, the admin compares
// it out of band and pins it with PinCertificate (admin + step-up), which
// captures the certificate again and pins it only if its key is the one
// the admin confirmed.

// ErrCertificateChanged reports that the server presents another certificate than the
// one whose fingerprint the admin confirmed.
var ErrCertificateChanged = errors.New("destinations: the server now presents another certificate")

// CertificateFetchError reports that the server's certificate could not
// be captured; Message is presentable.
type CertificateFetchError struct {
	Message string
	// Msg is Message translatable (D-238).
	Msg i18n.Msg
	err error
}

func (e *CertificateFetchError) Error() string {
	return "destinations: fetch certificate: " + e.Message
}
func (e *CertificateFetchError) Unwrap() error { return e.err }

// parseFingerprint accepts a SHA-256 in hex (with or without colons) or
// base64.
func parseFingerprint(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if h := strings.ReplaceAll(s, ":", ""); len(h) == 2*sha256.Size {
		if b, err := hex.DecodeString(h); err == nil {
			return b, nil
		}
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == sha256.Size {
			return b, nil
		}
	}
	return nil, fieldErr("fingerprint", "must be the SHA-256 of the certificate's public key (SPKI) in hex or base64")
}

// PinCertificate pins the certificate the WebDAV destination's server
// presents now, if the SHA-256 of its public key (SPKI) equals
// fingerprint (the value the admin compared from a test result; hex or
// base64). The certificate must be usable as the only root: valid now,
// for TLS servers, naming the URL's host. The capture runs only the TLS
// handshake (no credentials are sent) through the SSRF guard, rate
// limited like Test.
func (s *Service) PinCertificate(ctx context.Context, actor Actor, id uuid.UUID, fingerprint string) (Destination, error) {
	want, err := parseFingerprint(fingerprint)
	if err != nil {
		return Destination{}, err
	}
	r, err := s.load(ctx, sqlc.New(s.o.DB), id, false)
	if err != nil {
		return Destination{}, err
	}
	if r.webdav == nil {
		return Destination{}, ErrWrongType
	}
	u, err := webdav.ParseURL(r.webdav.URL, s.o.AllowHTTP)
	if err != nil {
		return Destination{}, serviceutil.FieldErrMsg("webdav.url", webdav.Message(err))
	}
	if u.Scheme() != "https" {
		return Destination{}, fieldErr("webdav.url", "a plain http URL (dev mode) has no certificate to pin")
	}
	release, err := s.acquire(actor)
	if err != nil {
		return Destination{}, err
	}
	defer release()
	cctx, cancel := context.WithTimeout(ctx, s.o.OperationTimeout)
	defer cancel()
	pin, err := storedPin(&r.row)
	if err != nil {
		return Destination{}, err
	}
	cfg := webdav.InspectConfig{URL: r.webdav.URL, Pin: pin, AllowHTTP: s.o.AllowHTTP, Guard: s.o.Guard}
	if r.webdav.CABundlePEM != "" {
		cfg.CABundlePEM = []byte(r.webdav.CABundlePEM)
	}
	rep := webdav.Inspect(cctx, cfg)
	if rep.Certificate == nil {
		s.o.Logger.InfoContext(ctx, "capturing a WebDAV certificate failed",
			slog.String("destination_id", id.String()), slog.Any("error", rep.Err))
		m := i18n.M("the server could not be reached or presented no certificate")
		return Destination{}, &CertificateFetchError{Message: m.String(), Msg: m, err: rep.Err}
	}
	if subtle.ConstantTimeCompare(rep.Certificate.SPKISHA256, want) != 1 {
		return Destination{}, ErrCertificateChanged
	}
	cert, err := rep.Certificate.Parsed()
	if err != nil {
		return Destination{}, fmt.Errorf("destinations: parse captured certificate: %w", err)
	}
	if err := webdav.CheckPinnable(cert, u.Host(), s.now()); err != nil {
		return Destination{}, serviceutil.FieldErrFrom("fingerprint", err, i18n.M("the certificate cannot be used as a pin"))
	}
	spki := hex.EncodeToString(rep.Certificate.SPKISHA256)
	pemText := string(rep.Certificate.PEM())
	var out *record
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		locked, err := s.load(ctx, q, id, true)
		if err != nil {
			return err
		}
		if locked.webdav == nil {
			return ErrWrongType
		}
		if locked.webdav.URL != r.webdav.URL {
			return ErrConflict // the destination points elsewhere now
		}
		row, err := q.SetDestinationTLSPin(ctx, sqlc.SetDestinationTLSPinParams{
			ID: id, TlsPinSha256: &spki, TlsPinCertificate: &pemText, SetBy: &actor.UserID,
		})
		if err != nil {
			return fmt.Errorf("destinations: set certificate pin: %w", err)
		}
		if out, err = decode(row); err != nil {
			return err
		}
		details := audit.Details{
			"name": locked.row.Name, "host": u.HostPort(), "spki_sha256": spki,
			"certificate_sha256": hex.EncodeToString(rep.Certificate.SHA256),
			"not_after":          cert.NotAfter.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if locked.row.TlsPinSha256 != nil {
			details["previous_spki_sha256"] = *locked.row.TlsPinSha256
		}
		return s.record(ctx, q, actor, audit.ActionDestinationTLSPinned, id, details, nil)
	})
	if err != nil {
		return Destination{}, err
	}
	return out.view(), nil
}

// ClearCertificatePin removes the certificate pin of a WebDAV destination
// (it is verified against the system roots or its CA bundle again).
// Audited and alerted; a destination without a pin is returned unchanged.
func (s *Service) ClearCertificatePin(ctx context.Context, actor Actor, id uuid.UUID) (Destination, error) {
	var out *record
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		r, err := s.load(ctx, q, id, true)
		if err != nil {
			return err
		}
		if r.webdav == nil {
			return ErrWrongType
		}
		if r.row.TlsPinSha256 == nil {
			out = r
			return nil
		}
		row, err := q.ClearDestinationTLSPin(ctx, id)
		if err != nil {
			return fmt.Errorf("destinations: clear certificate pin: %w", err)
		}
		if out, err = decode(row); err != nil {
			return err
		}
		host := ""
		if u, err := webdav.ParseURL(r.webdav.URL, true); err == nil {
			host = u.HostPort()
		}
		return s.record(ctx, q, actor, audit.ActionDestinationTLSPinCleared, id, audit.Details{
			"name": r.row.Name, "host": host, "previous_spki_sha256": *r.row.TlsPinSha256,
		}, nil)
	})
	if err != nil {
		return Destination{}, err
	}
	return out.view(), nil
}
