package destinations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/serviceutil"
	"github.com/jlentink/bekup/internal/tunnel"
)

// HostKeyInfo is a host key presented by an SFTP server (public data).
type HostKeyInfo struct {
	Type          string
	Fingerprint   string
	AuthorizedKey string
	// Host and Port are the server the key was fetched from; pass them to
	// ConfirmHostKey, which refuses the pin if the destination now points
	// elsewhere.
	Host string
	Port int
}

// FetchHostKey connects to the stored host and port of SFTP destination id
// through the SSRF guard, runs the SSH key exchange only and returns the
// host key the server presents (the trust-on-first-use step, D-122). The
// result is never trusted: an admin compares the fingerprint out of band
// and pins it with ConfirmHostKey. Only stored destinations can be probed
// (no free host/port: the API is not a port scanner, D-140), failures are
// normalized (refused, filtered, timed out and failed handshakes look the
// same), and it is rate limited like Test.
func (s *Service) FetchHostKey(ctx context.Context, actor Actor, id uuid.UUID) (HostKeyInfo, error) {
	r, err := s.load(ctx, sqlc.New(s.o.DB), id, false)
	if err != nil {
		return HostKeyInfo{}, err
	}
	if Type(r.row.Type) != TypeSFTP {
		return HostKeyInfo{}, ErrWrongType
	}
	host, port := r.sftp.Host, r.sftp.Port
	if port == 0 {
		port = defaultSFTPPort
	}
	release, err := s.acquire(actor)
	if err != nil {
		return HostKeyInfo{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, s.o.OperationTimeout)
	defer cancel()
	info, err := tunnel.FetchHostKey(ctx, host, port, s.o.Guard)
	if err != nil {
		s.o.Logger.InfoContext(ctx, "fetching an SSH host key failed",
			slog.String("destination_id", id.String()), slog.Any("error", err))
		return HostKeyInfo{}, hostKeyFetchError(err)
	}
	return HostKeyInfo{
		Type: info.Type, Fingerprint: info.FingerprintSHA256, AuthorizedKey: info.AuthorizedKey,
		Host: r.sftp.Host, Port: r.sftp.Port,
	}, nil
}

// Normalized host-key fetch failures (presentable). Like the runner's
// FetchHostKey (D-135), everything but a refused address and an
// unsupported key type is one "unreachable" category, so the result does
// not tell open, closed and filtered ports or non-SSH services apart.
const (
	msgFetchUnreachable = "the SSH server could not be reached or did not complete the SSH key exchange"
	msgFetchUnsupported = "the server's host key type is not accepted (ssh-ed25519, ECDSA or RSA ≥ 2048 bits required)"
	msgFetchNotAllowed  = "the host resolves to an address that is not allowed (SSRF guard); private addresses must be on the SSRF allowlist"
)

func hostKeyFetchError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return err
	case errors.Is(err, tunnel.ErrUnsupportedHostKey):
		return &HostKeyFetchError{Message: msgFetchUnsupported, err: err}
	case errors.Is(netguard.Normalize(err), netguard.ErrNotAllowed):
		return &HostKeyFetchError{Message: msgFetchNotAllowed, err: err}
	default:
		return &HostKeyFetchError{Message: msgFetchUnreachable, err: err}
	}
}

