package email

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/hostname"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// SettingKey is the settings row of the SMTP settings.
const SettingKey = "smtp"

// passwordAAD binds the sealed SMTP password to its row and field.
var passwordAAD = crypto.AAD{Table: "settings", RowID: SettingKey, Field: "password"}

// Mode is the transport security of the SMTP connection (D-180).
type Mode string

// Modes.
const (
	// ModeAuto (the default) upgrades with STARTTLS when the server offers
	// it and otherwise sends unencrypted (AUTH included). A server that
	// offers STARTTLS but fails the handshake is an error, never a
	// fallback to plain text. An active attacker can strip the offer.
	ModeAuto Mode = "auto"
	// ModeSTARTTLS connects in plain text and upgrades with STARTTLS
	// before anything else (refused when the server does not offer it).
	ModeSTARTTLS Mode = "starttls"
	// ModeImplicitTLS speaks TLS from the first byte (SMTPS, port 465).
	ModeImplicitTLS Mode = "implicit_tls"
	// ModeNone never encrypts, even when the server offers STARTTLS.
	ModeNone Mode = "none"

	// legacyModePlaintext is the 5.1 dev-only mode; stored settings that
	// still carry it are read as ModeNone.
	legacyModePlaintext Mode = "plaintext"
)

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool {
	return m == ModeAuto || m == ModeSTARTTLS || m == ModeImplicitTLS || m == ModeNone
}

// MayBeUnencrypted reports whether mail (and AUTH) may travel without TLS
// in mode m (auto and none).
func (m Mode) MayBeUnencrypted() bool { return m == ModeAuto || m == ModeNone }

// Limits and defaults of the settings.
const (
	DefaultConnectTimeout = 10 * time.Second
	DefaultSendTimeout    = 60 * time.Second
	maxConnectTimeoutSec  = 60
	minSendTimeoutSec     = 5
	maxSendTimeoutSec     = 300
	maxUsernameLen        = 256
	maxPasswordLen        = 512
	maxFromNameLen        = 128
	maxAddressLen         = 254
	maxCABundleBytes      = 256 << 10
)

// Config is the stored, non-secret part of the SMTP settings.
type Config struct {
	Enabled               bool   `json:"enabled"`
	Host                  string `json:"host"`
	Port                  int    `json:"port"`
	Mode                  Mode   `json:"mode"`
	Username              string `json:"username,omitempty"`
	FromAddress           string `json:"from_address"`
	FromName              string `json:"from_name,omitempty"`
	CABundlePEM           string `json:"ca_bundle_pem,omitempty"`
	ConnectTimeoutSeconds int    `json:"connect_timeout_seconds"`
	SendTimeoutSeconds    int    `json:"send_timeout_seconds"`
}

func (c *Config) connectTimeout() time.Duration {
	return time.Duration(c.ConnectTimeoutSeconds) * time.Second
}

func (c *Config) sendTimeout() time.Duration {
	return time.Duration(c.SendTimeoutSeconds) * time.Second
}

// View is the SMTP settings as the API shows them: never the password.
type View struct {
	Config
	// Configured is false until an admin saved settings.
	Configured  bool
	HasPassword bool
	// MayBeUnencrypted is true for ModeAuto and ModeNone (the admin UI
	// shows an "email may be sent unencrypted" badge).
	MayBeUnencrypted bool
	UpdatedAt        *time.Time
	UpdatedBy        *uuid.UUID
}

// UpdateInput replaces the SMTP settings. Password nil keeps the stored
// password; ClearPassword removes it. Password is zeroed by Update.
type UpdateInput struct {
	Enabled               bool
	Host                  string
	Port                  int
	Mode                  Mode
	Username              string
	Password              []byte
	ClearPassword         bool
	FromAddress           string
	FromName              string
	CABundlePEM           string
	ConnectTimeoutSeconds int
	SendTimeoutSeconds    int
}

// Actor is the admin performing a change.
type Actor struct {
	UserID uuid.UUID
	IP     netip.Addr
}

// Errors of the settings service.
var (
	// ErrConflict: the settings changed while an update was prepared.
	ErrConflict = errors.New("email: the SMTP settings were changed concurrently")
)

// FieldError is an invalid input (presentable).
type FieldError = serviceutil.FieldError

