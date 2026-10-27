package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// DB is what the auth services need from the app database: queries and
// transactions (*pgxpool.Pool implements it).
type DB interface {
	sqlc.DBTX
	db.TxBeginner
}

// Session defaults (security.session_*).
const (
	DefaultIdleTimeout     = 30 * time.Minute
	DefaultAbsoluteTimeout = 12 * time.Hour
	DefaultTouchInterval   = time.Minute
	// PendingSecondFactorTTL is the lifetime of a session that waits for
	// its second factor (task 1.9): the absolute expiry of the password
	// step's session.
	PendingSecondFactorTTL = 5 * time.Minute
)

// AAL values (sessions.aal).
const (
	AAL1 = 1 // password
	AAL2 = 2 // password + second factor
)

// Errors.
var (
	// ErrNoSession means the token does not belong to an active session
	// of an active user (unknown, malformed, revoked, expired, disabled or
	// locked). The session cookie should be cleared.
	ErrNoSession = errors.New("auth: no active session")
	// ErrSessionNotFound means a session to act on does not exist, is not
	// active or belongs to another user.
	ErrSessionNotFound = errors.New("auth: session not found")
)

// SessionOptions configure NewSessions.
type SessionOptions struct {
	// IdleTimeout is security.session_idle_timeout.
	IdleTimeout time.Duration
	// AbsoluteTimeout is security.session_absolute_timeout.
	AbsoluteTimeout time.Duration
	// TouchInterval is security.session_touch_interval: the minimum time
	// between two last-seen/idle-expiry writes of one session.
	TouchInterval time.Duration
	// Logger receives failed touch writes (nil: discarded).
	Logger *slog.Logger
	// Now replaces time.Now (tests).
	Now func() time.Time
	// Events receives revocations and rotations inside their transaction
	// (task 1.10: open WebSocket subscriptions of the session close at
	// once); nil: none are published.
	Events SessionEventPublisher
}

// Sessions manages server-side sessions (D-047): random tokens of which
// only sha256 is stored, idle and absolute expiry, sliding idle refresh
// with a write throttle, rotation, AAL and step-up tracking, revocation.
type Sessions struct {
	db   DB
	opts SessionOptions
}

// NewSessions returns the session store on d.
func NewSessions(d DB, o SessionOptions) *Sessions {
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = DefaultIdleTimeout
	}
	if o.AbsoluteTimeout <= 0 {
		o.AbsoluteTimeout = DefaultAbsoluteTimeout
	}
	o.AbsoluteTimeout = max(o.AbsoluteTimeout, o.IdleTimeout)
	if o.TouchInterval <= 0 {
		o.TouchInterval = DefaultTouchInterval
	}
	o.TouchInterval = min(o.TouchInterval, o.IdleTimeout/2)
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Sessions{db: d, opts: o}
}

// Issued is a newly created session and its token. The token goes into
// the cookie only; it is never stored or logged.
type Issued struct {
	Token   string
	Session sqlc.Session
}

// newParams describes a session to insert.
type newParams struct {
	userID   uuid.UUID
	aal      int
	stepUpAt *time.Time
	aal2At   *time.Time
	// absolute is the absolute expiry; zero: now + AbsoluteTimeout.
	absolute time.Time
	meta     RequestMeta
	// mfaPending: the session waits for its second factor (AAL1, no
	// step-up, short absolute expiry).
	mfaPending bool
}

func (s *Sessions) insert(ctx context.Context, q *sqlc.Queries, p newParams) (Issued, error) {
	if p.aal != AAL1 && p.aal != AAL2 {
		return Issued{}, fmt.Errorf("auth: invalid AAL %d", p.aal)
	}
	if p.mfaPending && (p.aal != AAL1 || p.stepUpAt != nil || p.aal2At != nil) {
		return Issued{}, errors.New("auth: a session waiting for its second factor must be AAL1 without step-up")
	}
	now := s.opts.Now()
	absolute := p.absolute
	if absolute.IsZero() {
		absolute = now.Add(s.opts.AbsoluteTimeout)
	}
	idle := now.Add(s.opts.IdleTimeout)
	if idle.After(absolute) {
		idle = absolute
	}
	token, hash := newSessionToken()
	params := sqlc.InsertSessionParams{
		TokenHash:         hash,
		UserID:            p.userID,
		Aal:               int16(p.aal), //nolint:gosec // G115: p.aal is 1 or 2 (checked above)
		StepupAt:          p.stepUpAt,
		Aal2At:            p.aal2At,
		IdleExpiresAt:     idle,
		AbsoluteExpiresAt: absolute,
		MfaPending:        p.mfaPending,
	}
	if p.meta.IP.IsValid() {
		ip := p.meta.IP
		params.Ip = &ip
	}
	if p.meta.UserAgent != "" {
		ua := p.meta.UserAgent
		params.UserAgent = &ua
	}
	row, err := q.InsertSession(ctx, params)
	if err != nil {
		return Issued{}, fmt.Errorf("auth: insert session: %w", err)
	}
	return Issued{Token: token, Session: row}, nil
}

