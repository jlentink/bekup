package connections

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// Errors of the service. Presentable messages live in the API layer.
var (
	ErrNotFound  = errors.New("connections: connection not found")
	ErrNameTaken = errors.New("connections: another connection already uses this name")
	// ErrConflict: the connection changed while an update was prepared.
	ErrConflict = errors.New("connections: the connection was changed concurrently")
	// ErrBusy: too many runner operations run at the same time (shared
	// with internal/destinations).
	ErrBusy = serviceutil.ErrBusy
	// ErrInUse: the connection is still used and cannot be deleted.
	ErrInUse = errors.New("connections: the connection is in use")
	// ErrNoSSH: the operation needs an SSH tunnel.
	ErrNoSSH = errors.New("connections: the connection has no SSH tunnel")
	// ErrRunnerUnavailable: the runner could not be reached.
	ErrRunnerUnavailable = errors.New("connections: the runner is unavailable")
)

// NotReadyError reports that the connection cannot be used yet; Reason is
// presentable.
type NotReadyError struct{ Reason string }

func (e *NotReadyError) Error() string { return "connections: not ready: " + e.Reason }

// RateLimitedError reports that the caller exceeded the per-user limit of
// runner operations.
type RateLimitedError = serviceutil.RateLimitedError

// DB is what the service needs from the app database (*pgxpool.Pool).
type DB interface {
	db.TxBeginner
	sqlc.DBTX
}

// Runner is the part of the runner API the service uses
// (runnerpb.RunnerServiceClient; a fake in unit tests).
type Runner = runnerpb.RunnerServiceClient

// Options configure New.
type Options struct {
	DB      DB
	Keyring *crypto.Keyring
	Auditor *audit.Auditor
	// Runner is the mTLS runner client (internal/runnerclient). Every
	// probe, certificate and host-key capture goes through it; serve never
	// connects to a target itself.
	Runner Runner
	// Catalog is the image catalog the engine/version/image keys are
	// checked against (default: the embedded catalog).
	Catalog *engine.Catalog
	// Alerts receives security events (pin mismatch, host-key mismatch,
	// TLS downgrade); nil logs them. Task 5.5 turns them into alerts.
	Alerts AlertFunc
	// Limiter is the per-user limit of runner operations; nil: 10 per
	// minute, burst 5.
	Limiter func(key string) (bool, time.Duration)
	// MaxConcurrent bounds concurrent runner operations (default 4).
	MaxConcurrent int
	// OperationTimeout bounds one runner operation (default 60s).
	OperationTimeout time.Duration
	// Now overrides the clock (tests).
	Now func() time.Time
	// Logger receives operational events (never secrets).
	Logger *slog.Logger
}

// Actor is the admin (or maintainer, for tests) performing an action.
type Actor struct {
	UserID uuid.UUID
	IP     netip.Addr
}

// Service manages connections; see the package documentation.
type Service struct {
	o       Options
	limiter *serviceutil.Limiter
}

// New validates o and returns the service.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Keyring == nil || o.Auditor == nil || o.Runner == nil {
		return nil, errors.New("connections: DB, Keyring, Auditor and Runner are required")
	}
	if o.Catalog == nil {
		cat, err := engine.Embedded()
		if err != nil {
			return nil, fmt.Errorf("connections: %w", err)
		}
		o.Catalog = cat
	}
	if o.MaxConcurrent <= 0 {
		o.MaxConcurrent = 4
	}
	if o.OperationTimeout <= 0 {
		o.OperationTimeout = 60 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	lim, err := serviceutil.NewLimiter(o.MaxConcurrent, o.Limiter)
	if err != nil {
		return nil, fmt.Errorf("connections: %w", err)
	}
	s := &Service{o: o, limiter: lim}
	if s.o.Alerts == nil {
		s.o.Alerts = logAlert(o.Logger)
	}
	return s, nil
}

// Catalog returns the image catalog the keys are checked against.
func (s *Service) Catalog() *engine.Catalog { return s.o.Catalog }

// --- reading ---------------------------------------------------------------

