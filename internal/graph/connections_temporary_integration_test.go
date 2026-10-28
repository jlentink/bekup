//go:build integration

package graph_test

// Temporary restore connections over GraphQL (task 4.4): admin only with a
// fresh step-up, invisible in connections and to maintainers, extend
// bounded by the max, delete audited; credentials never in a response. No
// runner call is made (newRunLimitEnv's runner panics).

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

func TestTemporaryConnectionsGraphQL(t *testing.T) {
	e := newRunLimitEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	e.user("plain@example.com", sqlc.UserRoleUser)
	adminC := e.mustLogin("admin@example.com")
	maintC := e.mustLogin("maint@example.com")
	plainC := e.mustLogin("plain@example.com")

	const secretPW = "TEMP-RESTORE-PASSWORD-5e1f"
	const fields = `id name temporary expiresAt ready settings { hasPassword username }`
	const createMut = `mutation($in: CreateTemporaryConnectionInput!) { createTemporaryConnection(input: $in) { ` + fields + ` } }`
	in := map[string]any{"in": map[string]any{
		"engine": "POSTGRES", "versionKey": "17", "host": "restore.example.test", "username": "restore", "password": secretPW,
	}}
	type conn struct {
		ID        string
		Name      string
		Temporary bool
		ExpiresAt *time.Time
		Ready     bool
		Settings  *struct {
			HasPassword bool
			Username    string
		}
	}

	// Step-up is required; maintainers and users are refused.
	e.staleStepUp(admin.ID)
	if r := e.gql(adminC, createMut, in); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("create without step-up: %+v", r.Errors)
	}
	for name, ck := range map[string]string{"maintainer": maintC, "user": plainC} {
		if r := e.gql(ck, createMut, in); r.code() != "FORBIDDEN" {
			t.Fatalf("%s create: %+v", name, r.Errors)
		}
	}
	e.freshStepUp(admin.ID)
	res := e.mustGQL(adminC, createMut, in)
	if respContains(t, res, secretPW) {
		t.Fatal("the password is in the response")
	}
	var c conn
	res.field(t, "createTemporaryConnection", &c)
	if !c.Temporary || c.ExpiresAt == nil || time.Until(*c.ExpiresAt) < 23*time.Hour || c.Name != "Temporary: restore.example.test" ||
		c.Settings == nil || !c.Settings.HasPassword || c.Ready {
		t.Fatalf("created: %+v", c)
	}

	// It must be the only root field of its operation.
	const twice = `mutation($in: CreateTemporaryConnectionInput!) { a: createTemporaryConnection(input: $in) { id } b: createTemporaryConnection(input: $in) { id } }`
	if r := e.gql(adminC, twice, in); r.code() != "VALIDATION" {
		t.Fatalf("two creations in one request: %+v", r.Errors)
	}

	// D-132: pinned TLS to an IP literal is refused.
	ip := map[string]any{"in": map[string]any{"engine": "POSTGRES", "versionKey": "17", "host": "10.0.0.7", "username": "u"}}
	if r := e.gql(adminC, createMut, ip); r.code() != "VALIDATION" {
		t.Fatalf("ip literal: %+v", r.Errors)
	}

	// Invisible in connections (admins and maintainers), in connection(id)
	// and to maintainers' runner operations; listed for admins only.
	var list []conn
	e.mustGQL(adminC, `{ connections { `+fields+` } }`, nil).field(t, "connections", &list)
	if len(list) != 0 {
		t.Fatalf("connections lists the temporary connection: %+v", list)
	}
	e.mustGQL(maintC, `{ connections { id } }`, nil).field(t, "connections", &list)
	if len(list) != 0 {
		t.Fatalf("maintainer list: %+v", list)
	}
	var one *conn
	e.mustGQL(adminC, `query($id: UUID!) { connection(id: $id) { id } }`, map[string]any{"id": c.ID}).field(t, "connection", &one)
	if one != nil {
		t.Fatalf("connection(id): %+v", one)
	}
	if r := e.gql(maintC, `mutation($id: UUID!) { testConnection(id: $id) { ok } }`, map[string]any{"id": c.ID}); r.code() != "NOT_FOUND" {
		t.Fatalf("maintainer test: %+v", r.Errors)
	}
	// For an admin the connection exists (not ready: no pin yet; no runner call).
	if r := e.gql(adminC, `mutation($id: UUID!) { testConnection(id: $id) { ok } }`, map[string]any{"id": c.ID}); r.code() != "CONFLICT" {
		t.Fatalf("admin test: %+v", r.Errors)
	}
	if r := e.gql(adminC, `mutation($id: UUID!) { deleteConnection(id: $id) }`, map[string]any{"id": c.ID}); r.code() != "NOT_FOUND" {
		t.Fatalf("deleteConnection: %+v", r.Errors)
	}
	for name, ck := range map[string]string{"maintainer": maintC, "user": plainC} {
		if r := e.gql(ck, `{ temporaryConnections { id } }`, nil); r.code() != "FORBIDDEN" {
			t.Fatalf("%s temporaryConnections: %+v", name, r.Errors)
		}
	}
	res = e.mustGQL(adminC, `{ temporaryConnections { `+fields+` } }`, nil)
	res.field(t, "temporaryConnections", &list)
	if len(list) != 1 || list[0].ID != c.ID || respContains(t, res, secretPW) {
		t.Fatalf("temporaryConnections: %+v", list)
	}

	// Extend: step-up, at most the max after the creation.
	const extendMut = `mutation($id: UUID!, $at: Time!) { extendTemporaryConnection(id: $id, expiresAt: $at) { ` + fields + ` } }`
	tooLate := time.Now().Add(connections.DefaultTempMaxTTL + time.Hour).UTC().Format(time.RFC3339)
	if r := e.gql(adminC, extendMut, map[string]any{"id": c.ID, "at": tooLate}); r.code() != "VALIDATION" {
		t.Fatalf("extend beyond the max: %+v", r.Errors)
	}
	at := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	e.staleStepUp(admin.ID)
	if r := e.gql(adminC, extendMut, map[string]any{"id": c.ID, "at": at.Format(time.RFC3339)}); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("extend without step-up: %+v", r.Errors)
	}
	if r := e.gql(maintC, extendMut, map[string]any{"id": c.ID, "at": at.Format(time.RFC3339)}); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer extend: %+v", r.Errors)
	}
	e.freshStepUp(admin.ID)
	e.mustGQL(adminC, extendMut, map[string]any{"id": c.ID, "at": at.Format(time.RFC3339)}).field(t, "extendTemporaryConnection", &c)
	if c.ExpiresAt == nil || !c.ExpiresAt.Equal(at) {
		t.Fatalf("extended: %+v", c)
	}

	// Delete: step-up, admin only, audited.
	const deleteMut = `mutation($id: UUID!) { deleteTemporaryConnection(id: $id) }`
	if r := e.gql(maintC, deleteMut, map[string]any{"id": c.ID}); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer delete: %+v", r.Errors)
	}
	e.mustGQL(adminC, deleteMut, map[string]any{"id": c.ID})
	if r := e.gql(adminC, deleteMut, map[string]any{"id": c.ID}); r.code() != "NOT_FOUND" {
		t.Fatalf("second delete: %+v", r.Errors)
	}
	for _, a := range []audit.Action{audit.ActionConnectionTemporaryCreated, audit.ActionConnectionTemporaryExtended, audit.ActionConnectionTemporaryDeleted} {
		page, err := e.auditor.List(t.Context(), audit.Filter{Action: a}, 0, 10)
		if err != nil || len(page.Entries) != 1 || strings.Contains(string(page.Entries[0].Details), secretPW) {
			t.Fatalf("audit %s: %v %+v", a, err, page.Entries)
		}
	}
}

// respContains reports whether the response data contains s.
func respContains(t *testing.T, r gqlResp, s string) bool {
	t.Helper()
	b, err := json.Marshal(r.Data)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(b), s)
}
