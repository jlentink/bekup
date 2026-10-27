package destinations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/serviceutil"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/sftp"
)

// Errors of the service. Presentable messages live in the API layer.
var (
	ErrNotFound  = errors.New("destinations: destination not found")
	ErrNameTaken = errors.New("destinations: another destination already uses this name")
	// ErrConflict: the destination changed while an update was prepared.
	ErrConflict = errors.New("destinations: the destination was changed concurrently")
	// ErrWrongType: the operation does not apply to the destination type.
	ErrWrongType = errors.New("destinations: operation not supported for this destination type")
	// ErrBusy: too many network operations (tests, host-key fetches) run
	// at the same time (shared with internal/connections).
	ErrBusy = serviceutil.ErrBusy
	// ErrInUse: the destination is still used and cannot be deleted.
	ErrInUse = errors.New("destinations: the destination is in use")
)

// NotReadyError reports that the destination cannot be opened yet; Reason is
// presentable.
type NotReadyError struct{ Reason string }

func (e *NotReadyError) Error() string { return "destinations: not ready: " + e.Reason }

// RateLimitedError reports that the caller exceeded the per-user limit of network
// operations.
type RateLimitedError = serviceutil.RateLimitedError

// HostKeyFetchError reports that the host key could not be fetched; Message is a
// normalized, presentable reason.
type HostKeyFetchError struct {
	Message string
	err     error
}

func (e *HostKeyFetchError) Error() string { return "destinations: fetch host key: " + e.Message }
func (e *HostKeyFetchError) Unwrap() error { return e.err }

// DB is what the service needs from the app database (*pgxpool.Pool).
type DB interface {
	db.TxBeginner
	sqlc.DBTX
}

// Options configure New.
type Options struct {
	DB      DB
	Keyring *crypto.Keyring
	Auditor *audit.Auditor
	// Guard is the SSRF guard every outbound connection (S3, SFTP, host-key
	// fetch) and every host check at save time goes through.
	Guard *netguard.Guard
	// DiskRoot is storage.disk_root.
	DiskRoot string
	// AllowHTTP permits http:// S3 endpoints (dev mode only).
	AllowHTTP bool
	// Limiter is the per-user limit of network operations (tests and
	// host-key fetches); nil: 10 per minute, burst 5.
	Limiter func(key string) (bool, time.Duration)
	// MaxConcurrent bounds concurrent network operations (default 4).
	MaxConcurrent int
	// OperationTimeout bounds one test or host-key fetch (default 60s).
	OperationTimeout time.Duration
	// Logger receives operational events (never secrets).
	Logger *slog.Logger
}

// Actor is the admin (or maintainer, for tests) performing an action.
type Actor struct {
	UserID uuid.UUID
	IP     netip.Addr
}

// Service manages destinations; see the package documentation.
type Service struct {
	o       Options
	limiter *serviceutil.Limiter
}

// New validates o and returns the service.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Keyring == nil || o.Auditor == nil || o.Guard == nil {
		return nil, errors.New("destinations: DB, Keyring, Auditor and Guard are required")
	}
	if o.MaxConcurrent <= 0 {
		o.MaxConcurrent = 4
	}
	if o.OperationTimeout <= 0 {
		o.OperationTimeout = 60 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	lim, err := serviceutil.NewLimiter(o.MaxConcurrent, o.Limiter)
	if err != nil {
		return nil, fmt.Errorf("destinations: %w", err)
	}
	return &Service{o: o, limiter: lim}, nil
}

// --- reading ---------------------------------------------------------------

// record is a decoded row.
type record struct {
	row  sqlc.Destination
	disk *DiskConfig
	s3   *S3Config
	sftp *SFTPConfig
}