func pinOf(certPEM *string, spki []byte, notAfter, pinnedAt *time.Time) *Pin {
	if certPEM == nil || notAfter == nil || pinnedAt == nil {
		return nil
	}
	p := &Pin{CertificatePEM: *certPEM, NotAfter: *notAfter, PinnedAt: *pinnedAt}
	if len(spki) > 0 {
		p.LeafSPKI = append([]byte(nil), spki...)
	}
	if b, _ := pem.Decode([]byte(*certPEM)); b != nil {
		if c, err := x509.ParseCertificate(b.Bytes); err == nil {
			p.Subject, p.Issuer, p.IsCA = c.Subject.String(), c.Issuer.String(), c.IsCA
		}
	}
	return p
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// view converts a row (never credentials).
func (s *Service) view(row *sqlc.Connection) Connection {
	c := Connection{
		ID: row.ID, Name: row.Name, Engine: engine.Kind(row.Engine), VersionKey: row.VersionKey, ImageKey: row.ImageKey,
		Host: row.Host, Port: int(row.Port), Username: row.Username, HasPassword: row.HasPassword,
		ClientCertPEM: deref(row.ClientCertPem), TLSMode: TLSMode(row.TlsMode),
		CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	for _, k := range []string{row.VersionKey, row.ImageKey} {
		if e, ok := s.o.Catalog.Lookup(c.Engine, k); ok && e.Legacy() {
			c.Legacy = true
		}
	}
	c.InsecureTransport = c.TLSMode != TLSPinned
	c.Pin = pinOf(row.TlsPinCertPem, row.TlsPinSpki, row.TlsPinNotAfter, row.TlsPinnedAt)
	c.BackupPin = pinOf(row.TlsBackupPinCertPem, row.TlsBackupPinSpki, row.TlsBackupPinNotAfter, row.TlsBackupPinnedAt)
	if c.Pin != nil {
		at := c.Pin.NotAfter
		c.PinExpiresAt = &at
	}
	c.PinStatus = pinStatus(c.TLSMode, c.Pin, s.o.Now())
	if row.SshEnabled {
		sh := &SSH{
			Host: deref(row.SshHost), Port: int(deref(row.SshPort)), User: deref(row.SshUser),
			Auth: SSHAuth(row.SshAuth), HasPassphrase: row.SshHasPassphrase, PublicKey: deref(row.SshPublicKey),
		}
		if row.SshHostKeyType != nil && row.SshHostKeyFingerprint != nil && row.SshHostKeyAuthorizedKey != nil && row.SshHostKeyConfirmedAt != nil {
			sh.HostKey = &HostKey{
				Type: *row.SshHostKeyType, Fingerprint: *row.SshHostKeyFingerprint,
				AuthorizedKey: *row.SshHostKeyAuthorizedKey, ConfirmedAt: *row.SshHostKeyConfirmedAt,
			}
		}
		c.SSH = sh
	}
	c.NotReadyReason = notReadyReason(&c)
	c.Ready = c.NotReadyReason == ""
	return c
}

func pinStatus(mode TLSMode, p *Pin, now time.Time) PinStatus {
	switch {
	case mode != TLSPinned:
		return PinNotRequired
	case p == nil:
		return PinMissing
	case !now.Before(p.NotAfter):
		return PinExpired
	case now.Add(7 * 24 * time.Hour).After(p.NotAfter):
		return PinExpires7d
	case now.Add(30 * 24 * time.Hour).After(p.NotAfter):
		return PinExpires30d
	default:
		return PinValid
	}
}

// notReadyReason says what is missing before c can be used.
func notReadyReason(c *Connection) string {
	var missing []string
	if c.TLSMode == TLSPinned && c.Pin == nil {
		missing = append(missing, "fetch and confirm the server's TLS certificate")
	}
	if c.SSH != nil {
		if c.SSH.HostKey == nil {
			missing = append(missing, "fetch and confirm the SSH bastion's host key")
		}
		if c.SSH.Auth == SSHAuthNone {
			missing = append(missing, "set an SSH password or key")
		}
	}
	return strings.Join(missing, "; ")
}

// List returns every connection that is not deleted, ordered by name.
func (s *Service) List(ctx context.Context) ([]Connection, error) {
	rows, err := sqlc.New(s.o.DB).ListConnections(ctx)
	if err != nil {
		return nil, fmt.Errorf("connections: list: %w", err)
	}
	out := make([]Connection, 0, len(rows))
	for i := range rows {
		out = append(out, s.view(&rows[i]))
	}
	return out, nil
}

// Get returns one connection (ErrNotFound when missing or deleted).
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Connection, error) {
	row, err := s.load(ctx, sqlc.New(s.o.DB), id, false)
	if err != nil {
		return Connection{}, err
	}
	return s.view(&row), nil
}

func (s *Service) load(ctx context.Context, q *sqlc.Queries, id uuid.UUID, lock bool) (sqlc.Connection, error) {
	var (
		row sqlc.Connection
		err error
	)
	if lock {
		row, err = q.GetConnectionForUpdate(ctx, id)
	} else {
		row, err = q.GetConnection(ctx, id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Connection{}, ErrNotFound
	}
	if err != nil {
		return sqlc.Connection{}, fmt.Errorf("connections: load: %w", err)
	}
	return row, nil
}

// --- writing ---------------------------------------------------------------

// state is the complete next state of a connection being saved.
type state struct {
	name          string
	engine        engine.Kind
	versionKey    string
	imageKey      string
	host          string
	port          int
	username      string
	tlsMode       TLSMode
	clientCertPEM string
	ssh           bool
	sshHost       string
	sshPort       int
	sshUser       string
	sshAuth       SSHAuth
	sshPass       bool
	sshPublicKey  string
	creds         *Credentials
	// credsChanged names the credential fields that were set or removed.
	credsChanged []string
	clearPins    bool
	clearHostKey bool
}

func stateOf(row *sqlc.Connection, creds *Credentials) *state {
	return &state{
		name: row.Name, engine: engine.Kind(row.Engine), versionKey: row.VersionKey, imageKey: row.ImageKey,
		host: row.Host, port: int(row.Port), username: row.Username, tlsMode: TLSMode(row.TlsMode),
		clientCertPEM: deref(row.ClientCertPem), ssh: row.SshEnabled, sshHost: deref(row.SshHost),
		sshPort: int(deref(row.SshPort)), sshUser: deref(row.SshUser), sshAuth: SSHAuth(row.SshAuth),
		sshPass: row.SshHasPassphrase, sshPublicKey: deref(row.SshPublicKey), creds: creds,
	}
}

func (st *state) changedCred(field string) {
	for _, f := range st.credsChanged {
		if f == field {
			return
		}
	}
	st.credsChanged = append(st.credsChanged, field)
}

// validate checks the whole next state.
func (s *Service) validate(st *state) error {
	if !st.engine.Valid() {
		return fieldErr("engine", "unknown engine")
	}
	vEntry, ok := s.o.Catalog.Lookup(st.engine, st.versionKey)
	if !ok {
		return fieldErr("versionKey", "%q is not a %s version of the image catalog", st.versionKey, st.engine)
	}
	iEntry, ok := s.o.Catalog.Lookup(st.engine, st.imageKey)
	if !ok {
		return fieldErr("imageKey", "%q is not a %s client image of the image catalog", st.imageKey, st.engine)
	}
	// PostgreSQL clients cannot dump a newer server (engine.CheckDumpCompat).
	if st.engine == engine.KindPostgres && iEntry.ClientVersion().Major < vEntry.ClientVersion().Major {
		return fieldErr("imageKey", "a PostgreSQL %s client cannot back up a PostgreSQL %s server; choose a client image of the same or a newer major version",
			iEntry.Key(), vEntry.Key())
	}
	if err := engine.ValidateHost(st.host); err != nil {
		return fieldErr("host", "invalid host name or IP address")
	}
	if err := engine.ValidatePort(st.port); err != nil {
		return fieldErr("port", "port out of range")
	}
	if err := engine.ValidateUser(st.username); err != nil {
		return fieldErr("username", "invalid user name")
	}
	if !st.tlsMode.Valid() {
		return fieldErr("tlsMode", "unknown TLS mode")
	}
	if st.clientCertPEM != "" && st.tlsMode == TLSDisabled {
		return fieldErr("clientCertificatePem", "a TLS client certificate needs TLS; remove it before disabling TLS")
	}
	if st.ssh {
		if err := engine.ValidateHost(st.sshHost); err != nil {
			return fieldErr("ssh.host", "invalid host name or IP address")
		}
		if st.sshPort < 1 || st.sshPort > 65535 {
			return fieldErr("ssh.port", "port out of range")
		}
		if err := checkSSHUser(st.sshUser); err != nil {
			return err
		}
	}
	return checkIPLiteralRule(st.tlsMode, st.host, st.ssh)
}

// applySSH applies an SSH input to st (the whole state is validated
// later). A changed bastion host or port removes the host-key pin.
func applySSH(st *state, in *SSHInput) error {
	host, port, user := strings.TrimSpace(in.Host), in.Port, strings.TrimSpace(in.User)
	if port == 0 {
		port = defaultSSHPort
	}
	if st.ssh && (st.sshHost != host || st.sshPort != port) {
		st.clearHostKey = true
	}
	st.ssh, st.sshHost, st.sshPort, st.sshUser = true, host, port, user
	switch {
	case in.Password != nil && in.PrivateKey != nil:
		return fieldErr("ssh", "set either an SSH password or a private key")
	case in.Passphrase != nil && in.PrivateKey == nil:
		return fieldErr("ssh.privateKeyPassphrase", "a passphrase needs a private key")
	case in.Password != nil:
		if err := checkPassword("ssh.password", in.Password); err != nil {
			return err
		}
		if len(st.creds.SSHPrivateKey) > 0 {
			st.changedCred("ssh_private_key")
		}
		if len(st.creds.SSHPassphrase) > 0 {
			st.changedCred("ssh_private_key_passphrase")
		}
		st.creds.clearSSH()
		st.creds.SSHPassword = append([]byte(nil), in.Password...)
		st.changedCred("ssh_password")
		st.sshAuth, st.sshPass, st.sshPublicKey = SSHAuthPassword, false, ""
	case in.PrivateKey != nil:
		if len(in.PrivateKey) == 0 {
			return fieldErr("ssh.privateKey", "the private key must not be empty")
		}
		line, err := checkSSHPrivateKey(in.PrivateKey, in.Passphrase)
		if err != nil {
			return err
		}
		if len(st.creds.SSHPassword) > 0 {
			st.changedCred("ssh_password")
		}
		if len(st.creds.SSHPassphrase) > 0 && len(in.Passphrase) == 0 {
			st.changedCred("ssh_private_key_passphrase")
		}
		st.creds.clearSSH()
		st.creds.SSHPrivateKey = append([]byte(nil), in.PrivateKey...)
		st.changedCred("ssh_private_key")
		if len(in.Passphrase) > 0 {
			st.creds.SSHPassphrase = append([]byte(nil), in.Passphrase...)
			st.changedCred("ssh_private_key_passphrase")
		}
		st.sshAuth, st.sshPass, st.sshPublicKey = SSHAuthPrivateKey, len(in.Passphrase) > 0, line
	}
	return nil
}

// disableSSH removes the tunnel with its credentials and host-key pin.
func disableSSH(st *state) {
	if !st.ssh {
		return
	}
	for name, b := range map[string][]byte{
		"ssh_password": st.creds.SSHPassword, "ssh_private_key": st.creds.SSHPrivateKey,
		"ssh_private_key_passphrase": st.creds.SSHPassphrase,
	} {
		if len(b) > 0 {
			st.changedCred(name)
		}
	}
	st.creds.clearSSH()
	st.ssh, st.sshHost, st.sshPort, st.sshUser = false, "", 0, ""
	st.sshAuth, st.sshPass, st.sshPublicKey = SSHAuthNone, false, ""
	st.clearHostKey = true
}

// registerSecrets registers the credential values with the log redaction
// registry for the duration of a call.
func registerSecrets(c *Credentials) func() {
	var undo []func()
	for _, b := range c.fields() {
		if len(*b) >= 4 {
			undo = append(undo, logging.DefaultRegistry.Register(*b))
		}
	}
	return func() {
		for _, u := range undo {
			u()
		}
	}
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func optInt32(n int, set bool) *int32 {
	if !set {
		return nil
	}
	v := int32(n) //nolint:gosec // G115: ports are validated 1..65535
	return &v
}

// Create validates and stores a new connection. The secret fields of in
// are zeroed before Create returns. A TLS mode other than pinned is an
// opt-out: it is audited as security.opt_out and raises an alert.
func (s *Service) Create(ctx context.Context, actor Actor, in CreateInput) (Connection, error) {
	defer crypto.Zero(in.Password)
	defer crypto.Zero(in.ClientKeyPEM)
	defer zeroSSHInput(in.SSH)
	name, err := normalizeName(in.Name)
	if err != nil {
		return Connection{}, err
	}
	st := &state{
		name: name, engine: in.Engine, versionKey: strings.TrimSpace(in.VersionKey), imageKey: strings.TrimSpace(in.ImageKey),
		host: strings.TrimSpace(in.Host), port: in.Port, username: strings.TrimSpace(in.Username), tlsMode: in.TLSMode,
		sshAuth: SSHAuthNone, creds: &Credentials{},
	}
	defer st.creds.Zero()
	if st.imageKey == "" {
		st.imageKey = st.versionKey
	}
	if st.port == 0 {
		st.port = defaultPort(st.engine)
	}
	if st.tlsMode == "" {
		st.tlsMode = TLSPinned
	}
	if in.Password != nil {
		if err := checkPassword("password", in.Password); err != nil {
			return Connection{}, err
		}
		st.creds.Password = append([]byte(nil), in.Password...)
		st.changedCred("password")
	}
	if in.ClientCertPEM != "" || in.ClientKeyPEM != nil {
		cert, err := checkClientCertificate(in.ClientCertPEM, in.ClientKeyPEM)
		if err != nil {
			return Connection{}, err
		}
		st.clientCertPEM = cert
		st.creds.ClientKey = append([]byte(nil), in.ClientKeyPEM...)
		st.changedCred("client_key")
	}
	if in.SSH != nil {
		if err := applySSH(st, in.SSH); err != nil {
			return Connection{}, err
		}
	}
	if err := s.validate(st); err != nil {
		return Connection{}, err
	}
	defer registerSecrets(st.creds)()

	id := uuid.New()
	sealed, err := sealCredentials(s.o.Keyring, id, st.creds)
	if err != nil {
		return Connection{}, err
	}
	var out sqlc.Connection
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		row, err := q.InsertConnection(ctx, sqlc.InsertConnectionParams{
			ID: id, Name: st.name, Engine: sqlc.ConnectionEngine(st.engine), VersionKey: st.versionKey, ImageKey: st.imageKey,
			Host: st.host, Port: int32(st.port), Username: st.username, HasPassword: len(st.creds.Password) > 0, //nolint:gosec // G115: validated port
			ClientCertPem: optString(st.clientCertPEM), TlsMode: sqlc.ConnectionTlsMode(st.tlsMode),
			SshEnabled: st.ssh, SshHost: optString(st.sshHost), SshPort: optInt32(st.sshPort, st.ssh), SshUser: optString(st.sshUser),
			SshAuth: sqlc.ConnectionSshAuth(st.sshAuth), SshHasPassphrase: st.sshPass, SshPublicKey: optString(st.sshPublicKey),
			SecretEnc: sealed, CreatedBy: &actor.UserID,
		})
		if serviceutil.IsUniqueViolation(err) {
			return ErrNameTaken
		}
		if err != nil {
			return fmt.Errorf("connections: insert: %w", err)
		}
		out = row
		details := audit.Details{"name": st.name, "settings": auditSettings(st), "credentials_set": st.credsChanged}
		if err := s.record(ctx, q, actor, audit.ActionConnectionCreated, id, details, st.credsChanged); err != nil {
			return err
		}
		if st.tlsMode != TLSPinned {
			return s.recordOptOut(ctx, q, actor, id, st.name, TLSPinned, st.tlsMode)
		}
		return nil
	})
	if err != nil {
		return Connection{}, err
	}
	if st.tlsMode != TLSPinned {
		s.o.Alerts(ctx, Alert{Kind: AlertTLSDowngrade, ConnectionID: id, ConnectionName: st.name,
			Details: map[string]any{"from": string(TLSPinned), "to": string(st.tlsMode)}})
	}
	return s.view(&out), nil
}

// Update validates and applies in. The secret fields of in are zeroed
// before Update returns. Validation happens before the row is locked; the
// update fails with ErrConflict when the row changed in between. Changing
// the host or port removes both certificate pins; changing the bastion's
// host or port (or removing the tunnel) removes its host-key pin.
func (s *Service) Update(ctx context.Context, actor Actor, id uuid.UUID, in UpdateInput) (Connection, error) {
	defer crypto.Zero(in.Password)
	defer crypto.Zero(in.ClientKeyPEM)
	defer zeroSSHInput(in.SSH)
	cur, err := s.load(ctx, sqlc.New(s.o.DB), id, false)
	if err != nil {
		return Connection{}, err
	}
	creds, err := openCredentials(s.o.Keyring, id, cur.SecretEnc)
	if err != nil {
		return Connection{}, err
	}
	st := stateOf(&cur, creds)
	defer st.creds.Zero()
	if err := applyUpdate(st, &in); err != nil {
		return Connection{}, err
	}
	if st.host != cur.Host || st.port != int(cur.Port) {
		st.clearPins = true
	}
	reentered, err := requireReentry(&cur, st, &in)
	if err != nil {
		return Connection{}, err
	}
	if err := s.validate(st); err != nil {
		return Connection{}, err
	}
	defer registerSecrets(st.creds)()

	changed, values := diff(&cur, st)
	if len(changed) == 0 {
		return s.view(&cur), nil
	}
	sealed := cur.SecretEnc
	if len(st.credsChanged) > 0 {
		if sealed, err = sealCredentials(s.o.Keyring, id, st.creds); err != nil {
			return Connection{}, err
		}
	}
	var out sqlc.Connection
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		locked, err := s.load(ctx, q, id, true)
		if err != nil {
			return err
		}
		if !locked.UpdatedAt.Equal(cur.UpdatedAt) {
			return ErrConflict
		}
		row, err := q.UpdateConnection(ctx, sqlc.UpdateConnectionParams{
			ID: id, Name: st.name, Engine: cur.Engine, VersionKey: st.versionKey, ImageKey: st.imageKey,
			Host: st.host, Port: int32(st.port), Username: st.username, HasPassword: len(st.creds.Password) > 0, //nolint:gosec // G115: validated port
			ClientCertPem: optString(st.clientCertPEM),
			SshEnabled:    st.ssh, SshHost: optString(st.sshHost), SshPort: optInt32(st.sshPort, st.ssh), SshUser: optString(st.sshUser),
			SshAuth: sqlc.ConnectionSshAuth(st.sshAuth), SshHasPassphrase: st.sshPass, SshPublicKey: optString(st.sshPublicKey),
			SecretEnc: sealed, ClearPins: st.clearPins, ClearHostKey: st.clearHostKey,
		})
		if serviceutil.IsUniqueViolation(err) {
			return ErrNameTaken
		}
		if err != nil {
			return fmt.Errorf("connections: update: %w", err)
		}
		out = row
		details := audit.Details{"name": st.name, "changed": changed, "values": values}
		if st.clearPins && (cur.TlsPinCertPem != nil || cur.TlsBackupPinCertPem != nil) {
			details["tls_pins_cleared"] = true
		}
		if st.clearHostKey && cur.SshHostKeyFingerprint != nil {
			details["host_key_cleared"] = true
		}
		if len(reentered) > 0 {
			// Sec M3: the endpoint changed, so the stored passwords were
			// not reused; the admin entered them again (or cleared them).
			details["reentered_for_new_endpoint"] = reentered
		}
		return s.record(ctx, q, actor, audit.ActionConnectionUpdated, id, details, st.credsChanged)
	})
	if err != nil {
		return Connection{}, err
	}
	return s.view(&out), nil
}

