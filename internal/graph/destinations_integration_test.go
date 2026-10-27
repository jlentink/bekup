//go:build integration

package graph_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/netguard"
)

// destEnv is an authEnv whose handler has the destination service (task
// 2.12). Every response body is kept so tests can assert that no secret
// value ever appears in one.
type destEnv struct {
	*authEnv
	svc    *destinations.Service
	bodies []string
}

func newDestEnv(t *testing.T) *destEnv {
	t.Helper()
	d := dbtest.Migrated(t)
	e := &authEnv{t: t, d: d, pool: d.AppPool(t)}
	e.auditor = audit.New(e.pool, audit.Options{Registry: logging.NewRegistry()})
	var err error
	e.hasher, err = auth.NewHasher(auth.HasherOptions{
		Params: auth.Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1}, MaxConcurrent: 4, WeakForTests: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessions(e.pool, auth.SessionOptions{})
	svc, err := auth.NewService(auth.ServiceOptions{DB: e.pool, Hasher: e.hasher, Sessions: sessions, Auditor: e.auditor, Factors: auth.NewSecondFactors()})
	if err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	guard, err := netguard.New(netguard.Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	dests, err := destinations.New(destinations.Options{
		DB: e.pool, Keyring: kr, Auditor: e.auditor, Guard: guard, DiskRoot: t.TempDir(),
		Limiter: func(string) (bool, time.Duration) { return true, 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	pol := authz.New(authz.Options{})
	logger := slog.New(slog.DiscardHandler)
	gql, err := graph.New(graph.Options{
		Logger: logger, MaxComplexity: 1000, MaxDepth: 12, MaxBodyBytes: 1 << 16,
		Access: graph.NewPolicyAccess(pol), Policy: pol, AuditLog: e.auditor, Auth: svc,
		Destinations: dests,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := httpserver.New(httpserver.Options{
		BaseURL: testBaseURL, MaxBodyBytes: 1 << 20, Logger: logger,
		Ready:     func(context.Context) error { return nil },
		RateLimit: httpserver.RateLimitOptions{PerSecond: 1000, Burst: 1000, MaxClients: 100},
		GraphQL:   gql,
		Sessions: &httpserver.SessionOptions{
			Resolver: httpserver.SessionsResolver{Sessions: sessions},
			Cookies:  auth.CookiePolicyFor(testBaseURL),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.h = srv.Handler()
	return &destEnv{authEnv: e, svc: dests}
}

// gql runs a request and records the raw response body.
func (e *destEnv) gql(cookie, query string, vars map[string]any) gqlResp {
	e.t.Helper()
	resp, w := e.do(cookie, "", query, vars)
	e.bodies = append(e.bodies, w.Body.String())
	return resp
}

// mustGQL fails the test on GraphQL errors.
func (e *destEnv) mustGQL(cookie, query string, vars map[string]any) gqlResp {
	e.t.Helper()
	resp := e.gql(cookie, query, vars)
	if len(resp.Errors) > 0 {
		e.t.Fatalf("%s: %+v", query, resp.Errors)
	}
	return resp
}

// assertNoSecrets checks every recorded response for the secret values.
func (e *destEnv) assertNoSecrets(secrets ...string) {
	e.t.Helper()
	for _, b := range e.bodies {
		for _, s := range secrets {
			if strings.Contains(b, s) {
				e.t.Fatalf("a response contains a secret value: %s", b)
			}
		}
	}
	// And the audit log.
	page, err := e.auditor.List(e.t.Context(), audit.Filter{}, 0, 200)
	if err != nil {
		e.t.Fatal(err)
	}
	for i := range page.Entries {
		for _, sec := range secrets {
			if strings.Contains(string(page.Entries[i].Details), sec) {
				e.t.Fatalf("the audit log contains a secret value")
			}
		}
	}
}

func (e *destEnv) freshStepUp(userID uuid.UUID) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), "UPDATE sessions SET stepup_at = now() WHERE user_id = $1", userID); err != nil {
		e.t.Fatal(err)
	}
}

func (e *destEnv) staleStepUp(userID uuid.UUID) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), "UPDATE sessions SET stepup_at = now() - interval '1 hour' WHERE user_id = $1", userID); err != nil {
		e.t.Fatal(err)
	}
}

const (
	destFields = `id name type noDelete objectLockMode objectLockRetentionDays ready notReadyReason createdAt updatedAt
		settings {
			disk { path }
			s3 { endpoint region bucket prefix pathStyle encryption kmsKeyId storageClass checksumMode conditionalWrites accessKeyId hasSecretAccessKey caBundlePem }
			sftp { host port user basePath dir authMethod hasPassword hasPrivateKey hasPassphrase publicKey hostKey { type fingerprintSha256 authorizedKey confirmedAt } }
		}`
	createDestMut = `mutation($in: CreateDestinationInput!) { createDestination(input: $in) { ` + destFields + ` } }`
	updateDestMut = `mutation($id: UUID!, $in: UpdateDestinationInput!) { updateDestination(id: $id, input: $in) { ` + destFields + ` } }`
	listDestQuery = `{ destinations { ` + destFields + ` } }`
	viewDestQuery = `query($id: UUID!) { destination(id: $id) { ` + destFields + ` } }`
	testDestMut   = `mutation($id: UUID!) { testDestination(id: $id) { ok probeKey probeRemains note steps { name ok code message } } }`
	deleteDestMut = `mutation($id: UUID!) { deleteDestination(id: $id) }`
)

type destModel struct {
	ID             string
	Name           string
	Type           string
	NoDelete       bool
	ObjectLockMode *string
	Ready          bool
	NotReadyReason *string
	Settings       *struct {
		Disk *struct{ Path string }
		S3   *struct {
			Bucket             string
			AccessKeyID        string `json:"accessKeyId"`
			HasSecretAccessKey bool
			CaBundlePem        *string
		}
		Sftp *struct {
			Host          string
			Port          int
			AuthMethod    string
			HasPassword   bool
			HasPrivateKey bool
			PublicKey     *string
			HostKey       *struct {
				Type              string
				FingerprintSha256 string
				AuthorizedKey     string
				ConfirmedAt       *string
			}
		}
	}
}

type testResultModel struct {
	OK           bool `json:"ok"`
	ProbeKey     *string
	ProbeRemains bool
	Steps        []struct {
		Name string
		OK   bool `json:"ok"`
		Code string
	}
}

const (
	s3SecretValue   = "S3-SECRET-VALUE-0f9e8d7c"
	s3SecretValue2  = "S3-SECRET-VALUE-second-1a2b"
	sftpPasswordVal = "SFTP-PASSWORD-VALUE-5c4b3a"
)

func TestDestinationsGraphQL(t *testing.T) {
	e := newDestEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	e.user("plain@example.com", sqlc.UserRoleUser)
	adminC := e.mustLogin("admin@example.com")
	maintC := e.mustLogin("maint@example.com")
	plainC := e.mustLogin("plain@example.com")
	e.freshStepUp(admin.ID)

	// Create one destination of each type.
	var disk, s3d, sftpD destModel
	e.mustGQL(adminC, createDestMut, map[string]any{"in": map[string]any{
		"name": "Local disk", "type": "DISK", "disk": map[string]any{"path": "local"},
	}}).field(t, "createDestination", &disk)
	e.mustGQL(adminC, createDestMut, map[string]any{"in": map[string]any{
		"name": "Offsite S3", "type": "S3", "noDelete": true,
		"s3": map[string]any{
			"endpoint": "https://127.0.0.1:9", "region": "us-east-1", "bucket": "backups", "pathStyle": true,
			"accessKeyId": "AKIATESTACCESS", "secretAccessKey": s3SecretValue,
			"objectLockMode": "GOVERNANCE", "objectLockRetentionDays": 7,
		},
	}}).field(t, "createDestination", &s3d)
	e.mustGQL(adminC, createDestMut, map[string]any{"in": map[string]any{
		"name": "Remote SFTP", "type": "SFTP",
		"sftp": map[string]any{"host": "127.0.0.1", "port": 2222, "user": "bekup", "basePath": "/upload", "password": sftpPasswordVal},
	}}).field(t, "createDestination", &sftpD)
	if !disk.Ready || disk.Settings.Disk.Path != "local" {
		t.Fatalf("disk: %+v", disk)
	}
	if !s3d.Ready || !s3d.NoDelete || s3d.ObjectLockMode == nil || *s3d.ObjectLockMode != "GOVERNANCE" ||
		!s3d.Settings.S3.HasSecretAccessKey || s3d.Settings.S3.AccessKeyID != "AKIATESTACCESS" {
		t.Fatalf("s3: %+v", s3d)
	}
	if sftpD.Ready || sftpD.NotReadyReason == nil || !sftpD.Settings.Sftp.HasPassword || sftpD.Settings.Sftp.AuthMethod != "PASSWORD" {
		t.Fatalf("sftp: %+v", sftpD)
	}

	// Validation errors.
	for name, in := range map[string]map[string]any{
		"type mismatch": {"name": "X", "type": "DISK", "s3": map[string]any{"region": "r", "bucket": "bbb", "accessKeyId": "AKIA1"}},
		"traversal":     {"name": "X", "type": "DISK", "disk": map[string]any{"path": "../etc"}},
		"private s3":    {"name": "X", "type": "S3", "s3": map[string]any{"endpoint": "https://10.0.0.1", "region": "us-east-1", "bucket": "bbb", "accessKeyId": "AKIA1", "secretAccessKey": "x"}},
		"http s3":       {"name": "X", "type": "S3", "s3": map[string]any{"endpoint": "http://127.0.0.1:9", "region": "us-east-1", "bucket": "bbb", "accessKeyId": "AKIA1", "secretAccessKey": "x"}},
		"no s3 secret":  {"name": "X", "type": "S3", "s3": map[string]any{"region": "us-east-1", "bucket": "bbb", "accessKeyId": "AKIA1"}},
		"dup name":      {"name": "local DISK", "type": "DISK", "disk": map[string]any{"path": "x"}},
	} {
		resp := e.gql(adminC, createDestMut, map[string]any{"in": in})
		want := "VALIDATION"
		if name == "dup name" {
			want = "CONFLICT"
		}
		if resp.code() != want {
			t.Errorf("%s: %+v", name, resp.Errors)
		}
	}

	// Maintainers see metadata only; users and anonymous callers nothing.
	var maintList []destModel
	e.mustGQL(maintC, listDestQuery, nil).field(t, "destinations", &maintList)
	if len(maintList) != 3 {
		t.Fatalf("maintainer list: %+v", maintList)
	}
	for _, d := range maintList {
		if d.Settings != nil || d.Name == "" {
			t.Fatalf("maintainer sees settings: %+v", d)
		}
	}
	var adminList []destModel
	e.mustGQL(adminC, listDestQuery, nil).field(t, "destinations", &adminList)
	if len(adminList) != 3 || adminList[0].Name != "Local disk" || adminList[1].Settings == nil {
		t.Fatalf("admin list: %+v", adminList)
	}
	var one *destModel
	e.mustGQL(maintC, viewDestQuery, map[string]any{"id": s3d.ID}).field(t, "destination", &one)
	if one == nil || one.Settings != nil || one.Type != "S3" {
		t.Fatalf("maintainer view: %+v", one)
	}
	e.mustGQL(adminC, viewDestQuery, map[string]any{"id": uuid.NewString()}).field(t, "destination", &one)
	if one != nil {
		t.Fatal("unknown destination is not null")
	}

	idVars := map[string]any{"id": s3d.ID}
	adminOnly := []struct {
		q    string
		vars map[string]any
	}{
		{createDestMut, map[string]any{"in": map[string]any{"name": "Y", "type": "DISK", "disk": map[string]any{"path": "y"}}}},
		{updateDestMut, map[string]any{"id": s3d.ID, "in": map[string]any{"name": "Z"}}},
		{deleteDestMut, idVars},
		{`mutation($id: UUID!) { fetchDestinationHostKey(id: $id) { type } }`, map[string]any{"id": sftpD.ID}},
		{`mutation($id: UUID!) { confirmDestinationHostKey(id: $id, authorizedKey: "ssh-ed25519 AAAA", expectedHost: "127.0.0.1", expectedPort: 2222) { id } }`, map[string]any{"id": sftpD.ID}},
		{`mutation($id: UUID!) { generateDestinationSshKeypair(id: $id) { publicKey } }`, map[string]any{"id": sftpD.ID}},
	}
	staff := []struct {
		q    string
		vars map[string]any
	}{
		{listDestQuery, nil}, {viewDestQuery, idVars}, {testDestMut, map[string]any{"id": disk.ID}},
	}
	check := func(who, cookie, q string, vars map[string]any, want string) {
		t.Helper()
		resp := e.gql(cookie, q, vars)
		if resp.code() != want {
			t.Errorf("%s %s: want %s, got %+v", who, q[:min(len(q), 60)], want, resp.Errors)
		}
	}
	for _, c := range adminOnly {
		check("maintainer", maintC, c.q, c.vars, "FORBIDDEN")
		check("user", plainC, c.q, c.vars, "FORBIDDEN")
		check("anonymous", "", c.q, c.vars, "UNAUTHENTICATED")
	}
	for _, c := range staff {
		check("user", plainC, c.q, c.vars, "FORBIDDEN")
		check("anonymous", "", c.q, c.vars, "UNAUTHENTICATED")
	}

	// Maintainers may test.
	var res testResultModel
	e.mustGQL(maintC, testDestMut, map[string]any{"id": disk.ID}).field(t, "testDestination", &res)
	if !res.OK || len(res.Steps) != 4 || res.Steps[0].Name != "connect" || res.ProbeKey == nil {
		t.Fatalf("disk test: %+v", res)
	}
	// Not ready: CONFLICT.
	check("admin", adminC, testDestMut, map[string]any{"id": sftpD.ID}, "CONFLICT")
	// testDestination must be the only field of its operation.
	resp := e.gql(adminC, `mutation($id: UUID!) { a: testDestination(id: $id) { ok } b: testDestination(id: $id) { ok } }`, map[string]any{"id": disk.ID})
	if resp.code() != "VALIDATION" {
		t.Fatalf("aliased tests: %+v", resp.Errors)
	}

	// Every change needs a fresh step-up.
	e.staleStepUp(admin.ID)
	for _, c := range adminOnly {
		if strings.Contains(c.q, "fetchDestinationHostKey") {
			continue // no step-up: it only reads a public key
		}
		check("stale admin", adminC, c.q, c.vars, "STEP_UP_REQUIRED")
	}
	e.freshStepUp(admin.ID)

	// Updates: without a secret the stored one stays; with one it changes.
	var upd destModel
	e.mustGQL(adminC, updateDestMut, map[string]any{"id": s3d.ID, "in": map[string]any{
		"name": "Offsite S3 (eu)",
		"s3": map[string]any{
			"endpoint": "https://127.0.0.1:9", "region": "us-east-1", "bucket": "backups-eu", "pathStyle": true,
			"accessKeyId": "AKIATESTACCESS",
		},
	}}).field(t, "updateDestination", &upd)
	if upd.Name != "Offsite S3 (eu)" || upd.Settings.S3.Bucket != "backups-eu" || !upd.Settings.S3.HasSecretAccessKey || upd.ObjectLockMode != nil {
		t.Fatalf("update: %+v", upd)
	}
	e.mustGQL(adminC, updateDestMut, map[string]any{"id": s3d.ID, "in": map[string]any{
		"s3": map[string]any{
			"endpoint": "https://127.0.0.1:9", "region": "us-east-1", "bucket": "backups-eu", "pathStyle": true,
			"accessKeyId": "AKIATESTACCESS", "secretAccessKey": s3SecretValue2,
		},
	}})
	// D-140 (Sec M3): moving the SFTP destination needs the password again.
	movedSFTP := map[string]any{"host": "127.0.0.1", "port": 2200, "user": "bekup", "basePath": "/upload"}
	resp = e.gql(adminC, updateDestMut, map[string]any{"id": sftpD.ID, "in": map[string]any{"sftp": movedSFTP}})
	if resp.code() != "VALIDATION" || !strings.Contains(fmt.Sprint(resp.Errors), "password") {
		t.Fatalf("retarget without the password: %+v", resp.Errors)
	}
	movedSFTP["password"] = sftpPasswordVal
	e.mustGQL(adminC, updateDestMut, map[string]any{"id": sftpD.ID, "in": map[string]any{"sftp": movedSFTP}})
	retargeted := false
	for _, en := range auditDetails(t, e.auditor, audit.ActionDestinationUpdated) {
		if v, ok := en["reentered_for_new_endpoint"]; ok && fmt.Sprint(v) == "[password]" {
			retargeted = true
		}
	}
	if !retargeted {
		t.Fatal("the password re-entry for a new endpoint was not audited")
	}

	// A disk input on an S3 destination is refused.
	check("admin", adminC, updateDestMut, map[string]any{"id": s3d.ID, "in": map[string]any{"disk": map[string]any{"path": "x"}}}, "VALIDATION")

	// Audit: create/update recorded, credentials only by name.
	if n := countAudit(t, e.auditor, audit.ActionDestinationCreated); n != 3 {
		t.Fatalf("created entries: %d", n)
	}
	if n := countAudit(t, e.auditor, audit.ActionSecretChanged); n < 3 {
		t.Fatalf("secret.changed entries: %d", n)
	}

	// Delete (soft): gone from the list, name reusable.
	if r := e.mustGQL(adminC, deleteDestMut, map[string]any{"id": disk.ID}); string(r.Data["deleteDestination"]) != "true" {
		t.Fatalf("delete: %s", r.Data["deleteDestination"])
	}
	check("admin", adminC, deleteDestMut, map[string]any{"id": disk.ID}, "NOT_FOUND")
	e.mustGQL(adminC, listDestQuery, nil).field(t, "destinations", &adminList)
	if len(adminList) != 2 {
		t.Fatalf("after delete: %+v", adminList)
	}
	e.mustGQL(adminC, createDestMut, map[string]any{"in": map[string]any{"name": "Local disk", "type": "DISK", "disk": map[string]any{"path": "local"}}})

	e.assertNoSecrets(s3SecretValue, s3SecretValue2, sftpPasswordVal)
}