func fieldErr(field, format string, a ...any) error { return serviceutil.FieldErr(field, format, a...) }

// DB is what the package needs from the app database (*pgxpool.Pool).
type DB interface {
	db.TxBeginner
	sqlc.DBTX
}

// SettingsOptions configure NewSettings.
type SettingsOptions struct {
	DB      DB
	Keyring *crypto.Keyring
	Auditor *audit.Auditor
	// Guard checks the SMTP host when it is saved (the dial-time check
	// still applies at every send).
	Guard *netguard.Guard
	// Dev is ignored: since D-180 the SMTP modes do not depend on dev mode
	// (kept so existing callers compile).
	Dev    bool
	Logger *slog.Logger
}

// Settings stores and reads the SMTP settings.
type Settings struct{ o SettingsOptions }

// NewSettings validates o.
func NewSettings(o SettingsOptions) (*Settings, error) {
	if o.DB == nil || o.Keyring == nil || o.Auditor == nil || o.Guard == nil {
		return nil, errors.New("email: DB, Keyring, Auditor and Guard are required")
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Settings{o: o}, nil
}

// stored is a decoded settings row.
type stored struct {
	cfg       Config
	sealed    []byte
	updatedAt time.Time
	updatedBy *uuid.UUID
}

func (s *Settings) read(ctx context.Context, q *sqlc.Queries, lock bool) (*stored, error) {
	var (
		row sqlc.Setting
		err error
	)
	if lock {
		row, err = q.GetSettingForUpdate(ctx, SettingKey)
	} else {
		row, err = q.GetSetting(ctx, SettingKey)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("email: read SMTP settings: %w", err)
	}
	st := &stored{sealed: row.SecretEnc, updatedAt: row.UpdatedAt, updatedBy: row.UpdatedBy}
	if err := json.Unmarshal(row.Value, &st.cfg); err != nil {
		return nil, fmt.Errorf("email: decode SMTP settings: %w", err)
	}
	if st.cfg.Mode == legacyModePlaintext {
		st.cfg.Mode = ModeNone // D-180: the old dev-only mode never encrypted
	}
	return st, nil
}

func (st *stored) view() View {
	if st == nil {
		return View{Config: Config{
			Mode: ModeAuto, Port: 587,
			ConnectTimeoutSeconds: int(DefaultConnectTimeout / time.Second),
			SendTimeoutSeconds:    int(DefaultSendTimeout / time.Second),
		}, MayBeUnencrypted: ModeAuto.MayBeUnencrypted()}
	}
	at := st.updatedAt
	return View{
		Config: st.cfg, Configured: true, HasPassword: len(st.sealed) > 0,
		MayBeUnencrypted: st.cfg.Mode.MayBeUnencrypted(), UpdatedAt: &at, UpdatedBy: st.updatedBy,
	}
}

// Get returns the settings (defaults with Configured false when none are
// stored).
func (s *Settings) Get(ctx context.Context) (View, error) {
	st, err := s.read(ctx, sqlc.New(s.o.DB), false)
	if err != nil {
		return View{}, err
	}
	return st.view(), nil
}

// loadForSend returns the stored settings and the unsealed password (nil
// when none). The caller zeroes the password. It fails with
// ErrNotConfigured when nothing is stored or sending is disabled.
func (s *Settings) loadForSend(ctx context.Context) (Config, []byte, error) {
	st, err := s.read(ctx, sqlc.New(s.o.DB), false)
	if err != nil {
		return Config{}, nil, err
	}
	if st == nil || !st.cfg.Enabled {
		return Config{}, nil, ErrNotConfigured
	}
	if len(st.sealed) == 0 {
		return st.cfg, nil, nil
	}
	pw, err := s.o.Keyring.Open(st.sealed, passwordAAD)
	if err != nil {
		return Config{}, nil, fmt.Errorf("email: open SMTP password: %w", err)
	}
	return st.cfg, pw, nil
}

// Update validates and stores in (admin + step-up are checked by the
// caller). Validation, including the SSRF check of the host, happens before
// the row is locked; ErrConflict when the row changed in between. Audited
// as setting.changed with the changed field names (the password by name
// only, plus secret.changed).
func (s *Settings) Update(ctx context.Context, actor Actor, in UpdateInput) (View, error) {
	defer crypto.Zero(in.Password)
	cur, err := s.read(ctx, sqlc.New(s.o.DB), false)
	if err != nil {
		return View{}, err
	}
	cfg, err := validate(&in)
	if err != nil {
		return View{}, err
	}
	hasStored := cur != nil && len(cur.sealed) > 0
	pwChange, err := passwordChange(cur, &cfg, &in, hasStored)
	if err != nil {
		return View{}, err
	}
	if err := checkHost(ctx, s.o.Guard, cfg.Host); err != nil {
		return View{}, fieldErr("host", "%s", err.Error())
	}
	if len(in.Password) >= 4 {
		defer logging.DefaultRegistry.Register(in.Password)()
	}

	var prevAt time.Time
	if cur != nil {
		prevAt = cur.updatedAt
	}
	changed, values := diffConfig(cur, &cfg)
	if pwChange != "" {
		changed = append(changed, "password")
	}
	if cur != nil && len(changed) == 0 {
		return cur.view(), nil
	}
	sealed := []byte(nil)
	if cur != nil {
		sealed = cur.sealed
	}
	switch pwChange {
	case "set":
		if sealed, err = s.o.Keyring.Seal(in.Password, passwordAAD); err != nil {
			return View{}, fmt.Errorf("email: seal SMTP password: %w", err)
		}
	case "cleared":
		sealed = nil
	}
	value, err := json.Marshal(cfg)
	if err != nil {
		return View{}, fmt.Errorf("email: encode SMTP settings: %w", err)
	}
	var out *stored
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		locked, err := s.read(ctx, q, true)
		if err != nil {
			return err
		}
		if (locked == nil) != (cur == nil) || (locked != nil && !locked.updatedAt.Equal(prevAt)) {
			return ErrConflict
		}
		row, err := q.PutSetting(ctx, sqlc.PutSettingParams{Key: SettingKey, Value: value, SecretEnc: sealed, UpdatedBy: &actor.UserID})
		if err != nil {
			return fmt.Errorf("email: store SMTP settings: %w", err)
		}
		out = &stored{cfg: cfg, sealed: row.SecretEnc, updatedAt: row.UpdatedAt, updatedBy: row.UpdatedBy}
		ev := audit.Event{
			Actor: audit.UserActor(actor.UserID), Action: audit.ActionSettingChanged,
			TargetType: "setting", TargetID: SettingKey, IP: actor.IP,
			Details: audit.Details{
				"setting": SettingKey, "changed": changed, "values": values, "created": cur == nil,
				"mode": string(cfg.Mode), "may_be_unencrypted": cfg.Mode.MayBeUnencrypted(),
			},
		}
		if _, err := s.o.Auditor.RecordTx(ctx, q, ev); err != nil {
			return err
		}
		if pwChange == "" {
			return nil
		}
		ev.Action = audit.ActionSecretChanged
		ev.Details = audit.Details{"setting": SettingKey, "fields": []string{"password"}, "change": pwChange, "via": string(audit.ActionSettingChanged)}
		_, err = s.o.Auditor.RecordTx(ctx, q, ev)
		return err
	})
	if err != nil {
		return View{}, err
	}
	if cfg.Mode.MayBeUnencrypted() {
		s.o.Logger.WarnContext(ctx, "smtp_may_be_unencrypted",
			slog.String("mode", string(cfg.Mode)),
			slog.String("detail", "e-mail and SMTP credentials may be sent without TLS"))
	}
	return out.view(), nil
}