func decode(row sqlc.Destination) (*record, error) {
	r := &record{row: row}
	var target any
	switch Type(row.Type) {
	case TypeDisk:
		r.disk = &DiskConfig{}
		target = r.disk
	case TypeS3:
		r.s3 = &S3Config{}
		target = r.s3
	case TypeSFTP:
		r.sftp = &SFTPConfig{}
		target = r.sftp
	default:
		return nil, fmt.Errorf("destinations: unknown type %q", row.Type)
	}
	if err := json.Unmarshal(row.Config, target); err != nil {
		return nil, fmt.Errorf("destinations: decode config of %s: %w", row.ID, err)
	}
	return r, nil
}

func (r *record) view() Destination {
	row := r.row
	d := Destination{
		ID: row.ID, Name: row.Name, Type: Type(row.Type), NoDelete: row.NoDelete,
		CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if row.ObjectLockMode != nil && row.ObjectLockRetentionDays != nil {
		d.ObjectLockMode = storage.LockMode(*row.ObjectLockMode)
		d.ObjectLockRetentionDays = int(*row.ObjectLockRetentionDays)
	}
	if row.HostKeyType != nil && row.HostKeyFingerprint != nil && row.HostKeyAuthorizedKey != nil && row.HostKeyConfirmedAt != nil {
		d.HostKey = &HostKey{
			Type: *row.HostKeyType, Fingerprint: *row.HostKeyFingerprint,
			AuthorizedKey: *row.HostKeyAuthorizedKey, ConfirmedAt: *row.HostKeyConfirmedAt,
		}
	}
	switch {
	case r.disk != nil:
		c := *r.disk
		d.Disk = &c
	case r.s3 != nil:
		c := *r.s3
		d.S3 = &c
		d.HasSecretAccessKey = len(row.SecretEnc) > 0
	case r.sftp != nil:
		c := *r.sftp
		d.SFTP = &c
		d.HasPassword = c.AuthMethod == AuthPassword && len(row.SecretEnc) > 0
		d.HasPrivateKey = c.AuthMethod == AuthPrivateKey && len(row.SecretEnc) > 0
		d.HasPassphrase = d.HasPrivateKey && c.HasPassphrase
	}
	d.NotReadyReason = notReadyReason(&d)
	d.Ready = d.NotReadyReason == ""
	return d
}

// notReadyReason says what is missing before d can be opened.
func notReadyReason(d *Destination) string {
	switch d.Type {
	case TypeS3:
		if !d.HasSecretAccessKey {
			return "the secret access key is missing"
		}
	case TypeSFTP:
		switch {
		case d.HostKey == nil && !d.HasPassword && !d.HasPrivateKey:
			return "confirm the server's host key and set a password or SSH key"
		case d.HostKey == nil:
			return "confirm the server's host key"
		case !d.HasPassword && !d.HasPrivateKey:
			return "set a password or SSH key"
		}
	case TypeDisk:
	}
	return ""
}

// List returns every destination that is not deleted, ordered by name.
func (s *Service) List(ctx context.Context) ([]Destination, error) {
	rows, err := sqlc.New(s.o.DB).ListDestinations(ctx)
	if err != nil {
		return nil, fmt.Errorf("destinations: list: %w", err)
	}
	out := make([]Destination, 0, len(rows))
	for i := range rows {
		r, err := decode(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, r.view())
	}
	return out, nil
}

// Get returns one destination (ErrNotFound when missing or deleted).
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Destination, error) {
	r, err := s.load(ctx, sqlc.New(s.o.DB), id, false)
	if err != nil {
		return Destination{}, err
	}
	return r.view(), nil
}

