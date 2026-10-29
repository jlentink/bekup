package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Errors of the service. Resolvers map them to API error codes.
var (
	// ErrInvalidCredentials is the only answer to a failed login, whatever
	// the cause (unknown e-mail, wrong password, disabled or locked
	// account, no password set): "invalid email or password".
	ErrInvalidCredentials = errors.New("auth: invalid email or password")
	// ErrWrongPassword means the current password given to ChangePassword
	// is wrong.
	ErrWrongPassword = errors.New("auth: current password is wrong")
)

// RateLimitedError means the caller exceeded the login rate limit.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string { return "auth: too many login attempts" }

// Factor is a second-factor kind offered after the password (tasks
// 1.7-1.9).
type Factor string

// Factors.
const (
	FactorTOTP         Factor = "totp"
	FactorPasskey      Factor = "passkey"
	FactorRecoveryCode Factor = "recovery_code"
)

// Login failure reasons recorded in the audit log (never the password).
const (
	reasonUnknownUser  = "unknown_user"
	reasonBadPassword  = "bad_password"
	reasonDisabled     = "disabled"
	reasonLocked       = "locked"
	reasonNoPassword   = "no_password"
	reasonInvalidInput = "invalid_input"
	// reasonTemporaryExpired: an admin-issued temporary password expired
	// before it was changed (task 1.6).
	reasonTemporaryExpired = "temporary_password_expired"
)

// maxEmailLength is the users.email limit.
const maxEmailLength = 254

// LoginLimiter takes one login attempt for a client address; false means
// the attempt is refused, with the time until the next one is allowed.
type LoginLimiter func(ip netip.Addr) (bool, time.Duration)

// AccountLimiter takes one password verification for an account key
// (AccountKey); false means it is refused, with the time until the next
// one is allowed.
type AccountLimiter func(key string) (bool, time.Duration)

// AccountKey is the AccountLimiter key of a known user.
func AccountKey(userID uuid.UUID) string { return "user:" + userID.String() }

// emailKey is the AccountLimiter key of a login for an unknown e-mail
// address: unknown and known addresses are limited alike (no enumeration
// through the limiter).
func emailKey(email string) string { return "email:" + strings.ToLower(email) }

// invalidInputKey is the shared AccountLimiter key of logins whose e-mail
// address is not even well-formed.
const invalidInputKey = "invalid"

// ServiceOptions configure NewService.
type ServiceOptions struct {
	DB       DB
	Hasher   *Hasher
	Policy   PasswordPolicy
	Sessions *Sessions
	Auditor  *audit.Auditor
	// Factors lists the user's enrolled second factors (required): a
	// login of an account with one is a two-step login (task 1.9).
	Factors FactorLister
	Lockout LockoutPolicy
	// LoginLimiter is the strict per-IP login limiter (nil: unlimited;
	// serve always sets it).
	LoginLimiter LoginLimiter
	// Limits, when set, holds the password and lockout policies instead of
	// Policy and Lockout (shared with TOTP and UserAdmin so a config
	// reload swaps them everywhere, D-200).
	Limits *Limits
	// AccountLimiter is the per-account password-verification limiter
	// (D-117), taken before every argon2id verification of a login,
	// password step-up or password change, keyed by AccountKey (nil:
	// unlimited; serve always sets it). It is separate from the HTTP
	// limiters: one request can only verify one password (single-root
	// credential operations), and this bounds verifications per account
	// across clients.
	AccountLimiter AccountLimiter
	// Notifier is told about committed lockouts (nil: none).
	Notifier LockoutNotifier
	Logger   *slog.Logger
	// Now replaces time.Now (tests).
	Now func() time.Time
}

// Service implements login, logout, password changes and session
// management for the API. It is safe for concurrent use.
type Service struct {
	o        ServiceOptions
	limits   *Limits
	refusals *lockedRefusals
}

