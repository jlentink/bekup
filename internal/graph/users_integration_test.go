//go:build integration

package graph_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/logging"
)

// usersEnv is an authEnv whose handler also has the user admin service
// (task 1.6).
type usersEnv struct {
	*authEnv
	// counter counts the queries of the user admin service (N+1 checks).
	counter *countingDB
}

func newUsersEnv(t *testing.T) *usersEnv {
	t.Helper()
	d := dbtest.Migrated(t)
	e := &authEnv{t: t, d: d, pool: d.AppPool(t)}
	e.auditor = audit.New(e.pool, audit.Options{Registry: logging.NewRegistry()})
	var err error
	e.hasher, err = auth.NewHasher(auth.HasherOptions{
		Params: auth.Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1}, MaxConcurrent: 4, WeakForTests: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessions(e.pool, auth.SessionOptions{})
	policy := auth.NewPasswordPolicy(12, 3)
	svc, err := auth.NewService(auth.ServiceOptions{DB: e.pool, Hasher: e.hasher, Sessions: sessions, Auditor: e.auditor, Policy: policy, Factors: auth.NewSecondFactors()})
	if err != nil {
		t.Fatal(err)
	}
	counter := &countingDB{DBTX: e.pool, begin: e.pool}
	users, err := auth.NewUserAdmin(auth.UserAdminOptions{DB: counter, Hasher: e.hasher, Sessions: sessions, Auditor: e.auditor, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	pol := authz.New(authz.Options{})
	logger := slog.New(slog.DiscardHandler)
	gql, err := graph.New(graph.Options{
		Logger: logger, MaxComplexity: 1000, MaxDepth: 12, MaxBodyBytes: 1 << 16,
		Access: graph.NewPolicyAccess(pol), Policy: pol, AuditLog: e.auditor, Auth: svc,
		Users: users,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := httpserver.New(httpserver.Options{
		BaseURL: testBaseURL, MaxBodyBytes: 1 << 20, Logger: logger,
		Ready:     func(context.Context) error { return nil },
		RateLimit: httpserver.RateLimitOptions{PerSecond: 1000, Burst: 1000, MaxClients: 100},
		GraphQL:   gql,
		Sessions: &httpserver.SessionOptions{
			Resolver: httpserver.SessionsResolver{Sessions: sessions},
			Cookies:  auth.CookiePolicyFor(testBaseURL),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.h = srv.Handler()
	return &usersEnv{authEnv: e, counter: counter}
}

func (e *usersEnv) staleStepUp(cookieUser uuid.UUID) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), "UPDATE sessions SET stepup_at = now() - interval '1 hour' WHERE user_id = $1", cookieUser); err != nil {
		e.t.Fatal(err)
	}
}

func (r gqlResp) field(t *testing.T, name string, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Data[name], v); err != nil {
		t.Fatalf("decode %s (%s): %v", name, r.Data[name], err)
	}
}

const (
	createUserMut = `mutation($e: String!, $r: Role!) { createUser(input: {email: $e, displayName: "Created User", role: $r}) {
		user { id email role passwordChangeRequired temporaryPasswordExpiresAt } temporaryPassword { password expiresAt } } }`
	updateRoleMut = `mutation($id: UUID!, $r: Role!) { updateUser(id: $id, input: {role: $r}) { id role } }`
)

func TestUserAdminGraphQL(t *testing.T) {
	e := newUsersEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	maint := e.user("maint@example.com", sqlc.UserRoleMaintainer)
	plain := e.user("plain@example.com", sqlc.UserRoleUser)
	adminC := e.mustLogin("admin@example.com")
	maintC := e.mustLogin("maint@example.com")
	plainC := e.mustLogin("plain@example.com")

	// Authz denials for maintainers, users and anonymous callers.
	idVars := map[string]any{"id": plain.ID.String()}
	for _, tc := range []struct{ q string }{
		{`{ users { edges { node { id } } } }`},
		{`query($id: UUID!) { user(id: $id) { id } }`},
		{`query($id: UUID!) { userSessions(userId: $id) { id } }`},
		{`mutation($id: UUID!) { disableUser(id: $id) { id } }`},
		{`mutation($id: UUID!) { enableUser(id: $id) { id } }`},
		{`mutation($id: UUID!) { unlockUser(id: $id) { id } }`},
		{`mutation($id: UUID!) { deleteUser(id: $id) }`},
		{`mutation($id: UUID!) { resetUserPassword(id: $id) { password } }`},
		{`mutation($id: UUID!) { revokeUserSessions(userId: $id) }`},
		{`mutation($id: UUID!) { resetUserSecondFactors(userId: $id) { revokedSessions } }`},
		{`mutation($id: UUID!) { updateUser(id: $id, input: {displayName: "x"}) { id } }`},
		{`mutation { createUser(input: {email: "x@example.com", displayName: "X", role: USER}) { user { id } } }`},
	} {
		for who, c := range map[string]string{"maintainer": maintC, "user": plainC, "anonymous": ""} {
			resp, _ := e.do(c, "", tc.q, idVars)
			want := "FORBIDDEN"
			if who == "anonymous" {
				want = "UNAUTHENTICATED"
			}
			if resp.code() != want {
				t.Errorf("%s %s: %+v", who, tc.q, resp.Errors)
			}
		}
	}

	// List and view.
	resp, _ := e.do(adminC, "", `{ users(first: 2) { edges { cursor node { email role disabled } } pageInfo { hasNextPage endCursor } } }`, nil)
	var page struct {
		Edges []struct {
			Node struct{ Email string }
		}
		PageInfo struct {
			HasNextPage bool
			EndCursor   string
		}
	}
	resp.field(t, "users", &page)
	if len(page.Edges) != 2 || !page.PageInfo.HasNextPage || page.Edges[0].Node.Email != "admin@example.com" {
		t.Fatalf("users: %+v %+v", page, resp.Errors)
	}
	resp, _ = e.do(adminC, "", `query($a: String) { users(first: 2, after: $a) { edges { node { email } } pageInfo { hasNextPage } } }`, map[string]any{"a": page.PageInfo.EndCursor})
	resp.field(t, "users", &page)
	if len(page.Edges) != 1 || page.PageInfo.HasNextPage || page.Edges[0].Node.Email != "plain@example.com" {
		t.Fatalf("page 2: %+v", page)
	}
	if resp, _ := e.do(adminC, "", `{ users(after: "bm9wZQ") { edges { cursor } } }`, nil); resp.code() != "VALIDATION" {
		t.Fatalf("bad cursor: %+v", resp.Errors)
	}
	if resp, _ := e.do(adminC, "", `query($id: UUID!) { user(id: $id) { email } }`, map[string]any{"id": uuid.NewString()}); len(resp.Errors) > 0 || string(resp.Data["user"]) != "null" {
		t.Fatalf("unknown user: %+v %s", resp.Errors, resp.Data["user"])
	}
	if resp, _ := e.do(adminC, "", `query($id: UUID!) { userSessions(userId: $id) { id current } }`, map[string]any{"id": plain.ID.String()}); len(resp.Errors) > 0 || strings.Count(string(resp.Data["userSessions"]), `"id"`) != 1 {
		t.Fatalf("user sessions: %+v %s", resp.Errors, resp.Data["userSessions"])
	}

	// Create with a temporary password; the new user must change it.
	resp, _ = e.do(adminC, "", createUserMut, map[string]any{"e": "fresh@example.com", "r": "USER"})
	var created struct {
		User struct {
			ID                     string
			PasswordChangeRequired bool
		}
		TemporaryPassword struct{ Password string }
	}
	resp.field(t, "createUser", &created)
	if len(resp.Errors) > 0 || !created.User.PasswordChangeRequired || len(created.TemporaryPassword.Password) < 32 {
		t.Fatalf("create: %+v %+v", resp.Errors, created)
	}
	if resp, _ := e.do(adminC, "", createUserMut, map[string]any{"e": "Fresh@Example.com", "r": "USER"}); resp.code() != "CONFLICT" {
		t.Fatalf("duplicate: %+v", resp.Errors)
	}
	resp, w := e.do("", "", `mutation($e: String!, $p: String!) { login(email: $e, password: $p) { passwordChangeRequired } }`,
		map[string]any{"e": "fresh@example.com", "p": created.TemporaryPassword.Password})
	freshC := sessionCookie(t, w)
	if len(resp.Errors) > 0 || !strings.Contains(string(resp.Data["login"]), `"passwordChangeRequired":true`) {
		t.Fatalf("temp login: %+v %s", resp.Errors, resp.Data["login"])
	}
	if resp, _ := e.do(freshC, "", `{ mySessions { id } }`, nil); len(resp.Errors) > 0 {
		t.Fatalf("own sessions: %+v", resp.Errors)
	}
	if resp, _ := e.do(freshC, "", `{ auditLog { edges { cursor } } }`, nil); resp.code() != "PASSWORD_CHANGE_REQUIRED" {
		t.Fatalf("gate: %+v", resp.Errors)
	}
	resp, w = e.do(freshC, "", `mutation($c: String!, $n: String!) { changePassword(currentPassword: $c, newPassword: $n) }`,
		map[string]any{"c": created.TemporaryPassword.Password, "n": "copper-lantern-orbit-73"})
	if len(resp.Errors) > 0 {
		t.Fatalf("change: %+v", resp.Errors)
	}
	freshC = sessionCookie(t, w)
	if resp, _ := e.do(freshC, "", `{ me { passwordChangeRequired } }`, nil); !strings.Contains(string(resp.Data["me"]), "false") {
		t.Fatalf("after change: %s", resp.Data["me"])
	}

	// Step-up is required for every change.
	e.staleStepUp(admin.ID)
	for _, q := range []string{
		`mutation($id: UUID!) { disableUser(id: $id) { id } }`,
		`mutation($id: UUID!) { resetUserPassword(id: $id) { password } }`,
		`mutation($id: UUID!) { updateUser(id: $id, input: {role: MAINTAINER}) { id } }`,
		`mutation($id: UUID!) { revokeUserSessions(userId: $id) }`,
		`mutation($id: UUID!) { deleteUser(id: $id) }`,
	} {
		if resp, _ := e.do(adminC, "", q, idVars); resp.code() != "STEP_UP_REQUIRED" {
			t.Errorf("stale step-up %s: %+v", q, resp.Errors)
		}
	}
	// Reading needs none.
	if resp, _ := e.do(adminC, "", `{ users { edges { cursor } } }`, nil); len(resp.Errors) > 0 {
		t.Fatalf("list with stale step-up: %+v", resp.Errors)
	}
	adminC = e.mustLogin("admin@example.com")

	// A role change revokes the target's sessions.
	resp, _ = e.do(adminC, "", updateRoleMut, map[string]any{"id": maint.ID.String(), "r": "USER"})
	if len(resp.Errors) > 0 || !strings.Contains(string(resp.Data["updateUser"]), `"role":"USER"`) {
		t.Fatalf("role change: %+v", resp.Errors)
	}
	if resp, _ := e.do(maintC, "", meQuery, nil); resp.code() != "UNAUTHENTICATED" {
		t.Fatalf("session survived role change: %+v", resp)
	}

	// Last admin invariant and self rules.
	if resp, _ := e.do(adminC, "", updateRoleMut, map[string]any{"id": admin.ID.String(), "r": "USER"}); resp.code() != "CONFLICT" {
		t.Fatalf("demote last admin: %+v", resp.Errors)
	}
	if resp, _ := e.do(adminC, "", `mutation($id: UUID!) { disableUser(id: $id) { id } }`, map[string]any{"id": admin.ID.String()}); resp.code() != "CONFLICT" {
		t.Fatalf("disable last admin: %+v", resp.Errors)
	}
	if resp, _ := e.do(adminC, "", `mutation($id: UUID!) { resetUserPassword(id: $id) { password } }`, map[string]any{"id": admin.ID.String()}); resp.code() != "FORBIDDEN" {
		t.Fatalf("self reset: %+v", resp.Errors)
	}

	// Reset, disable, enable, revoke, delete.
	plainC = e.mustLogin("plain@example.com")
	resp, _ = e.do(adminC, "", `mutation($id: UUID!) { resetUserPassword(id: $id) { password expiresAt } }`, idVars)
	if len(resp.Errors) > 0 || !strings.Contains(string(resp.Data["resetUserPassword"]), "password") {
		t.Fatalf("reset: %+v", resp.Errors)
	}
	if resp, _ := e.do(plainC, "", meQuery, nil); resp.code() != "UNAUTHENTICATED" {
		t.Fatal("session survived reset")
	}
	for _, q := range []string{
		`mutation($id: UUID!) { disableUser(id: $id) { disabled } }`,
		`mutation($id: UUID!) { enableUser(id: $id) { disabled } }`,
		`mutation($id: UUID!) { unlockUser(id: $id) { lockedUntil } }`,
		`mutation($id: UUID!) { revokeUserSessions(userId: $id) }`,
		`mutation($id: UUID!) { deleteUser(id: $id) }`,
	} {
		if resp, _ := e.do(adminC, "", q, idVars); len(resp.Errors) > 0 {
			t.Fatalf("%s: %+v", q, resp.Errors)
		}
	}
	if resp, _ := e.do(adminC, "", `mutation($id: UUID!) { enableUser(id: $id) { id } }`, idVars); resp.code() != "NOT_FOUND" {
		t.Fatalf("enable deleted: %+v", resp.Errors)
	}
	resp, _ = e.do(adminC, "", `query($id: UUID!) { user(id: $id) { deletedAt disabled } }`, idVars)
	if !strings.Contains(string(resp.Data["user"]), `"disabled":true`) || strings.Contains(string(resp.Data["user"]), `"deletedAt":null`) {
		t.Fatalf("deleted user: %s", resp.Data["user"])
	}

	// Everything is audited.
	for _, a := range []audit.Action{
		audit.ActionUserCreated, audit.ActionUserRoleChanged, audit.ActionUserPasswordReset, audit.ActionUserDisabled,
		audit.ActionUserEnabled, audit.ActionUserUnlocked, audit.ActionSessionsRevoked, audit.ActionUserDeleted,
	} {
		page, err := e.auditor.List(t.Context(), audit.Filter{Action: a}, 0, 10)
		if err != nil || len(page.Entries) == 0 || page.Entries[0].ActorUserID == nil || *page.Entries[0].ActorUserID != admin.ID {
			t.Errorf("audit %s: %+v %v", a, page.Entries, err)
		}
	}
}

func TestUserDirectoryGraphQL(t *testing.T) {
	e := newUsersEnv(t)
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	e.user("plain@example.com", sqlc.UserRoleUser)
	e.user("bob@example.com", sqlc.UserRoleUser)
	gone := e.user("gone@example.com", sqlc.UserRoleUser)
	if _, err := e.pool.Exec(t.Context(), "UPDATE users SET deleted_at = now(), disabled = true WHERE id = $1", gone.ID); err != nil {
		t.Fatal(err)
	}
	adminC := e.mustLogin("admin@example.com")
	maintC := e.mustLogin("maint@example.com")
	plainC := e.mustLogin("plain@example.com")

	const dirQ = `query($s: String, $first: Int, $after: String) { userDirectory(search: $s, first: $first, after: $after) {
		edges { cursor node { id displayName email role disabled } } pageInfo { hasNextPage endCursor } } }`
	type dirPage struct {
		Edges []struct {
			Node struct {
				Email    string
				Role     string
				Disabled bool
			}
		}
		PageInfo struct {
			HasNextPage bool
			EndCursor   *string
		}
	}

	// Backup owners and anonymous callers are refused.
	for who, c := range map[string]string{"user": plainC, "anonymous": ""} {
		resp, _ := e.do(c, "", dirQ, nil)
		want := "FORBIDDEN"
		if who == "anonymous" {
			want = "UNAUTHENTICATED"
		}
		if resp.code() != want {
			t.Errorf("%s: %+v", who, resp.Errors)
		}
	}

	// Admins and maintainers see every account that is not deleted.
	for who, c := range map[string]string{"admin": adminC, "maintainer": maintC} {
		resp, _ := e.do(c, "", dirQ, nil)
		if len(resp.Errors) > 0 {
			t.Fatalf("%s: %+v", who, resp.Errors)
		}
		var page dirPage
		resp.field(t, "userDirectory", &page)
		emails := make([]string, 0, len(page.Edges))
		for _, ed := range page.Edges {
			emails = append(emails, ed.Node.Email)
		}
		if got := strings.Join(emails, ","); got != "admin@example.com,bob@example.com,maint@example.com,plain@example.com" {
			t.Errorf("%s: directory = %s", who, got)
		}
	}

	// Search and pagination.
	resp, _ := e.do(maintC, "", dirQ, map[string]any{"s": "BOB"})
	var page dirPage
	resp.field(t, "userDirectory", &page)
	if len(page.Edges) != 1 || page.Edges[0].Node.Email != "bob@example.com" || page.Edges[0].Node.Role != "USER" {
		t.Errorf("search: %+v", page)
	}
	resp, _ = e.do(maintC, "", dirQ, map[string]any{"s": "%"})
	page = dirPage{}
	resp.field(t, "userDirectory", &page)
	if len(page.Edges) != 0 {
		t.Errorf("wildcard search matched: %+v", page)
	}
	resp, _ = e.do(maintC, "", dirQ, map[string]any{"first": 3})
	page = dirPage{}
	resp.field(t, "userDirectory", &page)
	if len(page.Edges) != 3 || !page.PageInfo.HasNextPage || page.PageInfo.EndCursor == nil {
		t.Fatalf("first page: %+v", page)
	}
	resp, _ = e.do(maintC, "", dirQ, map[string]any{"first": 3, "after": *page.PageInfo.EndCursor})
	page = dirPage{}
	resp.field(t, "userDirectory", &page)
	if len(page.Edges) != 1 || page.Edges[0].Node.Email != "plain@example.com" || page.PageInfo.HasNextPage {
		t.Errorf("second page: %+v", page)
	}
	for _, vars := range []map[string]any{
		{"first": 0}, {"first": 201}, {"after": "bogus"}, {"s": strings.Repeat("x", 201)},
	} {
		if resp, _ := e.do(maintC, "", dirQ, vars); resp.code() != "VALIDATION" {
			t.Errorf("%v: %+v", vars, resp.Errors)
		}
	}

	// The entry type carries no sensitive fields.
	for _, f := range []string{"lockedUntil", "lastSeenAt", "twoFactorEnrolled", "passwordChangeRequired", "deletedAt", "createdAt"} {
		resp, _ := e.do(maintC, "", `{ userDirectory { edges { node { id `+f+` } } } }`, nil)
		if len(resp.Errors) == 0 || !strings.Contains(resp.Errors[0].Message, "Cannot query field") {
			t.Errorf("field %s: %+v", f, resp.Errors)
		}
	}
}

func TestUserActivityFieldsGraphQL(t *testing.T) {
	e := newUsersEnv(t)
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	totpUser := e.user("totp@example.com", sqlc.UserRoleUser)
	keyUser := e.user("key@example.com", sqlc.UserRoleUser)
	e.user("never@example.com", sqlc.UserRoleUser)
	for i := range 6 {
		e.user("extra"+string(rune('a'+i))+"@example.com", sqlc.UserRoleUser)
	}
	ctx := t.Context()
	if _, err := e.pool.Exec(ctx, "UPDATE users SET totp_secret_enc = '\\x01'::bytea, totp_enabled_at = now() WHERE id = $1", totpUser.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO webauthn_credentials (user_id, credential_id, public_key, name)
		VALUES ($1, '\x000102030405060708090a0b0c0d0e0f'::bytea, '\x01'::bytea, 'Key')`, keyUser.ID); err != nil {
		t.Fatal(err)
	}
	adminC := e.mustLogin("admin@example.com")
	maintC := e.mustLogin("maint@example.com")
	totpC := e.mustLogin("totp@example.com")

	type node struct {
		Email             string
		LastSeenAt        *string
		TwoFactorEnrolled bool
	}
	var page struct {
		Edges []struct{ Node node }
	}
	before := e.counter.n.Load()
	resp, _ := e.do(adminC, "", `{ users(first: 50) { edges { node { email lastSeenAt twoFactorEnrolled } } } }`, nil)
	if len(resp.Errors) > 0 {
		t.Fatalf("users: %+v", resp.Errors)
	}
	// One query for the page and one for the facts of every user on it.
	if n := e.counter.n.Load() - before; n != 2 {
		t.Errorf("queries for a page of users = %d, want 2", n)
	}
	resp.field(t, "users", &page)
	if len(page.Edges) != 11 {
		t.Fatalf("users = %d", len(page.Edges))
	}
	for _, ed := range page.Edges {
		n := ed.Node
		wantSeen := n.Email == "admin@example.com" || n.Email == "maint@example.com" || n.Email == "totp@example.com"
		wantMFA := n.Email == "totp@example.com" || n.Email == "key@example.com"
		if (n.LastSeenAt != nil) != wantSeen || n.TwoFactorEnrolled != wantMFA {
			t.Errorf("%s: lastSeenAt=%v twoFactorEnrolled=%v", n.Email, n.LastSeenAt, n.TwoFactorEnrolled)
		}
	}

	// A single user (admin).
	resp, _ = e.do(adminC, "", `query($id: UUID!) { user(id: $id) { lastSeenAt twoFactorEnrolled } }`, map[string]any{"id": keyUser.ID.String()})
	var one node
	resp.field(t, "user", &one)
	if len(resp.Errors) > 0 || one.LastSeenAt != nil || !one.TwoFactorEnrolled {
		t.Errorf("user: %+v %+v", one, resp.Errors)
	}

	// Through me: lastSeenAt is admin only, twoFactorEnrolled also for the
	// account itself.
	if resp, _ := e.do(maintC, "", `{ me { lastSeenAt } }`, nil); resp.code() != "FORBIDDEN" {
		t.Errorf("maintainer me.lastSeenAt: %+v", resp.Errors)
	}
	resp, _ = e.do(totpC, "", `{ me { twoFactorEnrolled } }`, nil)
	var me node
	resp.field(t, "me", &me)
	if len(resp.Errors) > 0 || !me.TwoFactorEnrolled {
		t.Errorf("own twoFactorEnrolled: %+v %+v", me, resp.Errors)
	}
}
