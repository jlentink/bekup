package destinations

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/i18n"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/s3store"
	"github.com/jlentink/bekup/internal/storage/sftp"
	"github.com/jlentink/bekup/internal/storage/webdav"
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
		return nil, notReady(v.NotReadyMsg)
	}
	return s.openBackend(ctx, r, &v)
}

// openBackend builds the backend without the readiness check (Test runs
// it for a WebDAV destination whose only problem is a failed overwrite
// probe, so the admin can test again after fixing the server).
func (s *Service) openBackend(ctx context.Context, r *record, v *Destination) (storage.Destination, error) {
	switch v.Type {
	case TypeDisk:
		if s.o.DiskRoot == "" {
			return nil, notReady(i18n.M("storage.disk_root is not configured"))
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
	case TypeWebDAV:
		creds, err := openCredentials(s.o.Keyring, r.row.ID, r.row.SecretEnc)
		if err != nil {
			return nil, err
		}
		// webdav.Open takes ownership of the password (and destroys it).
		pw := crypto.NewSecret(creds.Password)
		creds.Password = nil
		creds.Zero()
		cfg, err := s.webdavConfig(r, pw, v.NoDelete)
		if err != nil {
			pw.Destroy()
			return nil, err
		}
		return webdav.Open(cfg)
	}
	return nil, fmt.Errorf("destinations: unknown type %q", v.Type)
}

// webdavConfig builds the backend configuration of a WebDAV record; it
// takes ownership of pw only on success.
func (s *Service) webdavConfig(r *record, pw *crypto.Secret, noDelete bool) (webdav.Config, error) {
	c := r.webdav
	cfg := webdav.Config{
		URL: c.URL, Flavor: webdav.Flavor(c.Flavor), Username: c.Username, Password: pw, Path: c.Path,
		NoDelete: noDelete, RequestTimeout: time.Duration(c.TimeoutSeconds) * time.Second,
		AllowHTTP: s.o.AllowHTTP, Guard: s.o.Guard, Logger: s.o.Logger,
	}
	if c.CABundlePEM != "" {
		cfg.CABundlePEM = []byte(c.CABundlePEM)
	}
	pin, err := storedPin(&r.row)
	if err != nil {
		return webdav.Config{}, err
	}
	cfg.Pin = pin
	return cfg, nil
}

// storedPin returns the stored certificate pin of a WebDAV row (nil:
// none).
func storedPin(row *sqlc.Destination) (*webdav.Pin, error) {
	if row.TlsPinSha256 == nil || row.TlsPinCertificate == nil {
		return nil, nil
	}
	spki, err := hex.DecodeString(*row.TlsPinSha256)
	if err != nil {
		return nil, fmt.Errorf("destinations: stored certificate pin of %s is malformed", row.ID)
	}
	return &webdav.Pin{CertificatePEM: []byte(*row.TlsPinCertificate), SPKISHA256: spki}, nil
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

// StepOverwrite is the WebDAV test step of the overwrite probe (D-222).
const StepOverwrite = "overwrite"

// TestResult is the outcome of Test: the storage probe's steps (connect,
// [overwrite,] write, read, delete) plus the WebDAV findings.
type TestResult struct {
	storage.TestResult
	// WebDAV is set for WebDAV destinations.
	WebDAV *WebDAVTest
}

// WebDAVTest is what a test of a WebDAV destination saw (public data and
// fixed enums only, never server text).
type WebDAVTest struct {
	// Trust is how the certificate is verified; Trusted whether it was.
	Trust   webdav.Trust
	Trusted bool
	// Certificate is the leaf the server presented (nil: none seen).
	Certificate *webdav.Certificate
	// PinMatches: the presented key is the pinned one.
	PinMatches bool
	// Server is what status.php or the DAV header suggested.
	Server webdav.ServerKind
	// UploadMode is the mode the write step used (UploadUnknown when it
	// did not run).
	UploadMode webdav.UploadMode
	// Overwrite is "refused" (required), "ignored" or "" (the probe did
	// not finish).
	Overwrite string
}

// Overwrite probe outcomes of WebDAVTest.
const (
	OverwriteRefused = "refused"
	OverwriteIgnored = "ignored"
)

// Test opens the destination and runs the storage probe (write, read,
// delete below the destination's prefix; D-121; WebDAV first inspects the
// certificate and runs the overwrite probe, D-222). The result carries
// only normalized codes and fixed messages (plus curated details), so it
// can be shown to maintainers. It is rate limited per caller and bounded
// in time and concurrency.
func (s *Service) Test(ctx context.Context, actor Actor, id uuid.UUID) (TestResult, error) {
	r, err := s.load(ctx, sqlc.New(s.o.DB), id, false)
	if err != nil {
		return TestResult{}, err
	}
	v := r.view()
	if !v.Ready && v.NotReadyReason != overwriteIgnoredReason {
		return TestResult{}, notReady(v.NotReadyMsg)
	}
	release, err := s.acquire(actor)
	if err != nil {
		return TestResult{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, s.o.OperationTimeout)
	defer cancel()
	d, err := s.openBackend(ctx, r, &v)
	if err != nil {
		var nr *NotReadyError
		if errors.As(err, &nr) {
			return TestResult{}, err
		}
		code := storage.Classify(err)
		if errors.Is(netguard.Normalize(err), netguard.ErrNotAllowed) || errors.Is(netguard.Normalize(err), netguard.ErrUnreachable) {
			code = storage.CodeNetwork
		}
		if code == storage.CodeHostKeyMismatch {
			if err := s.pinMismatch(ctx, actor, r); err != nil {
				return TestResult{}, err
			}
		}
		s.o.Logger.WarnContext(ctx, "destination test could not open the backend",
			slog.String("destination_id", id.String()), slog.String("code", string(code)), slog.Any("error", err))
		s.testFailed(ctx, id, code)
		s.recordTest(ctx, r, false, code)
		return TestResult{TestResult: storage.TestResult{Steps: []storage.TestStep{{Name: StepConnect, Code: code, Message: code.Message()}}}}, nil
	}
	defer func() {
		if err := d.Close(); err != nil {
			s.o.Logger.WarnContext(ctx, "closing destination after test failed", slog.String("destination_id", id.String()), slog.Any("error", err))
		}
	}()
	var out TestResult
	if wd, ok := d.(*webdav.Destination); ok {
		out = s.testWebDAV(ctx, wd)
	} else {
		out.TestResult = d.Test(ctx, storage.Key{})
		steps := make([]storage.TestStep, 0, len(out.Steps)+1)
		steps = append(steps, storage.TestStep{Name: StepConnect, OK: true, Code: storage.CodeOK, Message: storage.CodeOK.Message()})
		out.Steps = append(steps, out.Steps...)
	}
	for _, st := range out.Steps {
		if st.Code == storage.CodeHostKeyMismatch || st.Code == storage.CodeTLSPinMismatch {
			if err := s.pinMismatch(ctx, actor, r); err != nil {
				return TestResult{}, err
			}
			break
		}
	}
	res := &out.TestResult
	if !res.OK {
		s.o.Logger.InfoContext(ctx, "destination test failed", slog.String("destination_id", id.String()))
		code := storage.CodeOK
		for _, st := range res.Steps {
			if !st.OK {
				code = st.Code
				break
			}
		}
		s.testFailed(ctx, id, code)
		s.recordTest(ctx, r, false, code)
	} else {
		s.recordTest(ctx, r, true, storage.CodeOK)
	}
	return out, nil
}

// testWebDAV runs the steps of a WebDAV test: connect (the TLS handshake
// under the configured trust, the presented certificate captured either
// way), overwrite (the probe), then the storage probe. Server hints are
// collected even when a step failed (they explain wrong URLs or flavors).
func (s *Service) testWebDAV(ctx context.Context, wd *webdav.Destination) TestResult {
	info := &WebDAVTest{Trust: wd.Trust()}
	out := TestResult{WebDAV: info}
	rep := wd.InspectTLS(ctx)
	info.Certificate, info.Trusted, info.PinMatches = rep.Certificate, rep.Trusted, rep.PinMatches
	if !rep.Trusted {
		out.Steps = []storage.TestStep{storage.FailStep(StepConnect, rep.Err)}
		return out
	}
	out.Steps = []storage.TestStep{storage.OKStep(StepConnect)}
	if err := wd.ProbeOverwrite(ctx); err != nil {
		if storage.Classify(err) == storage.CodeOverwriteIgnored {
			info.Overwrite = OverwriteIgnored
		}
		out.Steps = append(out.Steps, storage.FailStep(StepOverwrite, err))
		info.Server = wd.DetectServer(ctx)
		return out
	}
	info.Overwrite = OverwriteRefused
	out.Steps = append(out.Steps, storage.OKStep(StepOverwrite))
	res := wd.Test(ctx, storage.Key{})
	res.Steps = append(out.Steps, res.Steps...)
	out.TestResult = res
	if len(res.Steps) > 2 && res.Steps[2].OK {
		info.UploadMode = wd.UploadMode()
	}
	info.Server = wd.DetectServer(ctx)
	return out
}

// storedCode is the syntax of a stored test code (the DB checks it too).
var storedCode = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

// recordTest stores the outcome of a Test (D-187): the normalized error
// class only, and only when the destination did not change while the test
// ran (the row's updated_at). Storing is best effort: a failure is logged
// and the test result still returned.
func (s *Service) recordTest(ctx context.Context, r *record, ok bool, code storage.ErrorCode) {
	arg := sqlc.RecordDestinationTestParams{ID: r.row.ID, SettingsAt: r.row.UpdatedAt, Ok: &ok}
	if !ok {
		c := string(code)
		if !storedCode.MatchString(c) {
			c = "failed"
		}
		arg.Code = &c
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	n, err := sqlc.New(s.o.DB).RecordDestinationTest(ctx, arg)
	switch {
	case err != nil:
		s.o.Logger.WarnContext(ctx, "storing the destination test result failed",
			slog.String("destination_id", r.row.ID.String()), slog.Any("error", err))
	case n == 0:
		s.o.Logger.InfoContext(ctx, "destination changed during its test; result not stored",
			slog.String("destination_id", r.row.ID.String()))
	}
}

// testFailed reports a failed test to the TestFailed hook.
func (s *Service) testFailed(ctx context.Context, id uuid.UUID, code storage.ErrorCode) {
	if s.o.TestFailed != nil {
		s.o.TestFailed(context.WithoutCancel(ctx), id, code)
	}
}

// pinMismatch records a pin violation seen by Test or a worker: an SFTP
// host key or a WebDAV certificate key other than the pinned one. A
// security event (possible impersonation of the server), audited (fail
// closed) and logged at WARN; the audit observer raises the
// security.system alert (task 5.5).
func (s *Service) pinMismatch(ctx context.Context, actor Actor, r *record) error {
	ev := audit.UserActor(actor.UserID)
	if actor.UserID == uuid.Nil {
		ev = audit.SystemActor() // a worker's run (RecordRunFailure)
	}
	event := audit.Event{Actor: ev, TargetType: "destination", TargetID: r.row.ID.String(), IP: actor.IP}
	switch {
	case r.sftp != nil:
		details := audit.Details{"name": r.row.Name, "host": r.sftp.Host, "port": r.sftp.Port}
		if r.row.HostKeyFingerprint != nil {
			details["pinned_fingerprint"] = *r.row.HostKeyFingerprint
		}
		s.o.Logger.WarnContext(ctx, "destination_ssh_host_key_mismatch",
			slog.String("destination_id", r.row.ID.String()), slog.String("host", r.sftp.Host), slog.Int("port", r.sftp.Port))
		event.Action, event.Details = audit.ActionDestinationHostKeyMismatch, details
	case r.webdav != nil:
		host := ""
		if u, err := webdav.ParseURL(r.webdav.URL, true); err == nil {
			host = u.HostPort()
		}
		details := audit.Details{"name": r.row.Name, "host": host}
		if r.row.TlsPinSha256 != nil {
			details["pinned_spki_sha256"] = *r.row.TlsPinSha256
		}
		s.o.Logger.WarnContext(ctx, "destination_tls_pin_mismatch",
			slog.String("destination_id", r.row.ID.String()), slog.String("host", host))
		event.Action, event.Details = audit.ActionDestinationTLSPinMismatch, details
	default:
		return nil
	}
	_, err := s.o.Auditor.Record(ctx, event)
	return err
}

// RecordRunFailure handles the security event of a destination failure a
// worker saw (backup runs, task 3.6): an SFTP host-key or WebDAV
// certificate-pin mismatch is audited with the system actor (fail closed)
// and logged, as in Test. Other codes are ignored.
func (s *Service) RecordRunFailure(ctx context.Context, id uuid.UUID, code storage.ErrorCode) error {
	if code != storage.CodeHostKeyMismatch && code != storage.CodeTLSPinMismatch {
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
	if (code == storage.CodeHostKeyMismatch) != (r.sftp != nil) || (code == storage.CodeTLSPinMismatch) != (r.webdav != nil) {
		return nil
	}
	return s.pinMismatch(ctx, Actor{}, r)
}

// acquire applies the per-user rate limit and the concurrency bound of
// network operations.
func (s *Service) acquire(actor Actor) (func(), error) {
	return s.limiter.Acquire(actor.UserID.String())
}
