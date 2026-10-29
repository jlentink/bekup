//go:build integration

package graph_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/monitor"
)

// newSystemHealth is the health snapshot of the run env: the real queries
// on the test database, no runner.
func newSystemHealth(t *testing.T, db sqlc.DBTX) *monitor.Health {
	t.Helper()
	h, err := monitor.NewHealth(monitor.HealthOptions{DB: db, SchedulerEnabled: true, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

const systemHealthQuery = `{ systemHealth { checkedAt schedulerEnabled lastSchedulerTick earliestPinNotAfter oldestLastSuccess
	runner { up dockerOk version activeRuns maxConcurrentRuns }
	certificates { component notAfter }
	queue { queue state jobs } } }`

func TestSystemHealthGraphQL(t *testing.T) {
	e := newRunEnv(t)
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.own(e.jobA, owner.ID)
	adminC := e.mustLogin("admin@example.com")

	var h struct {
		CheckedAt        time.Time
		SchedulerEnabled bool
		Runner           struct {
			Up      bool
			Version *string
		}
		Certificates []struct{ Component string }
		Queue        *[]struct {
			Queue, State string
			Jobs         int
		}
	}
	e.mustGQL(adminC, systemHealthQuery, nil).field(t, "systemHealth", &h)
	if h.CheckedAt.IsZero() || !h.SchedulerEnabled || h.Runner.Up || h.Runner.Version != nil {
		t.Errorf("systemHealth = %+v", h)
	}
	if h.Queue == nil {
		t.Error("queue is null although the database is up")
	}

	// Admins only: maintainers, owners and users are forbidden, anonymous
	// callers unauthenticated.
	for _, email := range []string{"maint@example.com", "owner@example.com"} {
		if r := e.gql(e.mustLogin(email), systemHealthQuery, nil); r.code() != "FORBIDDEN" {
			t.Errorf("%s: code %q", email, r.code())
		}
	}
	if r := e.gql("", systemHealthQuery, nil); r.code() != "UNAUTHENTICATED" {
		t.Errorf("anonymous: code %q", r.code())
	}
}