func (s *Service) load(ctx context.Context, q *sqlc.Queries, id uuid.UUID, lock bool) (*record, error) {
	var (
		row sqlc.Destination
		err error
	)
	if lock {
		row, err = q.GetDestinationForUpdate(ctx, id)
	} else {
		row, err = q.GetDestination(ctx, id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("destinations: load: %w", err)
	}
	return decode(row)
}

// --- writing ---------------------------------------------------------------

// state is the complete next state of a destination being saved.
type state struct {
	typ      Type
	name     string
	noDelete bool
	lockMode storage.LockMode
	lockDays int
	disk     *DiskConfig
	s3       *S3Config
	sftp     *SFTPConfig
	creds    *Credentials
	// credsChanged names the credential fields that were set or removed.
	credsChanged []string
	clearHostKey bool
	// reentered names the passwords entered again because the endpoint
	// changed (D-140).
	reentered []string
}

func (st *state) config() (json.RawMessage, error) {
	var v any
	switch st.typ {
	case TypeDisk:
		v = st.disk
	case TypeS3:
		v = st.s3
	case TypeSFTP:
		v = st.sftp
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("destinations: encode config: %w", err)
	}
	return b, nil
}

func (st *state) lockColumns() (*string, *int32) {
	if st.lockMode == storage.LockNone {
		return nil, nil
	}
	m := string(st.lockMode)
	d := int32(st.lockDays) //nolint:gosec // G115: validated 1..36500
	return &m, &d
}

// registerSecrets registers the credential values with the log redaction
// registry for the duration of a call.
func registerSecrets(c *Credentials) func() {
	var undo []func()
	for _, b := range [][]byte{c.SecretAccessKey, c.Password, c.PrivateKey, c.Passphrase} {
		if len(b) >= 4 {
			undo = append(undo, logging.DefaultRegistry.Register(b))
		}
	}
	return func() {
		for _, u := range undo {
			u()
		}
	}
}

// Create validates and stores a new destination. The secret fields of in
// are zeroed before Create returns.
func (s *Service) Create(ctx context.Context, actor Actor, in CreateInput) (Destination, error) {
	defer zeroInputs(in.S3, in.SFTP)
	name, err := normalizeName(in.Name)
	if err != nil {
		return Destination{}, err
	}
	if !in.Type.Valid() {
		return Destination{}, fieldErr("type", "unknown destination type")
	}
	if err := onlyInputFor(in.Type, in.Disk != nil, in.S3 != nil, in.SFTP != nil, true); err != nil {
		return Destination{}, err
	}
	st := &state{typ: in.Type, name: name, noDelete: in.NoDelete, creds: &Credentials{}}
	defer st.creds.Zero()
	if err := s.apply(ctx, st, nil, in.Disk, in.S3, in.SFTP); err != nil {
		return Destination{}, err
	}
	defer registerSecrets(st.creds)()

	id := uuid.New()
	sealed, err := sealCredentials(s.o.Keyring, id, st.creds)
	if err != nil {
		return Destination{}, err
	}
	cfg, err := st.config()
	if err != nil {
		return Destination{}, err
	}
	mode, days := st.lockColumns()
	var out *record
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		row, err := q.InsertDestination(ctx, sqlc.InsertDestinationParams{
			ID: id, Name: name, Type: sqlc.DestinationType(st.typ), Config: cfg, SecretEnc: sealed,
			NoDelete: st.noDelete, ObjectLockMode: mode, ObjectLockRetentionDays: days, CreatedBy: &actor.UserID,
		})
		if serviceutil.IsUniqueViolation(err) {
			return ErrNameTaken
		}
		if err != nil {
			return fmt.Errorf("destinations: insert: %w", err)
		}
		if out, err = decode(row); err != nil {
			return err
		}
		details := audit.Details{
			"name": name, "type": string(st.typ), "no_delete": st.noDelete,
			"object_lock_mode": string(st.lockMode), "object_lock_retention_days": st.lockDays,
			"settings": auditSettings(st), "credentials_set": st.credsChanged,
		}
		return s.record(ctx, q, actor, audit.ActionDestinationCreated, id, details, st.credsChanged)
	})
	if err != nil {
		return Destination{}, err
	}
	return out.view(), nil
}