// passwordChange decides what happens to the stored password: "" (kept),
// "set" or "cleared". A stored password is never sent to a changed endpoint
// (D-140): when host, port or username change, it must be entered again;
// likewise when the transport is weakened (starttls or implicit_tls to
// auto or none) or the CA bundle changes (M5 review L3, D-198).
func passwordChange(cur *stored, cfg *Config, in *UpdateInput, hasStored bool) (string, error) {
	switch {
	case in.ClearPassword && in.Password != nil:
		return "", fieldErr("password", "set a password or clear it, not both")
	case in.Password != nil:
		if err := checkPassword(in.Password); err != nil {
			return "", err
		}
	}
	willHave := (hasStored && !in.ClearPassword) || in.Password != nil
	if cfg.Username == "" && willHave {
		if in.Password != nil {
			return "", fieldErr("password", "a password needs a username")
		}
		if hasStored {
			return "", fieldErr("password", "without a username the stored password must be cleared (clearPassword)")
		}
	}
	if cfg.Username != "" && !willHave {
		return "", fieldErr("password", "a username needs a password")
	}
	if hasStored && !in.ClearPassword && in.Password == nil && cur != nil &&
		(cur.cfg.Host != cfg.Host || cur.cfg.Port != cfg.Port || cur.cfg.Username != cfg.Username) {
		return "", fieldErr("password", "the server or username changed: enter the password again; a stored password is never sent to a new endpoint")
	}
	if hasStored && !in.ClearPassword && in.Password == nil && cur != nil {
		// M5 review L3: weakening the transport or changing the trusted
		// CA could expose the stored password to whoever answers next, so
		// it needs the password again (proof the admin knows it, and a
		// deliberate act).
		if weakensTransport(cur.cfg.Mode, cfg.Mode) {
			return "", fieldErr("password", "the connection security is weakened: enter the password again; a stored password is never sent over a weaker connection without it")
		}
		if cur.cfg.CABundlePEM != cfg.CABundlePEM {
			return "", fieldErr("password", "the CA bundle changed: enter the password again; a stored password is never sent to a server verified differently without it")
		}
	}
	switch {
	case in.Password != nil:
		return "set", nil
	case in.ClearPassword && hasStored:
		return "cleared", nil
	default:
		return "", nil
	}
}

