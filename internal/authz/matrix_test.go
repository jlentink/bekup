package authz_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/graph/apierror"
)

// The authorization matrix: for every operation the expected outcome per
// subject, written independently of the policy table so that a change to
// either one fails this test.
//
// Columns of who (one character each):
//
//	1 admin
//	2 maintainer
//	3 maintainer who is also an owner of the job (D-067)
//	4 backup owner of the job (role user)
//	5 user who does not own the job (owns another one)
//	6 anonymous
//
// Y = allowed, F = FORBIDDEN, U = UNAUTHENTICATED.
//
// fresh is the freshness every allowed subject needs: "-" none, "S"
// step-up (STEP_UP_REQUIRED when stale), "2" a fresh second factor
// (STEP_UP_REQUIRED when stale, REQUIRES_2FA_ENROLLMENT without a factor).
//
// List operations (job.list, run.list, ...) allow every user: results are
// filtered to the jobs they own (authz.JobScope).
type expect struct {
	who   string
	fresh string
}

var matrix = map[authz.Operation]expect{
	authz.OpAccountView:              {"YYYYYU", "-"},
	authz.OpAccountLogout:            {"YYYYYU", "-"},
	authz.OpAccountStepUp:            {"YYYYYU", "-"},
	authz.OpAccountSecondFactorLogin: {"YYYYYU", "-"},
	authz.OpAccountPasswordChange:    {"YYYYYU", "S"},
	authz.OpAccountSessionsView:      {"YYYYYU", "-"},
	authz.OpAccountSessionsRevoke:    {"YYYYYU", "-"},
	authz.OpAccountSecondFactorEdit:  {"YYYYYU", "S"},

	authz.OpUserList:             {"YFFFFU", "-"},
	authz.OpUserView:             {"YFFFFU", "-"},
	authz.OpUserCreate:           {"YFFFFU", "S"},
	authz.OpUserUpdate:           {"YFFFFU", "S"},
	authz.OpUserDisable:          {"YFFFFU", "S"},
	authz.OpUserEnable:           {"YFFFFU", "S"},
	authz.OpUserDelete:           {"YFFFFU", "S"},
	authz.OpUserPasswordReset:    {"YFFFFU", "S"},
	authz.OpUserSecondFactorEdit: {"YFFFFU", "S"},
	authz.OpUserSessionsView:     {"YFFFFU", "-"},
	authz.OpUserSessionsRevoke:   {"YFFFFU", "S"},
	authz.OpUserDirectoryView:    {"YYYFFU", "-"},

	authz.OpMFAPolicyView:   {"YFFFFU", "-"},
	authz.OpMFAPolicyUpdate: {"YFFFFU", "S"},

	authz.OpSettingsView:        {"YFFFFU", "-"},
	authz.OpSettingsUpdate:      {"YFFFFU", "S"},
	authz.OpSMTPView:            {"YFFFFU", "-"},
	authz.OpSMTPUpdate:          {"YFFFFU", "S"},
	authz.OpSMTPTest:            {"YFFFFU", "-"},
	authz.OpSSRFAllowlistView:   {"YFFFFU", "-"},
	authz.OpSSRFAllowlistUpdate: {"YFFFFU", "S"},

	authz.OpConnectionList:            {"YYYFFU", "-"},
	authz.OpConnectionView:            {"YYYFFU", "-"},
	authz.OpConnectionCreate:          {"YFFFFU", "S"},
	authz.OpConnectionUpdate:          {"YFFFFU", "-"},
	authz.OpConnectionSecretUpdate:    {"YFFFFU", "S"},
	authz.OpConnectionDelete:          {"YFFFFU", "-"},
	authz.OpConnectionTest:            {"YYYFFU", "-"},
	authz.OpConnectionListDatabases:   {"YYYFFU", "-"},
	authz.OpConnectionPrePull:         {"YYYFFU", "-"},
	authz.OpConnectionHostKeyConfirm:  {"YFFFFU", "S"},
	authz.OpConnectionCertFetch:       {"YFFFFU", "-"},
	authz.OpConnectionPinConfirm:      {"YFFFFU", "S"},
	authz.OpConnectionTLSDowngrade:    {"YFFFFU", "S"},
	authz.OpConnectionSSHKeyGenerate:  {"YFFFFU", "S"},
	authz.OpTemporaryConnectionCreate: {"YFFFFU", "S"},

	authz.OpDestinationList:           {"YYYFFU", "-"},
	authz.OpDestinationView:           {"YYYFFU", "-"},
	authz.OpDestinationCreate:         {"YFFFFU", "S"},
	authz.OpDestinationUpdate:         {"YFFFFU", "-"},
	authz.OpDestinationSecretUpdate:   {"YFFFFU", "S"},
	authz.OpDestinationDelete:         {"YFFFFU", "-"},
	authz.OpDestinationTest:           {"YYYFFU", "-"},
	authz.OpDestinationHostKeyConfirm: {"YFFFFU", "S"},
	authz.OpDestinationSSHKeyGenerate: {"YFFFFU", "S"},
	authz.OpStorageUsageView:          {"YYYFFU", "-"},

	authz.OpJobList:               {"YYYYYU", "-"},
	authz.OpJobView:               {"YYYYFU", "-"},
	authz.OpJobCreate:             {"YYYFFU", "-"},
	authz.OpJobUpdate:             {"YYYFFU", "-"},
	authz.OpJobDelete:             {"YYYFFU", "-"},
	authz.OpJobSchedulePreview:    {"YYYFFU", "-"},
	authz.OpJobOwnersAssign:       {"YYYFFU", "S"},
	authz.OpJobRetentionUpdate:    {"YYYFFU", "-"},
	authz.OpJobEncryptionKeysEdit: {"YYYFFU", "S"},
	authz.OpJobEncryptionOptOut:   {"YFFFFU", "S"},
	authz.OpJobPassphraseExport:   {"YFFFFU", "S"},
	authz.OpJobTestRestoreEdit:    {"YYYFFU", "-"},
	authz.OpJobSharedChannelsEdit: {"YFFFFU", "-"},

	authz.OpRunList:     {"YYYYYU", "-"},
	authz.OpRunView:     {"YYYYFU", "-"},
	authz.OpRunLogsView: {"YYYYFU", "-"},
	authz.OpRunNow:      {"YYYYFU", "-"},
	authz.OpRunCancel:   {"YYYFFU", "-"},

	authz.OpArtifactList:     {"YYYYYU", "-"},
	authz.OpArtifactView:     {"YYYYFU", "-"},
	authz.OpArtifactDownload: {"YFFYFU", "S"},

	authz.OpRestoreTargetsList: {"YYYYFU", "-"},
	authz.OpRestoreArtifact:    {"YYYYFU", "2"},
	authz.OpRestoreGlobals:     {"YFFFFU", "2"},
	authz.OpRestoreView:        {"YYYYFU", "-"},

	authz.OpSharedChannelList:     {"YFFFFU", "-"},
	authz.OpSharedChannelEdit:     {"YFFFFU", "S"},
	authz.OpSharedChannelTest:     {"YFFFFU", "-"},
	authz.OpPersonalChannelEdit:   {"YYYYYU", "S"},
	authz.OpPersonalChannelTest:   {"YYYYYU", "-"},
	authz.OpNotificationPrefsEdit: {"YYYYYU", "-"},
	authz.OpNotificationInbox:     {"YYYYYU", "-"},

	authz.OpDashboardView:    {"YYYYYU", "-"},
	authz.OpAlertList:        {"YFFFFU", "-"},
	authz.OpAlertAcknowledge: {"YFFFFU", "-"},
	authz.OpRunnerHealthView: {"YYYFFU", "-"},

	authz.OpAuditLogView:         {"YFFFFU", "-"},
	authz.OpAuditChainStatusView: {"YFFFFU", "-"},
	authz.OpKeysView:             {"YFFFFU", "-"},
}

