package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// User administration (task 1.6, D-110).
const (
	// TemporaryPasswordTTL is how long an admin-issued temporary password
	// can be used to sign in (it must then be changed at once).
	TemporaryPasswordTTL = 72 * time.Hour
	// temporaryPasswordBytes is the entropy of a temporary password (160
	// bits).
	temporaryPasswordBytes = 20
	// MaxUserPage bounds one page of ListUsers.
	MaxUserPage = 200
)

// User administration errors.
var (
	// ErrUserNotFound means the account does not exist or is deleted.
	ErrUserNotFound = errors.New("auth: user not found")
	// ErrEmailTaken means another (not deleted) account has the address.
	ErrEmailTaken = errors.New("auth: e-mail address already in use")
	// ErrLastAdmin means the change would leave no active admin.
	ErrLastAdmin = errors.New("auth: the last active admin cannot be removed, demoted or disabled")
	// ErrSelfAction means an admin tried an action on their own account
	// that must go through another path (own password: changePassword;
	// deleting oneself is not allowed).
	ErrSelfAction = errors.New("auth: not allowed on your own account")
)

// Actor is who performs a user administration action: a signed-in admin
// (API) or a host operator (`bekup admin`, D-069).
type Actor struct {
	// UserID is the acting admin (uuid.Nil for the CLI).
	UserID uuid.UUID
	// CLI marks a break-glass CLI action (audited with actor kind cli,
	// OS user and hostname).
	CLI bool
	// IP is the client address of an API caller.
	IP netip.Addr
}

// AdminActor is a signed-in admin acting through the API.
func AdminActor(p Principal, meta RequestMeta) Actor { return Actor{UserID: p.UserID, IP: meta.IP} }

// OperatorActor is a host operator running `bekup admin`.
func OperatorActor() Actor { return Actor{CLI: true} }

func (a Actor) self(id uuid.UUID) bool { return !a.CLI && a.UserID != uuid.Nil && a.UserID == id }

// event builds the audit event: api is the action for an admin, cli for
// the CLI (the API action when cli is empty).
func (a Actor) event(api, cli audit.Action, targetID uuid.UUID, d audit.Details) audit.Event {
	if a.CLI {
		if cli == "" {
			cli = api
		}
		// The CLI auditor's observer (notify.ObserveAudit) raises the
		// security.system alert to all admins for every break-glass CLI
		// action (D-069, task 5.5).
		return audit.CLIEvent(cli, "user", targetID.String(), d)
	}
	return audit.Event{
		Actor: audit.UserActor(a.UserID), Action: api, IP: a.IP,
		TargetType: "user", TargetID: targetID.String(), Details: d,
	}
}

// UserAdminOptions configure NewUserAdmin.
type UserAdminOptions struct {
	DB     DB
	Hasher *Hasher
	Policy PasswordPolicy
	// Limits, when set, replaces Policy with the shared, reloadable
	// password policy (D-200).
	Limits   *Limits
	Sessions *Sessions
	Auditor  *audit.Auditor
	Logger   *slog.Logger
	// FactorReset is told about committed second-factor resets (nil:
	// nobody; the security alerts come from the audit entries through
	// the audit observer, task 5.5).
	FactorReset FactorResetNotifier
	// Now replaces time.Now (tests).
	Now func() time.Time
}

// FactorResetEvent describes a committed reset of all second factors of
// an account (lost-factor recovery, D-112).
type FactorResetEvent struct {
	// ActorID is the acting admin (uuid.Nil for the CLI).
	ActorID uuid.UUID
	CLI     bool
	UserID  uuid.UUID
}

// FactorResetNotifier is told about committed second-factor resets.
type FactorResetNotifier interface {
	SecondFactorsReset(ctx context.Context, ev FactorResetEvent)
}

// NopFactorResetNotifier ignores every event (the alert comes from the
// audit entry, task 5.5).
type NopFactorResetNotifier struct{}