// validate checks in and returns the normalized config.
// weakensTransport reports whether a mode change gives up mandatory TLS.
func weakensTransport(from, to Mode) bool {
	return !from.MayBeUnencrypted() && to.MayBeUnencrypted()
}

func validate(in *UpdateInput) (Config, error) {
	c := Config{
		Enabled:               in.Enabled,
		Host:                  hostname.Normalize(strings.TrimSpace(in.Host)),
		Port:                  in.Port,
		Mode:                  in.Mode,
		Username:              in.Username,
		FromAddress:           strings.TrimSpace(in.FromAddress),
		FromName:              strings.TrimSpace(in.FromName),
		CABundlePEM:           strings.TrimSpace(in.CABundlePEM),
		ConnectTimeoutSeconds: in.ConnectTimeoutSeconds,
		SendTimeoutSeconds:    in.SendTimeoutSeconds,
	}
	if c.Host == "" {
		return c, fieldErr("host", "the SMTP host is required")
	}
	if err := hostname.Validate(c.Host); err != nil {
		return c, fieldErr("host", "invalid host name: %s", err.Error())
	}
	if c.Port < 1 || c.Port > 65535 {
		return c, fieldErr("port", "the port must be between 1 and 65535")
	}
	if !c.Mode.Valid() {
		return c, fieldErr("mode", "unknown mode")
	}
	if len(c.Username) > maxUsernameLen || hasControl(c.Username) || strings.TrimSpace(c.Username) != c.Username {
		return c, fieldErr("username", "the username must be at most %d characters without control characters or surrounding spaces", maxUsernameLen)
	}
	if err := CheckAddress(c.FromAddress); err != nil {
		return c, fieldErr("fromAddress", "%s", err.Error())
	}
	if len(c.FromName) > maxFromNameLen || hasControl(c.FromName) {
		return c, fieldErr("fromName", "the sender name must be at most %d characters without control characters", maxFromNameLen)
	}
	if c.CABundlePEM != "" {
		if len(c.CABundlePEM) > maxCABundleBytes {
			return c, fieldErr("caBundlePem", "CA bundle too large (max %d KiB)", maxCABundleBytes>>10)
		}
		if c.Mode == ModeNone {
			return c, fieldErr("caBundlePem", "a CA bundle needs a mode that uses TLS (auto, STARTTLS or implicit TLS)")
		}
		if err := checkCABundle([]byte(c.CABundlePEM)); err != nil {
			return c, err
		}
		c.CABundlePEM += "\n"
	}
	if c.ConnectTimeoutSeconds == 0 {
		c.ConnectTimeoutSeconds = int(DefaultConnectTimeout / time.Second)
	}
	if c.SendTimeoutSeconds == 0 {
		c.SendTimeoutSeconds = int(DefaultSendTimeout / time.Second)
	}
	if c.ConnectTimeoutSeconds < 1 || c.ConnectTimeoutSeconds > maxConnectTimeoutSec {
		return c, fieldErr("connectTimeoutSeconds", "must be between 1 and %d", maxConnectTimeoutSec)
	}
	if c.SendTimeoutSeconds < minSendTimeoutSec || c.SendTimeoutSeconds > maxSendTimeoutSec {
		return c, fieldErr("sendTimeoutSeconds", "must be between %d and %d", minSendTimeoutSec, maxSendTimeoutSec)
	}
	return c, nil
}