// Update validates and applies in. The secret fields of in are zeroed
// before Update returns. Validation (including DNS checks) happens before
// the row is locked; the update fails with ErrConflict when the row changed
// in between.
func (s *Service) Update(ctx context.Context, actor Actor, id uuid.UUID, in UpdateInput) (Destination, error) {
	defer zeroInputs(in.S3, in.SFTP)
	cur, err := s.load(ctx, sqlc.New(s.o.DB), id, false)
	if err != nil {
		return Destination{}, err
	}
	typ := Type(cur.row.Type)
	if err := onlyInputFor(typ, in.Disk != nil, in.S3 != nil, in.SFTP != nil, false); err != nil {
		return Destination{}, err
	}
	creds, err := openCredentials(s.o.Keyring, id, cur.row.SecretEnc)
	if err != nil {
		return Destination{}, err
	}
	st := &state{typ: typ, name: cur.row.Name, noDelete: cur.row.NoDelete, creds: creds, disk: cur.disk, s3: cur.s3, sftp: cur.sftp}
	defer st.creds.Zero()
	if cur.row.ObjectLockMode != nil && cur.row.ObjectLockRetentionDays != nil {
		st.lockMode, st.lockDays = storage.LockMode(*cur.row.ObjectLockMode), int(*cur.row.ObjectLockRetentionDays)
	}
	if in.Name != nil {
		if st.name, err = normalizeName(*in.Name); err != nil {
			return Destination{}, err
		}
	}
	if in.NoDelete != nil {
		st.noDelete = *in.NoDelete
	}
	if err := s.apply(ctx, st, cur, in.Disk, in.S3, in.SFTP); err != nil {
		return Destination{}, err
	}
	defer registerSecrets(st.creds)()

	changed, values := diff(cur, st)
	if len(changed) == 0 {
		return cur.view(), nil
	}
	sealed := cur.row.SecretEnc
	if len(st.credsChanged) > 0 {
		if sealed, err = sealCredentials(s.o.Keyring, id, st.creds); err != nil {
			return Destination{}, err
		}
	}
	cfg, err := st.config()
	if err != nil {
		return Destination{}, err
	}
	mode, days := st.lockColumns()
	var out *record
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		locked, err := s.load(ctx, q, id, true)
		if err != nil {
			return err
		}
		if !locked.row.UpdatedAt.Equal(cur.row.UpdatedAt) {
			return ErrConflict
		}
		row, err := q.UpdateDestination(ctx, sqlc.UpdateDestinationParams{
			ID: id, Name: st.name, Config: cfg, SecretEnc: sealed, NoDelete: st.noDelete,
			ObjectLockMode: mode, ObjectLockRetentionDays: days, ClearHostKey: st.clearHostKey,
		})
		if serviceutil.IsUniqueViolation(err) {
			return ErrNameTaken
		}
		if err != nil {
			return fmt.Errorf("destinations: update: %w", err)
		}
		if out, err = decode(row); err != nil {
			return err
		}
		details := audit.Details{"name": st.name, "type": string(typ), "changed": changed, "values": values}
		if st.clearHostKey && cur.row.HostKeyFingerprint != nil {
			details["host_key_cleared"] = true
		}
		if len(st.reentered) > 0 {
			// Sec M3: the endpoint changed, so the stored password was
			// not reused; the admin entered it again (or set a key).
			details["reentered_for_new_endpoint"] = st.reentered
		}
		return s.record(ctx, q, actor, audit.ActionDestinationUpdated, id, details, st.credsChanged)
	})
	if err != nil {
		return Destination{}, err
	}
	return out.view(), nil
}

// onlyInputFor checks that only the input of typ is set (and, when
// required, that it is set).
func onlyInputFor(typ Type, disk, s3, sftpSet, required bool) error {
	set := map[Type]bool{TypeDisk: disk, TypeS3: s3, TypeSFTP: sftpSet}
	for t, ok := range set {
		if ok && t != typ {
			return fieldErr(string(t), "settings for %s do not match the destination type %s", t, typ)
		}
	}
	if required && !set[typ] {
		return fieldErr(string(typ), "the %s settings are required", typ)
	}
	return nil
}