// SecondFactorsReset does nothing.
func (NopFactorResetNotifier) SecondFactorsReset(context.Context, FactorResetEvent) {}

// UserAdmin manages accounts for admins (GraphQL) and the break-glass CLI:
// create, update (display name, role, disabled), password resets, unlock,
// soft delete and other users' sessions. Every change is audited in the
// transaction that makes it; role changes, disabling, password resets and
// deletion revoke all sessions of the account; at least one active admin
// always remains.
type UserAdmin struct {
	o UserAdminOptions
}

// NewUserAdmin returns the service.
func NewUserAdmin(o UserAdminOptions) (*UserAdmin, error) {
	if o.DB == nil || o.Hasher == nil || o.Sessions == nil || o.Auditor == nil {
		return nil, errors.New("auth: DB, Hasher, Sessions and Auditor are required")
	}
	if o.Limits == nil {
		o.Limits = NewLimits(o.Policy, LockoutPolicy{})
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.FactorReset == nil {
		o.FactorReset = NopFactorResetNotifier{}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &UserAdmin{o: o}, nil
}

// Policy returns the password policy.
func (s *UserAdmin) Policy() PasswordPolicy { return s.o.Limits.Policy() }

// TemporaryPassword is a generated one-time password. It is shown to the
// admin once and never stored or logged in plain text.
type TemporaryPassword struct {
	Password  string
	ExpiresAt time.Time
}

// newTemporaryPassword generates a temporary password and its hash.
func (s *UserAdmin) newTemporaryPassword(ctx context.Context) (TemporaryPassword, string, error) {
	pw, err := randomCode(temporaryPasswordBytes)
	if err != nil {
		return TemporaryPassword{}, "", err
	}
	hash, err := s.o.Hasher.Hash(ctx, pw)
	if err != nil {
		return TemporaryPassword{}, "", err
	}
	return TemporaryPassword{Password: pw, ExpiresAt: s.o.Now().Add(TemporaryPasswordTTL)}, hash, nil
}

// Get returns the account id (also a deleted one).
func (s *UserAdmin) Get(ctx context.Context, id uuid.UUID) (sqlc.User, error) {
	u, err := sqlc.New(s.o.DB).GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.User{}, ErrUserNotFound
	}
	if err != nil {
		return sqlc.User{}, fmt.Errorf("auth: load user: %w", err)
	}
	return u, nil
}

// ByEmail returns the account (not deleted) with the e-mail address.
func (s *UserAdmin) ByEmail(ctx context.Context, email string) (sqlc.User, error) {
	email = strings.TrimSpace(email)
	if email == "" || len(email) > 4*maxEmailLength {
		return sqlc.User{}, ErrUserNotFound
	}
	u, err := sqlc.New(s.o.DB).GetActiveUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.User{}, ErrUserNotFound
	}
	if err != nil {
		return sqlc.User{}, fmt.Errorf("auth: load user: %w", err)
	}
	return u, nil
}

// UserFilter narrows List; zero values do not filter.
type UserFilter struct {
	// Search matches a substring of the e-mail address or display name
	// (case-insensitive).
	Search         string
	Role           Role
	Disabled       *bool
	IncludeDeleted bool
}

// UserCursor is the position after the last user of a page.
type UserCursor struct {
	Email string
	ID    uuid.UUID
}

