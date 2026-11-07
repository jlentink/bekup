package apiaccess

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// noDB fails every call: the unit tests prove that validation and
// authorization happen before the database is touched.
type noDB struct{ calls int }

var errNoDB = errors.New("no database in unit tests")

func (d *noDB) Begin(context.Context) (pgx.Tx, error) { d.calls++; return nil, errNoDB }
func (d *noDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	d.calls++
	return pgconn.CommandTag{}, errNoDB
}
func (d *noDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	d.calls++
	return nil, errNoDB
}
func (d *noDB) QueryRow(context.Context, string, ...any) pgx.Row { d.calls++; return errRow{} }

type errRow struct{}

func (errRow) Scan(...any) error { return errNoDB }

type nopAuditor struct{}

func (nopAuditor) RecordTx(context.Context, *sqlc.Queries, audit.Event) (audit.Entry, error) {
	return audit.Entry{}, nil
}

// fakeAuthorizer records the calls and answers err.
type fakeAuthorizer struct {
	err   error
	calls []string
}

func (f *fakeAuthorizer) Authorize(_ context.Context, op authz.Operation, res ...authz.Resource) error {
	f.calls = append(f.calls, fmt.Sprintf("%s%v", op, res))
	return f.err
}

func (f *fakeAuthorizer) JobScope(context.Context) (authz.Scope, uuid.UUID, error) {
	return authz.ScopeAll, uuid.Nil, nil
}

var testNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func newTestService(t *testing.T, a *fakeAuthorizer) (*Service, *noDB) {
	t.Helper()
	d := &noDB{}
	s, err := NewService(ServiceOptions{DB: d, Auditor: nopAuditor{}, Authorizer: a, Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatal(err)
	}
	return s, d
}

func principalCtx(t *testing.T, role auth.Role) context.Context {
	t.Helper()
	return auth.WithPrincipal(t.Context(), auth.Principal{
		UserID: uuid.New(), SessionID: uuid.New(), Role: role, AAL: auth.AAL1, StepUpAt: testNow,
	})
}

func fieldOf(t *testing.T, err error) string {
	t.Helper()
	var fe *serviceutil.FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("error %v is not a field error", err)
	}
	return fe.Field
}

func TestNewServiceRequires(t *testing.T) {
	if _, err := NewService(ServiceOptions{}); err == nil {
		t.Fatal("service without DB, auditor and authorizer accepted")
	}
}

// TestCreateValidatesBeforeTheDatabase: every input error is a field
// error, the jobs are authorized before the other fields are checked, and
// nothing reaches the database.
func TestCreateValidatesBeforeTheDatabase(t *testing.T) {
	job := uuid.New()
	good := CreateInput{Name: "ci", JobIDs: []uuid.UUID{job}, ExpiresAt: testNow.Add(90 * 24 * time.Hour),
		AllowedCIDRs: []string{"192.0.2.0/24"}}
	cases := []struct {
		name  string
		edit  func(*CreateInput)
		field string
	}{
		{"no jobs", func(in *CreateInput) { in.JobIDs = nil }, FieldJobIDs},
		{"nil job", func(in *CreateInput) { in.JobIDs = []uuid.UUID{uuid.Nil} }, FieldJobIDs},
		{"too many jobs", func(in *CreateInput) {
			in.JobIDs = make([]uuid.UUID, MaxJobsPerToken+1)
			for i := range in.JobIDs {
				in.JobIDs[i] = uuid.New()
			}
		}, FieldJobIDs},
		{"empty name", func(in *CreateInput) { in.Name = "  " }, FieldName},
		{"long name", func(in *CreateInput) { in.Name = strings.Repeat("é", MaxNameRunes+1) }, FieldName},
		{"control character", func(in *CreateInput) { in.Name = "ci\npipeline" }, FieldName},
		{"line separator", func(in *CreateInput) { in.Name = "ci\u2028pipeline" }, FieldName},
		{"no expiry", func(in *CreateInput) { in.ExpiresAt = time.Time{} }, FieldExpiresAt},
		{"expiry too soon", func(in *CreateInput) { in.ExpiresAt = testNow.Add(22 * time.Hour) }, FieldExpiresAt},
		{"expiry in the past", func(in *CreateInput) { in.ExpiresAt = testNow.Add(-time.Hour) }, FieldExpiresAt},
		{"expiry too late", func(in *CreateInput) { in.ExpiresAt = testNow.Add(MaxExpiry + 2*time.Hour) }, FieldExpiresAt},
		{"expiry and never", func(in *CreateInput) { in.NeverExpires = true }, FieldExpiresAt},
		{"no addresses", func(in *CreateInput) { in.AllowedCIDRs = nil }, FieldAllowedCIDRs},
		{"both", func(in *CreateInput) { in.AllowAnyAddress = true }, FieldAllowedCIDRs},
		{"bad network", func(in *CreateInput) { in.AllowedCIDRs = []string{"10.0.0.1/8"} }, FieldAllowedCIDRs},
	}
	for _, c := range cases {
		a := &fakeAuthorizer{}
		s, d := newTestService(t, a)
		in := good
		in.JobIDs = append([]uuid.UUID(nil), good.JobIDs...)
		c.edit(&in)
		_, err := s.Create(principalCtx(t, auth.RoleAdmin), in)
		if got := fieldOf(t, err); got != c.field {
			t.Errorf("%s: field %q, want %q (%v)", c.name, got, c.field, err)
		}
		if d.calls != 0 {
			t.Errorf("%s: %d database calls", c.name, d.calls)
		}
	}
}

