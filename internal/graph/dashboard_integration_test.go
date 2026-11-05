//go:build integration

package graph_test

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/egress"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/pubsub"
)

// newDashEnv is a run environment with the D-196 services: the SSRF
// allowlist (its own guard, returned), the audit chain status and the
// destination usage.
func newDashEnv(t *testing.T) (*runEnv, *netguard.Set) {
	t.Helper()
	guard, err := netguard.NewSet(netguard.Options{AllowHosts: []string{"boot.example"}})
	if err != nil {
		t.Fatal(err)
	}
	e := newRunEnv(t, func(a *authEnv, o *graph.Options) {
		// Alerts come from the audit observer (as in serve).
		alerting := audit.New(a.pool, audit.Options{Observer: notify.ObserveAudit})
		eg, err := egress.New(egress.Options{
			DB: a.pool, Auditor: alerting, Policy: o.Policy, Guards: guard, Publisher: &pubsub.Publisher{Bus: o.Bus},
			ConfigHosts: []string{"boot.example"},
		})
		if err != nil {
			t.Fatal(err)
		}
		kr, err := crypto.GenerateKeyring(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(kr.Destroy)
		dests, err := destinations.New(destinations.Options{DB: a.pool, Keyring: kr, Auditor: a.auditor, Guard: guard.Guard(netguard.ScopeDestinations), DiskRoot: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		o.Egress, o.AuditChain, o.Destinations = eg, a.auditor, dests
	})
	return e, guard
}

const (
	ssrfQuery   = `{ ssrfAllowlist { cidrs hosts configCidrs configHosts loopbackAllowed ignoredEntries maxEntries updatedAt updatedBy } }`
	ssrfUpdate  = `mutation($in: SsrfAllowlistInput!) { updateSsrfAllowlist(input: $in) { cidrs hosts updatedAt } }`
	chainQuery  = `{ auditChainStatus { lastVerification { checkedAt ok full finding verifiedThroughId } headId headHash headAt latestSignedHead { chainId } } }`
	usageQuery  = `{ destinationUsage { destinationId name type storedBytes artifactCount backupCount } }`
	spaceQuery  = `query($id: UUID!) { destinationSpace(id: $id) { destinationId status reason totalBytes freeBytes usedBytes newestSetBytes low checkedAt } }`
	storageJobs = `{ jobs(first: 50) { edges { node { id storage { totalBytes artifactCount backupCount } } } } }`
	storageJob  = `query($id: UUID!) { job(id: $id) { storage { totalBytes artifactCount backupCount
		perDestination { destinationId name type deleted storedBytes artifactCount backupCount }
		newestRun { runId status at bytes } trend { runId status at bytes } } } }`
	missedJobs = `{ jobs(first: 50) { edges { node { id missed missedSince } } } }`
)

type ssrfGQL struct {
	Cidrs, Hosts, ConfigCidrs, ConfigHosts []string
	LoopbackAllowed                        bool
	IgnoredEntries, MaxEntries             int
	UpdatedAt, UpdatedBy                   *string
}

func TestSSRFAllowlistGraphQL(t *testing.T) {
	e, guard := newDashEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	adminC, maintC := e.mustLogin("admin@example.com"), e.mustLogin("maint@example.com")

	if r := e.gql(maintC, ssrfQuery, nil); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer read: %+v", r.Errors)
	}
	var v ssrfGQL
	e.mustGQL(adminC, ssrfQuery, nil).field(t, "ssrfAllowlist", &v)
	if len(v.Cidrs) != 0 || !slices.Equal(v.ConfigHosts, []string{"boot.example"}) || v.MaxEntries != egress.MaxEntries || v.UpdatedAt != nil || v.LoopbackAllowed {
		t.Fatalf("initial %+v", v)
	}
	in := map[string]any{"cidrs": []string{"10.20.0.0/16"}, "hosts": []string{"minio.lan"}}
	e.staleStepUp(admin.ID)
	if r := e.gql(adminC, ssrfUpdate, map[string]any{"in": in}); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("stale step-up: %+v", r.Errors)
	}
	if r := e.gql(maintC, ssrfUpdate, map[string]any{"in": in}); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer update: %+v", r.Errors)
	}
	e.freshStepUp(admin.ID)
	bad := map[string]any{"cidrs": []string{"10.0.0.0/8", "169.254.169.254/32"}, "hosts": []string{}}
	r := e.gql(adminC, ssrfUpdate, map[string]any{"in": bad})
	if r.code() != "VALIDATION" || r.Errors[0].Extensions["field"] != "cidrs.1" {
		t.Fatalf("metadata CIDR: %+v", r.Errors)
	}
	e.mustGQL(adminC, ssrfUpdate, map[string]any{"in": in}).field(t, "updateSsrfAllowlist", &v)
	if !slices.Equal(v.Cidrs, []string{"10.20.0.0/16"}) || v.UpdatedAt == nil {
		t.Fatalf("updated %+v", v)
	}
	if err := guard.Guard(netguard.ScopeDestinations).Check(mustAddr("10.20.1.1"), ""); err != nil {
		t.Fatalf("not applied: %v", err)
	}
	// A second save with the old (null) expectedUpdatedAt conflicts.
	if r := e.gql(adminC, ssrfUpdate, map[string]any{"in": in}); r.code() != "CONFLICT" {
		t.Fatalf("stale save: %+v", r.Errors)
	}
}

