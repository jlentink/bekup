//go:build integration

package apiaccess_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/apiaccess"
	"github.com/jlentink/bekup/internal/apikey"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// Token management (D-220) against a real database: the policy with the
// database-backed job ownership, the alerting auditor and the external
// role's functions to prove what a created, changed or revoked token can
// do.

type tokenEnv struct {
	*env
	svc *apiaccess.Service
}

func newTokenEnv(t *testing.T) *tokenEnv {
	t.Helper()
	e := newEnv(t)
	svc, err := apiaccess.NewService(apiaccess.ServiceOptions{
		DB: e.pool, Auditor: e.alerting, Authorizer: authz.New(authz.Options{Ownership: jobs.Ownership{DB: e.pool}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &tokenEnv{env: e, svc: svc}
}

// as is the context of user id with role, stepped up now unless stale.
func (e *tokenEnv) as(id uuid.UUID, role auth.Role, stale bool) context.Context {
	now := time.Now()
	p := auth.Principal{UserID: id, SessionID: uuid.New(), Role: role, AAL: auth.AAL1, StepUpAt: now}
	if stale {
		p.StepUpAt = now.Add(-time.Hour)
	}
	return auth.WithPrincipal(e.ctx, p)
}

func code(err error) apierror.Code {
	if e, ok := apierror.As(err); ok {
		return e.Code
	}
	return ""
}

func field(err error) string {
	var fe *serviceutil.FieldError
	if errors.As(err, &fe) {
		return fe.Field
	}
	return ""
}

// auditDetails returns the details of the newest entry of action for the
// token, or nil.
func (e *tokenEnv) auditDetails(action string, id uuid.UUID) map[string]any {
	e.t.Helper()
	var raw []byte
	err := e.pool.QueryRow(e.ctx, `SELECT details FROM audit_log WHERE action = $1 AND target_type = 'api_token' AND target_id = $2
		ORDER BY id DESC LIMIT 1`, action, id.String()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *tokenEnv) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *tokenEnv) alerts(kind string, id uuid.UUID) int {
	return e.count(`SELECT count(*) FROM notification_events WHERE type = 'security.system' AND detail->>'kind' = $1 AND detail->>'api_id' = $2`,
		kind, id.String())
}

// accountAlerts counts the security.account alerts of kind about token id
// to user u.
func (e *tokenEnv) accountAlerts(kind string, id, u uuid.UUID) int {
	return e.count(`SELECT count(*) FROM notification_events WHERE type = 'security.account' AND detail->>'kind' = $1 AND detail->>'api_id' = $2 AND user_id = $3`,
		kind, id.String(), u)
}

// authenticate calls api.authenticate as bekup_external.
func (e *tokenEnv) authenticate(plaintext string, ip netip.Addr) bool {
	e.t.Helper()
	k, err := apikey.Parse(plaintext)
	if err != nil {
		e.t.Fatal(err)
	}
	h := k.Hash()
	k.Zero()
	r, err := e.extQ.ExternalAuthenticate(e.ctx, sqlc.ExternalAuthenticateParams{TokenID: k.ID, SecretHash: h[:], ClientIp: &ip})
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return r.TokenID != nil
}

// listLen is the length of a list detail (-1 when it is not a list).
func listLen(d map[string]any, k string) int {
	l, ok := d[k].([]any)
	if !ok {
		return -1
	}
	return len(l)
}

func find(list []apiaccess.Token, id uuid.UUID) *apiaccess.Token {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func TestIntegrationTokenService(t *testing.T) {
	e := newTokenEnv(t)
	admin := e.user(sqlc.UserRoleAdmin)
	owner := e.user(sqlc.UserRoleUser)
	other := e.user(sqlc.UserRoleUser)
	maint := e.user(sqlc.UserRoleMaintainer)
	jobA := e.job(owner, maint)
	jobB := e.job(other)
	empty, err := e.svc.List(e.as(admin, auth.RoleAdmin, false), nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty list: %v %v", empty, err)
	}
	deleted := e.job(owner)
	e.exec("UPDATE jobs SET deleted_at = now() WHERE id = $1", deleted)
	expires := time.Now().Add(90 * 24 * time.Hour)
	inside := netip.MustParseAddr("192.0.2.10")
	outside := netip.MustParseAddr("198.51.100.1")

	// A backup owner creates a token for their job: the plaintext parses,
	// only its hash is stored, the audit entry and the alert carry no
	// secret.
	ownerCtx := e.as(owner, auth.RoleUser, false)
	created, err := e.svc.Create(ownerCtx, apiaccess.CreateInput{
		Name: "  deploy pipeline ", JobIDs: []uuid.UUID{jobA, jobA}, ExpiresAt: expires, AllowedCIDRs: []string{"192.0.2.0/24", "2001:db8::1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	k, err := apikey.Parse(created.Plaintext)
	if err != nil || k.ID != created.Token.ID {
		t.Fatalf("plaintext: %v", err)
	}
	var stored []byte
	if err := e.pool.QueryRow(e.ctx, "SELECT secret_hash FROM api_tokens WHERE id = $1", k.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if h := k.Hash(); !slices.Equal(stored, h[:]) {
		t.Fatal("stored hash differs")
	}
	tok := created.Token
	if tok.Name != "deploy pipeline" || tok.Prefix != apikey.DisplayPrefix(tok.ID) || !strings.HasPrefix(created.Plaintext, tok.Prefix) ||
		len(tok.Jobs) != 1 || tok.Jobs[0].ID != jobA || !tok.CreatedByCaller || tok.CreatedBy == nil || tok.CreatedBy.ID != owner ||
		tok.AllowAnyAddress || len(tok.AllowedCIDRs) != 2 || tok.AllowedCIDRs[1].String() != "2001:db8::1/128" || tok.LastSeen != nil {
		t.Fatalf("created token %+v", tok)
	}
	d := e.auditDetails("api_token.created", tok.ID)
	if d == nil || d["api_token_name"] != "deploy pipeline" || d["allow_any_address"] != false ||
		listLen(d, "job_ids") != 1 || listLen(d, "allowed_cidrs") != 2 || d["expires_at"] == "" {
		t.Fatalf("audit %v", d)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE details::text LIKE '%' || $1 || '%'`, created.Plaintext[len(tok.Prefix):]); n != 0 {
		t.Fatal("the secret reached the audit log")
	}
	if e.alerts("api_token_created", tok.ID) != 1 || e.alerts("api_token_any_address", tok.ID) != 0 {
		t.Fatal("create alerts")
	}
	if !e.authenticate(created.Plaintext, inside) {
		t.Fatal("the address list is not what the external API enforces")
	}

	// Refusals: a job the owner does not own, a maintainer who is listed
	// as owner, a stale step-up, a deleted or unknown job (admins), bad
	// input; none of them writes a token.
	before := e.count("SELECT count(*) FROM api_tokens")
	in := func(jobs ...uuid.UUID) apiaccess.CreateInput {
		return apiaccess.CreateInput{Name: "x", JobIDs: jobs, ExpiresAt: expires, AllowAnyAddress: true}
	}
	if _, err := e.svc.Create(ownerCtx, in(jobA, jobB)); code(err) != apierror.Forbidden {
		t.Fatalf("foreign job: %v", err)
	}
	if _, err := e.svc.Create(e.as(maint, auth.RoleMaintainer, false), in(jobA)); code(err) != apierror.Forbidden {
		t.Fatalf("maintainer-owner: %v", err)
	}
	if _, err := e.svc.Create(e.as(owner, auth.RoleUser, true), in(jobA)); code(err) != apierror.StepUpRequired {
		t.Fatalf("stale step-up: %v", err)
	}
	adminCtx := e.as(admin, auth.RoleAdmin, false)
	if _, err := e.svc.Create(adminCtx, in(jobA, deleted)); field(err) != apiaccess.FieldJobIDs {
		t.Fatalf("deleted job: %v", err)
	}
	if _, err := e.svc.Create(adminCtx, in(uuid.New())); field(err) != apiaccess.FieldJobIDs {
		t.Fatalf("unknown job: %v", err)
	}
	if _, err := e.svc.Create(adminCtx, apiaccess.CreateInput{Name: "x", JobIDs: []uuid.UUID{jobA}, ExpiresAt: time.Now().Add(400 * 24 * time.Hour), AllowAnyAddress: true}); field(err) != apiaccess.FieldExpiresAt {
		t.Fatalf("long expiry: %v", err)
	}
	if n := e.count("SELECT count(*) FROM api_tokens"); n != before {
		t.Fatalf("refusals wrote %d tokens", n-before)
	}

	// An admin token for both jobs that allows any address: two alerts.
	wide, err := e.svc.Create(adminCtx, apiaccess.CreateInput{Name: "release", JobIDs: []uuid.UUID{jobA, jobB}, ExpiresAt: expires, AllowAnyAddress: true})
	if err != nil {
		t.Fatal(err)
	}
	if e.alerts("api_token_created", wide.Token.ID) != 1 || e.alerts("api_token_any_address", wide.Token.ID) != 1 {
		t.Fatal("any-address alerts")
	}
	if !e.authenticate(wide.Plaintext, outside) {
		t.Fatal("any address refused")
	}

	// Sightings: an accepted address (run request) and a refused one.
	ip := inside
	h := k.Hash()
	if r, err := e.extQ.ExternalRequestRun(e.ctx, sqlc.ExternalRequestRunParams{TokenID: k.ID, SecretHash: h[:], JobID: jobA, Ip: &ip}); err != nil || r.Refusal != nil {
		t.Fatalf("request: %v %v", r, err)
	}
	list, err := e.svc.List(ownerCtx, &jobA)
	if err != nil {
		t.Fatal(err)
	}
	if mine := find(list, tok.ID); mine == nil || mine.LastSeen == nil || mine.LastSeen.IP == nil || *mine.LastSeen.IP != inside ||
		mine.LastSeen.Refused || mine.LastUsedAt == nil {
		t.Fatalf("sighting %+v", mine)
	}
	if e.authenticate(created.Plaintext, outside) { // records a denial
		t.Fatal("accepted from outside the list")
	}
	list, err = e.svc.List(ownerCtx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mine := find(list, tok.ID); mine == nil || mine.LastSeen == nil || *mine.LastSeen.IP != outside || !mine.LastSeen.Refused {
		t.Fatalf("refused sighting %+v", mine)
	}

	// Visibility: admins see everything; the owner of jobA sees the admin
	// token with jobB counted only and without its creator; the owner of
	// jobB sees only the admin token; maintainers nothing.
	all, err := e.svc.List(adminCtx, nil)
	if err != nil || len(all) != 2 || all[0].ID != wide.Token.ID {
		t.Fatalf("admin list: %v %v", all, err)
	}
	if a := find(all, tok.ID); a.CreatedBy == nil || a.CreatedBy.ID != owner || a.CreatedByCaller {
		t.Fatalf("admin view of the owner's token %+v", a)
	}
	w := find(list, wide.Token.ID)
	if len(list) != 2 || w == nil || len(w.Jobs) != 1 || w.Jobs[0].ID != jobA || w.HiddenJobs != 1 || w.CreatedBy != nil || w.CreatedByCaller {
		t.Fatalf("owner view %+v", list)
	}
	otherCtx := e.as(other, auth.RoleUser, false)
	ol, err := e.svc.List(otherCtx, nil)
	if err != nil || len(ol) != 1 || ol[0].ID != wide.Token.ID || len(ol[0].Jobs) != 1 || ol[0].Jobs[0].ID != jobB || ol[0].HiddenJobs != 1 {
		t.Fatalf("other owner view %+v %v", ol, err)
	}
	if _, err := e.svc.List(otherCtx, &jobA); !errors.Is(err, apiaccess.ErrForbidden) {
		t.Fatalf("foreign job filter: %v", err)
	}
	if _, err := e.svc.List(e.as(maint, auth.RoleMaintainer, false), nil); code(err) != apierror.Forbidden {
		t.Fatalf("maintainer list: %v", err)
	}
	if jl, err := e.svc.List(adminCtx, &jobB); err != nil || len(jl) != 1 || jl[0].ID != wide.Token.ID {
		t.Fatalf("job filter: %v %v", jl, err)
	}

	// Address changes: the owner of jobB may change the admin token (a job
	// of its scope is theirs); a narrowed list is audited with the old
	// values and alerts nothing; "any address" again alerts.
	upd, err := e.svc.UpdateAddresses(otherCtx, wide.Token.ID, []string{"203.0.113.0/24"}, false)
	if err != nil || upd.AllowAnyAddress || len(upd.AllowedCIDRs) != 1 || upd.HiddenJobs != 1 {
		t.Fatalf("narrow: %+v %v", upd, err)
	}
	d = e.auditDetails("api_token.addresses_changed", wide.Token.ID)
	if d == nil || d["previous_allow_any_address"] != true || d["allow_any_address"] != false || listLen(d, "allowed_cidrs") != 1 {
		t.Fatalf("addresses audit %v", d)
	}
	if e.authenticate(wide.Plaintext, outside) {
		t.Fatal("narrowed token accepted from outside")
	}
	entries := e.count("SELECT count(*) FROM audit_log WHERE action = 'api_token.addresses_changed'")
	if _, err := e.svc.UpdateAddresses(otherCtx, wide.Token.ID, []string{" 203.0.113.0/24"}, false); err != nil {
		t.Fatal(err)
	}
	if e.count("SELECT count(*) FROM audit_log WHERE action = 'api_token.addresses_changed'") != entries {
		t.Fatal("an unchanged list was audited")
	}
	if d["widened"] != false || e.alerts("api_token_addresses_widened", wide.Token.ID) != 0 {
		t.Fatalf("a narrowing counted as a widening: %v", d)
	}
	// M1: a network the old list did not cover alerts the admins and the
	// token's creator (the admin), naming the backup owner who changed it;
	// a reordered list is unchanged (finding 7).
	if _, err := e.svc.UpdateAddresses(otherCtx, wide.Token.ID, []string{"203.0.113.0/24", "198.51.100.0/24"}, false); err != nil {
		t.Fatal(err)
	}
	d = e.auditDetails("api_token.addresses_changed", wide.Token.ID)
	if d["widened"] != true || d["changed_by_creator"] != false || d["creator_id"] != admin.String() || d["actor_name"] == nil {
		t.Fatalf("widening audit %v", d)
	}
	if e.alerts("api_token_addresses_widened", wide.Token.ID) != 1 || e.accountAlerts("api_token_addresses_widened", wide.Token.ID, admin) != 1 {
		t.Fatal("no widening alerts")
	}
	entries = e.count("SELECT count(*) FROM audit_log WHERE action = 'api_token.addresses_changed'")
	if _, err := e.svc.UpdateAddresses(otherCtx, wide.Token.ID, []string{"198.51.100.0/24", "203.0.113.0/24"}, false); err != nil {
		t.Fatal(err)
	}
	if e.count("SELECT count(*) FROM audit_log WHERE action = 'api_token.addresses_changed'") != entries {
		t.Fatal("a reordered list was audited")
	}
	if _, err := e.svc.UpdateAddresses(otherCtx, tok.ID, nil, true); code(err) != apierror.Forbidden {
		t.Fatalf("foreign token: %v", err)
	}
	if _, err := e.svc.UpdateAddresses(e.as(owner, auth.RoleUser, true), tok.ID, nil, true); code(err) != apierror.StepUpRequired {
		t.Fatalf("stale update: %v", err)
	}
	if _, err := e.svc.UpdateAddresses(ownerCtx, tok.ID, []string{"10.0.0.1/8"}, false); field(err) != apiaccess.FieldAllowedCIDRs {
		t.Fatalf("bad network: %v", err)
	}
	if _, err := e.svc.UpdateAddresses(ownerCtx, tok.ID, nil, true); err != nil {
		t.Fatal(err)
	}
	if e.alerts("api_token_any_address", tok.ID) != 1 || e.accountAlerts("api_token_addresses_widened", tok.ID, owner) != 1 ||
		e.alerts("api_token_addresses_widened", tok.ID) != 0 {
		t.Fatal("no any-address alerts after the change")
	}

	// Revocation: unknown tokens are NOT_FOUND for admins only, a token
	// outside the caller's jobs is FORBIDDEN, a revoked token cannot be
	// revoked or changed again and the external API refuses it.
	if _, err := e.svc.Revoke(otherCtx, uuid.New()); code(err) != apierror.Forbidden {
		t.Fatalf("unknown as owner: %v", err)
	}
	if _, err := e.svc.Revoke(adminCtx, uuid.New()); !errors.Is(err, apiaccess.ErrNotFound) {
		t.Fatalf("unknown as admin: %v", err)
	}
	if _, err := e.svc.Revoke(otherCtx, tok.ID); code(err) != apierror.Forbidden {
		t.Fatalf("foreign revoke: %v", err)
	}
	if _, err := e.svc.Revoke(e.as(maint, auth.RoleMaintainer, false), tok.ID); code(err) != apierror.Forbidden {
		t.Fatalf("maintainer revoke: %v", err)
	}
	rev, err := e.svc.Revoke(ownerCtx, tok.ID)
	if err != nil || rev.RevokedAt == nil {
		t.Fatalf("revoke: %+v %v", rev, err)
	}
	if d := e.auditDetails("api_token.revoked", tok.ID); d == nil || d["api_token_name"] != "deploy pipeline" {
		t.Fatalf("revoke audit %v", d)
	}
	if _, err := e.svc.Revoke(ownerCtx, tok.ID); !errors.Is(err, apiaccess.ErrRevoked) {
		t.Fatalf("second revoke: %v", err)
	}
	if _, err := e.svc.UpdateAddresses(ownerCtx, tok.ID, []string{"192.0.2.0/24"}, false); !errors.Is(err, apiaccess.ErrRevoked) {
		t.Fatalf("update after revoke: %v", err)
	}
	if e.authenticate(created.Plaintext, inside) {
		t.Fatal("a revoked token authenticates")
	}

	// A token without expiry (D-229): stored with never_expires and no
	// date, valid, audited and alerted.
	forever, err := e.svc.Create(ownerCtx, apiaccess.CreateInput{Name: "forever", JobIDs: []uuid.UUID{jobA}, NeverExpires: true, AllowedCIDRs: []string{"192.0.2.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	if !forever.Token.NeverExpires || forever.Token.ExpiresAt != nil {
		t.Fatalf("never-expiring token %+v", forever.Token)
	}
	if n := e.count("SELECT count(*) FROM api_tokens WHERE id = $1 AND never_expires AND expires_at IS NULL", forever.Token.ID); n != 1 {
		t.Fatal("never_expires not stored")
	}
	if d := e.auditDetails("api_token.created", forever.Token.ID); d == nil || d["never_expires"] != true || d["expires_at"] != "" {
		t.Fatalf("never-expiring audit %v", d)
	}
	if e.alerts("api_token_created", forever.Token.ID) != 1 || e.alerts("api_token_never_expires", forever.Token.ID) != 1 {
		t.Fatal("never-expiring alerts")
	}
	if !e.authenticate(forever.Plaintext, inside) {
		t.Fatal("a never-expiring token is refused")
	}
	// The column is not writable for bekup_app, and the guard refuses a
	// change even for the owner role.
	if _, err := e.pool.Exec(e.ctx, "UPDATE api_tokens SET never_expires = false, expires_at = now() + interval '1 day' WHERE id = $1", forever.Token.ID); err == nil {
		t.Fatal("never_expires changed")
	}
	// Both an expiry and never: refused before the database.
	if _, err := e.svc.Create(adminCtx, apiaccess.CreateInput{Name: "x", JobIDs: []uuid.UUID{jobA}, ExpiresAt: expires, NeverExpires: true, AllowAnyAddress: true}); field(err) != apiaccess.FieldExpiresAt {
		t.Fatalf("expiry and never: %v", err)
	}

}

// TestAddressRulesMatchDatabase: ParseAddresses and the database's
// api_cidrs_valid (the CHECK of api_tokens.allowed_cidrs) agree on every
// single network, in particular the widest allowed prefixes (M1).
func TestAddressRulesMatchDatabase(t *testing.T) {
	e := newTokenEnv(t)
	for _, c := range []string{
		"0.0.0.0/0", "0.0.0.0/1", "128.0.0.0/1", "8.0.0.0/7", "10.0.0.0/8", "10.0.0.0/16", "192.0.2.7/32",
		"::/0", "2000::/3", "2001:db8::/31", "2001:db8::/32", "2001:db8::1/128", "::/96",
		"::ffff:0:0/96", "::ffff:192.0.2.0/120",
	} {
		_, goErr := apiaccess.ParseAddresses([]string{c}, false)
		var sqlOK bool
		if err := e.pool.QueryRow(e.ctx, "SELECT public.api_cidrs_valid(ARRAY[$1::cidr])", c).Scan(&sqlOK); err != nil {
			t.Fatalf("%s: %v", c, err)
		}
		if (goErr == nil) != sqlOK {
			t.Errorf("%s: ParseAddresses error %v, api_cidrs_valid %v", c, goErr, sqlOK)
		}
	}
}
