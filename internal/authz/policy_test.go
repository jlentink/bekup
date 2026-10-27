package authz_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/graph/apierror"
)

const (
	iAdmin = iota
	iMaintainer
	iMaintainerOwner
	iOwner
	iUser
	iAnon
)

func TestUnknownOperationDenied(t *testing.T) {
	f := newFixture()
	err := f.pol.Authorize(f.ctx(t, iAdmin, false), authz.Operation("bogus.op"))
	if !apierror.IsCode(err, apierror.Forbidden) {
		t.Fatalf("unknown op as admin: %v", err)
	}
	if err := f.pol.Precheck(f.ctx(t, iAdmin, false), "bogus.op"); !apierror.IsCode(err, apierror.Forbidden) {
		t.Fatalf("unknown op precheck: %v", err)
	}
	if authz.Operation("bogus.op").Known() || !authz.OpAuditLogView.Known() {
		t.Fatal("Known")
	}
}

func TestAnonymousUnauthenticated(t *testing.T) {
	f := newFixture()
	anon := f.ctx(t, iAnon, false)
	for _, op := range authz.Operations() {
		if err := f.pol.Authorize(anon, op, f.resources(op)...); !apierror.IsCode(err, apierror.Unauthenticated) {
			t.Errorf("%s anonymous: %v", op, err)
		}
		if err := f.pol.Precheck(anon, op); !apierror.IsCode(err, apierror.Unauthenticated) {
			t.Errorf("%s anonymous precheck: %v", op, err)
		}
	}
	// An incomplete principal is anonymous.
	ctx := auth.WithPrincipal(t.Context(), auth.Principal{Role: auth.RoleAdmin})
	if err := f.pol.Authorize(ctx, authz.OpAccountView); !apierror.IsCode(err, apierror.Unauthenticated) {
		t.Errorf("invalid principal: %v", err)
	}
}

// D-067: a maintainer listed as owner still cannot download, even with
// the job resource and a fresh step-up.
func TestMaintainerOwnerCannotDownload(t *testing.T) {
	f := newFixture()
	ctx := f.ctx(t, iMaintainerOwner, false)
	if owns, err := f.own.IsJobOwner(ctx, f.subjects[iMaintainerOwner].UserID, f.job); err != nil || !owns {
		t.Fatal("fixture: maintainer is not an owner", err)
	}
	err := f.pol.Authorize(ctx, authz.OpArtifactDownload, authz.Job{ID: f.job})
	if !apierror.IsCode(err, apierror.Forbidden) {
		t.Fatalf("maintainer-owner download: %v", err)
	}
	if err := f.pol.Precheck(ctx, authz.OpArtifactDownload); !apierror.IsCode(err, apierror.Forbidden) {
		t.Fatalf("maintainer-owner download precheck: %v", err)
	}
	if err := f.pol.Authorize(f.ctx(t, iOwner, false), authz.OpArtifactDownload, authz.Job{ID: f.job}); err != nil {
		t.Fatalf("owner download: %v", err)
	}
}

func TestOwnerScope(t *testing.T) {
	f := newFixture()
	owner := f.ctx(t, iOwner, false)
	other := uuid.New()
	f.own.AddOwner(other, f.subjects[iUser].UserID)
	cases := []struct {
		name string
		res  []authz.Resource
		want byte
	}{
		{"own job", []authz.Resource{authz.Job{ID: f.job}}, 'Y'},
		{"no resource", nil, 'F'},
		{"nil job", []authz.Resource{authz.Job{}}, 'F'},
		{"foreign job", []authz.Resource{authz.Job{ID: other}}, 'F'},
		{"own and foreign", []authz.Resource{authz.Job{ID: f.job}, authz.Job{ID: other}}, 'F'},
		{"unexpected target", []authz.Resource{authz.Job{ID: f.job}, authz.RestoreTarget{ConnectionID: f.conn, Database: "app"}}, 'F'},
	}
	for _, c := range cases {
		if got := outcome(f.pol.Authorize(owner, authz.OpRunNow, c.res...)); got != c.want {
			t.Errorf("%s: got %c, want %c", c.name, got, c.want)
		}
	}
	// Precheck lets the owner through to the resolver, which must then
	// call Authorize with the job.
	if err := f.pol.Precheck(owner, authz.OpRunNow); err != nil {
		t.Errorf("owner precheck: %v", err)
	}
	if err := f.pol.Precheck(owner, authz.OpJobCreate); !apierror.IsCode(err, apierror.Forbidden) {
		t.Errorf("owner precheck job.create: %v", err)
	}
}