// CreateTx creates a session for userID at aal with q (inside the
// caller's transaction). With stepUp the session starts with a fresh
// step-up (a login is a fresh authentication).
func (s *Sessions) CreateTx(ctx context.Context, q *sqlc.Queries, userID uuid.UUID, aal int, stepUp bool, meta RequestMeta) (Issued, error) {
	p := newParams{userID: userID, aal: aal, meta: meta}
	if stepUp {
		now := s.opts.Now()
		p.stepUpAt = &now
	}
	if aal == AAL2 {
		now := s.opts.Now()
		p.aal2At = &now
	}
	return s.insert(ctx, q, p)
}

// CreatePendingTx creates the session of a login's password step for an
// account with a second factor (task 1.9): AAL1, no step-up, marked
// mfa_pending and valid for PendingSecondFactorTTL. The principal of such
// a session may only complete the second factor or log out.
func (s *Sessions) CreatePendingTx(ctx context.Context, q *sqlc.Queries, userID uuid.UUID, meta RequestMeta) (Issued, error) {
	return s.insert(ctx, q, newParams{
		userID: userID, aal: AAL1, meta: meta, mfaPending: true,
		absolute: s.opts.Now().Add(min(PendingSecondFactorTTL, s.opts.AbsoluteTimeout)),
	})
}

// Resolve returns the principal and user of the session behind token. It
// returns ErrNoSession for a malformed, unknown, revoked or expired token
// and for a disabled user. A lockout (users.locked_until) only blocks new
// password logins: sessions established before it stay valid, so failing
// logins with someone's e-mail address cannot log them out (D-107). Well-formed tokens always
// cost one indexed lookup, whether they exist or not. Activity is written
// at most every TouchInterval (sliding idle expiry, capped by the absolute
// expiry); a failed touch is logged and does not fail the request.
func (s *Sessions) Resolve(ctx context.Context, token string, meta RequestMeta) (Principal, sqlc.User, error) {
	hash, ok := hashSessionToken(token)
	if !ok {
		return Principal{}, sqlc.User{}, ErrNoSession
	}
	q := sqlc.New(s.db)
	row, err := q.GetSessionWithUserByTokenHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, sqlc.User{}, ErrNoSession
	}
	if err != nil {
		return Principal{}, sqlc.User{}, fmt.Errorf("auth: look up session: %w", err)
	}
	sess, user := row.Session, row.User
	now := s.opts.Now()
	if user.Disabled || user.DeletedAt != nil {
		return Principal{}, sqlc.User{}, ErrNoSession
	}
	role := Role(user.Role)
	if !role.Valid() {
		return Principal{}, sqlc.User{}, ErrNoSession
	}
	if now.Sub(sess.LastSeenAt) >= s.opts.TouchInterval {
		s.touch(ctx, q, sess.ID, now, meta)
	}
	return principalOf(sess, user, role), user, nil
}

func (s *Sessions) touch(ctx context.Context, q *sqlc.Queries, id uuid.UUID, now time.Time, meta RequestMeta) {
	p := sqlc.TouchSessionParams{ID: id, IdleExpiresAt: now.Add(s.opts.IdleTimeout)}
	if meta.IP.IsValid() {
		ip := meta.IP
		p.Ip = &ip
	}
	if meta.UserAgent != "" {
		ua := meta.UserAgent
		p.UserAgent = &ua
	}
	if _, err := q.TouchSession(ctx, p); err != nil {
		s.opts.Logger.WarnContext(ctx, "session touch failed", slog.String("session_id", id.String()), slog.Any("error", err))
	}
}