// apply validates the type-specific input (if any) into st. cur is the
// stored destination for updates (nil when creating).
func (s *Service) apply(ctx context.Context, st *state, cur *record, disk *DiskInput, s3in *S3Input, sftpIn *SFTPInput) error {
	switch {
	case disk != nil:
		c, err := validateDisk(disk)
		if err != nil {
			return err
		}
		st.disk = &c
	case s3in != nil:
		if err := s.applyS3(ctx, st, s3in); err != nil {
			return err
		}
	case sftpIn != nil:
		var prev *SFTPConfig
		if cur != nil {
			prev = cur.sftp
		}
		if err := s.applySFTP(ctx, st, prev, sftpIn); err != nil {
			return err
		}
	}
	if st.typ != TypeS3 && st.lockMode != storage.LockNone {
		return fieldErr("objectLockMode", "Object Lock is only available for S3 destinations")
	}
	return nil
}

func (s *Service) applyS3(ctx context.Context, st *state, in *S3Input) error {
	c, err := s3ConfigOf(in)
	if err != nil {
		return err
	}
	if in.SecretAccessKey != nil {
		if len(in.SecretAccessKey) == 0 {
			return fieldErr("s3.secretAccessKey", "the secret access key must not be empty")
		}
		crypto.Zero(st.creds.SecretAccessKey)
		st.creds.SecretAccessKey = append([]byte(nil), in.SecretAccessKey...)
		st.credsChanged = append(st.credsChanged, "secret_access_key")
	}
	if len(st.creds.SecretAccessKey) == 0 {
		return fieldErr("s3.secretAccessKey", "the secret access key is required")
	}
	if !in.ObjectLockMode.Valid() {
		return fieldErr("s3.objectLockMode", "unknown Object Lock mode")
	}
	cfg := s.s3Config(&c, st.creds, in.ObjectLockMode, in.ObjectLockRetentionDays, st.noDelete)
	err = cfg.Validate()
	cfg.SecretAccessKey.Destroy()
	if err != nil {
		return fieldErr("s3", "%s", s3Message(err))
	}
	if c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if err != nil {
			return fieldErr("s3.endpoint", "invalid endpoint URL")
		}
		if err := s.checkHost(ctx, u.Hostname()); err != nil {
			return fieldErr("s3.endpoint", "%s", err.Error())
		}
	}
	st.s3 = &c
	st.lockMode, st.lockDays = in.ObjectLockMode, in.ObjectLockRetentionDays
	if st.lockMode == storage.LockNone {
		st.lockDays = 0
	}
	return nil
}

// s3Message strips the sentinel prefix of an s3store validation error and
// joins the individual problems.
func s3Message(err error) string {
	msg := err.Error()
	if _, rest, ok := strings.Cut(msg, "s3 destination: "); ok {
		msg = rest
	}
	return strings.ReplaceAll(msg, "\n", "; ")
}