// requireReentry enforces that a stored password is never sent to a new
// endpoint (D-140): when the database endpoint changes (host, port, or the
// route through the tunnel: enabling or removing it, or another bastion
// host or port), a stored database password must be entered again or
// cleared in the same update; when the bastion's host, port or user
// changes, a stored SSH password must be replaced (password or key).
// Private keys and TLS client keys stay: they authenticate without
// revealing the secret to the peer. It returns the re-entered fields.
func requireReentry(cur *sqlc.Connection, st *state, in *UpdateInput) ([]string, error) {
	var reentered []string
	bastionMoved := cur.SshEnabled && st.ssh &&
		(st.sshHost != deref(cur.SshHost) || st.sshPort != int(deref(cur.SshPort)))
	dbMoved := st.host != cur.Host || st.port != int(cur.Port) || st.ssh != cur.SshEnabled || bastionMoved
	if dbMoved && cur.HasPassword {
		if in.Password == nil && !in.ClearPassword {
			return nil, fieldErr("password", "the database endpoint changed: enter the database password again (or clear it); a stored password is never sent to a new endpoint")
		}
		reentered = append(reentered, "password")
	}
	sshMoved := bastionMoved || (cur.SshEnabled && st.ssh && st.sshUser != deref(cur.SshUser))
	if sshMoved && SSHAuth(cur.SshAuth) == SSHAuthPassword {
		if in.SSH == nil || (in.SSH.Password == nil && in.SSH.PrivateKey == nil) {
			return nil, fieldErr("ssh.password", "the SSH bastion or user changed: enter the SSH password again (or set a private key); a stored password is never sent to a new endpoint")
		}
		reentered = append(reentered, "ssh_password")
	}
	return reentered, nil
}