func principalOf(sess sqlc.Session, user sqlc.User, role Role) Principal {
	p := Principal{
		UserID: sess.UserID, Role: role, SessionID: sess.ID, AAL: int(sess.Aal),
		PasswordChangeRequired: user.PasswordTemporaryUntil != nil,
		MFAPending:             sess.MfaPending,
	}
	if sess.StepupAt != nil {
		p.StepUpAt = *sess.StepupAt
	}
	if sess.Aal2At != nil {
		p.AAL2At = *sess.Aal2At
	}
	return p
}

// lockActive locks the active session id (FOR UPDATE).
func lockActive(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (sqlc.Session, error) {
	sess, err := q.GetActiveSessionForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Session{}, ErrSessionNotFound
	}
	if err != nil {
		return sqlc.Session{}, fmt.Errorf("auth: lock session: %w", err)
	}
	return sess, nil
}

// RotateTx replaces the active session id by a new one with a new token
// (privilege changes, D-047). The new session keeps the user, AAL,
// step-up and second-factor times and the absolute expiry of the old one
// (rotation never extends a session's lifetime). The old session is
// revoked. meta records the current client.
func (s *Sessions) RotateTx(ctx context.Context, q *sqlc.Queries, id uuid.UUID, meta RequestMeta) (Issued, error) {
	return s.rotateTx(ctx, q, id, meta, nil)
}

// rotateTx is RotateTx; mutate may adjust the new session's parameters.
func (s *Sessions) rotateTx(ctx context.Context, q *sqlc.Queries, id uuid.UUID, meta RequestMeta, mutate func(*newParams)) (Issued, error) {
	old, err := lockActive(ctx, q, id)
	if err != nil {
		return Issued{}, err
	}
	p := newParams{
		userID: old.UserID, aal: int(old.Aal), stepUpAt: old.StepupAt, aal2At: old.Aal2At,
		absolute: old.AbsoluteExpiresAt, meta: meta, mfaPending: old.MfaPending,
	}
	if mutate != nil {
		mutate(&p)
	}
	if _, err := q.RevokeSession(ctx, old.ID); err != nil {
		return Issued{}, fmt.Errorf("auth: revoke rotated session: %w", err)
	}
	if err := s.emit(ctx, q, SessionEvent{Kind: SessionRotated, UserID: old.UserID, SessionID: old.ID}); err != nil {
		return Issued{}, err
	}
	return s.insert(ctx, q, p)
}

// Rotate is RotateTx in its own transaction.
func (s *Sessions) Rotate(ctx context.Context, id uuid.UUID, meta RequestMeta) (Issued, error) {
	var out Issued
	err := db.Tx(ctx, s.db, func(q *sqlc.Queries) error {
		var err error
		out, err = s.RotateTx(ctx, q, id, meta)
		return err
	})
	return out, err
}

// ElevateAALTx raises the session's AAL after a successful second factor
// (task 1.9): the session is rotated (an AAL increase is a privilege
// change), the second-factor and step-up times are set to now. aal must
// be AAL2 and not lower than the current level. A session that waits for
// its second factor is completed with CompleteSecondFactorTx instead.
func (s *Sessions) ElevateAALTx(ctx context.Context, q *sqlc.Queries, id uuid.UUID, aal int, meta RequestMeta) (Issued, error) {
	if aal != AAL2 {
		return Issued{}, fmt.Errorf("auth: cannot elevate to AAL %d", aal)
	}
	sess, err := lockActive(ctx, q, id)
	if err != nil {
		return Issued{}, err
	}
	if sess.MfaPending {
		return Issued{}, ErrNotPending
	}
	now := s.opts.Now()
	return s.rotateTx(ctx, q, id, meta, func(p *newParams) {
		p.aal = aal
		p.aal2At = &now
		p.stepUpAt = &now
	})
}

// ErrNotPending means a second-factor completion was attempted on a
// session that is not waiting for one, or an elevation on one that is.
var ErrNotPending = errors.New("auth: the session is not waiting for a second factor")