// NewService returns the service.
func NewService(o ServiceOptions) (*Service, error) {
	if o.DB == nil || o.Hasher == nil || o.Sessions == nil || o.Auditor == nil || o.Factors == nil {
		return nil, errors.New("auth: DB, Hasher, Sessions, Auditor and Factors are required")
	}
	if o.Limits == nil {
		o.Limits = NewLimits(o.Policy, o.Lockout)
	}
	if o.Notifier == nil {
		o.Notifier = NopLockoutNotifier{}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o, limits: o.Limits, refusals: newLockedRefusals()}, nil
}

// Hasher returns the password hasher (task 1.6 hashes admin-set passwords
// with it).
func (s *Service) Hasher() *Hasher { return s.o.Hasher }

// Policy returns the password policy.
func (s *Service) Policy() PasswordPolicy { return s.limits.Policy() }

// Limits returns the shared password and lockout policies.
func (s *Service) Limits() *Limits { return s.limits }

// Sessions returns the session store.
func (s *Service) Sessions() *Sessions { return s.o.Sessions }

// LoginResult is a completed login or the first (password) step of a
// two-step login.
type LoginResult struct {
	User sqlc.User
	// Issued is the new session; its token goes into the cookie.
	Issued Issued
	// SecondFactorRequired is set when the account has a second factor:
	// the session waits for it (mfa_pending, task 1.9) and can do nothing
	// else but log out.
	SecondFactorRequired bool
	// AvailableFactors are the factor kinds the login can be completed
	// with (recovery codes when unused ones are left).
	AvailableFactors []Factor
	// PasswordChangeRequired is set when the user signed in with a
	// temporary password: the session can do nothing but change it (task
	// 1.6).
	PasswordChangeRequired bool
}

// Login checks email and password. For an account without a second
// factor it creates an AAL1 session with a fresh step-up (the password was
// just entered; D-107 as amended by D-112). For an account with one it
// creates a session waiting for the second factor (mfa_pending, no
// step-up, PendingSecondFactorTTL) and reports the available factors; the
// login completes with SecondFactorFlow.CompleteSecondFactor. Every failure is ErrInvalidCredentials (or a
// *RateLimitedError / ErrBusy for overload), takes about as long as a
// wrong password (a dummy argon2id verification runs whenever no real
// hash is checked) and is audited with a reason code. current is the
// session the browser presented, if any; it is revoked on success so a
// login always rotates the session.
func (s *Service) Login(ctx context.Context, email, password string, meta RequestMeta, current uuid.UUID) (LoginResult, error) {
	if s.o.LoginLimiter != nil {
		if ok, wait := s.o.LoginLimiter(meta.IP); !ok {
			s.o.Logger.WarnContext(ctx, "login rate limit exceeded", slog.String("client_ip", meta.IP.String()))
			return LoginResult{}, &RateLimitedError{RetryAfter: wait}
		}
	}
	email = strings.TrimSpace(email)
	if email == "" || utf8.RuneCountInString(email) > maxEmailLength || !utf8.ValidString(email) {
		if err := s.limitAccount(ctx, invalidInputKey); err != nil {
			return LoginResult{}, err
		}
		return LoginResult{}, s.failUnverified(ctx, password, nil, reasonInvalidInput, meta)
	}
	user, err := sqlc.New(s.o.DB).GetUserByEmail(ctx, email)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if err := s.limitAccount(ctx, emailKey(email)); err != nil {
			return LoginResult{}, err
		}
		return LoginResult{}, s.failUnverified(ctx, password, nil, reasonUnknownUser, meta)
	case err != nil:
		return LoginResult{}, fmt.Errorf("auth: look up user: %w", err)
	}
	if err := s.limitAccount(ctx, AccountKey(user.ID)); err != nil {
		return LoginResult{}, err
	}
	switch {
	case user.Disabled:
		return LoginResult{}, s.failUnverified(ctx, password, &user, reasonDisabled, meta)
	case user.PasswordHash == nil:
		return LoginResult{}, s.failUnverified(ctx, password, &user, reasonNoPassword, meta)
	case user.PasswordTemporaryUntil != nil && !user.PasswordTemporaryUntil.After(s.o.Now()):
		return LoginResult{}, s.failUnverified(ctx, password, &user, reasonTemporaryExpired, meta)
	}
	att, err := s.reserveAttempt(ctx, user.ID)
	if isLocked(err) {
		return LoginResult{}, s.refuseLockedLogin(ctx, password, &user, meta)
	}
	if err != nil {
		return LoginResult{}, err
	}
	ok, rehash, err := s.o.Hasher.Verify(ctx, password, *user.PasswordHash)
	if err != nil && !errors.Is(err, ErrInvalidHash) {
		return LoginResult{}, err
	}
	if errors.Is(err, ErrInvalidHash) {
		s.o.Logger.ErrorContext(ctx, "stored password hash is invalid", slog.String("user_id", user.ID.String()))
	}
	if !ok {
		return LoginResult{}, s.failPassword(ctx, &user, att, audit.ActionAuthLoginFailed, reasonBadPassword, meta, ErrInvalidCredentials)
	}
	factors, err := s.loginFactors(ctx, user.ID)
	if err != nil {
		return LoginResult{}, err
	}
	return s.completeLogin(ctx, user, password, rehash, meta, current, factors)
}

