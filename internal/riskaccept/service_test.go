package riskaccept

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// countingDB fails every call and counts them: validation must happen
// before the database.
type countingDB struct{ calls int }

func (d *countingDB) Begin(context.Context) (pgx.Tx, error) {
	d.calls++
	return nil, errors.New("no database")
}

func (d *countingDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	d.calls++
	return pgconn.CommandTag{}, errors.New("no database")
}

func (d *countingDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	d.calls++
	return nil, errors.New("no database")
}

func (d *countingDB) QueryRow(context.Context, string, ...any) pgx.Row {
	d.calls++
	return errRow{}
}

type errRow struct{}

func (errRow) Scan(...any) error { return errors.New("no database") }

type nopAuditor struct{}

func (nopAuditor) RecordTx(context.Context, *sqlc.Queries, audit.Event) (audit.Entry, error) {
	return audit.Entry{}, nil
}

func newTestService(t *testing.T) (*Service, *countingDB) {
	t.Helper()
	d := &countingDB{}
	s, err := New(Options{DB: d, Auditor: nopAuditor{}, Authorizer: authz.New(authz.Options{Ownership: &authztest.Ownership{}})})
	if err != nil {
		t.Fatal(err)
	}
	return s, d
}

func ctxAs(t *testing.T, r auth.Role) context.Context {
	return authztest.Context(t.Context(), authztest.Principal(r))
}

func fieldOf(err error) string {
	var fe *serviceutil.FieldError
	if errors.As(err, &fe) {
		return fe.Field
	}
	return ""
}

func TestAcceptValidatesBeforeTheDatabase(t *testing.T) {
	conn := uuid.New()
	good := AcceptInput{Kind: KindConnectionTLS, ConnectionID: conn, ConditionID: strings.Repeat("ab", 32), Duration: DurationMonth, Reason: "legacy server"}
	cases := []struct {
		name  string
		edit  func(*AcceptInput)
		field string
	}{
		{"unknown kind", func(in *AcceptInput) { in.Kind = "nope" }, FieldKind},
		{"missing subject", func(in *AcceptInput) { in.ConnectionID = uuid.Nil }, FieldSubject},
		{"job on a connection kind", func(in *AcceptInput) { in.JobID = uuid.New() }, FieldSubject},
		{"bad duration", func(in *AcceptInput) { in.Duration = "week" }, FieldDuration},
		{"empty reason", func(in *AcceptInput) { in.Reason = "  " }, FieldReason},
		{"long reason", func(in *AcceptInput) { in.Reason = strings.Repeat("x", MaxReasonRunes+1) }, FieldReason},
		{"control in reason", func(in *AcceptInput) { in.Reason = "a\x01b" }, FieldReason},
		{"bad condition id", func(in *AcceptInput) { in.ConditionID = "zz" }, FieldConditionID},
	}
	for _, c := range cases {
		s, d := newTestService(t)
		in := good
		c.edit(&in)
		_, err := s.Accept(ctxAs(t, auth.RoleAdmin), in)
		if got := fieldOf(err); got != c.field {
			t.Errorf("%s: field %q, want %q (%v)", c.name, got, c.field, err)
		}
		if d.calls != 0 {
			t.Errorf("%s: %d database calls", c.name, d.calls)
		}
	}
	// A line break is fine in a reason.
	if r, err := normalizeReason(" two\nlines "); err != nil || r != "two\nlines" {
		t.Fatalf("reason %q %v", r, err)
	}
}

// TestAcceptRefusals: maintainers never, owners not for instance or
// connection kinds, anonymous callers are UNAUTHENTICATED; nothing reaches
// the database.
func TestAcceptRefusals(t *testing.T) {
	in := AcceptInput{Kind: KindInsecureDBTLS, ConditionID: strings.Repeat("ab", 32), Duration: DurationForever, Reason: "managed database"}
	for _, r := range []auth.Role{auth.RoleMaintainer, auth.RoleUser} {
		s, d := newTestService(t)
		_, err := s.Accept(ctxAs(t, r), in)
		if !apierror.IsCode(err, apierror.Forbidden) && !errors.Is(err, ErrForbidden) {
			t.Errorf("%s: %v", r, err)
		}
		if d.calls != 0 {
			t.Errorf("%s: %d database calls", r, d.calls)
		}
	}
	s, _ := newTestService(t)
	if _, err := s.Accept(t.Context(), in); !apierror.IsCode(err, apierror.Unauthenticated) {
		t.Fatalf("anonymous: %v", err)
	}
	// An owner with a job they do not own.
	job := AcceptInput{Kind: KindEncryptionOptOut, JobID: uuid.New(), ConditionID: strings.Repeat("ab", 32), Duration: DurationYear, Reason: "x"}
	if _, err := s.Accept(ctxAs(t, auth.RoleUser), job); !apierror.IsCode(err, apierror.Forbidden) {
		t.Fatalf("foreign job: %v", err)
	}
}

func TestSubjectRules(t *testing.T) {
	c, j := uuid.New(), uuid.New()
	for _, k := range Kinds {
		var err error
		switch {
		case k.OnConnection():
			err = checkSubject(k, c, uuid.Nil)
		case k.OnJob():
			err = checkSubject(k, uuid.Nil, j)
		default:
			err = checkSubject(k, uuid.Nil, uuid.Nil)
		}
		if err != nil {
			t.Errorf("%s: %v", k, err)
		}
		if checkSubject(k, c, j) == nil {
			t.Errorf("%s: two subjects accepted", k)
		}
	}
}
