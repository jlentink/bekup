package destinations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/s3store"
	"github.com/jlentink/bekup/internal/storage/sftp"
)

// s3Config builds the s3store configuration. The returned SecretAccessKey
// is a copy owned by the caller (destroy it after use / after Close).
func (s *Service) s3Config(c *S3Config, creds *Credentials, mode storage.LockMode, days int, noDelete bool) s3store.Config {
	conditional := c.ConditionalWrites
	cfg := s3store.Config{
		Endpoint: c.Endpoint, Region: c.Region, Bucket: c.Bucket, Prefix: c.Prefix, PathStyle: c.PathStyle,
		AccessKeyID:     c.AccessKeyID,
		SecretAccessKey: crypto.NewSecret(append([]byte(nil), creds.SecretAccessKey...)),
		SSE:             s3store.SSE(c.SSE), KMSKeyID: c.KMSKeyID, StorageClass: c.StorageClass,
		Checksum:          s3store.ChecksumMode(c.Checksum),
		LockMode:          mode,
		RetentionDays:     days,
		NoDelete:          noDelete,
		ConditionalWrites: &conditional,
		AllowHTTP:         s.o.AllowHTTP,
		Guard:             s.o.Guard,
		Logger:            s.o.Logger,
	}
	if c.CABundlePEM != "" {
		cfg.CABundlePEM = []byte(c.CABundlePEM)
	}
	return cfg
}

// Open builds the storage backend of a destination (for the backup,
// retention and restore workers, tasks 3.x). Every connection goes through
// the SSRF guard; credentials are opened from the sealed blob and handed
// to the backend, which owns them until Close. A destination that is not
// ready fails with *NotReadyError. Open does not check authorization: the
// caller has already done that.
//
// Unlike the API reads, Open also opens a soft-deleted destination:
// artifacts keep referencing it, and retention and restore must still
// reach them (the API hides deleted rows).
func (s *Service) Open(ctx context.Context, id uuid.UUID) (storage.Destination, error) {
	row, err := sqlc.New(s.o.DB).GetDestinationIncludingDeleted(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("destinations: load: %w", err)
	}
	r, err := decode(row)
	if err != nil {
		return nil, err
	}
	return s.open(ctx, r)
}

func (s *Service) open(ctx context.Context, r *record) (storage.Destination, error) {
	v := r.view()
	if !v.Ready {
		return nil, &NotReadyError{Reason: v.NotReadyReason}
	}
	switch v.Type {
	case TypeDisk:
		if s.o.DiskRoot == "" {
			return nil, &NotReadyError{Reason: "storage.disk_root is not configured"}
		}
		return storage.OpenDisk(storage.DiskConfig{Root: s.o.DiskRoot, Dir: r.disk.Path, NoDelete: v.NoDelete})
	case TypeS3:
		creds, err := openCredentials(s.o.Keyring, r.row.ID, r.row.SecretEnc)
		if err != nil {
			return nil, err
		}
		cfg := s.s3Config(r.s3, creds, v.ObjectLockMode, v.ObjectLockRetentionDays, v.NoDelete)
		creds.Zero()
		d, err := s3store.Open(ctx, cfg)
		if err != nil {
			cfg.SecretAccessKey.Destroy()
			return nil, err
		}
		return &closeHook{Destination: d, after: cfg.SecretAccessKey.Destroy}, nil
	case TypeSFTP:
		creds, err := openCredentials(s.o.Keyring, r.row.ID, r.row.SecretEnc)
		if err != nil {
			return nil, err
		}
		auth := sftp.Auth{}
		// sftp.Open takes ownership of the secrets (and destroys them).
		if len(creds.Password) > 0 {
			auth.Password = crypto.NewSecret(creds.Password)
		}
		if len(creds.PrivateKey) > 0 {
			auth.PrivateKey = crypto.NewSecret(creds.PrivateKey)
		}
		if len(creds.Passphrase) > 0 {
			auth.Passphrase = crypto.NewSecret(creds.Passphrase)
		}
		// The slices now belong to the secrets.
		creds.Password, creds.PrivateKey, creds.Passphrase = nil, nil, nil
		return sftp.Open(sftp.Config{
			Host: r.sftp.Host, Port: r.sftp.Port, User: r.sftp.User, Auth: auth,
			HostKeyPin: sftp.HostKeyPin{
				Type: v.HostKey.Type, FingerprintSHA256: v.HostKey.Fingerprint, AuthorizedKey: v.HostKey.AuthorizedKey,
			},
			BasePath: r.sftp.BasePath, Dir: r.sftp.Dir, NoDelete: v.NoDelete,
			Guard: s.o.Guard, Logger: s.o.Logger,
		})
	}
	return nil, fmt.Errorf("destinations: unknown type %q", v.Type)
}

// closeHook runs after once the wrapped destination is closed.
type closeHook struct {
	storage.Destination
	after func()
}

func (c *closeHook) Close() error {
	err := c.Destination.Close()
	if c.after != nil {
		c.after()
		c.after = nil
	}
	return err
}