// limitAccount takes one password verification for key from the
// AccountLimiter: a *RateLimitedError when none is left (logged, not
// audited: the limiter exists to bound work under attack).
func (s *Service) limitAccount(ctx context.Context, key string) error {
	if s.o.AccountLimiter == nil {
		return nil
	}
	if ok, wait := s.o.AccountLimiter(key); !ok {
		s.o.Logger.WarnContext(ctx, "password verification rate limit exceeded for an account",
			slog.String("client_ip", RequestMetaFrom(ctx).IP.String()))
		return &RateLimitedError{RetryAfter: wait}
	}
	return nil
}

// reserveAttempt counts a password verification of userID before it runs
// (D-117): it fails with a *RateLimitedError (RetryAfter: the rest of the
// lock) while the account is locked, without counting. Reaching the
// threshold locks the account in the same statement, so concurrent
// attempts can never verify more than the threshold allows.
func (s *Service) reserveAttempt(ctx context.Context, userID uuid.UUID) (attempt, error) {
	threshold, base, maxSecs := s.limits.Lockout().reserveArgs()
	q := sqlc.New(s.o.DB)
	for range 2 {
		now := s.o.Now()
		row, err := q.ReserveLoginAttempt(ctx, sqlc.ReserveLoginAttemptParams{
			ID: userID, Threshold: threshold, AtTime: now, BaseSeconds: base, MaxSeconds: maxSecs,
		})
		if err == nil {
			return attempt{count: row.FailedLoginCount, lockedUntil: row.LockedUntil}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return attempt{}, fmt.Errorf("auth: reserve password attempt: %w", err)
		}
		u, err := q.GetUserByID(ctx, userID)
		if err != nil {
			return attempt{}, fmt.Errorf("auth: load user: %w", err)
		}
		if u.LockedUntil != nil && u.LockedUntil.After(now) {
			return attempt{}, &lockedError{RateLimitedError{RetryAfter: u.LockedUntil.Sub(now)}}
		}
		// Unlocked in between (an admin unlock, a success): try again.
	}
	// Never verify without a reservation.
	return attempt{}, &RateLimitedError{RetryAfter: time.Second}
}

// lockedError is the *RateLimitedError of an attempt refused while the
// account is locked (RetryAfter: the rest of the lock).
type lockedError struct {
	RateLimitedError
}

func (e *lockedError) Unwrap() error { return &e.RateLimitedError }

// isLocked reports whether err is the refusal of a locked account.
func isLocked(err error) bool {
	var le *lockedError
	return errors.As(err, &le)
}