func (s *Service) applySFTP(ctx context.Context, st *state, prev *SFTPConfig, in *SFTPInput) error {
	c, err := sftpConfigOf(in)
	if err != nil {
		return err
	}
	if prev != nil {
		c.AuthMethod, c.HasPassphrase, c.PublicKey = prev.AuthMethod, prev.HasPassphrase, prev.PublicKey
	}
	if err := sftp.ValidateTarget(c.Host, c.Port, c.User, c.BasePath, c.Dir); err != nil {
		return fieldErr("sftp", "%s", sftpMessage(err))
	}
	// D-140: a stored password is never sent to a new endpoint. When the
	// host, port, user or base path changes, a stored password must be
	// entered again (or replaced by a key) in the same update; a private
	// key may stay (it does not reveal the secret to the server).
	if prev != nil && prev.AuthMethod == AuthPassword && len(st.creds.Password) > 0 &&
		(prev.Host != c.Host || prev.Port != c.Port || prev.User != c.User || prev.BasePath != c.BasePath) {
		if in.Password == nil && in.PrivateKey == nil {
			return fieldErr("sftp.password", "the server, user or base path changed: enter the password again (or set a private key); a stored password is never sent to a new endpoint")
		}
		st.reentered = append(st.reentered, "password")
	}
	switch {
	case in.Password != nil:
		if len(in.Password) == 0 {
			return fieldErr("sftp.password", "the password must not be empty")
		}
		if len(st.creds.PrivateKey) > 0 {
			st.credsChanged = append(st.credsChanged, "private_key")
		}
		st.creds.Zero()
		st.creds.Password = append([]byte(nil), in.Password...)
		st.credsChanged = append(st.credsChanged, "password")
		c.AuthMethod, c.HasPassphrase, c.PublicKey = AuthPassword, false, ""
	case in.PrivateKey != nil:
		if len(in.PrivateKey) == 0 {
			return fieldErr("sftp.privateKey", "the private key must not be empty")
		}
		line, err := checkPrivateKey(in.PrivateKey, in.Passphrase)
		if err != nil {
			return err
		}
		if len(st.creds.Password) > 0 {
			st.credsChanged = append(st.credsChanged, "password")
		}
		st.creds.Zero()
		st.creds.PrivateKey = append([]byte(nil), in.PrivateKey...)
		st.credsChanged = append(st.credsChanged, "private_key")
		if len(in.Passphrase) > 0 {
			st.creds.Passphrase = append([]byte(nil), in.Passphrase...)
			st.credsChanged = append(st.credsChanged, "private_key_passphrase")
		}
		c.AuthMethod, c.HasPassphrase, c.PublicKey = AuthPrivateKey, len(in.Passphrase) > 0, line
	}
	if err := s.checkHost(ctx, c.Host); err != nil {
		return fieldErr("sftp.host", "%s", err.Error())
	}
	if prev != nil && (prev.Host != c.Host || prev.Port != c.Port) {
		st.clearHostKey = true
	}
	st.sftp = &c
	return nil
}

// sftpMessage strips the sentinel prefix of an sftp validation error.
func sftpMessage(err error) string {
	if _, rest, ok := strings.Cut(err.Error(), "invalid sftp configuration: "); ok {
		return rest
	}
	if _, rest, ok := strings.Cut(err.Error(), "sftp directory: "); ok {
		return "directory: " + rest
	}
	return "invalid settings"
}

// checkHost resolves host and checks every address against the SSRF
// guard (the dial-time check still applies later). The messages are for
// admins (D-120: the blocked address and reason are admin information).
func (s *Service) checkHost(ctx context.Context, host string) error {
	if _, err := s.o.Guard.ResolveAndCheck(ctx, host); err != nil {
		var blocked *netguard.BlockedAddressError
		switch {
		case errors.As(err, &blocked):
			return fmt.Errorf("the host resolves to %s, which is not allowed (%s); private addresses must be on the SSRF allowlist", blocked.IP, blocked.Reason)
		case errors.Is(err, context.Canceled):
			return err
		default:
			return errors.New("the host cannot be resolved")
		}
	}
	return nil
}

// Delete soft deletes a destination (its row stays for artifacts, jobs and
// the audit log; the name can be reused).
func (s *Service) Delete(ctx context.Context, actor Actor, id uuid.UUID) error {
	return db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		r, err := s.load(ctx, q, id, true)
		if err != nil {
			return err
		}
		if err := s.checkUnused(ctx, q, id); err != nil {
			return err
		}
		n, err := q.SoftDeleteDestination(ctx, id)
		if err != nil {
			return fmt.Errorf("destinations: delete: %w", err)
		}
		if n != 1 {
			return ErrNotFound
		}
		return s.record(ctx, q, actor, audit.ActionDestinationDeleted, id,
			audit.Details{"name": r.row.Name, "type": string(r.row.Type)}, nil)
	})
}