func checkPassword(p []byte) error {
	if len(p) == 0 {
		return fieldErr("password", "the password must not be empty (use clearPassword to remove it)")
	}
	if len(p) > maxPasswordLen {
		return fieldErr("password", "the password must be at most %d bytes", maxPasswordLen)
	}
	if bytes.ContainsAny(p, "\x00\r\n") {
		return fieldErr("password", "the password must not contain NUL or line breaks")
	}
	return nil
}

// checkCABundle requires PEM certificates only.
func checkCABundle(b []byte) error {
	n := 0
	for {
		var block *pem.Block
		block, b = pem.Decode(b)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return fieldErr("caBundlePem", "the CA bundle may only contain CERTIFICATE blocks")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return fieldErr("caBundlePem", "the CA bundle contains an invalid certificate")
		}
		n++
	}
	if n == 0 || strings.TrimSpace(string(b)) != "" {
		return fieldErr("caBundlePem", "the CA bundle must consist of PEM certificates")
	}
	return nil
}

// checkHost resolves host and checks every address against the SSRF guard.
// The messages are for admins (D-120).
func checkHost(ctx context.Context, g *netguard.Guard, host string) error {
	if _, err := g.ResolveAndCheck(ctx, host); err != nil {
		var blocked *netguard.BlockedAddressError
		switch {
		case errors.As(err, &blocked):
			return fmt.Errorf("the host resolves to %s, which is not allowed (%s); private relays must be on the SSRF allowlist", blocked.IP, blocked.Reason)
		case errors.Is(err, context.Canceled):
			return err
		default:
			return errors.New("the host cannot be resolved")
		}
	}
	return nil
}

// CheckAddress validates a bare e-mail address (no display name, no
// comments, no control characters, at most 254 bytes).
func CheckAddress(addr string) error {
	if addr == "" {
		return errors.New("the e-mail address is required")
	}
	if len(addr) > maxAddressLen || hasControl(addr) {
		return errors.New("invalid e-mail address")
	}
	a, err := mail.ParseAddress(addr)
	if err != nil || a.Name != "" || a.Address != addr {
		return errors.New("invalid e-mail address")
	}
	local, domain, ok := strings.Cut(addr, "@")
	if !ok || local == "" || domain == "" || strings.ContainsAny(addr, " <>\"(),;:\\[]") {
		return errors.New("invalid e-mail address")
	}
	return nil
}

// hasControl reports C0/C1 control characters, DEL and invalid UTF-8
// (header injection protection).
func hasControl(s string) bool {
	if !utf8.ValidString(s) {
		return true
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == '\u2028' || r == '\u2029' {
			return true
		}
	}
	return false
}

// diffConfig lists the changed fields with old/new values (the CA bundle as
// a flag only).
func diffConfig(cur *stored, next *Config) ([]string, map[string]any) {
	var old Config
	if cur != nil {
		old = cur.cfg
	}
	oldM, newM := configMap(&old), configMap(next)
	keys := make([]string, 0, len(newM))
	for k := range newM {
		keys = append(keys, k)
	}
	for k := range oldM {
		if _, ok := newM[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	var changed []string
	values := map[string]any{}
	for _, k := range keys {
		o, n := oldM[k], newM[k]
		if fmt.Sprint(o) == fmt.Sprint(n) {
			continue
		}
		changed = append(changed, k)
		if k == "ca_bundle_pem" {
			values[k] = map[string]any{"old": o != nil && o != "", "new": n != nil && n != ""}
			continue
		}
		values[k] = map[string]any{"old": o, "new": n}
	}
	return changed, values
}

func configMap(c *Config) map[string]any {
	b, err := json.Marshal(c)
	if err != nil {
		return map[string]any{}
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{}
	}
	return m
}