// List returns up to limit users after cursor (nil: from the start),
// ordered by e-mail address, and whether more follow.
func (s *UserAdmin) List(ctx context.Context, f UserFilter, after *UserCursor, limit int) ([]sqlc.User, bool, error) {
	if limit < 1 || limit > MaxUserPage {
		return nil, false, fmt.Errorf("auth: page size %d out of range", limit)
	}
	p := sqlc.ListUsersParams{Lim: int32(limit + 1)}
	if f.IncludeDeleted {
		t := true
		p.IncludeDeleted = &t
	}
	if f.Role != "" {
		if !f.Role.Valid() {
			return nil, false, &FieldError{"filter.role", "unknown role"}
		}
		r := sqlc.UserRole(f.Role)
		p.Role = &r
	}
	p.Disabled = f.Disabled
	if f.Search != "" {
		esc := likeEscaper.Replace(f.Search)
		p.Search = &esc
	}
	if after != nil {
		p.AfterEmail, p.AfterID = &after.Email, &after.ID
	}
	rows, err := sqlc.New(s.o.DB).ListUsers(ctx, p)
	if err != nil {
		return nil, false, fmt.Errorf("auth: list users: %w", err)
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	return rows, more, nil
}

// DirectoryEntry is the minimal view of an account in the user directory
// (the owner picker for admins and maintainers, D-184): no password,
// lockout, second-factor or session state.
type DirectoryEntry = sqlc.ListUserDirectoryRow

// Directory returns up to limit accounts that are not deleted, after
// cursor (nil: from the start), ordered by e-mail address, and whether
// more follow. search (may be empty) matches a substring of the e-mail
// address or display name (case-insensitive).
func (s *UserAdmin) Directory(ctx context.Context, search string, after *UserCursor, limit int) ([]DirectoryEntry, bool, error) {
	if limit < 1 || limit > MaxUserPage {
		return nil, false, fmt.Errorf("auth: page size %d out of range", limit)
	}
	p := sqlc.ListUserDirectoryParams{Lim: int32(limit + 1)}
	if search != "" {
		esc := likeEscaper.Replace(search)
		p.Search = &esc
	}
	if after != nil {
		p.AfterEmail, p.AfterID = &after.Email, &after.ID
	}
	rows, err := sqlc.New(s.o.DB).ListUserDirectory(ctx, p)
	if err != nil {
		return nil, false, fmt.Errorf("auth: list user directory: %w", err)
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	return rows, more, nil
}

// MaxActivityBatch bounds the accounts of one Activity call.
const MaxActivityBatch = 500

// UserActivity is the admin-only activity summary of an account.
type UserActivity struct {
	// LastSeenAt is the most recent activity of any of its sessions (nil:
	// none on record; revoked and expired sessions count until they are
	// purged, sessions waiting for their second factor never).
	LastSeenAt *time.Time
	// TwoFactorEnrolled reports a TOTP authenticator or a passkey.
	TwoFactorEnrolled bool
}

// Activity returns the activity summary of the given accounts in one
// query (unknown ids are absent from the result).
func (s *UserAdmin) Activity(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]UserActivity, error) {
	if len(ids) > MaxActivityBatch {
		return nil, fmt.Errorf("auth: %d accounts exceed the activity batch", len(ids))
	}
	out := make(map[uuid.UUID]UserActivity, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := sqlc.New(s.o.DB).UserActivityFacts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("auth: load user activity: %w", err)
	}
	for _, r := range rows {
		a := UserActivity{TwoFactorEnrolled: r.TwoFactorEnrolled}
		if r.LastSeenAt.After(time.Unix(0, 0)) {
			t := r.LastSeenAt
			a.LastSeenAt = &t
		}
		out[r.ID] = a
	}
	return out, nil
}

// likeEscaper escapes the LIKE wildcards (ESCAPE '\').
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// NewUser is the input of Create.
type NewUser struct {
	Email       string
	DisplayName string
	Role        Role
	// Password is the initial password chosen by a CLI operator (checked
	// against the policy). Empty: a temporary password is generated that
	// must be changed at the first login (the API never accepts an
	// admin-chosen password).
	Password string
}

// Create creates an account. With an empty Password the result carries
// the generated temporary password. The first admin is created this way
// by `bekup admin create` (D-113).
func (s *UserAdmin) Create(ctx context.Context, a Actor, in NewUser) (sqlc.User, *TemporaryPassword, error) {
	email, err := NormalizeEmail("email", in.Email)
	if err != nil {
		return sqlc.User{}, nil, err
	}
	name, err := NormalizeDisplayName("displayName", in.DisplayName)
	if err != nil {
		return sqlc.User{}, nil, err
	}
	if !in.Role.Valid() {
		return sqlc.User{}, nil, &FieldError{"role", "unknown role"}
	}
	var (
		hash string
		temp *TemporaryPassword
	)
	if in.Password != "" {
		if err := s.o.Limits.Policy().Check(in.Password, email, name); err != nil {
			return sqlc.User{}, nil, err
		}
		if hash, err = s.o.Hasher.Hash(ctx, in.Password); err != nil {
			return sqlc.User{}, nil, err
		}
	} else {
		tp, h, err := s.newTemporaryPassword(ctx)
		if err != nil {
			return sqlc.User{}, nil, err
		}
		temp, hash = &tp, h
	}
	var user sqlc.User
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		p := sqlc.CreateUserParams{Email: email, DisplayName: name, PasswordHash: &hash, Role: sqlc.UserRole(in.Role)}
		if temp != nil {
			p.PasswordTemporaryUntil = &temp.ExpiresAt
		}
		u, err := q.CreateUser(ctx, p)
		if isUniqueViolation(err) {
			return ErrEmailTaken
		}
		if err != nil {
			return fmt.Errorf("auth: create user: %w", err)
		}
		user = u
		_, err = s.o.Auditor.RecordTx(ctx, q, a.event(audit.ActionUserCreated, audit.ActionCLIAdminCreated, u.ID, audit.Details{
			"email": email, "role": string(in.Role), "temporary_login_issued": temp != nil,
		}))
		return err
	})
	if err != nil {
		return sqlc.User{}, nil, err
	}
	return user, temp, nil
}

