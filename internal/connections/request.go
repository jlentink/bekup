package connections

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// The runner request builder: a connection row plus its unsealed
// credentials become a runnerpb.Target. Credential bytes are copied into
// the request; the caller zeroes the request with runnerpb.ZeroSecrets
// right after the RPC (Target's release function does that).

func engineOf(k engine.Kind) (runnerpb.Engine, error) {
	switch k {
	case engine.KindPostgres:
		return runnerpb.Engine_ENGINE_POSTGRES, nil
	case engine.KindMySQL:
		return runnerpb.Engine_ENGINE_MYSQL, nil
	case engine.KindMariaDB:
		return runnerpb.Engine_ENGINE_MARIADB, nil
	default:
		return runnerpb.Engine_ENGINE_UNSPECIFIED, fmt.Errorf("connections: unknown engine %q", k)
	}
}

func tlsModeOf(m TLSMode) runnerpb.TlsMode {
	switch m {
	case TLSPinned:
		return runnerpb.TlsMode_TLS_MODE_PINNED
	case TLSUnverified:
		return runnerpb.TlsMode_TLS_MODE_UNVERIFIED
	case TLSDisabled:
		return runnerpb.TlsMode_TLS_MODE_DISABLED
	default:
		return runnerpb.TlsMode_TLS_MODE_UNSPECIFIED
	}
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return append([]byte(nil), b...)
}

// certPin converts a stored pin (PEM anchor + leaf SPKI) to the wire form.
func certPin(certPEM *string, spki []byte) (*runnerpb.CertificatePin, error) {
	if certPEM == nil {
		return nil, nil
	}
	b, _ := pem.Decode([]byte(*certPEM))
	if b == nil || b.Type != "CERTIFICATE" {
		return nil, errors.New("connections: stored pin is not a PEM certificate")
	}
	return &runnerpb.CertificatePin{AnchorDer: b.Bytes, LeafSpkiSha256: cloneBytes(spki)}, nil
}

// sshTunnel builds the tunnel of row (nil without one). It needs a
// confirmed host key and a credential.
func sshTunnel(row *sqlc.Connection, creds *Credentials) (*runnerpb.SshTunnel, error) {
	if !row.SshEnabled {
		return nil, nil
	}
	if row.SshHostKeyAuthorizedKey == nil || row.SshHostKeyType == nil || row.SshHostKeyFingerprint == nil {
		return nil, &NotReadyError{Reason: "fetch and confirm the SSH bastion's host key"}
	}
	t := &runnerpb.SshTunnel{
		Host: deref(row.SshHost), Port: uint32(deref(row.SshPort)), User: deref(row.SshUser), //nolint:gosec // G115: validated port
		HostKeyPin: &runnerpb.HostKeyPin{
			Type: *row.SshHostKeyType, FingerprintSha256: *row.SshHostKeyFingerprint, AuthorizedKey: *row.SshHostKeyAuthorizedKey,
		},
	}
	switch {
	case len(creds.SSHPrivateKey) > 0:
		t.PrivateKeyPem = cloneBytes(creds.SSHPrivateKey)
		t.Passphrase = cloneBytes(creds.SSHPassphrase)
	case len(creds.SSHPassword) > 0:
		t.Password = cloneBytes(creds.SSHPassword)
	default:
		return nil, &NotReadyError{Reason: "set an SSH password or key"}
	}
	return t, nil
}

// buildTarget converts row and creds into a runner target. It refuses a
// pinned connection without a pin (the runner would refuse it too).
func buildTarget(row *sqlc.Connection, creds *Credentials) (*runnerpb.Target, error) {
	eng, err := engineOf(engine.Kind(row.Engine))
	if err != nil {
		return nil, err
	}
	mode := TLSMode(row.TlsMode)
	t := &runnerpb.Target{
		Engine: eng, VersionKey: row.ImageKey, Host: row.Host, Port: uint32(row.Port), User: row.Username, //nolint:gosec // G115: validated port
		Password: cloneBytes(creds.Password), TlsMode: tlsModeOf(mode),
	}
	if mode == TLSPinned {
		cur, err := certPin(row.TlsPinCertPem, row.TlsPinSpki)
		if err != nil {
			runnerpb.ZeroSecrets(t)
			return nil, err
		}
		if cur == nil {
			runnerpb.ZeroSecrets(t)
			return nil, &NotReadyError{Reason: "fetch and confirm the server's TLS certificate"}
		}
		t.Pins = append(t.Pins, cur)
		if staged, err := certPin(row.TlsBackupPinCertPem, row.TlsBackupPinSpki); err != nil {
			runnerpb.ZeroSecrets(t)
			return nil, err
		} else if staged != nil {
			t.Pins = append(t.Pins, staged)
		}
	}
	if row.ClientCertPem != nil && mode != TLSDisabled {
		t.ClientCertificate = &runnerpb.ClientCertificate{CertPem: []byte(*row.ClientCertPem), KeyPem: cloneBytes(creds.ClientKey)}
	}
	st, err := sshTunnel(row, creds)
	if err != nil {
		runnerpb.ZeroSecrets(t)
		return nil, err
	}
	t.SshTunnel = st
	return t, nil
}

// Target loads a connection and returns its runner target with the
// credentials unsealed (task 3.x workers). Call release right after the RPC:
// it zeroes every credential in the request.
func (s *Service) Target(ctx context.Context, id uuid.UUID) (*runnerpb.Target, func(), error) {
	row, err := s.load(ctx, sqlc.New(s.o.DB), id, false)
	if err != nil {
		return nil, nil, err
	}
	return s.target(&row)
}

// target unseals row's credentials and builds the request; the
// credentials are zeroed before it returns (the request holds copies).
func (s *Service) target(row *sqlc.Connection) (*runnerpb.Target, func(), error) {
	creds, err := openCredentials(s.o.Keyring, row.ID, row.SecretEnc)
	if err != nil {
		return nil, nil, err
	}
	defer creds.Zero()
	t, err := buildTarget(row, creds)
	if err != nil {
		return nil, nil, err
	}
	return t, func() { runnerpb.ZeroSecrets(t) }, nil
}
