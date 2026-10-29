package resolvers

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/httpserver"
)

// User administration resolvers (task 1.6; the first admin is created
// with `bekup admin create` only, D-113).

var (
	errUsersNotConfigured = errors.New("resolvers: user admin service not configured")
	errUserNotFound       = apierror.New(apierror.NotFound, "user not found")
)

const (
	userCursorPrefix = "user:"
	maxUserCursorLen = 512
	maxUserSearchLen = 200
	defaultUserPage  = 50
)

// userAdminError maps UserAdmin errors to API errors.
func userAdminError(err error) error {
	var (
		fe *auth.FieldError
		pe *auth.PolicyError
		rl *auth.RateLimitedError
	)
	switch {
	case err == nil:
		return nil
	case errors.As(err, &fe):
		return apierror.Invalid(fe.Field, fe.Message)
	case errors.As(err, &pe):
		return apierror.Invalid("password", pe.Message)
	case errors.As(err, &rl):
		return apierror.Newf(apierror.RateLimited, "too many attempts; try again in %d seconds",
			httpserver.RetryAfterSeconds(rl.RetryAfter))
	case errors.Is(err, auth.ErrBusy):
		return errBusy
	case errors.Is(err, auth.ErrUserNotFound):
		return errUserNotFound
	case errors.Is(err, auth.ErrEmailTaken):
		return apierror.New(apierror.Conflict, "another account already uses this e-mail address")
	case errors.Is(err, auth.ErrLastAdmin):
		return apierror.New(apierror.Conflict, "at least one active admin must remain")
	case errors.Is(err, auth.ErrSelfAction):
		return apierror.New(apierror.Forbidden, "not allowed on your own account")
	default:
		return err
	}
}

// adminCaller returns the user admin service and the acting admin.
func (r *Resolver) adminCaller(ctx context.Context) (*auth.UserAdmin, auth.Principal, auth.Actor, error) {
	if r.Users == nil {
		return nil, auth.Principal{}, auth.Actor{}, errUsersNotConfigured
	}
	p, err := caller(ctx)
	if err != nil {
		return nil, auth.Principal{}, auth.Actor{}, err
	}
	return r.Users, p, auth.AdminActor(p, auth.RequestMetaFrom(ctx)), nil
}

func encodeUserCursor(id uuid.UUID, email string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(userCursorPrefix + id.String() + ":" + email))
}

func decodeUserCursor(after *string) (*auth.UserCursor, error) {
	if after == nil || *after == "" {
		return nil, nil
	}
	errBad := apierror.Invalid("after", "invalid cursor")
	if len(*after) > maxUserCursorLen {
		return nil, errBad
	}
	raw, err := base64.RawURLEncoding.DecodeString(*after)
	if err != nil || !utf8.Valid(raw) {
		return nil, errBad
	}
	rest, ok := strings.CutPrefix(string(raw), userCursorPrefix)
	if !ok {
		return nil, errBad
	}
	idText, email, ok := strings.Cut(rest, ":")
	if !ok || email == "" {
		return nil, errBad
	}
	id, err := uuid.Parse(idText)
	if err != nil || id.String() != idText {
		return nil, errBad
	}
	return &auth.UserCursor{Email: email, ID: id}, nil
}

func (r *Resolver) users(ctx context.Context, filter *model.UserFilter, first *int, after *string) (*model.UserConnection, error) {
	svc, _, _, err := r.adminCaller(ctx)
	if err != nil {
		return nil, err
	}
	limit := defaultUserPage
	if first != nil {
		if *first < 1 || *first > auth.MaxUserPage {
			return nil, apierror.Invalid("first", "first must be between 1 and 200")
		}
		limit = *first
	}
	cur, err := decodeUserCursor(after)
	if err != nil {
		return nil, err
	}
	var f auth.UserFilter
	if filter != nil {
		if f.Search, err = cleanUserSearch("filter.search", filter.Search); err != nil {
			return nil, err
		}
		if filter.Role != nil {
			role, ok := authRole(*filter.Role)
			if !ok {
				return nil, apierror.Invalid("filter.role", "unknown role")
			}
			f.Role = role
		}
		f.Disabled = filter.Disabled
		f.IncludeDeleted = filter.IncludeDeleted != nil && *filter.IncludeDeleted
	}
	rows, more, err := svc.List(ctx, f, cur, limit)
	if err != nil {
		return nil, userAdminError(err)
	}
	conn := &model.UserConnection{Edges: make([]model.UserEdge, 0, len(rows)), PageInfo: &model.PageInfo{HasNextPage: more, HasPreviousPage: cur != nil}}
	ids := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		u, err := userModel(rows[i])
		if err != nil {
			return nil, err
		}
		conn.Edges = append(conn.Edges, model.UserEdge{Cursor: encodeUserCursor(rows[i].ID, rows[i].Email), Node: u})
		ids = append(ids, rows[i].ID)
	}
	// User.lastSeenAt / twoFactorEnrolled load for the whole page at once.
	hintUsers(ctx, ids)
	if n := len(conn.Edges); n > 0 {
		start, end := conn.Edges[0].Cursor, conn.Edges[n-1].Cursor
		conn.PageInfo.StartCursor, conn.PageInfo.EndCursor = &start, &end
	}
	return conn, nil
}