// refuseLockedLogin answers a login of a locked account like any other
// failure (dummy verification for timing, ErrInvalidCredentials) and
// records the refusal, at most once per lock window (D-117).
func (s *Service) refuseLockedLogin(ctx context.Context, password string, user *sqlc.User, meta RequestMeta) error {
	if err := s.o.Hasher.DummyVerify(ctx, password); err != nil {
		return err
	}
	return s.recordLockedRefusal(ctx, user.ID, audit.ActionAuthLoginFailed, audit.SystemActor(), nil, meta, ErrInvalidCredentials)
}

// recordLockedRefusal records an attempt refused while user is locked,
// at most once per lock window (the further refusals are counted and
// reported with the next entry), and returns ret.
func (s *Service) recordLockedRefusal(ctx context.Context, userID uuid.UUID, action audit.Action, actor audit.Actor, extra audit.Details, meta RequestMeta, ret error) error {
	u, err := s.User(ctx, userID)
	if err != nil {
		return err
	}
	now := s.o.Now()
	until := now
	if u.LockedUntil != nil {
		until = *u.LockedUntil
	}
	record, previous := s.refusals.note(userID, until, now)
	if !record {
		return ret
	}
	details := audit.Details{"reason": reasonLocked, "locked_until": until.UTC().Format(time.RFC3339), "aggregated": true}
	for k, v := range extra {
		details[k] = v
	}
	if previous > 0 {
		details["refused_while_locked"] = previous
	}
	if _, err := s.o.Auditor.Record(ctx, audit.Event{
		Actor: actor, Action: action, IP: meta.IP,
		TargetType: "user", TargetID: userID.String(), Details: details,
	}); err != nil {
		return err
	}
	return ret
}

// loginFactors returns the factor kinds userID can complete a login with:
// the enrolled kinds plus recovery codes when unused ones are left (only
// when a factor is enrolled: codes stand in for one).
func (s *Service) loginFactors(ctx context.Context, userID uuid.UUID) ([]Factor, error) {
	enrolled, err := s.o.Factors.Enrolled(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(enrolled) == 0 {
		return nil, nil
	}
	n, err := sqlc.New(s.o.DB).CountUnusedRecoveryCodes(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: count recovery codes: %w", err)
	}
	if n > 0 {
		enrolled = append(enrolled, FactorRecoveryCode)
	}
	return enrolled, nil
}

// completeLogin resets the lockout state, rehashes if needed, rotates the
// session and records the success, all in one transaction.
func (s *Service) completeLogin(ctx context.Context, user sqlc.User, password string, rehash bool, meta RequestMeta, current uuid.UUID, factors []Factor) (LoginResult, error) {
	var newHash string
	if rehash {
		h, err := s.o.Hasher.Hash(ctx, password)
		if err != nil {
			// Not fatal: the next login tries again.
			s.o.Logger.WarnContext(ctx, "password rehash skipped", slog.String("user_id", user.ID.String()), slog.Any("error", err))
		} else {
			newHash = h
		}
	}
	var res LoginResult
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		if err := q.ResetLoginFailures(ctx, user.ID); err != nil {
			return fmt.Errorf("auth: reset login failures: %w", err)
		}
		if newHash != "" {
			if _, err := q.RehashUserPassword(ctx, sqlc.RehashUserPasswordParams{ID: user.ID, NewHash: &newHash, OldHash: user.PasswordHash}); err != nil {
				return fmt.Errorf("auth: store rehashed password: %w", err)
			}
		}
		if current != uuid.Nil {
			n, err := q.RevokeSession(ctx, current)
			if err != nil {
				return fmt.Errorf("auth: revoke previous session: %w", err)
			}
			if n > 0 {
				if err := s.o.Sessions.emit(ctx, q, SessionEvent{Kind: SessionRevoked, SessionID: current}); err != nil {
					return err
				}
			}
		}
		pending := len(factors) > 0
		var issued Issued
		var err error
		if pending {
			issued, err = s.o.Sessions.CreatePendingTx(ctx, q, user.ID, meta)
		} else {
			issued, err = s.o.Sessions.CreateTx(ctx, q, user.ID, AAL1, true, meta)
		}
		if err != nil {
			return err
		}
		details := audit.Details{"session_id": issued.Session.ID.String(), "aal": AAL1, "second_factor_pending": pending}
		if newHash != "" {
			details["rehashed"] = true
		}
		if _, err := s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(user.ID), Action: audit.ActionAuthLoginSucceeded,
			TargetType: "user", TargetID: user.ID.String(), IP: meta.IP, Details: details,
		}); err != nil {
			return err
		}
		res = LoginResult{
			User: user, Issued: issued, PasswordChangeRequired: user.PasswordTemporaryUntil != nil,
			SecondFactorRequired: pending, AvailableFactors: factors,
		}
		return nil
	})
	if err != nil {
		return LoginResult{}, err
	}
	return res, nil
}