// StepConnect is the TestResult step reported when the backend could not
// be opened (connection, TLS, bucket detection).
const StepConnect = "connect"

// Test opens the destination and runs the storage probe (write, read,
// delete below the destination's prefix; D-121). The result carries only
// normalized codes and fixed messages, so it can be shown to maintainers.
// It is rate limited per caller and bounded in time and concurrency.
func (s *Service) Test(ctx context.Context, actor Actor, id uuid.UUID) (storage.TestResult, error) {
	r, err := s.load(ctx, sqlc.New(s.o.DB), id, false)
	if err != nil {
		return storage.TestResult{}, err
	}
	if v := r.view(); !v.Ready {
		return storage.TestResult{}, &NotReadyError{Reason: v.NotReadyReason}
	}
	release, err := s.acquire(actor)
	if err != nil {
		return storage.TestResult{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, s.o.OperationTimeout)
	defer cancel()
	d, err := s.open(ctx, r)
	if err != nil {
		var nr *NotReadyError
		if errors.As(err, &nr) {
			return storage.TestResult{}, err
		}
		code := storage.Classify(err)
		if errors.Is(netguard.Normalize(err), netguard.ErrNotAllowed) || errors.Is(netguard.Normalize(err), netguard.ErrUnreachable) {
			code = storage.CodeNetwork
		}
		if code == storage.CodeHostKeyMismatch {
			if err := s.hostKeyMismatch(ctx, actor, r); err != nil {
				return storage.TestResult{}, err
			}
		}
		s.o.Logger.WarnContext(ctx, "destination test could not open the backend",
			slog.String("destination_id", id.String()), slog.String("code", string(code)), slog.Any("error", err))
		return storage.TestResult{Steps: []storage.TestStep{{Name: StepConnect, Code: code, Message: code.Message()}}}, nil
	}
	defer func() {
		if err := d.Close(); err != nil {
			s.o.Logger.WarnContext(ctx, "closing destination after test failed", slog.String("destination_id", id.String()), slog.Any("error", err))
		}
	}()
	res := d.Test(ctx, storage.Key{})
	for _, st := range res.Steps {
		if st.Code == storage.CodeHostKeyMismatch {
			if err := s.hostKeyMismatch(ctx, actor, r); err != nil {
				return storage.TestResult{}, err
			}
			break
		}
	}
	steps := make([]storage.TestStep, 0, len(res.Steps)+1)
	steps = append(steps, storage.TestStep{Name: StepConnect, OK: true, Code: storage.CodeOK, Message: storage.CodeOK.Message()})
	res.Steps = append(steps, res.Steps...)
	if !res.OK {
		s.o.Logger.InfoContext(ctx, "destination test failed", slog.String("destination_id", id.String()))
	}
	return res, nil
}

// hostKeyMismatch records a host-key pin violation seen by Test: a
// security event (possible impersonation of the SFTP server), audited
// (fail closed) and logged at WARN; task 5.5 turns it into an alert.
func (s *Service) hostKeyMismatch(ctx context.Context, actor Actor, r *record) error {
	details := audit.Details{"name": r.row.Name, "host": r.sftp.Host, "port": r.sftp.Port}
	if r.row.HostKeyFingerprint != nil {
		details["pinned_fingerprint"] = *r.row.HostKeyFingerprint
	}
	s.o.Logger.WarnContext(ctx, "destination_ssh_host_key_mismatch",
		slog.String("destination_id", r.row.ID.String()), slog.String("host", r.sftp.Host), slog.Int("port", r.sftp.Port))
	ev := audit.UserActor(actor.UserID)
	if actor.UserID == uuid.Nil {
		ev = audit.SystemActor() // a worker's run (RecordRunFailure)
	}
	_, err := s.o.Auditor.Record(ctx, audit.Event{
		Actor: ev, Action: audit.ActionDestinationHostKeyMismatch, TargetType: "destination",
		TargetID: r.row.ID.String(), IP: actor.IP, Details: details,
	})
	return err
}

// RecordRunFailure handles the security event of a destination failure a
// worker saw (backup runs, task 3.6): an SFTP host-key mismatch is audited
// with the system actor (fail closed) and logged, as in Test. Other codes
// are ignored.
func (s *Service) RecordRunFailure(ctx context.Context, id uuid.UUID, code storage.ErrorCode) error {
	if code != storage.CodeHostKeyMismatch {
		return nil
	}
	ctx = context.WithoutCancel(ctx)
	row, err := sqlc.New(s.o.DB).GetDestinationIncludingDeleted(ctx, id)
	if err != nil {
		return fmt.Errorf("destinations: load: %w", err)
	}
	r, err := decode(row)
	if err != nil {
		return err
	}
	if r.sftp == nil {
		return nil
	}
	return s.hostKeyMismatch(ctx, Actor{}, r)
}

// acquire applies the per-user rate limit and the concurrency bound of
// network operations.
func (s *Service) acquire(actor Actor) (func(), error) {
	return s.limiter.Acquire(actor.UserID.String())
}