// TestSSRFAllowlistScopesGraphQL (M5 review, D-198): entries carry
// scopes; personal webhooks are only widened by an explicit scope; scope
// errors name the entry field; securityAlertTypes is admin only.
func TestSSRFAllowlistScopesGraphQL(t *testing.T) {
	e, guard := newDashEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	adminC, maintC := e.mustLogin("admin@example.com"), e.mustLogin("maint@example.com")
	e.freshStepUp(admin.ID)
	const (
		q   = `{ ssrfAllowlist { cidrEntries { value scopes } hostEntries { value scopes } configScopes defaultScopes personalWebhooksWidened updatedAt } }`
		mut = `mutation($in: SsrfAllowlistInput!) { updateSsrfAllowlist(input: $in) { cidrEntries { value scopes } hostEntries { value scopes } personalWebhooksWidened updatedAt } }`
	)
	type entry struct {
		Value  string
		Scopes []string
	}
	var v struct {
		CidrEntries, HostEntries    []entry
		ConfigScopes, DefaultScopes []string
		PersonalWebhooksWidened     bool
		UpdatedAt                   *string
	}
	e.mustGQL(adminC, q, nil).field(t, "ssrfAllowlist", &v)
	if slices.Contains(v.ConfigScopes, "PERSONAL_WEBHOOKS") || slices.Contains(v.DefaultScopes, "PERSONAL_WEBHOOKS") || v.PersonalWebhooksWidened {
		t.Fatalf("initial %+v", v)
	}
	for _, c := range []struct {
		in    map[string]any
		field string
	}{
		{map[string]any{"cidrEntries": []map[string]any{{"value": "10.1.0.0/16", "scopes": []string{}}}, "hosts": []string{}}, "cidrEntries.0.scopes"},
		{map[string]any{"cidrEntries": []map[string]any{{"value": "169.254.169.254/32"}}, "hosts": []string{}}, "cidrEntries.0.value"},
		{map[string]any{"cidrs": []string{}, "hostEntries": []map[string]any{{"value": "*.co.uk"}}}, "hostEntries.0.value"},
		{map[string]any{"cidrs": []string{}, "cidrEntries": []map[string]any{}, "hosts": []string{}}, "cidrEntries"},
		{map[string]any{"hosts": []string{}}, "cidrEntries"},
	} {
		r := e.gql(adminC, mut, map[string]any{"in": c.in})
		if r.code() != "VALIDATION" || r.Errors[0].Extensions["field"] != c.field {
			t.Fatalf("%v: %+v", c.in, r.Errors)
		}
	}
	in := map[string]any{
		"cidrEntries": []map[string]any{{"value": "10.50.0.0/16", "scopes": []string{"PERSONAL_WEBHOOKS"}}, {"value": "10.51.0.0/16"}},
		"hostEntries": []map[string]any{{"value": "minio.lan", "scopes": []string{"DESTINATIONS"}}},
	}
	e.mustGQL(adminC, mut, map[string]any{"in": in}).field(t, "updateSsrfAllowlist", &v)
	if !v.PersonalWebhooksWidened || len(v.CidrEntries) != 2 || !slices.Equal(v.CidrEntries[0].Scopes, []string{"PERSONAL_WEBHOOKS"}) ||
		!slices.Equal(v.CidrEntries[1].Scopes, []string{"DESTINATIONS", "SMTP", "SHARED_WEBHOOKS"}) || !slices.Equal(v.HostEntries[0].Scopes, []string{"DESTINATIONS"}) {
		t.Fatalf("updated %+v", v)
	}
	personal := guard.Guard(netguard.ScopePersonalWebhooks)
	if personal.Check(mustAddr("10.50.0.1"), "") != nil || personal.Check(mustAddr("10.51.0.1"), "") == nil ||
		guard.Guard(netguard.ScopeSMTP).Check(mustAddr("10.50.0.1"), "") == nil {
		t.Fatal("scopes not applied")
	}
	// The alert reference: admin only, includes the allowlist alert.
	if r := e.gql(maintC, `{ securityAlertTypes { kind } }`, nil); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer alert types: %+v", r.Errors)
	}
	var types []struct{ Kind, Severity, Description, Repeat string }
	e.mustGQL(adminC, `{ securityAlertTypes { kind severity description repeat } }`, nil).field(t, "securityAlertTypes", &types)
	if !slices.ContainsFunc(types, func(a struct{ Kind, Severity, Description, Repeat string }) bool {
		return a.Kind == notify.KindSSRFAllowlistWidened && a.Severity == "ERROR" && a.Repeat == "every time" && a.Description != ""
	}) {
		t.Fatalf("alert types %+v", types)
	}
}