// TestCreateAuthorizesEveryJob: the policy sees every (deduplicated) job;
// a refusal stops before anything else, also before the other input
// checks.
func TestCreateAuthorizesEveryJob(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	denied := apierror.New(apierror.Forbidden, "not allowed")
	auth1 := &fakeAuthorizer{err: denied}
	s, d := newTestService(t, auth1)
	_, err := s.Create(principalCtx(t, auth.RoleUser), CreateInput{Name: "", JobIDs: []uuid.UUID{a, b, a}})
	if !errors.Is(err, denied) {
		t.Fatalf("Create = %v, want the policy's refusal", err)
	}
	if len(auth1.calls) != 1 || !strings.HasPrefix(auth1.calls[0], string(authz.OpAPITokenCreate)) ||
		strings.Count(auth1.calls[0], "{") != 2 {
		t.Fatalf("authorize calls %v", auth1.calls)
	}
	if d.calls != 0 {
		t.Fatalf("%d database calls", d.calls)
	}
	// Without a principal: the policy answers (UNAUTHENTICATED).
	auth2 := &fakeAuthorizer{err: apierror.New(apierror.Unauthenticated, "authentication required")}
	s2, _ := newTestService(t, auth2)
	if _, err := s2.Create(t.Context(), CreateInput{}); !apierror.IsCode(err, apierror.Unauthenticated) {
		t.Fatalf("anonymous: %v", err)
	}
}

func TestListAndManageAuthorizeFirst(t *testing.T) {
	denied := apierror.New(apierror.Forbidden, "not allowed")
	a := &fakeAuthorizer{err: denied}
	s, d := newTestService(t, a)
	ctx := principalCtx(t, auth.RoleMaintainer)
	if _, err := s.List(ctx, nil); !errors.Is(err, denied) {
		t.Fatalf("List = %v", err)
	}
	if d.calls != 0 {
		t.Fatalf("List touched the database %d times", d.calls)
	}
	for _, op := range []func() error{
		func() error { _, err := s.Revoke(t.Context(), uuid.New()); return err },
		func() error { _, err := s.UpdateAddresses(t.Context(), uuid.New(), nil, true); return err },
	} {
		if err := op(); !errors.Is(err, denied) {
			t.Fatalf("anonymous manage = %v", err)
		}
	}
}

