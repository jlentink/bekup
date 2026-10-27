package resolvers

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
)

type listerFunc func(context.Context, audit.Filter, int64, int) (audit.Page, error)

func (f listerFunc) List(ctx context.Context, flt audit.Filter, c int64, l int) (audit.Page, error) {
	return f(ctx, flt, c, l)
}

func sp(s string) *string { return &s }
func ip(i int) *int       { return &i }

func TestAuditCursor(t *testing.T) {
	for _, id := range []int64{1, 42, 1 << 62} {
		c := encodeAuditCursor(id)
		got, err := decodeAuditCursor(&c)
		if err != nil || got != id {
			t.Errorf("%d: round trip %d, %v", id, got, err)
		}
	}
	if got, err := decodeAuditCursor(nil); got != 0 || err != nil {
		t.Errorf("nil cursor: %d %v", got, err)
	}
	if got, err := decodeAuditCursor(sp("")); got != 0 || err != nil {
		t.Errorf("empty cursor: %d %v", got, err)
	}
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	for _, bad := range []string{"!!", enc("42"), enc("audit:"), enc("audit:0"), enc("audit:-1"), enc("audit:01"),
		enc("audit:1x"), enc("run:42"), strings.Repeat("A", 100)} {
		if _, err := decodeAuditCursor(&bad); !apierror.IsCode(err, apierror.Validation) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestPageSize(t *testing.T) {
	if n, err := pageSize(nil); n != audit.DefaultListLimit || err != nil {
		t.Errorf("default: %d %v", n, err)
	}
	if n, err := pageSize(ip(audit.MaxListLimit)); n != audit.MaxListLimit || err != nil {
		t.Errorf("max: %d %v", n, err)
	}
	for _, bad := range []int{0, -1, audit.MaxListLimit + 1} {
		if _, err := pageSize(ip(bad)); !apierror.IsCode(err, apierror.Validation) {
			t.Errorf("%d: %v", bad, err)
		}
	}
}

func TestAuditFilter(t *testing.T) {
	uid := uuid.New()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	kind := model.AuditActorKindCli
	f, err := auditFilter(&model.AuditLogFilter{
		Action: sp("user.created"), ActionPrefix: sp("user."), ActorUserID: &uid, ActorKind: &kind,
		TargetType: sp("user"), TargetID: sp("u1"), From: &from, To: &to,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := audit.Filter{Action: "user.created", ActionPrefix: "user.", ActorUserID: &uid, ActorKind: audit.ActorCLI,
		TargetType: "user", TargetID: "u1", From: from, To: to}
	if f.Action != want.Action || f.ActionPrefix != want.ActionPrefix || *f.ActorUserID != uid ||
		f.ActorKind != want.ActorKind || f.TargetType != want.TargetType || f.TargetID != want.TargetID ||
		!f.From.Equal(from) || !f.To.Equal(to) {
		t.Errorf("filter %+v", f)
	}
	if f, err := auditFilter(nil); err != nil || f != (audit.Filter{}) {
		t.Errorf("nil filter: %+v %v", f, err)
	}
	bogus := model.AuditActorKind("ROOT")
	for name, in := range map[string]*model.AuditLogFilter{
		"empty action":     {Action: sp("")},
		"long action":      {Action: sp(strings.Repeat("a", 129))},
		"long prefix":      {ActionPrefix: sp(strings.Repeat("a", 129))},
		"long target":      {TargetID: sp(strings.Repeat("a", 257))},
		"long type":        {TargetType: sp(strings.Repeat("a", 129))},
		"bad utf8":         {TargetID: sp("\xff")},
		"bad kind":         {ActorKind: &bogus},
		"to before from":   {From: &to, To: &from},
		"to equal to from": {From: &from, To: &from},
	} {
		if _, err := auditFilter(in); !apierror.IsCode(err, apierror.Validation) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAuditLogResolver(t *testing.T) {
	ctx := t.Context()
	// Not configured: an internal (masked) error.
	if _, err := (&Resolver{}).Query().AuditLog(ctx, nil, nil, nil); err == nil {
		t.Fatal("nil lister accepted")
	} else if typed, ok := apierror.As(err); ok {
		t.Fatalf("typed error for a server problem: %v", typed)
	}
	var gotCursor int64
	r := &Resolver{AuditLog: listerFunc(func(_ context.Context, _ audit.Filter, c int64, _ int) (audit.Page, error) {
		gotCursor = c
		return audit.Page{Entries: []audit.ListEntry{
			{ID: 9, ActorKind: audit.ActorSystem, Action: "a.b"},
			{ID: 7, ActorKind: audit.ActorCLI, Action: "a.c", Details: []byte(`{"x":"y"}`)},
		}}, nil
	})}
	after := encodeAuditCursor(10)
	conn, err := r.Query().AuditLog(ctx, nil, ip(2), &after)
	if err != nil {
		t.Fatal(err)
	}
	if gotCursor != 10 || len(conn.Edges) != 2 || conn.PageInfo.HasNextPage || !conn.PageInfo.HasPreviousPage {
		t.Fatalf("conn %+v", conn)
	}
	if *conn.PageInfo.StartCursor != encodeAuditCursor(9) || *conn.PageInfo.EndCursor != encodeAuditCursor(7) {
		t.Error("cursors")
	}
	n0, n1 := conn.Edges[0].Node, conn.Edges[1].Node
	if n0.ActorKind != model.AuditActorKindSystem || n0.Details != "{}" || n0.IP != nil || n0.ID != "9" {
		t.Errorf("node 0 %+v", n0)
	}
	if n1.ActorKind != model.AuditActorKindCli || n1.Details != `{"x":"y"}` {
		t.Errorf("node 1 %+v", n1)
	}
	// Validation errors stop before the lister.
	r.AuditLog = listerFunc(func(context.Context, audit.Filter, int64, int) (audit.Page, error) {
		t.Fatal("lister called")
		return audit.Page{}, nil
	})
	if _, err := r.Query().AuditLog(ctx, nil, ip(0), nil); !apierror.IsCode(err, apierror.Validation) {
		t.Errorf("first 0: %v", err)
	}
	// A lister failure is internal.
	boom := errors.New("db down")
	r.AuditLog = listerFunc(func(context.Context, audit.Filter, int64, int) (audit.Page, error) { return audit.Page{}, boom })
	if _, err := r.Query().AuditLog(ctx, nil, nil, nil); !errors.Is(err, boom) {
		t.Errorf("lister error: %v", err)
	}
	conn, err = (&Resolver{AuditLog: listerFunc(func(context.Context, audit.Filter, int64, int) (audit.Page, error) {
		return audit.Page{}, nil
	})}).Query().AuditLog(ctx, nil, nil, nil)
	if err != nil || len(conn.Edges) != 0 || conn.PageInfo.StartCursor != nil || conn.PageInfo.HasPreviousPage {
		t.Errorf("empty page %+v %v", conn, err)
	}
}