func TestRestoreTargets(t *testing.T) {
	f := newFixture()
	job := authz.Job{ID: f.job}
	foreignConn := uuid.New()
	tgt := func(conn uuid.UUID, db string, mod func(*authz.RestoreTarget)) authz.RestoreTarget {
		r := authz.RestoreTarget{ConnectionID: conn, Database: db}
		if mod != nil {
			mod(&r)
		}
		return r
	}
	newDB := func(r *authz.RestoreTarget) { r.NewDatabase = true }
	temp := func(r *authz.RestoreTarget) { r.Temporary = true }
	priv := func(r *authz.RestoreTarget) { r.PrivilegedRole = true }
	cases := []struct {
		name    string
		subject int
		target  *authz.RestoreTarget
		want    byte
	}{
		{"owner: own database", iOwner, ptr(tgt(f.conn, "app", nil)), 'Y'},
		{"owner: new db on own connection", iOwner, ptr(tgt(f.conn, "app_copy", newDB)), 'Y'},
		{"owner: other existing db on own connection", iOwner, ptr(tgt(f.conn, "billing", nil)), 'F'},
		{"owner: foreign connection", iOwner, ptr(tgt(foreignConn, "app", nil)), 'F'},
		{"owner: new db on foreign connection", iOwner, ptr(tgt(foreignConn, "x", newDB)), 'F'},
		{"owner: temporary connection", iOwner, ptr(tgt(f.conn, "app", temp)), 'F'},
		{"owner: privileged role", iOwner, ptr(tgt(f.conn, "app", priv)), 'F'},
		{"owner: no target", iOwner, nil, 'F'},
		{"owner: empty database", iOwner, ptr(tgt(f.conn, "", nil)), 'F'},
		{"maintainer: any connection", iMaintainer, ptr(tgt(foreignConn, "x", nil)), 'Y'},
		{"maintainer: temporary connection", iMaintainer, ptr(tgt(foreignConn, "x", temp)), 'F'},
		{"maintainer: privileged role", iMaintainer, ptr(tgt(foreignConn, "x", priv)), 'F'},
		{"maintainer: nil connection", iMaintainer, ptr(tgt(uuid.Nil, "x", nil)), 'F'},
		{"admin: temporary connection", iAdmin, ptr(tgt(foreignConn, "x", temp)), 'Y'},
		{"admin: privileged role", iAdmin, ptr(tgt(foreignConn, "x", priv)), 'Y'},
		{"admin: no target", iAdmin, nil, 'F'},
		{"user-not-owner: own other job's db", iUser, ptr(tgt(f.conn, "app", nil)), 'F'},
	}
	for _, c := range cases {
		res := []authz.Resource{job}
		if c.target != nil {
			res = append(res, *c.target)
		}
		if got := outcome(f.pol.Authorize(f.ctx(t, c.subject, false), authz.OpRestoreArtifact, res...)); got != c.want {
			t.Errorf("%s: got %c, want %c", c.name, got, c.want)
		}
	}
	// Two targets are ambiguous.
	err := f.pol.Authorize(f.ctx(t, iAdmin, false), authz.OpRestoreArtifact, job,
		tgt(f.conn, "app", nil), tgt(f.conn, "app2", nil))
	if !apierror.IsCode(err, apierror.Forbidden) {
		t.Errorf("two targets: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestLookupErrorsDeny(t *testing.T) {
	f := newFixture()
	boom := errors.New("db down")
	f.own.Err = boom
	err := f.pol.Authorize(f.ctx(t, iOwner, false), authz.OpJobView, authz.Job{ID: f.job})
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("ownership error: %v", err)
	}
	if typed, ok := apierror.As(err); ok {
		t.Fatal("a lookup failure must stay internal (masked), not a typed error:", typed)
	}
	f.own.Err = nil
	f.factors.Err = boom
	err = f.pol.Authorize(f.ctx(t, iAdmin, true), authz.OpRestoreGlobals)
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("factor lookup error: %v", err)
	}
}