// CompleteSecondFactorTx replaces the mfa_pending session id by a fully
// signed-in AAL2 session (task 1.9): the old session is revoked (a
// rotation event closes its sockets), the new one has a new token, a fresh
// step-up and a full absolute lifetime from now. withAAL2At records the
// second-factor time as well (passkey or TOTP; not for a recovery code,
// which does not satisfy the restore check). ErrNotPending when the
// session is not waiting for a second factor.
func (s *Sessions) CompleteSecondFactorTx(ctx context.Context, q *sqlc.Queries, id uuid.UUID, withAAL2At bool, meta RequestMeta) (Issued, error) {
	old, err := lockActive(ctx, q, id)
	if err != nil {
		return Issued{}, err
	}
	if !old.MfaPending {
		return Issued{}, ErrNotPending
	}
	now := s.opts.Now()
	p := newParams{userID: old.UserID, aal: AAL2, stepUpAt: &now, meta: meta}
	if withAAL2At {
		p.aal2At = &now
	}
	if _, err := q.RevokeSession(ctx, old.ID); err != nil {
		return Issued{}, fmt.Errorf("auth: revoke pending session: %w", err)
	}
	if err := s.emit(ctx, q, SessionEvent{Kind: SessionRotated, UserID: old.UserID, SessionID: old.ID}); err != nil {
		return Issued{}, err
	}
	return s.insert(ctx, q, p)
}

// MarkSecondFactorTx records a step-up with a passkey or TOTP on an AAL2
// session (task 1.9): step-up and second-factor times become now, without
// rotation (the AAL does not change). ErrSessionNotFound when the session
// is not an active AAL2 session.
func (s *Sessions) MarkSecondFactorTx(ctx context.Context, q *sqlc.Queries, id uuid.UUID) error {
	n, err := q.MarkSessionSecondFactor(ctx, id)
	if err != nil {
		return fmt.Errorf("auth: mark second factor: %w", err)
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// ElevateAAL is ElevateAALTx in its own transaction. The caller sets the
// returned token as the new session cookie and audits the event.
func (s *Sessions) ElevateAAL(ctx context.Context, id uuid.UUID, aal int) (Issued, error) {
	meta := RequestMetaFrom(ctx)
	var out Issued
	err := db.Tx(ctx, s.db, func(q *sqlc.Queries) error {
		var err error
		out, err = s.ElevateAALTx(ctx, q, id, aal, meta)
		return err
	})
	return out, err
}

// MarkStepUpTx records a successful step-up (fresh re-authentication) on
// the active session id.
func (s *Sessions) MarkStepUpTx(ctx context.Context, q *sqlc.Queries, id uuid.UUID) error {
	n, err := q.MarkSessionStepUp(ctx, id)
	if err != nil {
		return fmt.Errorf("auth: mark step-up: %w", err)
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// MarkStepUp is MarkStepUpTx outside a transaction.
func (s *Sessions) MarkStepUp(ctx context.Context, id uuid.UUID) error {
	return s.MarkStepUpTx(ctx, sqlc.New(s.db), id)
}

// RevokeTx revokes the session id of userID; ErrSessionNotFound when it is
// not an unrevoked session of that user.
func (s *Sessions) RevokeTx(ctx context.Context, q *sqlc.Queries, userID, id uuid.UUID) error {
	n, err := q.RevokeUserSession(ctx, sqlc.RevokeUserSessionParams{ID: id, UserID: userID})
	if err != nil {
		return fmt.Errorf("auth: revoke session: %w", err)
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return s.emit(ctx, q, SessionEvent{Kind: SessionRevoked, UserID: userID, SessionID: id})
}

// RevokeAllTx revokes every session of userID and returns how many were
// active (password change; task 1.6 role changes and disabling).
func (s *Sessions) RevokeAllTx(ctx context.Context, q *sqlc.Queries, userID uuid.UUID) (int64, error) {
	n, err := q.RevokeUserSessions(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("auth: revoke sessions: %w", err)
	}
	if n > 0 {
		if err := s.emit(ctx, q, SessionEvent{Kind: SessionsRevokedAll, UserID: userID}); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// List returns the active sessions of userID, most recently used first.
func (s *Sessions) List(ctx context.Context, userID uuid.UUID) ([]sqlc.Session, error) {
	rows, err := sqlc.New(s.db).ListActiveUserSessions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: list sessions: %w", err)
	}
	return rows, nil
}

// DeleteStale deletes sessions that expired or were revoked more than
// retain ago.
func (s *Sessions) DeleteStale(ctx context.Context, retain time.Duration) (int64, error) {
	n, err := sqlc.New(s.db).DeleteStaleSessions(ctx, s.opts.Now().Add(-retain))
	if err != nil {
		return 0, fmt.Errorf("auth: delete stale sessions: %w", err)
	}
	return n, nil
}