// failUnverified handles a login that is refused without checking a real
// hash: it runs the dummy verification (timing), records the failure and
// returns ErrInvalidCredentials. user is nil for an unknown account (the
// attempted e-mail is not recorded: users often type a password into it).
func (s *Service) failUnverified(ctx context.Context, password string, user *sqlc.User, reason string, meta RequestMeta) error {
	if err := s.o.Hasher.DummyVerify(ctx, password); err != nil {
		return err
	}
	ev := audit.Event{
		Actor: audit.SystemActor(), Action: audit.ActionAuthLoginFailed, IP: meta.IP,
		TargetType: "user", Details: audit.Details{"reason": reason},
	}
	if user != nil {
		ev.TargetID = user.ID.String()
	}
	if _, err := s.o.Auditor.Record(ctx, ev); err != nil {
		return err
	}
	return ErrInvalidCredentials
}

// failPassword records a wrong password of an attempt reserved by
// reserveAttempt (already counted) and, when reserving it locked the
// account, the lockout, in one transaction; then notifies about the new
// lockout and returns ret.
func (s *Service) failPassword(ctx context.Context, user *sqlc.User, att attempt, action audit.Action, reason string, meta RequestMeta, ret error) error {
	var locked *LockoutEvent
	refused := s.refusals.take(user.ID)
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		details := audit.Details{"reason": reason, "failed_count": att.count}
		if refused > 0 {
			details["refused_while_locked"] = refused
		}
		if _, err := s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.SystemActor(), Action: action, IP: meta.IP,
			TargetType: "user", TargetID: user.ID.String(), Details: details,
		}); err != nil {
			return err
		}
		if !att.locked() {
			return nil
		}
		until := *att.lockedUntil
		if _, err := s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.SystemActor(), Action: audit.ActionAuthLockout, IP: meta.IP,
			TargetType: "user", TargetID: user.ID.String(),
			Details: audit.Details{
				"failed_count": att.count, "locked_until": until.UTC().Format(time.RFC3339),
				"lock_seconds": int64(s.limits.Lockout().LockFor(int(att.count)) / time.Second),
			},
		}); err != nil {
			return err
		}
		locked = &LockoutEvent{UserID: user.ID, Until: until, Failures: int(att.count), IP: meta.IP}
		return nil
	})
	if err != nil {
		return err
	}
	if locked != nil {
		s.o.Logger.WarnContext(ctx, "account locked after failed logins",
			slog.String("user_id", user.ID.String()), slog.Int("failed_count", locked.Failures),
			slog.Time("locked_until", locked.Until))
		s.o.Notifier.AccountLocked(ctx, *locked)
	}
	return ret
}