func TestStepUpWindow(t *testing.T) {
	p := authztest.Principal(auth.RoleAdmin)
	p.StepUpAt = time.Now().Add(-3 * time.Minute)
	p.AAL2At = p.StepUpAt
	ctx := authztest.Context(t.Context(), p)

	def := authz.New(authz.Options{})
	if def.StepUpWindow() != authz.MaxStepUpWindow {
		t.Fatalf("default window %v", def.StepUpWindow())
	}
	if err := def.Authorize(ctx, authz.OpUserCreate); err != nil {
		t.Errorf("3m old step-up within 5m: %v", err)
	}
	if err := def.StepUp(ctx); err != nil {
		t.Errorf("@stepUp 3m within 5m: %v", err)
	}
	short := authz.New(authz.Options{StepUpWindow: 2 * time.Minute})
	if err := short.Authorize(ctx, authz.OpUserCreate); !apierror.IsCode(err, apierror.StepUpRequired) {
		t.Errorf("3m old step-up within 2m: %v", err)
	}
	if err := short.StepUp(ctx); !apierror.IsCode(err, apierror.StepUpRequired) {
		t.Errorf("@stepUp 3m within 2m: %v", err)
	}
	if err := short.Authorize(ctx, authz.OpRestoreGlobals); !apierror.IsCode(err, apierror.Requires2FAEnrollment) {
		t.Errorf("AAL2 3m within 2m without factor: %v", err)
	}
	// A configured window above the maximum is clamped.
	long := authz.New(authz.Options{StepUpWindow: time.Hour})
	if long.StepUpWindow() != authz.MaxStepUpWindow {
		t.Errorf("window not clamped: %v", long.StepUpWindow())
	}
	p.StepUpAt = time.Now().Add(-10 * time.Minute)
	if err := long.Authorize(authztest.Context(t.Context(), p), authz.OpUserCreate); !apierror.IsCode(err, apierror.StepUpRequired) {
		t.Errorf("10m old step-up with clamped window: %v", err)
	}
}

// A password-only step-up (AAL1 session) never satisfies the restore gate.
func TestRestoreNeedsSecondFactor(t *testing.T) {
	f := newFixture()
	p := *f.subjects[iAdmin]
	p.AAL = 1
	p.StepUpAt = time.Now()
	err := f.pol.Authorize(authztest.Context(t.Context(), p), authz.OpRestoreGlobals)
	if !apierror.IsCode(err, apierror.StepUpRequired) {
		t.Fatalf("AAL1 restore: %v", err)
	}
}

func TestDirectiveChecks(t *testing.T) {
	f := newFixture()
	admin, maint, anon := f.ctx(t, iAdmin, false), f.ctx(t, iMaintainer, false), t.Context()
	if err := f.pol.Authenticated(admin); err != nil {
		t.Error(err)
	}
	if err := f.pol.Authenticated(anon); !apierror.IsCode(err, apierror.Unauthenticated) {
		t.Error(err)
	}
	if err := f.pol.HasRole(maint, auth.RoleAdmin, auth.RoleMaintainer); err != nil {
		t.Error(err)
	}
	if err := f.pol.HasRole(maint, auth.RoleAdmin); !apierror.IsCode(err, apierror.Forbidden) {
		t.Error(err)
	}
	if err := f.pol.HasRole(admin); !apierror.IsCode(err, apierror.Forbidden) {
		t.Error("empty role list allowed")
	}
	if err := f.pol.HasRole(anon, auth.RoleAdmin); !apierror.IsCode(err, apierror.Unauthenticated) {
		t.Error(err)
	}
	if err := f.pol.StepUp(f.ctx(t, iUser, true)); !apierror.IsCode(err, apierror.StepUpRequired) {
		t.Error(err)
	}
	if err := f.pol.StepUp(anon); !apierror.IsCode(err, apierror.Unauthenticated) {
		t.Error(err)
	}
}