// applyUpdate applies the non-SSH and SSH changes of in to st.
func applyUpdate(st *state, in *UpdateInput) error {
	var err error
	if in.Name != nil {
		if st.name, err = normalizeName(*in.Name); err != nil {
			return err
		}
	}
	if in.VersionKey != nil {
		st.versionKey = strings.TrimSpace(*in.VersionKey)
	}
	if in.ImageKey != nil {
		st.imageKey = strings.TrimSpace(*in.ImageKey)
	}
	if in.Host != nil {
		st.host = strings.TrimSpace(*in.Host)
	}
	if in.Port != nil {
		st.port = *in.Port
	}
	if in.Username != nil {
		st.username = strings.TrimSpace(*in.Username)
	}
	switch {
	case in.ClearPassword && in.Password != nil:
		return fieldErr("password", "set either a new password or clearPassword")
	case in.ClearPassword:
		if len(st.creds.Password) > 0 {
			crypto.Zero(st.creds.Password)
			st.creds.Password = nil
			st.changedCred("password")
		}
	case in.Password != nil:
		if err := checkPassword("password", in.Password); err != nil {
			return err
		}
		crypto.Zero(st.creds.Password)
		st.creds.Password = append([]byte(nil), in.Password...)
		st.changedCred("password")
	}
	switch {
	case in.ClearClientCertificate && (in.ClientCertPEM != nil || in.ClientKeyPEM != nil):
		return fieldErr("clientCertificatePem", "set either a new client certificate or clearClientCertificate")
	case in.ClearClientCertificate:
		if st.clientCertPEM != "" || len(st.creds.ClientKey) > 0 {
			st.clientCertPEM = ""
			crypto.Zero(st.creds.ClientKey)
			st.creds.ClientKey = nil
			st.changedCred("client_key")
		}
	case in.ClientCertPEM != nil || in.ClientKeyPEM != nil:
		cert, err := checkClientCertificate(deref(in.ClientCertPEM), in.ClientKeyPEM)
		if err != nil {
			return err
		}
		st.clientCertPEM = cert
		crypto.Zero(st.creds.ClientKey)
		st.creds.ClientKey = append([]byte(nil), in.ClientKeyPEM...)
		st.changedCred("client_key")
	}
	switch {
	case in.DisableSSH && in.SSH != nil:
		return fieldErr("ssh", "set either SSH settings or disableSsh")
	case in.DisableSSH:
		disableSSH(st)
	case in.SSH != nil:
		return applySSH(st, in.SSH)
	}
	return nil
}