// passwordVerified clears the failure count of userID after a verified
// password (the attempt was counted when it was reserved).
func (s *Service) passwordVerified(ctx context.Context, userID uuid.UUID) error {
	if err := sqlc.New(s.o.DB).ResetLoginFailures(ctx, userID); err != nil {
		return fmt.Errorf("auth: reset login failures: %w", err)
	}
	return nil
}

// User returns the user with id.
func (s *Service) User(ctx context.Context, id uuid.UUID) (sqlc.User, error) {
	u, err := sqlc.New(s.o.DB).GetUserByID(ctx, id)
	if err != nil {
		return sqlc.User{}, fmt.Errorf("auth: load user: %w", err)
	}
	return u, nil
}

// Logout revokes the caller's session and records it.
func (s *Service) Logout(ctx context.Context, p Principal, meta RequestMeta) error {
	return db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		if err := s.o.Sessions.RevokeTx(ctx, q, p.UserID, p.SessionID); err != nil {
			return err
		}
		_, err := s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(p.UserID), Action: audit.ActionAuthLogout, IP: meta.IP,
			TargetType: "session", TargetID: p.SessionID.String(),
		})
		return err
	})
}

// ListSessions returns the caller's active sessions.
func (s *Service) ListSessions(ctx context.Context, p Principal) ([]sqlc.Session, error) {
	return s.o.Sessions.List(ctx, p.UserID)
}

// RevokeSession revokes one of the caller's sessions (ErrSessionNotFound
// for any other id) and records it. It reports whether that was the
// caller's current session (the cookie must then be cleared).
func (s *Service) RevokeSession(ctx context.Context, p Principal, id uuid.UUID, meta RequestMeta) (bool, error) {
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		if err := s.o.Sessions.RevokeTx(ctx, q, p.UserID, id); err != nil {
			return err
		}
		_, err := s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(p.UserID), Action: audit.ActionSessionRevoked, IP: meta.IP,
			TargetType: "session", TargetID: id.String(),
			Details: audit.Details{"user_id": p.UserID.String(), "current": id == p.SessionID},
		})
		return err
	})
	if err != nil {
		return false, err
	}
	return id == p.SessionID, nil
}

// ChangePassword changes the caller's password: the current password must
// be right (a wrong one counts towards the lockout), the new one must pass
// the policy and differ from it. Every session of the user is revoked and
// the caller gets a new one (same AAL, step-up and absolute expiry), all
// in one transaction with the audit entry.
func (s *Service) ChangePassword(ctx context.Context, p Principal, current, next string, meta RequestMeta) (Issued, error) {
	user, err := s.User(ctx, p.UserID)
	if err != nil {
		return Issued{}, err
	}
	// The caller owns the account: no dummy verification is needed to
	// hide its state, and none runs (D-117).
	if user.Disabled || user.PasswordHash == nil ||
		(user.PasswordTemporaryUntil != nil && !user.PasswordTemporaryUntil.After(s.o.Now())) {
		return Issued{}, ErrWrongPassword
	}
	if err := s.limitAccount(ctx, AccountKey(user.ID)); err != nil {
		return Issued{}, err
	}
	att, err := s.reserveAttempt(ctx, user.ID)
	if isLocked(err) {
		return Issued{}, s.recordLockedRefusal(ctx, user.ID, audit.ActionAuthPasswordChangeFailed, audit.UserActor(user.ID), nil, meta, err)
	}
	if err != nil {
		return Issued{}, err
	}
	ok, _, err := s.o.Hasher.Verify(ctx, current, *user.PasswordHash)
	if err != nil && !errors.Is(err, ErrInvalidHash) {
		return Issued{}, err
	}
	if !ok {
		return Issued{}, s.failPassword(ctx, &user, att, audit.ActionAuthPasswordChangeFailed, reasonBadPassword, meta, ErrWrongPassword)
	}
	if err := s.passwordVerified(ctx, user.ID); err != nil {
		return Issued{}, err
	}
	if subtle.ConstantTimeCompare([]byte(current), []byte(next)) == 1 {
		return Issued{}, &PolicyError{ReasonUnchanged, "the new password must differ from the current one"}
	}
	if err := s.limits.Policy().Check(next, user.Email, user.DisplayName); err != nil {
		return Issued{}, err
	}
	hash, err := s.o.Hasher.Hash(ctx, next)
	if err != nil {
		return Issued{}, err
	}
	var out Issued
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		old, err := lockActive(ctx, q, p.SessionID)
		if err != nil {
			return err
		}
		if _, err := q.UpdateUserPassword(ctx, sqlc.UpdateUserPasswordParams{ID: user.ID, PasswordHash: &hash}); err != nil {
			return fmt.Errorf("auth: update password: %w", err)
		}
		revoked, err := s.o.Sessions.RevokeAllTx(ctx, q, user.ID)
		if err != nil {
			return err
		}
		out, err = s.o.Sessions.insert(ctx, q, newParams{
			userID: user.ID, aal: int(old.Aal), stepUpAt: old.StepupAt, aal2At: old.Aal2At,
			absolute: old.AbsoluteExpiresAt, meta: meta, mfaPending: old.MfaPending,
		})
		if err != nil {
			return err
		}
		_, err = s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(user.ID), Action: audit.ActionAuthPasswordChanged, IP: meta.IP,
			TargetType: "user", TargetID: user.ID.String(),
			Details: audit.Details{"revoked_sessions": revoked, "session_id": out.Session.ID.String()},
		})
		return err
	})
	if err != nil {
		return Issued{}, err
	}
	return out, nil
}