// UserChanges is the input of Update; nil fields stay unchanged.
type UserChanges struct {
	DisplayName *string
	Role        *Role
	Disabled    *bool
}

// UpdateResult is a completed Update.
type UpdateResult struct {
	User sqlc.User
	// RevokedSessions is the number of the account's sessions revoked (a
	// role change or disabling revokes all of them).
	RevokedSessions int64
}

// Update changes the display name, role and/or disabled flag. A role
// change or disabling revokes every session of the account (privilege
// change, D-047). Demoting or disabling the last active admin fails with
// ErrLastAdmin (checked under row locks, so concurrent changes cannot
// both pass).
func (s *UserAdmin) Update(ctx context.Context, a Actor, id uuid.UUID, c UserChanges) (UpdateResult, error) {
	var name string
	if c.DisplayName != nil {
		n, err := NormalizeDisplayName("displayName", *c.DisplayName)
		if err != nil {
			return UpdateResult{}, err
		}
		name = n
	}
	if c.Role != nil && !c.Role.Valid() {
		return UpdateResult{}, &FieldError{"role", "unknown role"}
	}
	var res UpdateResult
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		admins, err := q.LockActiveAdmins(ctx)
		if err != nil {
			return fmt.Errorf("auth: lock admins: %w", err)
		}
		u, err := lockUser(ctx, q, id)
		if err != nil {
			return err
		}
		demote := c.Role != nil && u.Role == sqlc.UserRoleAdmin && *c.Role != RoleAdmin
		disable := c.Disabled != nil && *c.Disabled && !u.Disabled
		if (demote || disable) && slices.Contains(admins, u.ID) && len(admins) <= 1 {
			return ErrLastAdmin
		}
		revoke := false
		if c.DisplayName != nil && name != u.DisplayName {
			if err := q.UpdateUserDisplayName(ctx, sqlc.UpdateUserDisplayNameParams{ID: id, DisplayName: name}); err != nil {
				return fmt.Errorf("auth: update display name: %w", err)
			}
			if _, err := s.o.Auditor.RecordTx(ctx, q, a.event(audit.ActionUserUpdated, "", id, audit.Details{
				"display_name": name, "previous_display_name": u.DisplayName,
			})); err != nil {
				return err
			}
		}
		if c.Role != nil && sqlc.UserRole(*c.Role) != u.Role {
			if err := q.UpdateUserRole(ctx, sqlc.UpdateUserRoleParams{ID: id, Role: sqlc.UserRole(*c.Role)}); err != nil {
				return fmt.Errorf("auth: update role: %w", err)
			}
			if _, err := s.o.Auditor.RecordTx(ctx, q, a.event(audit.ActionUserRoleChanged, "", id, audit.Details{
				"role": string(*c.Role), "previous_role": string(u.Role),
			})); err != nil {
				return err
			}
			revoke = true
		}
		if c.Disabled != nil && *c.Disabled != u.Disabled {
			if err := q.SetUserDisabled(ctx, sqlc.SetUserDisabledParams{ID: id, Disabled: *c.Disabled}); err != nil {
				return fmt.Errorf("auth: set disabled: %w", err)
			}
			action, cli := audit.ActionUserEnabled, audit.Action("")
			if *c.Disabled {
				action, cli = audit.ActionUserDisabled, audit.ActionCLIAdminDisabled
				revoke = true
			}
			if _, err := s.o.Auditor.RecordTx(ctx, q, a.event(action, cli, id, nil)); err != nil {
				return err
			}
		}
		if revoke {
			n, err := s.revokeAll(ctx, q, a, id, "privilege_change")
			if err != nil {
				return err
			}
			res.RevokedSessions = n
		}
		res.User, err = q.GetUserByID(ctx, id)
		if err != nil {
			return fmt.Errorf("auth: reload user: %w", err)
		}
		return nil
	})
	if err != nil {
		return UpdateResult{}, err
	}
	return res, nil
}

