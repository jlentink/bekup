//go:build integration

package graph_test

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/notify"
)

// attachmentCounter counts the attachment queries (batching check).
type attachmentCounter struct {
	*pgxpool.Pool
	n atomic.Int64
}

func (c *attachmentCounter) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if strings.Contains(sql, "FROM job_shared_channels a") && strings.Contains(sql, "a.job_id = ANY") {
		c.n.Add(1)
	}
	return c.Pool.Query(ctx, sql, args...)
}

// TestJobSharedChannelAttachments (D-198): staff see the shared channels
// attached to each job (no addresses, URLs or keys), loaded with one
// query per page; backup owners get null.
func TestJobSharedChannelAttachments(t *testing.T) {
	counter := &attachmentCounter{}
	e := newRunEnv(t, func(a *authEnv, o *graph.Options) {
		counter.Pool = a.pool
		kr, err := crypto.GenerateKeyring(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(kr.Destroy)
		guard, err := netguard.New(netguard.Options{})
		if err != nil {
			t.Fatal(err)
		}
		api, err := notify.NewAPI(notify.APIOptions{
			DB: counter, Keyring: kr, Auditor: a.auditor, Policy: o.Policy, Guard: guard, PersonalGuard: guard,
			Logger: slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatal(err)
		}
		o.Notifications = api
	})
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	ctx := t.Context()
	jobA, err := dbtest.InsertJob(ctx, e.pool, dbtest.JobRow{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	jobB, err := dbtest.InsertJob(ctx, e.pool, dbtest.JobRow{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	e.own(jobA, owner.ID)
	hook, list := uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO shared_channels (id, name, type, config, url_enc, hmac_key_enc) VALUES ($1, 'Ops hook', 'webhook', '{"display_url":"https://hooks.example.com/…"}', '\x01', '\x02')`, []any{hook}},
		{`INSERT INTO shared_channels (id, name, type, config) VALUES ($1, 'DBA list', 'email_list', '{"addresses":["dba@example.com"]}')`, []any{list}},
		{`INSERT INTO job_shared_channels (job_id, channel_id, groups, events) VALUES ($1, $2, '{backup}', '{}')`, []any{jobA, hook}},
		{`INSERT INTO job_shared_channels (job_id, channel_id, groups, events) VALUES ($1, $2, '{}', '{restore.failed}')`, []any{jobA, list}},
		{`INSERT INTO job_shared_channels (job_id, channel_id) VALUES ($1, $2)`, []any{jobB, list}},
	} {
		if _, err := e.pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	const query = `{ jobs(first: 50) { edges { node { id sharedChannelAttachments { channelId name type displayUrl enabled groups events } } } } }`
	type att struct {
		ChannelID, Name, Type string
		DisplayURL            *string
		Enabled               bool
		Groups, Events        []string
	}
	type page struct {
		Edges []struct {
			Node struct {
				ID                       string
				SharedChannelAttachments *[]att
			}
		}
	}
	byJob := func(c string) map[string]*[]att {
		t.Helper()
		var p page
		resp := e.mustGQL(c, query, nil)
		if body := string(resp.Data["jobs"]); strings.Contains(body, "dba@example.com") {
			t.Fatal("e-mail addresses exposed")
		}
		resp.field(t, "jobs", &p)
		out := map[string]*[]att{}
		for _, edge := range p.Edges {
			out[edge.Node.ID] = edge.Node.SharedChannelAttachments
		}
		return out
	}
	for _, email := range []string{"admin@example.com", "maint@example.com"} {
		before := counter.n.Load()
		got := byJob(e.mustLogin(email))
		if n := counter.n.Load() - before; n != 1 {
			t.Fatalf("%s: %d attachment queries for one page", email, n)
		}
		a, b := got[jobA.String()], got[jobB.String()]
		if a == nil || len(*a) != 2 || b == nil || len(*b) != 1 {
			t.Fatalf("%s: %+v", email, got)
		}
		first := (*a)[1] // ordered by name: "DBA list", "Ops hook"
		if first.Name != "Ops hook" || first.Type != "WEBHOOK" || first.DisplayURL == nil || !first.Enabled ||
			len(first.Groups) != 1 || first.Groups[0] != "BACKUP" {
			t.Fatalf("%s: webhook attachment %+v", email, first)
		}
		if l := (*a)[0]; l.Type != "EMAIL_LIST" || l.DisplayURL != nil || len(l.Events) != 1 || l.Events[0] != "restore.failed" {
			t.Fatalf("%s: list attachment %+v", email, l)
		}
	}
	got := byJob(e.mustLogin("owner@example.com"))
	if v, ok := got[jobA.String()]; !ok || v != nil {
		t.Fatalf("owner: %+v", got)
	}
}
