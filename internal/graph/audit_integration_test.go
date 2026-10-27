//go:build integration

package graph_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/logging"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

type entry struct {
	ID          string  `json:"id"`
	Action      string  `json:"action"`
	ActorKind   string  `json:"actorKind"`
	ActorUserID *string `json:"actorUserId"`
	TargetID    *string `json:"targetId"`
	Details     string  `json:"details"`
	Hash        string  `json:"hash"`
}

type auditResp struct {
	Data *struct {
		AuditLog struct {
			Edges []struct {
				Cursor string `json:"cursor"`
				Node   entry  `json:"node"`
			} `json:"edges"`
			PageInfo struct {
				HasNextPage bool    `json:"hasNextPage"`
				EndCursor   *string `json:"endCursor"`
			} `json:"pageInfo"`
		} `json:"auditLog"`
	} `json:"data"`
	Errors []struct {
		Message    string         `json:"message"`
		Extensions map[string]any `json:"extensions"`
	} `json:"errors"`
}

const auditQuery = `query($filter: AuditLogFilter, $first: Int, $after: String) {
  auditLog(filter: $filter, first: $first, after: $after) {
    edges { cursor node { id action actorKind actorUserId targetId details hash } }
    pageInfo { hasNextPage endCursor }
  }
}`

// TestAuditLogQuery runs Query.auditLog against a real, migrated database
// through the GraphQL handler, with the principal injected the way the
// session middleware (task 1.5) will.
func TestAuditLogQuery(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	pool := dbtest.Migrated(t).AppPool(t)
	auditor := audit.New(pool, audit.Options{Registry: logging.NewRegistry()})

	actor := uuid.New()
	for i := range 5 {
		ev := audit.Event{Actor: audit.UserActor(actor), Action: audit.ActionUserUpdated, TargetType: "user",
			TargetID: fmt.Sprintf("u%d", i), Details: audit.Details{"n": i, "password": "never-stored"}}
		if _, err := auditor.Record(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := auditor.Record(ctx, audit.Event{Actor: audit.SystemActor(), Action: audit.ActionSettingChanged}); err != nil {
		t.Fatal(err)
	}

	pol := authz.New(authz.Options{})
	h, err := graph.New(graph.Options{
		Logger: slog.New(slog.DiscardHandler), MaxComplexity: 1000, MaxDepth: 12, MaxBodyBytes: 1 << 16,
		Access: graph.NewPolicyAccess(pol), Policy: pol, AuditLog: auditor,
	})
	if err != nil {
		t.Fatal(err)
	}
	run := func(p *auth.Principal, vars map[string]any) auditResp {
		t.Helper()
		body, err := json.Marshal(map[string]any{"query": auditQuery, "variables": vars})
		if err != nil {
			t.Fatal(err)
		}
		rctx := ctx
		if p != nil {
			rctx = auth.WithPrincipal(ctx, *p)
		}
		req := httptest.NewRequestWithContext(rctx, http.MethodPost, "/graphql", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var resp auditResp
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode %s: %v", w.Body.String(), err)
		}
		return resp
	}
	code := func(r auditResp) string {
		if len(r.Errors) == 0 {
			return ""
		}
		if c, ok := r.Errors[0].Extensions["code"].(string); ok {
			return c
		}
		return ""
	}

	if r := run(nil, nil); code(r) != "UNAUTHENTICATED" || r.Data != nil {
		t.Errorf("anonymous: %+v", r)
	}
	maint := authztest.Principal(auth.RoleMaintainer)
	if r := run(&maint, nil); code(r) != "FORBIDDEN" || r.Data != nil {
		t.Errorf("maintainer: %+v", r)
	}

	admin := authztest.Principal(auth.RoleAdmin)
	// Page through the user's entries two at a time, newest first.
	filter := map[string]any{"actorUserId": actor.String(), "action": "user.updated"}
	var ids []string
	var after any
	for page := 0; page < 5; page++ {
		r := run(&admin, map[string]any{"filter": filter, "first": 2, "after": after})
		if len(r.Errors) != 0 || r.Data == nil {
			t.Fatalf("admin page %d: %+v", page, r.Errors)
		}
		for _, e := range r.Data.AuditLog.Edges {
			ids = append(ids, e.Node.TargetIDOr())
			if e.Node.ActorKind != "USER" || e.Node.ActorUserID == nil || *e.Node.ActorUserID != actor.String() ||
				e.Node.Action != "user.updated" || len(e.Node.Hash) != 64 {
				t.Errorf("entry %+v", e.Node)
			}
			if bytes.Contains([]byte(e.Node.Details), []byte("never-stored")) {
				t.Errorf("secret detail exposed: %s", e.Node.Details)
			}
		}
		if !r.Data.AuditLog.PageInfo.HasNextPage {
			break
		}
		after = *r.Data.AuditLog.PageInfo.EndCursor
	}
	want := []string{"u4", "u3", "u2", "u1", "u0"}
	if fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Errorf("paged targets %v, want %v", ids, want)
	}

	// Filters on actor kind, target and time.
	r := run(&admin, map[string]any{"filter": map[string]any{"actorKind": "SYSTEM"}})
	if len(r.Errors) != 0 || len(r.Data.AuditLog.Edges) != 1 || r.Data.AuditLog.Edges[0].Node.Action != "setting.changed" {
		t.Errorf("actorKind filter: %+v", r)
	}
	r = run(&admin, map[string]any{"filter": map[string]any{"targetType": "user", "targetId": "u2"}})
	if len(r.Errors) != 0 || len(r.Data.AuditLog.Edges) != 1 {
		t.Errorf("target filter: %+v", r)
	}
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	r = run(&admin, map[string]any{"filter": map[string]any{"from": future}})
	if len(r.Errors) != 0 || len(r.Data.AuditLog.Edges) != 0 {
		t.Errorf("time filter: %+v", r)
	}
	if r := run(&admin, map[string]any{"after": "bogus"}); code(r) != "VALIDATION" {
		t.Errorf("bad cursor: %+v", r)
	}
}

// TargetIDOr returns the target id or "".
func (e entry) TargetIDOr() string {
	if e.TargetID == nil {
		return ""
	}
	return *e.TargetID
}