// SetDisabled disables or enables the account (see Update).
func (s *UserAdmin) SetDisabled(ctx context.Context, a Actor, id uuid.UUID, disabled bool) (UpdateResult, error) {
	return s.Update(ctx, a, id, UserChanges{Disabled: &disabled})
}

// ResetPassword sets a new password, clears the lockout and revokes every
// session of the account. password is the new password chosen by a CLI
// operator (checked against the policy, permanent); empty generates a
// temporary password (returned) that must be changed at the next login.
// Admins reset their own password with changePassword (ErrSelfAction).
func (s *UserAdmin) ResetPassword(ctx context.Context, a Actor, id uuid.UUID, password string) (*TemporaryPassword, int64, error) {
	if a.self(id) {
		return nil, 0, ErrSelfAction
	}
	u, err := s.Get(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	if u.DeletedAt != nil {
		return nil, 0, ErrUserNotFound
	}
	var (
		hash string
		temp *TemporaryPassword
	)
	if password != "" {
		if err := s.o.Limits.Policy().Check(password, u.Email, u.DisplayName); err != nil {
			return nil, 0, err
		}
		if hash, err = s.o.Hasher.Hash(ctx, password); err != nil {
			return nil, 0, err
		}
	} else {
		tp, h, err := s.newTemporaryPassword(ctx)
		if err != nil {
			return nil, 0, err
		}
		temp, hash = &tp, h
	}
	var revoked int64
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		if _, err := lockUser(ctx, q, id); err != nil {
			return err
		}
		p := sqlc.SetUserPasswordParams{ID: id, PasswordHash: &hash}
		if temp != nil {
			p.PasswordTemporaryUntil = &temp.ExpiresAt
		}
		if _, err := q.SetUserPassword(ctx, p); err != nil {
			return fmt.Errorf("auth: set password: %w", err)
		}
		n, err := s.o.Sessions.RevokeAllTx(ctx, q, id)
		if err != nil {
			return err
		}
		revoked = n
		_, err = s.o.Auditor.RecordTx(ctx, q, a.event(audit.ActionUserPasswordReset, audit.ActionCLIAdminPasswordReset, id, audit.Details{
			"temporary_login_issued": temp != nil, "revoked_sessions": n, "lockout_cleared": true,
		}))
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	return temp, revoked, nil
}

// SecondFactorReset is a completed ResetSecondFactors.
type SecondFactorReset struct {
	User sqlc.User
	// HadTOTP: TOTP was enabled.
	HadTOTP bool
	// Passkeys and RecoveryCodes are the numbers of rows removed.
	Passkeys      int64
	RecoveryCodes int64
	// RevokedSessions is the number of the account's sessions revoked.
	RevokedSessions int64
}

// ResetSecondFactors is the lost-factor recovery (task 1.9, D-112): it
// removes every second factor of the account (TOTP, also a pending
// enrollment; passkeys and open WebAuthn ceremonies; recovery codes),
// clears the second-factor lockout, sets users.mfa_reset_at (a new 2FA
// enrollment grace period starts then) and revokes every session, all in
// one audited transaction. Admins cannot reset their own factors
// (ErrSelfAction): they remove them one by one with step-up, or another
// admin or the CLI resets them.
func (s *UserAdmin) ResetSecondFactors(ctx context.Context, a Actor, id uuid.UUID) (SecondFactorReset, error) {
	if a.self(id) {
		return SecondFactorReset{}, ErrSelfAction
	}
	var res SecondFactorReset
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		u, err := lockUser(ctx, q, id)
		if err != nil {
			return err
		}
		res.HadTOTP = u.TotpSecretEnc != nil
		if _, err := q.ResetUserSecondFactorState(ctx, id); err != nil {
			return fmt.Errorf("auth: reset TOTP: %w", err)
		}
		if res.Passkeys, err = q.DeleteUserWebAuthnCredentials(ctx, id); err != nil {
			return fmt.Errorf("auth: delete passkeys: %w", err)
		}
		if _, err := q.DeleteUserWebAuthnCeremonies(ctx, id); err != nil {
			return fmt.Errorf("auth: delete passkey ceremonies: %w", err)
		}
		if res.RecoveryCodes, err = q.DeleteRecoveryCodes(ctx, id); err != nil {
			return fmt.Errorf("auth: delete recovery codes: %w", err)
		}
		if res.RevokedSessions, err = s.o.Sessions.RevokeAllTx(ctx, q, id); err != nil {
			return err
		}
		if _, err := s.o.Auditor.RecordTx(ctx, q, a.event(audit.ActionUserSecondFactorsReset, audit.ActionCLIAdminMFAReset, id, audit.Details{
			"email": u.Email, "authenticator_app_removed": res.HadTOTP, "passkeys": res.Passkeys,
			"recovery_codes": res.RecoveryCodes, "revoked_sessions": res.RevokedSessions,
		})); err != nil {
			return err
		}
		res.User, err = q.GetUserByID(ctx, id)
		if err != nil {
			return fmt.Errorf("auth: reload user: %w", err)
		}
		return nil
	})
	if err != nil {
		return SecondFactorReset{}, err
	}
	s.o.Logger.WarnContext(ctx, "all second factors of an account were reset",
		slog.String("user_id", id.String()), slog.Bool("cli", a.CLI), slog.String("actor_id", a.UserID.String()),
		slog.Bool("authenticator_app_removed", res.HadTOTP), slog.Int64("passkeys", res.Passkeys), slog.Int64("revoked_sessions", res.RevokedSessions))
	s.o.FactorReset.SecondFactorsReset(ctx, FactorResetEvent{ActorID: a.UserID, CLI: a.CLI, UserID: id})
	return res, nil
}