// checkUnused is the hook that refuses deleting a destination that is
// still in use. Jobs (task 3.5, job_destinations) must refuse it while an
// enabled job targets the destination; artifacts (tasks 3.6+) keep
// referencing a soft-deleted row, so they never block the soft delete but
// retention/restore must still be able to open it. Until those tables
// exist nothing references destinations.
func (s *Service) checkUnused(context.Context, *sqlc.Queries, uuid.UUID) error {
	return nil
}

// record writes one audit entry, plus a secret.changed entry when
// credential fields changed.
func (s *Service) record(ctx context.Context, q *sqlc.Queries, actor Actor, action audit.Action, id uuid.UUID, details audit.Details, credsChanged []string) error {
	ev := audit.Event{
		Actor: audit.UserActor(actor.UserID), Action: action, TargetType: "destination",
		TargetID: id.String(), IP: actor.IP, Details: details,
	}
	if _, err := s.o.Auditor.RecordTx(ctx, q, ev); err != nil {
		return err
	}
	if len(credsChanged) == 0 {
		return nil
	}
	ev.Action = audit.ActionSecretChanged
	ev.Details = audit.Details{"fields": credsChanged, "via": string(action)}
	_, err := s.o.Auditor.RecordTx(ctx, q, ev)
	return err
}

// auditSettings returns the non-secret settings of st for the audit log
// (the CA bundle and public key only as a flag).
func auditSettings(st *state) map[string]any {
	m := configMap(st.disk, st.s3, st.sftp)
	for _, k := range []string{"ca_bundle_pem", "public_key"} {
		if v, ok := m[k]; ok {
			m[k] = v != ""
		}
	}
	return m
}

func configMap(disk *DiskConfig, s3c *S3Config, sftpc *SFTPConfig) map[string]any {
	var v any
	switch {
	case disk != nil:
		v = disk
	case s3c != nil:
		v = s3c
	case sftpc != nil:
		v = sftpc
	default:
		return map[string]any{}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{}
	}
	return m
}

// diff lists the changed fields between the stored destination and st,
// with old/new values for non-secret, short fields. Credential fields are
// listed by name only.
func diff(cur *record, st *state) ([]string, map[string]any) {
	var changed []string
	values := map[string]any{}
	add := func(field string, oldV, newV any, withValues bool) {
		changed = append(changed, field)
		if withValues {
			values[field] = map[string]any{"old": oldV, "new": newV}
		}
	}
	if cur.row.Name != st.name {
		add("name", cur.row.Name, st.name, true)
	}
	if cur.row.NoDelete != st.noDelete {
		add("no_delete", cur.row.NoDelete, st.noDelete, true)
	}
	var curMode string
	var curDays int
	if cur.row.ObjectLockMode != nil && cur.row.ObjectLockRetentionDays != nil {
		curMode, curDays = *cur.row.ObjectLockMode, int(*cur.row.ObjectLockRetentionDays)
	}
	if curMode != string(st.lockMode) {
		add("object_lock_mode", curMode, string(st.lockMode), true)
	}
	if curDays != st.lockDays {
		add("object_lock_retention_days", curDays, st.lockDays, true)
	}
	oldM := configMap(cur.disk, cur.s3, cur.sftp)
	newM := configMap(st.disk, st.s3, st.sftp)
	keys := make([]string, 0, len(oldM)+len(newM))
	for k := range oldM {
		keys = append(keys, k)
	}
	for k := range newM {
		if _, ok := oldM[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		o, n := oldM[k], newM[k]
		if fmt.Sprint(o) == fmt.Sprint(n) {
			continue
		}
		add(k, o, n, k != "ca_bundle_pem" && k != "public_key")
	}
	changed = append(changed, st.credsChanged...)
	if st.clearHostKey && cur.row.HostKeyFingerprint != nil {
		changed = append(changed, "host_key")
	}
	return changed, values
}