// ConfirmHostKey pins the SFTP host key given as an authorized_keys line
// (as returned by FetchHostKey and checked out of band by the admin).
// Only accepted key types (ed25519, ECDSA, RSA ≥ 2048) can be pinned.
// expectedHost and expectedPort are the server the key was fetched from
// (HostKeyInfo.Host/Port): if the destination now points elsewhere, the
// pin is refused with ErrConflict (checked under the row lock).
func (s *Service) ConfirmHostKey(ctx context.Context, actor Actor, id uuid.UUID, authorizedKey, expectedHost string, expectedPort int) (Destination, error) {
	info, err := serviceutil.HostKeyLine(authorizedKey, "authorizedKey")
	if err != nil {
		return Destination{}, err
	}
	line, typ, fp := info.AuthorizedKey, info.Type, info.FingerprintSHA256
	var out *record
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		r, err := s.load(ctx, q, id, true)
		if err != nil {
			return err
		}
		if Type(r.row.Type) != TypeSFTP {
			return ErrWrongType
		}
		if r.sftp.Host != expectedHost || r.sftp.Port != expectedPort {
			return ErrConflict
		}
		row, err := q.SetDestinationHostKey(ctx, sqlc.SetDestinationHostKeyParams{
			ID: id, HostKeyType: &typ, HostKeyFingerprint: &fp, HostKeyAuthorizedKey: &line, ConfirmedBy: &actor.UserID,
		})
		if err != nil {
			return fmt.Errorf("destinations: set host key: %w", err)
		}
		if out, err = decode(row); err != nil {
			return err
		}
		details := audit.Details{"name": r.row.Name, "host_key_type": typ, "fingerprint": fp, "host": r.sftp.Host, "port": r.sftp.Port}
		if r.row.HostKeyFingerprint != nil {
			details["previous_fingerprint"] = *r.row.HostKeyFingerprint
		}
		return s.record(ctx, q, actor, audit.ActionDestinationHostKeyConfirmed, id, details, nil)
	})
	if err != nil {
		return Destination{}, err
	}
	return out.view(), nil
}

// SSHKeypair is the public half of a generated keypair.
type SSHKeypair struct {
	// AuthorizedKey is the authorized_keys line to install on the server.
	AuthorizedKey string
	// Fingerprint is the OpenSSH SHA-256 fingerprint of the public key.
	Fingerprint string
}

// GenerateSSHKeypair creates an ed25519 keypair for an SFTP destination,
// stores the private key sealed (replacing a stored password or key) and
// returns only the public key. The private key never leaves the server.
func (s *Service) GenerateSSHKeypair(ctx context.Context, actor Actor, id uuid.UUID) (SSHKeypair, Destination, error) {
	kp, err := crypto.GenerateSSHKeypair("bekup-destination-" + id.String())
	if err != nil {
		return SSHKeypair{}, Destination{}, err
	}
	defer kp.PrivateKeyPEM.Destroy()
	var out *record
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		r, err := s.load(ctx, q, id, true)
		if err != nil {
			return err
		}
		if Type(r.row.Type) != TypeSFTP {
			return ErrWrongType
		}
		var removed []string
		switch {
		case r.sftp.AuthMethod == AuthPassword && len(r.row.SecretEnc) > 0:
			removed = append(removed, "password")
		case r.sftp.AuthMethod == AuthPrivateKey && len(r.row.SecretEnc) > 0:
			if r.sftp.HasPassphrase {
				removed = append(removed, "private_key_passphrase")
			}
		}
		creds := &Credentials{PrivateKey: append([]byte(nil), kp.PrivateKeyPEM.Bytes()...)}
		sealed, err := sealCredentials(s.o.Keyring, id, creds)
		creds.Zero()
		if err != nil {
			return err
		}
		cfg := *r.sftp
		cfg.AuthMethod, cfg.HasPassphrase, cfg.PublicKey = AuthPrivateKey, false, kp.AuthorizedKey
		raw, err := json.Marshal(&cfg)
		if err != nil {
			return fmt.Errorf("destinations: encode config: %w", err)
		}
		row, err := q.SetDestinationAuth(ctx, sqlc.SetDestinationAuthParams{ID: id, Config: raw, SecretEnc: sealed})
		if err != nil {
			return fmt.Errorf("destinations: store keypair: %w", err)
		}
		if out, err = decode(row); err != nil {
			return err
		}
		changed := append([]string{"private_key"}, removed...)
		return s.record(ctx, q, actor, audit.ActionDestinationSSHKeyGenerated, id,
			audit.Details{"name": r.row.Name, "public_key_fingerprint": kp.Fingerprint, "changed": changed}, changed)
	})
	if err != nil {
		return SSHKeypair{}, Destination{}, err
	}
	return SSHKeypair{AuthorizedKey: kp.AuthorizedKey, Fingerprint: kp.Fingerprint}, out.view(), nil
}