// Unlock clears the login lockout of the account.
func (s *UserAdmin) Unlock(ctx context.Context, a Actor, id uuid.UUID) (sqlc.User, error) {
	var out sqlc.User
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		u, err := lockUser(ctx, q, id)
		if err != nil {
			return err
		}
		if _, err := q.UnlockUser(ctx, id); err != nil {
			return fmt.Errorf("auth: unlock: %w", err)
		}
		d := audit.Details{"failed_count": u.FailedLoginCount, "was_locked": u.LockedUntil != nil && u.LockedUntil.After(s.o.Now())}
		if _, err := s.o.Auditor.RecordTx(ctx, q, a.event(audit.ActionUserUnlocked, audit.ActionCLIAdminUnlocked, id, d)); err != nil {
			return err
		}
		out, err = q.GetUserByID(ctx, id)
		if err != nil {
			return fmt.Errorf("auth: reload user: %w", err)
		}
		return nil
	})
	return out, err
}

// Delete soft deletes the account: it is disabled, its password removed,
// every session revoked and the row kept (audit entries reference it).
// Admins cannot delete themselves (ErrSelfAction) or the last active
// admin (ErrLastAdmin).
func (s *UserAdmin) Delete(ctx context.Context, a Actor, id uuid.UUID) error {
	if a.self(id) {
		return ErrSelfAction
	}
	return db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		admins, err := q.LockActiveAdmins(ctx)
		if err != nil {
			return fmt.Errorf("auth: lock admins: %w", err)
		}
		u, err := lockUser(ctx, q, id)
		if err != nil {
			return err
		}
		if slices.Contains(admins, u.ID) && len(admins) <= 1 {
			return ErrLastAdmin
		}
		n, err := s.o.Sessions.RevokeAllTx(ctx, q, id)
		if err != nil {
			return err
		}
		if _, err := q.SoftDeleteUser(ctx, id); err != nil {
			return fmt.Errorf("auth: delete user: %w", err)
		}
		_, err = s.o.Auditor.RecordTx(ctx, q, a.event(audit.ActionUserDeleted, "", id, audit.Details{
			"email": u.Email, "role": string(u.Role), "revoked_sessions": n,
		}))
		return err
	})
}