// Delete soft deletes a connection (its row stays for jobs, artifacts and
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
		n, err := q.SoftDeleteConnection(ctx, id)
		if err != nil {
			return fmt.Errorf("connections: delete: %w", err)
		}
		if n != 1 {
			return ErrNotFound
		}
		return s.record(ctx, q, actor, audit.ActionConnectionDeleted, id,
			audit.Details{"name": r.Name, "engine": string(r.Engine), "host": r.Host, "port": r.Port}, nil)
	})
}

// checkUnused is the hook that refuses deleting a connection that is still
// in use. Jobs (task 3.5) must refuse it while a job backs up the
// connection; artifacts (tasks 3.6+) keep referencing a soft-deleted row.
// Until those tables exist nothing references connections.
func (s *Service) checkUnused(context.Context, *sqlc.Queries, uuid.UUID) error {
	return nil
}

// SetTLSMode changes the TLS mode. A downgrade (pinned → unverified →
// disabled) is audited as security.opt_out and raises an alert; the
// caller must have authorized connection.tls_downgrade. Pins are kept.
func (s *Service) SetTLSMode(ctx context.Context, actor Actor, id uuid.UUID, mode TLSMode) (Connection, error) {
	if !mode.Valid() {
		return Connection{}, fieldErr("mode", "unknown TLS mode")
	}
	var (
		out       sqlc.Connection
		downgrade bool
		from      TLSMode
	)
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		cur, err := s.load(ctx, q, id, true)
		if err != nil {
			return err
		}
		from = TLSMode(cur.TlsMode)
		if from == mode {
			out = cur
			return nil
		}
		if mode == TLSDisabled && cur.ClientCertPem != nil {
			return fieldErr("mode", "a TLS client certificate needs TLS; remove it before disabling TLS")
		}
		if err := checkIPLiteralRule(mode, cur.Host, cur.SshEnabled); err != nil {
			return err
		}
		row, err := q.SetConnectionTLSMode(ctx, sqlc.SetConnectionTLSModeParams{ID: id, TlsMode: sqlc.ConnectionTlsMode(mode)})
		if err != nil {
			return fmt.Errorf("connections: set tls mode: %w", err)
		}
		out = row
		downgrade = IsDowngrade(from, mode)
		if err := s.record(ctx, q, actor, audit.ActionConnectionTLSMode, id, audit.Details{
			"name": cur.Name, "from": string(from), "to": string(mode), "downgrade": downgrade,
		}, nil); err != nil {
			return err
		}
		if downgrade {
			return s.recordOptOut(ctx, q, actor, id, cur.Name, from, mode)
		}
		return nil
	})
	if err != nil {
		return Connection{}, err
	}
	if downgrade {
		s.o.Alerts(ctx, Alert{Kind: AlertTLSDowngrade, ConnectionID: id, ConnectionName: out.Name,
			Details: map[string]any{"from": string(from), "to": string(mode)}})
	}
	return s.view(&out), nil
}