// cleanUserSearch validates a user search string (trimmed, at most
// maxUserSearchLen characters, no NUL).
func cleanUserSearch(field string, in *string) (string, error) {
	if in == nil {
		return "", nil
	}
	s := strings.TrimSpace(*in)
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxUserSearchLen || strings.ContainsRune(s, 0) {
		return "", apierror.Invalid(field, "search must be at most 200 characters")
	}
	return s, nil
}

// userDirectory lists the minimal facts of active accounts for the owner
// picker (admins and maintainers, D-184). It never returns more than
// UserDirectoryEntry carries.
func (r *Resolver) userDirectory(ctx context.Context, search *string, first *int, after *string) (*model.UserDirectoryConnection, error) {
	if r.Users == nil {
		return nil, errUsersNotConfigured
	}
	if _, err := caller(ctx); err != nil {
		return nil, err
	}
	limit := defaultUserPage
	if first != nil {
		if *first < 1 || *first > auth.MaxUserPage {
			return nil, apierror.Invalid("first", "first must be between 1 and 200")
		}
		limit = *first
	}
	cur, err := decodeUserCursor(after)
	if err != nil {
		return nil, err
	}
	q, err := cleanUserSearch("search", search)
	if err != nil {
		return nil, err
	}
	rows, more, err := r.Users.Directory(ctx, q, cur, limit)
	if err != nil {
		return nil, userAdminError(err)
	}
	conn := &model.UserDirectoryConnection{
		Edges:    make([]model.UserDirectoryEdge, 0, len(rows)),
		PageInfo: &model.PageInfo{HasNextPage: more, HasPreviousPage: cur != nil},
	}
	for i := range rows {
		role, err := modelRole(auth.Role(rows[i].Role))
		if err != nil {
			return nil, err
		}
		conn.Edges = append(conn.Edges, model.UserDirectoryEdge{Cursor: encodeUserCursor(rows[i].ID, rows[i].Email), Node: &model.UserDirectoryEntry{
			ID: rows[i].ID, DisplayName: rows[i].DisplayName, Email: rows[i].Email, Role: role, Disabled: rows[i].Disabled,
		}})
	}
	if n := len(conn.Edges); n > 0 {
		start, end := conn.Edges[0].Cursor, conn.Edges[n-1].Cursor
		conn.PageInfo.StartCursor, conn.PageInfo.EndCursor = &start, &end
	}
	return conn, nil
}

// userActivity returns the admin facts of u. lastSeenAt is admin only;
// twoFactorEnrolled also for the account itself (selfOK). Others get
// FORBIDDEN (the User type is also reachable through me and other
// fields).
func (r *Resolver) userActivity(ctx context.Context, u *model.User, selfOK bool) (auth.UserActivity, error) {
	if r.Users == nil {
		return auth.UserActivity{}, errUsersNotConfigured
	}
	p, err := caller(ctx)
	if err != nil {
		return auth.UserActivity{}, err
	}
	if !selfOK || p.UserID != u.ID {
		if r.Authz == nil {
			return auth.UserActivity{}, errors.New("resolvers: authz policy not configured")
		}
		if err := r.Authz.Authorize(ctx, authz.OpUserView); err != nil {
			return auth.UserActivity{}, err
		}
	}
	return r.loadUserActivity(ctx, u.ID)
}

func (r *Resolver) userLastSeenAt(ctx context.Context, u *model.User) (*time.Time, error) {
	a, err := r.userActivity(ctx, u, false)
	return a.LastSeenAt, err
}

func (r *Resolver) userTwoFactorEnrolled(ctx context.Context, u *model.User) (bool, error) {
	a, err := r.userActivity(ctx, u, true)
	return a.TwoFactorEnrolled, err
}