// Sessions returns the active sessions of the account.
func (s *UserAdmin) Sessions(ctx context.Context, id uuid.UUID) ([]sqlc.Session, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	return s.o.Sessions.List(ctx, id)
}

// RevokeSessions revokes every session of the account and returns how
// many were active.
func (s *UserAdmin) RevokeSessions(ctx context.Context, a Actor, id uuid.UUID) (int64, error) {
	var n int64
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		if _, err := s.Get(ctx, id); err != nil {
			return err
		}
		var err error
		n, err = s.revokeAll(ctx, q, a, id, "admin")
		return err
	})
	return n, err
}

// revokeAll revokes every session of id and records it.
func (s *UserAdmin) revokeAll(ctx context.Context, q *sqlc.Queries, a Actor, id uuid.UUID, reason string) (int64, error) {
	n, err := s.o.Sessions.RevokeAllTx(ctx, q, id)
	if err != nil {
		return 0, err
	}
	_, err = s.o.Auditor.RecordTx(ctx, q, a.event(audit.ActionSessionsRevoked, "", id, audit.Details{
		"revoked_sessions": n, "reason": reason,
	}))
	return n, err
}

// lockUser locks the (not deleted) account id.
func lockUser(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (sqlc.User, error) {
	u, err := q.GetUserForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.User{}, ErrUserNotFound
	}
	if err != nil {
		return sqlc.User{}, fmt.Errorf("auth: lock user: %w", err)
	}
	return u, nil
}

// ActiveAdminExists reports whether an active (not disabled, not deleted)
// admin account exists. serve warns at startup when none does (D-113).
func (s *UserAdmin) ActiveAdminExists(ctx context.Context) (bool, error) {
	ok, err := sqlc.New(s.o.DB).ActiveAdminExists(ctx)
	if err != nil {
		return false, fmt.Errorf("auth: check for an active admin: %w", err)
	}
	return ok, nil
}
