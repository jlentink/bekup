//go:build integration

package graph_test

// The stored last test result of connections and destinations over
// GraphQL (D-187): metadata for admins and maintainers, normalized codes
// and the privilege verdict only, stale after a settings change.

import (
	"context"
	"testing"

	"google.golang.org/grpc"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// probeRunner answers Probe with a fixed response or error.
type probeRunner struct {
	runnerpb.RunnerServiceClient
	resp *runnerpb.ProbeResponse
	err  error
}

func (p *probeRunner) Probe(context.Context, *runnerpb.ProbeRequest, ...grpc.CallOption) (*runnerpb.ProbeResponse, error) {
	return p.resp, p.err
}

type lastTestGQL struct {
	At         string
	Ok         bool
	Code       *string
	Stale      bool
	Privileges *struct {
		Status  string
		Reasons []string
	}
}

func TestConnectionLastTestGraphQL(t *testing.T) {
	runner := &probeRunner{resp: &runnerpb.ProbeResponse{
		ServerVersion: "11.8.2", DetectedVersionKey: "11.8", VersionKeyCompatible: true, Databases: []string{"app"},
		Privileges: &runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED, Reasons: []string{"global:FILE"}},
	}}
	e := newRunnerlessConnEnv(t, runner)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	e.user("plain@example.com", sqlc.UserRoleUser)
	adminC, maintC, plainC := e.mustLogin("admin@example.com"), e.mustLogin("maint@example.com"), e.mustLogin("plain@example.com")
	e.freshStepUp(admin.ID)

	var conn struct{ ID string }
	e.mustGQL(adminC, `mutation($in: CreateConnectionInput!) { createConnection(input: $in) { id } }`, map[string]any{"in": map[string]any{
		"name": "Maria", "engine": "MARIADB", "versionKey": "11.8", "host": "10.1.2.3", "username": "root", "tlsMode": "DISABLED",
	}}).field(t, "createConnection", &conn)

	const q = `query($id: UUID!) { connection(id: $id) { lastTest { at ok code stale privileges { status reasons } } } }`
	lastTest := func(cookie string) *lastTestGQL {
		t.Helper()
		var c struct{ LastTest *lastTestGQL }
		e.mustGQL(cookie, q, map[string]any{"id": conn.ID}).field(t, "connection", &c)
		return c.LastTest
	}
	if lt := lastTest(adminC); lt != nil {
		t.Fatalf("untested connection: %+v", lt)
	}

	// A maintainer tests; admins and maintainers both see the result.
	e.mustGQL(maintC, `mutation($id: UUID!) { testConnection(id: $id) { ok } }`, map[string]any{"id": conn.ID})
	for name, c := range map[string]string{"admin": adminC, "maintainer": maintC} {
		lt := lastTest(c)
		if lt == nil || !lt.Ok || lt.Code != nil || lt.Stale || lt.At == "" || lt.Privileges == nil ||
			lt.Privileges.Status != "PRIVILEGED" || len(lt.Privileges.Reasons) != 1 || lt.Privileges.Reasons[0] != "global:FILE" {
			t.Fatalf("%s: %+v", name, lt)
		}
	}
	// Users never see connections.
	if r := e.gql(plainC, q, map[string]any{"id": conn.ID}); r.code() != "FORBIDDEN" {
		t.Fatalf("user: %+v", r.Errors)
	}

	// A failure stores the normalized code only (never the raw error).
	runner.resp, runner.err = nil, runnerpb.ReasonAuthFailed.Status()
	e.mustGQL(maintC, `mutation($id: UUID!) { testConnection(id: $id) { ok } }`, map[string]any{"id": conn.ID})
	lt := lastTest(maintC)
	if lt == nil || lt.Ok || lt.Code == nil || *lt.Code != string(runnerpb.ReasonAuthFailed) || lt.Privileges != nil {
		t.Fatalf("failure: %+v", lt)
	}

	// A settings change marks it stale.
	e.mustGQL(adminC, `mutation($id: UUID!, $in: UpdateConnectionInput!) { updateConnection(id: $id, input: $in) { id } }`,
		map[string]any{"id": conn.ID, "in": map[string]any{"name": "Maria 2"}})
	if lt := lastTest(maintC); lt == nil || !lt.Stale {
		t.Fatalf("not stale: %+v", lt)
	}
}

func TestDestinationLastTestGraphQL(t *testing.T) {
	e := newDestEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	e.user("plain@example.com", sqlc.UserRoleUser)
	adminC, maintC, plainC := e.mustLogin("admin@example.com"), e.mustLogin("maint@example.com"), e.mustLogin("plain@example.com")
	e.freshStepUp(admin.ID)

	var d struct{ ID string }
	e.mustGQL(adminC, createDestMut, map[string]any{"in": map[string]any{
		"name": "Local disk", "type": "DISK", "disk": map[string]any{"path": "local"},
	}}).field(t, "createDestination", &d)

	const q = `{ destinations { id lastTest { at ok code stale } } }`
	lastTest := func(cookie string) *lastTestGQL {
		t.Helper()
		var list []struct {
			ID       string
			LastTest *lastTestGQL
		}
		e.mustGQL(cookie, q, nil).field(t, "destinations", &list)
		if len(list) != 1 {
			t.Fatalf("list: %+v", list)
		}
		return list[0].LastTest
	}
	if lt := lastTest(maintC); lt != nil {
		t.Fatalf("untested destination: %+v", lt)
	}
	e.mustGQL(maintC, testDestMut, map[string]any{"id": d.ID})
	for name, c := range map[string]string{"admin": adminC, "maintainer": maintC} {
		if lt := lastTest(c); lt == nil || !lt.Ok || lt.Code != nil || lt.Stale || lt.At == "" {
			t.Fatalf("%s: %+v", name, lt)
		}
	}
	if r := e.gql(plainC, q, nil); r.code() != "FORBIDDEN" {
		t.Fatalf("user: %+v", r.Errors)
	}
	e.mustGQL(adminC, `mutation($id: UUID!, $in: UpdateDestinationInput!) { updateDestination(id: $id, input: $in) { id } }`,
		map[string]any{"id": d.ID, "in": map[string]any{"name": "Disk 2"}})
	if lt := lastTest(adminC); lt == nil || !lt.Stale {
		t.Fatalf("not stale: %+v", lt)
	}
}