func TestAuditChainStatusGraphQL(t *testing.T) {
	e, _ := newDashEnv(t)
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	adminC, maintC := e.mustLogin("admin@example.com"), e.mustLogin("maint@example.com")
	if r := e.gql(maintC, chainQuery, nil); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer: %+v", r.Errors)
	}
	type chain struct {
		LastVerification *struct {
			CheckedAt         string
			Ok, Full          bool
			Finding           *string
			VerifiedThroughID *string
		}
		HeadID, HeadHash *string
		LatestSignedHead *struct{ ChainID string }
	}
	var c chain
	e.mustGQL(adminC, chainQuery, nil).field(t, "auditChainStatus", &c)
	if c.LastVerification != nil || c.HeadID == nil || c.HeadHash == nil || len(*c.HeadHash) != 64 || c.LatestSignedHead != nil {
		t.Fatalf("before any verification %+v", c)
	}
	if err := audit.SaveVerification(t.Context(), sqlc.New(e.pool), audit.Verification{
		CheckedAt: time.Now(), Full: true, Finding: "hash_mismatch:3", VerifiedThroughID: 2,
	}); err != nil {
		t.Fatal(err)
	}
	e.mustGQL(adminC, chainQuery, nil).field(t, "auditChainStatus", &c)
	v := c.LastVerification
	if v == nil || v.Ok || !v.Full || v.Finding == nil || *v.Finding != "hash_mismatch:3" || v.VerifiedThroughID == nil || *v.VerifiedThroughID != "2" {
		t.Fatalf("after a failed verification %+v", v)
	}
}

func TestDestinationUsageGraphQL(t *testing.T) {
	e, _ := newDashEnv(t)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.own(e.jobA, owner.ID)
	maintC, ownerC := e.mustLogin("maint@example.com"), e.mustLogin("owner@example.com")
	if r := e.gql(ownerC, usageQuery, nil); r.code() != "FORBIDDEN" {
		t.Fatalf("owner: %+v", r.Errors)
	}
	for range 2 {
		e.completeRun(e.newRun(e.jobA, nil))
	}
	// A deleted artifact does not count.
	if _, err := e.pool.Exec(t.Context(), `UPDATE artifacts SET state = 'deleted', deleted_at = now()
		WHERE id = (SELECT id FROM artifacts WHERE kind = 'manifest' ORDER BY created_at LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	empty := uuid.New()
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO destinations (id, name, type, config) VALUES ($1, 'Archive', 'disk', '{"path":"y"}')`, empty); err != nil {
		t.Fatal(err)
	}
	type usage struct {
		DestinationID, Name, Type string
		StoredBytes               float64
		ArtifactCount             int
		BackupCount               int
	}
	var got []usage
	e.mustGQL(maintC, usageQuery, nil).field(t, "destinationUsage", &got)
	if len(got) != 2 || got[0].DestinationID != empty.String() || got[0].ArtifactCount != 0 || got[0].StoredBytes != 0 ||
		got[1].DestinationID != e.dest.String() || got[1].ArtifactCount != 3 || got[1].StoredBytes != 40+40+900 || got[1].Type != "DISK" ||
		got[0].BackupCount != 0 || got[1].BackupCount != 2 {
		t.Fatalf("usage %+v", got)
	}
}