func TestJobScope(t *testing.T) {
	f := newFixture()
	for i, want := range map[int]authz.Scope{iAdmin: authz.ScopeAll, iMaintainer: authz.ScopeAll, iOwner: authz.ScopeOwned} {
		s, uid, err := f.pol.JobScope(f.ctx(t, i, false))
		if err != nil || s != want || uid != f.subjects[i].UserID {
			t.Errorf("%s: %v %v %v", subjectNames[i], s, uid, err)
		}
	}
	if _, _, err := f.pol.JobScope(t.Context()); !apierror.IsCode(err, apierror.Unauthenticated) {
		t.Errorf("anonymous scope: %v", err)
	}
}

func TestOwnerAssignment(t *testing.T) {
	f := newFixture()
	maint := f.subjects[iMaintainer]
	other := uuid.New()
	if err := f.pol.AuthorizeOwnerAssignment(f.ctx(t, iMaintainer, false), []uuid.UUID{other}); err != nil {
		t.Errorf("maintainer assigns another user: %v", err)
	}
	if err := f.pol.AuthorizeOwnerAssignment(f.ctx(t, iMaintainer, false), []uuid.UUID{other, maint.UserID}); !apierror.IsCode(err, apierror.Forbidden) {
		t.Errorf("maintainer assigns self: %v", err)
	}
	if err := f.pol.AuthorizeOwnerAssignment(f.ctx(t, iMaintainer, true), []uuid.UUID{other}); !apierror.IsCode(err, apierror.StepUpRequired) {
		t.Errorf("maintainer without step-up: %v", err)
	}
	admin := f.subjects[iAdmin]
	if err := f.pol.AuthorizeOwnerAssignment(f.ctx(t, iAdmin, false), []uuid.UUID{admin.UserID}); err != nil {
		t.Errorf("admin assigns self: %v", err)
	}
	if err := f.pol.AuthorizeOwnerAssignment(f.ctx(t, iOwner, false), []uuid.UUID{other}); !apierror.IsCode(err, apierror.Forbidden) {
		t.Errorf("owner assigns: %v", err)
	}
	if err := f.pol.AuthorizeOwnerAssignment(t.Context(), nil); !apierror.IsCode(err, apierror.Unauthenticated) {
		t.Errorf("anonymous assigns: %v", err)
	}
}

func TestRuleForIsACopy(t *testing.T) {
	r, ok := authz.RuleFor(authz.OpAuditLogView)
	if !ok || len(r.Roles) != 1 {
		t.Fatal(r, ok)
	}
	r.Roles[0] = auth.RoleUser
	r2, _ := authz.RuleFor(authz.OpAuditLogView)
	if r2.Roles[0] != auth.RoleAdmin {
		t.Fatal("RuleFor exposes the policy table")
	}
	if _, ok := authz.RuleFor("nope.nope"); ok {
		t.Fatal("unknown rule found")
	}
}

func TestDefaultsDenyOwnersAndFactors(t *testing.T) {
	pol := authz.New(authz.Options{})
	ctx := authztest.Context(t.Context(), authztest.Principal(auth.RoleUser))
	if err := pol.Authorize(ctx, authz.OpJobView, authz.Job{ID: uuid.New()}); !apierror.IsCode(err, apierror.Forbidden) {
		t.Errorf("NoOwnership owner check: %v", err)
	}
	actx := authztest.Context(t.Context(), authztest.Stale(authztest.Principal(auth.RoleAdmin)))
	if err := pol.Authorize(actx, authz.OpRestoreGlobals); !apierror.IsCode(err, apierror.Requires2FAEnrollment) {
		t.Errorf("NoSecondFactors: %v", err)
	}
}
