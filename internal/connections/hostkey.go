package connections

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/serviceutil"
	"github.com/jlentink/bekup/internal/sshpolicy"
)

// HostKeyInfo is a host key presented by a bastion (public data).
type HostKeyInfo struct {
	Type          string
	Fingerprint   string
	AuthorizedKey string
	// Host and Port are the bastion the key was fetched from; pass them
	// to ConfirmHostKey, which refuses the pin if the bastion changed in
	// the meantime.
	Host string
	Port int
}

// FetchHostKey captures the SSH bastion's host key through the runner's
// FetchHostKey (TOFU, D-122): the runner dials through its target
// allowlist and stops after the key exchange. Nothing is stored or
// trusted; an admin compares the fingerprint out of band and confirms it.
func (s *Service) FetchHostKey(ctx context.Context, actor Actor, id uuid.UUID) (HostKeyInfo, error) {
	ctx, row, done, err := s.begin(ctx, actor, id)
	if err != nil {
		return HostKeyInfo{}, err
	}
	defer done()
	if !row.SshEnabled {
		return HostKeyInfo{}, ErrNoSSH
	}
	resp, err := s.o.Runner.FetchHostKey(ctx, &runnerpb.FetchHostKeyRequest{
		Ctx: runContext(), Host: deref(row.SshHost), Port: uint32(deref(row.SshPort)), //nolint:gosec // G115: validated port
	})
	if err != nil {
		return HostKeyInfo{}, s.runnerError(ctx, actor, &row, "fetch_ssh_host_key", err)
	}
	// Re-check what the runner returned before showing it.
	info, err := sshpolicy.ParseHostKeyLine(resp.GetAuthorizedKey())
	if err != nil || info.Type != resp.GetType() || info.FingerprintSHA256 != resp.GetFingerprintSha256() {
		return HostKeyInfo{}, &TargetError{Code: CodeRunnerFailed, Message: "the runner returned an invalid host key"}
	}
	return HostKeyInfo{
		Type: info.Type, Fingerprint: info.FingerprintSHA256, AuthorizedKey: info.AuthorizedKey,
		Host: deref(row.SshHost), Port: int(deref(row.SshPort)),
	}, nil
}

// ConfirmHostKey pins the bastion's host key given as an authorized_keys
// line (as returned by FetchHostKey and compared out of band). Only
// accepted key types (ed25519, ECDSA, RSA ≥ 2048) can be pinned.
// expectedHost and expectedPort are the bastion the key was fetched from
// (HostKeyInfo.Host/Port): if the connection's bastion changed since, the
// pin is refused with ErrConflict (checked under the row lock).
func (s *Service) ConfirmHostKey(ctx context.Context, actor Actor, id uuid.UUID, authorizedKey, expectedHost string, expectedPort int) (Connection, error) {
	info, err := serviceutil.HostKeyLine(authorizedKey, "authorizedKey")
	if err != nil {
		return Connection{}, err
	}
	line, typ, fp := info.AuthorizedKey, info.Type, info.FingerprintSHA256
	var out sqlc.Connection
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		r, err := s.load(ctx, q, id, true)
		if err != nil {
			return err
		}
		if !r.SshEnabled {
			return ErrNoSSH
		}
		if deref(r.SshHost) != expectedHost || int(deref(r.SshPort)) != expectedPort {
			return ErrConflict
		}
		out, err = q.SetConnectionHostKey(ctx, sqlc.SetConnectionHostKeyParams{
			ID: id, HostKeyType: &typ, HostKeyFingerprint: &fp, HostKeyAuthorizedKey: &line, ConfirmedBy: &actor.UserID,
		})
		if err != nil {
			return fmt.Errorf("connections: set host key: %w", err)
		}
		details := audit.Details{
			"name": r.Name, "host_key_type": typ, "fingerprint": fp,
			"ssh_host": deref(r.SshHost), "ssh_port": deref(r.SshPort),
		}
		if r.SshHostKeyFingerprint != nil {
			details["previous_fingerprint"] = *r.SshHostKeyFingerprint
		}
		return s.record(ctx, q, actor, audit.ActionConnectionHostKeyConfirmed, id, details, nil)
	})
	if err != nil {
		return Connection{}, err
	}
	return s.view(&out), nil
}

// SSHKeypair is the public half of a generated keypair.
type SSHKeypair struct {
	// AuthorizedKey is the authorized_keys line to install on the bastion.
	AuthorizedKey string
	// Fingerprint is the OpenSSH SHA-256 fingerprint of the public key.
	Fingerprint string
}

// GenerateSSHKeypair creates an ed25519 keypair for the connection's SSH
// tunnel, stores the private key sealed (replacing a stored SSH password
// or key) and returns only the public key.
func (s *Service) GenerateSSHKeypair(ctx context.Context, actor Actor, id uuid.UUID) (SSHKeypair, Connection, error) {
	kp, err := crypto.GenerateSSHKeypair("bekup-connection-" + id.String())
	if err != nil {
		return SSHKeypair{}, Connection{}, err
	}
	defer kp.PrivateKeyPEM.Destroy()
	var out sqlc.Connection
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		r, err := s.load(ctx, q, id, true)
		if err != nil {
			return err
		}
		if !r.SshEnabled {
			return ErrNoSSH
		}
		creds, err := openCredentials(s.o.Keyring, id, r.SecretEnc)
		if err != nil {
			return err
		}
		defer creds.Zero()
		changed := []string{"ssh_private_key"}
		if len(creds.SSHPassword) > 0 {
			changed = append(changed, "ssh_password")
		}
		if len(creds.SSHPassphrase) > 0 {
			changed = append(changed, "ssh_private_key_passphrase")
		}
		creds.clearSSH()
		creds.SSHPrivateKey = append([]byte(nil), kp.PrivateKeyPEM.Bytes()...)
		sealed, err := sealCredentials(s.o.Keyring, id, creds)
		if err != nil {
			return err
		}
		out, err = q.SetConnectionSSHAuth(ctx, sqlc.SetConnectionSSHAuthParams{
			ID: id, SshAuth: sqlc.ConnectionSshAuthPrivateKey, SshHasPassphrase: false,
			SshPublicKey: &kp.AuthorizedKey, SecretEnc: sealed,
		})
		if err != nil {
			return fmt.Errorf("connections: store keypair: %w", err)
		}
		return s.record(ctx, q, actor, audit.ActionConnectionSSHKeyGenerated, id,
			audit.Details{"name": r.Name, "public_key_fingerprint": kp.Fingerprint, "changed": changed}, changed)
	})
	if err != nil {
		return SSHKeypair{}, Connection{}, err
	}
	return SSHKeypair{AuthorizedKey: kp.AuthorizedKey, Fingerprint: kp.Fingerprint}, s.view(&out), nil
}