var subjectNames = [6]string{"admin", "maintainer", "maintainer-owner", "owner", "user-not-owner", "anonymous"}

// fixture is the world of the matrix: job J (owned by the owner and the
// maintainer-owner) backs up database "app" on connection C; job J2 (owned
// by the other user) backs up "other" on C2.
type fixture struct {
	pol      *authz.Policy
	own      *authztest.Ownership
	factors  *authztest.SecondFactors
	subjects [6]*auth.Principal
	job      uuid.UUID
	conn     uuid.UUID
}

func newFixture() *fixture {
	f := &fixture{own: &authztest.Ownership{}, factors: &authztest.SecondFactors{Enrolled: map[uuid.UUID]bool{}},
		job: uuid.New(), conn: uuid.New()}
	roles := []auth.Role{auth.RoleAdmin, auth.RoleMaintainer, auth.RoleMaintainer, auth.RoleUser, auth.RoleUser}
	for i, r := range roles {
		p := authztest.Principal(r)
		f.subjects[i] = &p
		f.factors.Enrolled[p.UserID] = true
	}
	job2, conn2 := uuid.New(), uuid.New()
	f.own.AddOwner(f.job, f.subjects[2].UserID)
	f.own.AddOwner(f.job, f.subjects[3].UserID)
	f.own.AddOwner(job2, f.subjects[4].UserID)
	f.own.AddDatabase(f.job, f.conn, "app")
	f.own.AddDatabase(job2, conn2, "other")
	f.pol = authz.New(authz.Options{Ownership: f.own, SecondFactors: f.factors})
	return f
}

func (f *fixture) ctx(t *testing.T, i int, stale bool) context.Context {
	if f.subjects[i] == nil {
		return t.Context()
	}
	p := *f.subjects[i]
	if stale {
		p = authztest.Stale(p)
	}
	return authztest.Context(t.Context(), p)
}