func TestExpiryBounds(t *testing.T) {
	s, _ := newTestService(t, &fakeAuthorizer{})
	for _, d := range []time.Duration{24 * time.Hour, 23*time.Hour + 30*time.Minute, 30 * 24 * time.Hour, MaxExpiry, MaxExpiry + 30*time.Minute} {
		got, err := s.checkExpiry(testNow.Add(d), false)
		if err != nil || got == nil {
			t.Errorf("%s: %v %v", d, got, err)
			continue
		}
		if got.Location() != time.UTC || !got.Equal(testNow.Add(d)) {
			t.Errorf("%s: %v", d, got)
		}
	}
	// Database precision.
	got, err := s.checkExpiry(testNow.Add(48*time.Hour+1234*time.Nanosecond), false)
	if err != nil || got == nil || got.Nanosecond()%1000 != 0 {
		t.Fatalf("truncation: %v %v", got, err)
	}
	// Never (D-229): an explicit choice without a date.
	if got, err := s.checkExpiry(time.Time{}, true); err != nil || got != nil {
		t.Fatalf("never: %v %v", got, err)
	}
	if _, err := s.checkExpiry(testNow.Add(48*time.Hour), true); fieldOf(t, err) != FieldExpiresAt {
		t.Fatalf("never with a date: %v", err)
	}
	if _, err := s.checkExpiry(time.Time{}, false); fieldOf(t, err) != FieldExpiresAt {
		t.Fatalf("neither: %v", err)
	}
}

func TestNormalizeJobIDs(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	got, err := normalizeJobIDs([]uuid.UUID{b, a, b, a})
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestCreatedNeverPrintsThePlaintext(t *testing.T) {
	c := Created{Token: Token{ID: uuid.New()}, Plaintext: "bkp_secret"}
	for _, s := range []string{c.String(), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c), fmt.Sprintf("%+v", &c)} {
		if strings.Contains(s, "bkp_") {
			t.Fatalf("printed %q", s)
		}
	}
}

// TestWidensAndSameNetworks: what counts as a widening (M1) and that a
// reordered list is unchanged (finding 7).
func TestWidensAndSameNetworks(t *testing.T) {
	p := func(ss ...string) []netip.Prefix {
		out := make([]netip.Prefix, len(ss))
		for i, s := range ss {
			out[i] = netip.MustParsePrefix(s)
		}
		return out
	}
	cases := []struct {
		name            string
		old             []netip.Prefix
		oldAny          bool
		nets            []netip.Prefix
		anyAddr, widens bool
	}{
		{"same", p("192.0.2.0/24"), false, p("192.0.2.0/24"), false, false},
		{"narrower", p("192.0.2.0/24"), false, p("192.0.2.128/25"), false, false},
		{"subset", p("192.0.2.0/24", "198.51.100.0/24"), false, p("198.51.100.7/32"), false, false},
		{"wider prefix", p("192.0.2.0/25"), false, p("192.0.2.0/24"), false, true},
		{"new network", p("192.0.2.0/24"), false, p("192.0.2.0/24", "203.0.113.0/24"), false, true},
		{"other family", p("10.0.0.0/8"), false, p("2001:db8::/32"), false, true},
		{"v6 inside", p("2001:db8::/32"), false, p("2001:db8:1::/48"), false, false},
		{"to any", p("192.0.2.0/24"), false, nil, true, true},
		{"from any", nil, true, p("192.0.2.0/24"), false, false},
		{"any to any", nil, true, nil, true, false},
	}
	for _, c := range cases {
		if got := widens(c.old, c.oldAny, c.nets, c.anyAddr); got != c.widens {
			t.Errorf("%s: widens = %v, want %v", c.name, got, c.widens)
		}
	}
	a := p("192.0.2.0/24", "2001:db8::/32", "10.0.0.0/8")
	if !sameNetworks(a, p("2001:db8::/32", "10.0.0.0/8", "192.0.2.0/24")) {
		t.Error("a reordered list differs")
	}
	if sameNetworks(a, p("192.0.2.0/24", "2001:db8::/32")) || sameNetworks(a, p("192.0.2.0/24", "2001:db8::/32", "10.0.0.0/9")) {
		t.Error("different lists are the same")
	}
	if !sameNetworks(a, a) || a[0] != netip.MustParsePrefix("192.0.2.0/24") {
		t.Error("sameNetworks changed its input")
	}
}