// VerifyPasswordStepUp checks the password of userID for a step-up (task
// 1.9: only accounts without a second factor step up with the password).
// A wrong password answers ErrWrongPassword and counts towards the login
// lockout (audited as auth.stepup.failed). While the account is locked the
// step-up is refused with a *RateLimitedError before argon2id runs (the
// caller owns the account, so its state needs no hiding; D-117); a
// disabled or password-less account answers ErrWrongPassword. The caller
// records the success.
func (s *Service) VerifyPasswordStepUp(ctx context.Context, userID uuid.UUID, password string, meta RequestMeta) error {
	user, err := s.User(ctx, userID)
	if err != nil {
		return err
	}
	reason := ""
	switch {
	case user.Disabled || user.DeletedAt != nil:
		reason = reasonDisabled
	case user.PasswordHash == nil:
		reason = reasonNoPassword
	case user.PasswordTemporaryUntil != nil && !user.PasswordTemporaryUntil.After(s.o.Now()):
		reason = reasonTemporaryExpired
	}
	if reason != "" {
		if _, err := s.o.Auditor.Record(ctx, audit.Event{
			Actor: audit.UserActor(userID), Action: audit.ActionAuthStepUpFailed, IP: meta.IP,
			TargetType: "user", TargetID: userID.String(),
			Details: audit.Details{"method": string(StepUpPassword), "reason": reason},
		}); err != nil {
			return err
		}
		return ErrWrongPassword
	}
	if err := s.limitAccount(ctx, AccountKey(user.ID)); err != nil {
		return err
	}
	att, err := s.reserveAttempt(ctx, user.ID)
	if isLocked(err) {
		return s.recordLockedRefusal(ctx, user.ID, audit.ActionAuthStepUpFailed, audit.UserActor(user.ID),
			audit.Details{"method": string(StepUpPassword)}, meta, err)
	}
	if err != nil {
		return err
	}
	ok, _, err := s.o.Hasher.Verify(ctx, password, *user.PasswordHash)
	if err != nil && !errors.Is(err, ErrInvalidHash) {
		return err
	}
	if !ok {
		return s.failPassword(ctx, &user, att, audit.ActionAuthStepUpFailed, reasonBadPassword, meta, ErrWrongPassword)
	}
	return s.passwordVerified(ctx, user.ID)
}