func (r *Resolver) user(ctx context.Context, id uuid.UUID) (*model.User, error) {
	svc, _, _, err := r.adminCaller(ctx)
	if err != nil {
		return nil, err
	}
	u, err := svc.Get(ctx, id)
	if errors.Is(err, auth.ErrUserNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return userModel(u)
}

func (r *Resolver) userSessions(ctx context.Context, userID uuid.UUID) ([]model.Session, error) {
	svc, p, _, err := r.adminCaller(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := svc.Sessions(ctx, userID)
	if err != nil {
		return nil, userAdminError(err)
	}
	out := make([]model.Session, 0, len(rows))
	for i := range rows {
		out = append(out, sessionModel(&rows[i], p.SessionID))
	}
	return out, nil
}

func (r *Resolver) createUser(ctx context.Context, in model.CreateUserInput) (*model.CreatedUser, error) {
	svc, _, actor, err := r.adminCaller(ctx)
	if err != nil {
		return nil, err
	}
	role, ok := authRole(in.Role)
	if !ok {
		return nil, apierror.Invalid("role", "unknown role")
	}
	u, temp, err := svc.Create(ctx, actor, auth.NewUser{Email: in.Email, DisplayName: in.DisplayName, Role: role})
	if err != nil {
		return nil, userAdminError(err)
	}
	if temp == nil {
		return nil, errors.New("resolvers: no temporary password generated")
	}
	m, err := userModel(u)
	if err != nil {
		return nil, err
	}
	return &model.CreatedUser{User: m, TemporaryPassword: &model.TemporaryPassword{Password: temp.Password, ExpiresAt: temp.ExpiresAt}}, nil
}

// clearIfSelf clears the caller's session cookie when their own sessions
// were revoked by the change.
func clearIfSelf(ctx context.Context, p auth.Principal, target uuid.UUID, revoked int64) {
	if revoked > 0 && p.UserID == target {
		if cookies, ok := auth.CookieSetterFrom(ctx); ok {
			cookies.ClearSessionCookie()
		}
	}
}

func (r *Resolver) applyUserChanges(ctx context.Context, id uuid.UUID, c auth.UserChanges) (*model.User, error) {
	svc, p, actor, err := r.adminCaller(ctx)
	if err != nil {
		return nil, err
	}
	res, err := svc.Update(ctx, actor, id, c)
	if err != nil {
		return nil, userAdminError(err)
	}
	clearIfSelf(ctx, p, id, res.RevokedSessions)
	return userModel(res.User)
}

func (r *Resolver) updateUser(ctx context.Context, id uuid.UUID, in model.UpdateUserInput) (*model.User, error) {
	c := auth.UserChanges{DisplayName: in.DisplayName, Disabled: in.Disabled}
	if in.Role != nil {
		role, ok := authRole(*in.Role)
		if !ok {
			return nil, apierror.Invalid("input.role", "unknown role")
		}
		c.Role = &role
	}
	if c.DisplayName == nil && c.Role == nil && c.Disabled == nil {
		return nil, apierror.Invalid("input", "nothing to change")
	}
	return r.applyUserChanges(ctx, id, c)
}

func (r *Resolver) setUserDisabled(ctx context.Context, id uuid.UUID, disabled bool) (*model.User, error) {
	return r.applyUserChanges(ctx, id, auth.UserChanges{Disabled: &disabled})
}

func (r *Resolver) resetUserPassword(ctx context.Context, id uuid.UUID) (*model.TemporaryPassword, error) {
	svc, _, actor, err := r.adminCaller(ctx)
	if err != nil {
		return nil, err
	}
	temp, _, err := svc.ResetPassword(ctx, actor, id, "")
	if err != nil {
		return nil, userAdminError(err)
	}
	if temp == nil {
		return nil, errors.New("resolvers: no temporary password generated")
	}
	return &model.TemporaryPassword{Password: temp.Password, ExpiresAt: temp.ExpiresAt}, nil
}

func (r *Resolver) resetUserSecondFactors(ctx context.Context, userID uuid.UUID) (*model.SecondFactorReset, error) {
	svc, _, actor, err := r.adminCaller(ctx)
	if err != nil {
		return nil, err
	}
	res, err := svc.ResetSecondFactors(ctx, actor, userID)
	if err != nil {
		return nil, userAdminError(err)
	}
	u, err := userModel(res.User)
	if err != nil {
		return nil, err
	}
	return &model.SecondFactorReset{
		User: u, RemovedTotp: res.HadTOTP, RemovedPasskeys: int(res.Passkeys),
		RemovedRecoveryCodes: int(res.RecoveryCodes), RevokedSessions: int(res.RevokedSessions),
	}, nil
}

func (r *Resolver) unlockUser(ctx context.Context, id uuid.UUID) (*model.User, error) {
	svc, _, actor, err := r.adminCaller(ctx)
	if err != nil {
		return nil, err
	}
	u, err := svc.Unlock(ctx, actor, id)
	if err != nil {
		return nil, userAdminError(err)
	}
	return userModel(u)
}

func (r *Resolver) deleteUser(ctx context.Context, id uuid.UUID) (bool, error) {
	svc, _, actor, err := r.adminCaller(ctx)
	if err != nil {
		return false, err
	}
	if err := svc.Delete(ctx, actor, id); err != nil {
		return false, userAdminError(err)
	}
	return true, nil
}

func (r *Resolver) revokeUserSessions(ctx context.Context, userID uuid.UUID) (int, error) {
	svc, p, actor, err := r.adminCaller(ctx)
	if err != nil {
		return 0, err
	}
	n, err := svc.RevokeSessions(ctx, actor, userID)
	if err != nil {
		return 0, userAdminError(err)
	}
	clearIfSelf(ctx, p, userID, n)
	return int(n), nil
}

// authRole maps a GraphQL Role to an auth.Role.
func authRole(r model.Role) (auth.Role, bool) {
	switch r {
	case model.RoleAdmin:
		return auth.RoleAdmin, true
	case model.RoleMaintainer:
		return auth.RoleMaintainer, true
	case model.RoleUser:
		return auth.RoleUser, true
	default:
		return "", false
	}
}