// TestDestinationSpaceGraphQL (D-225): staff only; the disk backend
// reports its file system; the answer is cached; the newest set of
// backups is the newest run's present files.
func TestDestinationSpaceGraphQL(t *testing.T) {
	e, _ := newDashEnv(t)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.own(e.jobA, owner.ID)
	maintC, ownerC := e.mustLogin("maint@example.com"), e.mustLogin("owner@example.com")
	vars := map[string]any{"id": e.dest.String()}
	if r := e.gql(ownerC, spaceQuery, vars); r.code() != "FORBIDDEN" {
		t.Fatalf("owner: %+v", r.Errors)
	}
	e.completeRun(e.newRun(e.jobA, nil))
	e.completeRun(e.newRun(e.jobA, nil))
	type space struct {
		DestinationID, Status string
		Reason                *string
		TotalBytes, FreeBytes *float64
		UsedBytes             *float64
		NewestSetBytes        float64
		Low                   bool
		CheckedAt             *time.Time
	}
	var first, second space
	e.mustGQL(maintC, spaceQuery, vars).field(t, "destinationSpace", &first)
	if first.Status != "AVAILABLE" || first.Reason != nil || first.TotalBytes == nil || first.FreeBytes == nil ||
		*first.TotalBytes <= 0 || first.NewestSetBytes != 40+900 || first.CheckedAt == nil {
		t.Fatalf("first %+v", first)
	}
	e.mustGQL(maintC, spaceQuery, vars).field(t, "destinationSpace", &second)
	if second.CheckedAt == nil || !second.CheckedAt.Equal(*first.CheckedAt) {
		t.Fatalf("not cached: %v then %v", first.CheckedAt, second.CheckedAt)
	}
	if r := e.gql(maintC, spaceQuery, map[string]any{"id": uuid.NewString()}); r.code() != "NOT_FOUND" {
		t.Fatalf("unknown destination: %+v", r.Errors)
	}
	s3 := uuid.New()
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO destinations (id, name, type, config) VALUES ($1, 'Bucket', 's3',
		'{"region":"eu-west-1","bucket":"b","accessKeyId":"AKIAEXAMPLE"}')`, s3); err != nil {
		t.Fatal(err)
	}
	var s3s space
	e.mustGQL(maintC, spaceQuery, map[string]any{"id": s3.String()}).field(t, "destinationSpace", &s3s)
	if s3s.Status != "UNSUPPORTED" || s3s.Reason == nil || s3s.TotalBytes != nil || s3s.CheckedAt != nil {
		t.Fatalf("s3 %+v", s3s)
	}
}

// TestJobStorageGraphQL (D-225): totals in one aggregate per page, the
// details per job, scoped to the owner's jobs.
func TestJobStorageGraphQL(t *testing.T) {
	e, _ := newDashEnv(t)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.own(e.jobA, owner.ID)
	adminC, ownerC := e.mustLogin("admin@example.com"), e.mustLogin("owner@example.com")
	first := e.newRun(e.jobA, nil)
	e.completeRun(first)
	second := e.newRun(e.jobA, nil)
	e.completeRun(second)
	// The first run's manifest was deleted by retention.
	if _, err := e.pool.Exec(t.Context(), `UPDATE artifacts SET state = 'deleted', deleted_at = now()
		WHERE run_id = $1 AND kind = 'manifest'`, first.RunID); err != nil {
		t.Fatal(err)
	}
	type totals struct {
		TotalBytes                 float64
		ArtifactCount, BackupCount int
	}
	type page struct {
		Edges []struct {
			Node struct {
				ID      string
				Storage totals
			}
		}
	}
	var p page
	before := e.counter.n.Load()
	e.mustGQL(adminC, storageJobs, nil).field(t, "jobs", &p)
	if n := e.counter.n.Load() - before; n != 1 {
		t.Fatalf("%d run reader queries for Job.storage of a page, want 1", n)
	}
	got := map[string]totals{}
	for _, ed := range p.Edges {
		got[ed.Node.ID] = ed.Node.Storage
	}
	if got[e.jobA.String()] != (totals{TotalBytes: 40 + 40 + 900, ArtifactCount: 3, BackupCount: 2}) || got[e.jobB.String()] != (totals{}) {
		t.Fatalf("totals %+v", got)
	}
	e.mustGQL(ownerC, storageJobs, nil).field(t, "jobs", &p)
	if len(p.Edges) != 1 || p.Edges[0].Node.ID != e.jobA.String() {
		t.Fatalf("owner page %+v", p)
	}
	type run struct {
		RunID, Status string
		At            time.Time
		Bytes         float64
	}
	var detail struct {
		Storage struct {
			totals
			PerDestination []struct {
				DestinationID, Name, Type  string
				Deleted                    bool
				StoredBytes                float64
				ArtifactCount, BackupCount int
			}
			NewestRun *run
			Trend     []run
		}
	}
	e.mustGQL(ownerC, storageJob, map[string]any{"id": e.jobA.String()}).field(t, "job", &detail)
	st := detail.Storage
	if len(st.PerDestination) != 1 || st.PerDestination[0].DestinationID != e.dest.String() || st.PerDestination[0].Name != "Local disk" ||
		st.PerDestination[0].StoredBytes != 980 || st.PerDestination[0].BackupCount != 2 {
		t.Fatalf("per destination %+v", st.PerDestination)
	}
	if st.NewestRun == nil || st.NewestRun.RunID != second.RunID.String() || st.NewestRun.Bytes != 940 || st.NewestRun.Status != "SUCCESS" {
		t.Fatalf("newest %+v", st.NewestRun)
	}
	if len(st.Trend) != 2 || st.Trend[0].RunID != first.RunID.String() || st.Trend[1].RunID != second.RunID.String() ||
		st.Trend[0].Bytes != 940 || st.Trend[1].Bytes != 940 {
		t.Fatalf("trend %+v", st.Trend)
	}
	if r := e.gql(ownerC, storageJob, map[string]any{"id": e.jobB.String()}); r.code() != "FORBIDDEN" {
		t.Fatalf("other job: %+v", r.Errors)
	}
}

func TestJobMissedGraphQL(t *testing.T) {
	e, _ := newDashEnv(t)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.own(e.jobA, owner.ID)
	adminC, ownerC := e.mustLogin("admin@example.com"), e.mustLogin("owner@example.com")
	since := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO job_missed_state (job_id, missed_since, last_missed_at, reason, checked_at)
		VALUES ($1, $2, $2, 'run_failed', now() - interval '1 minute')`, e.jobA, since); err != nil {
		t.Fatal(err)
	}
	type page struct {
		Edges []struct {
			Node struct {
				ID          string
				Missed      bool
				MissedSince *time.Time
			}
		}
	}
	missed := func(cookie string) map[string]*time.Time {
		t.Helper()
		var p page
		e.mustGQL(cookie, missedJobs, nil).field(t, "jobs", &p)
		out := map[string]*time.Time{}
		for _, ed := range p.Edges {
			if ed.Node.Missed != (ed.Node.MissedSince != nil) {
				t.Fatalf("missed and missedSince disagree: %+v", ed.Node)
			}
			out[ed.Node.ID] = ed.Node.MissedSince
		}
		return out
	}
	before := e.counter.n.Load()
	m := missed(adminC)
	if n := e.counter.n.Load() - before; n != 1 {
		t.Fatalf("%d run reader queries for Job.missed of a page, want 1", n)
	}
	if len(m) != 2 || m[e.jobA.String()] == nil || !m[e.jobA.String()].Equal(since) || m[e.jobB.String()] != nil {
		t.Fatalf("admin view %v", m)
	}
	if m := missed(ownerC); len(m) != 1 || m[e.jobA.String()] == nil {
		t.Fatalf("owner view %v", m)
	}
	// A successful run after the check ends the outage at once.
	e.completeRun(e.newRun(e.jobA, nil))
	if m := missed(adminC); m[e.jobA.String()] != nil {
		t.Fatalf("after a success %v", m)
	}
}

func mustAddr(s string) netip.Addr { return netip.MustParseAddr(s) }