func (f *fixture) resources(op authz.Operation) []authz.Resource {
	res := []authz.Resource{authz.Job{ID: f.job}}
	if r, _ := authz.RuleFor(op); r.NeedsTarget {
		res = append(res, authz.RestoreTarget{ConnectionID: f.conn, Database: "app"})
	}
	return res
}

func outcome(err error) byte {
	switch {
	case err == nil:
		return 'Y'
	case apierror.IsCode(err, apierror.Forbidden):
		return 'F'
	case apierror.IsCode(err, apierror.Unauthenticated):
		return 'U'
	case apierror.IsCode(err, apierror.StepUpRequired):
		return 'S'
	case apierror.IsCode(err, apierror.Requires2FAEnrollment):
		return 'E'
	default:
		return '?'
	}
}

func TestMatrix(t *testing.T) {
	f := newFixture()
	for _, op := range authz.Operations() {
		want, ok := matrix[op]
		if !ok {
			continue // reported by TestMatrixCoversCatalog
		}
		if len(want.who) != len(subjectNames) {
			t.Fatalf("%s: matrix row %q must have %d columns", op, want.who, len(subjectNames))
		}
		res := f.resources(op)
		for i, name := range subjectNames {
			got := outcome(f.pol.Authorize(f.ctx(t, i, false), op, res...))
			if got != want.who[i] {
				t.Errorf("%s as %s (fresh): got %c, want %c", op, name, got, want.who[i])
			}
			// Precheck (the root-field gate) never denies what Authorize allows.
			if want.who[i] == 'Y' {
				if err := f.pol.Precheck(f.ctx(t, i, false), op); err != nil {
					t.Errorf("%s as %s: Precheck denies an allowed operation: %v", op, name, err)
				}
			}
			if want.who[i] != 'Y' {
				continue
			}
			// The same subject with a stale step-up / second factor.
			wantStale := byte('Y')
			if want.fresh == "S" || want.fresh == "2" {
				wantStale = 'S'
			}
			if got := outcome(f.pol.Authorize(f.ctx(t, i, true), op, res...)); got != wantStale {
				t.Errorf("%s as %s (stale): got %c, want %c", op, name, got, wantStale)
			}
		}
		if want.fresh == "2" {
			// Without an enrolled factor: enrollment first (D-057).
			for i := range 5 {
				if want.who[i] != 'Y' {
					continue
				}
				uid := f.subjects[i].UserID
				f.factors.Enrolled[uid] = false
				if got := outcome(f.pol.Authorize(f.ctx(t, i, true), op, res...)); got != 'E' {
					t.Errorf("%s as %s without a factor: got %c, want E", op, subjectNames[i], got)
				}
				f.factors.Enrolled[uid] = true
			}
		}
	}
}

// TestMatrixCoversCatalog fails when an operation constant has no policy
// rule or no matrix row, or a matrix row names an unknown operation.
func TestMatrixCoversCatalog(t *testing.T) {
	declared := declaredOperations(t)
	if len(declared) == 0 {
		t.Fatal("no Operation constants found in operations.go")
	}
	known := map[authz.Operation]bool{}
	for _, op := range authz.Operations() {
		known[op] = true
	}
	for _, op := range declared {
		if !known[op] {
			t.Errorf("operation %q is declared but has no rule in the policy table", op)
		}
		if _, ok := matrix[op]; !ok {
			t.Errorf("operation %q has no row in the authz matrix (matrix_test.go)", op)
		}
	}
	declaredSet := map[authz.Operation]bool{}
	for _, op := range declared {
		declaredSet[op] = true
	}
	for op := range known {
		if !declaredSet[op] {
			t.Errorf("policy rule for %q has no Operation constant", op)
		}
	}
	for op := range matrix {
		if !known[op] {
			t.Errorf("matrix row for unknown operation %q", op)
		}
	}
}

// declaredOperations parses operations.go and returns the values of all
// constants of type Operation.
func declaredOperations(t *testing.T) []authz.Operation {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "operations.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []authz.Operation
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			id, ok := vs.Type.(*ast.Ident)
			if !ok || id.Name != "Operation" {
				continue
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("Operation constant with a non-literal value")
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, authz.Operation(s))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	for i := 1; i < len(out); i++ {
		if out[i] == out[i-1] {
			t.Errorf("duplicate operation value %q", out[i])
		}
	}
	return out
}

// TestMatrixInvariants checks the global rules of the decisions on the
// table itself.
func TestMatrixInvariants(t *testing.T) {
	for op, e := range matrix {
		if e.who[0] != 'Y' {
			t.Errorf("%s: admins may do everything (D-011)", op)
		}
		if e.who[5] != 'U' {
			t.Errorf("%s: anonymous callers must be UNAUTHENTICATED", op)
		}
		// D-067: being an owner never gives a maintainer more.
		if e.who[1] != e.who[2] {
			t.Errorf("%s: maintainer-owner differs from maintainer (D-067)", op)
		}
	}
	if matrix[authz.OpArtifactDownload].who[1] != 'F' {
		t.Error("maintainers must never download (D-012, D-067)")
	}
}