// record writes one audit entry, plus a secret.changed entry when
// credential fields changed.
func (s *Service) record(ctx context.Context, q *sqlc.Queries, actor Actor, action audit.Action, id uuid.UUID, details audit.Details, credsChanged []string) error {
	ev := audit.Event{
		Actor: audit.UserActor(actor.UserID), Action: action, TargetType: "connection",
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

// recordOptOut records the security.opt_out of a TLS downgrade.
func (s *Service) recordOptOut(ctx context.Context, q *sqlc.Queries, actor Actor, id uuid.UUID, name string, from, to TLSMode) error {
	_, err := s.o.Auditor.RecordTx(ctx, q, audit.Event{
		Actor: audit.UserActor(actor.UserID), Action: audit.ActionSecurityOptOut, TargetType: "connection",
		TargetID: id.String(), IP: actor.IP,
		Details: audit.Details{"setting": "connection.tls_mode", "name": name, "from": string(from), "to": string(to)},
	})
	return err
}

// auditSettings returns the non-secret settings of st for the audit log
// (the client certificate and SSH public key only as flags).
func auditSettings(st *state) map[string]any {
	m := map[string]any{
		"engine": string(st.engine), "version_key": st.versionKey, "image_key": st.imageKey,
		"host": st.host, "port": st.port, "username": st.username, "tls_mode": string(st.tlsMode),
		"client_certificate": st.clientCertPEM != "", "ssh_enabled": st.ssh,
	}
	if st.ssh {
		m["ssh_host"], m["ssh_port"], m["ssh_user"], m["ssh_auth"] = st.sshHost, st.sshPort, st.sshUser, string(st.sshAuth)
	}
	return m
}

// diff lists the changed fields between the stored connection and st, with
// old/new values for non-secret, short fields. Credential fields are listed
// by name only.
func diff(cur *sqlc.Connection, st *state) ([]string, map[string]any) {
	var changed []string
	values := map[string]any{}
	cmp := func(field string, oldV, newV any, withValues bool) {
		if fmt.Sprint(oldV) == fmt.Sprint(newV) {
			return
		}
		changed = append(changed, field)
		if withValues {
			values[field] = map[string]any{"old": oldV, "new": newV}
		}
	}
	cmp("name", cur.Name, st.name, true)
	cmp("version_key", cur.VersionKey, st.versionKey, true)
	cmp("image_key", cur.ImageKey, st.imageKey, true)
	cmp("host", cur.Host, st.host, true)
	cmp("port", int(cur.Port), st.port, true)
	cmp("username", cur.Username, st.username, true)
	cmp("client_certificate", deref(cur.ClientCertPem), st.clientCertPEM, false)
	cmp("ssh_enabled", cur.SshEnabled, st.ssh, true)
	cmp("ssh_host", deref(cur.SshHost), st.sshHost, true)
	cmp("ssh_port", int(deref(cur.SshPort)), st.sshPort, true)
	cmp("ssh_user", deref(cur.SshUser), st.sshUser, true)
	cmp("ssh_auth", string(cur.SshAuth), string(st.sshAuth), true)
	cmp("ssh_public_key", deref(cur.SshPublicKey), st.sshPublicKey, false)
	changed = append(changed, st.credsChanged...)
	if st.clearPins && (cur.TlsPinCertPem != nil || cur.TlsBackupPinCertPem != nil) {
		changed = append(changed, "tls_pins")
	}
	if st.clearHostKey && cur.SshHostKeyFingerprint != nil {
		changed = append(changed, "ssh_host_key")
	}
	return changed, values
}

// certFingerprint is the SHA-256 of a certificate's DER as hex (audit).
func certFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}
