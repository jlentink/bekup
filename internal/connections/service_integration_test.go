//go:build integration

package connections

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/ssh"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/runnerpb"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// fakeRunner records requests (cloned before the service zeroes them) and
// answers with scripted responses.
type fakeRunner struct {
	runnerpb.RunnerServiceClient

	mu       sync.Mutex
	probes   []*runnerpb.ProbeRequest
	fetches  []*runnerpb.FetchServerCertificateRequest
	probeErr error
	probe    *runnerpb.ProbeResponse
	chain    *runnerpb.FetchServerCertificateResponse
	hostKey  *runnerpb.FetchHostKeyResponse
	live     []*runnerpb.ProbeRequest
}

func (f *fakeRunner) Probe(_ context.Context, req *runnerpb.ProbeRequest, _ ...grpc.CallOption) (*runnerpb.ProbeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, _ := proto.Clone(req).(*runnerpb.ProbeRequest) //nolint:errcheck // test fake: the clone has the same type
	f.probes = append(f.probes, c)
	f.live = append(f.live, req)
	if f.probeErr != nil {
		return nil, f.probeErr
	}
	return f.probe, nil
}

func (f *fakeRunner) FetchServerCertificate(_ context.Context, req *runnerpb.FetchServerCertificateRequest, _ ...grpc.CallOption) (*runnerpb.FetchServerCertificateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, _ := proto.Clone(req).(*runnerpb.FetchServerCertificateRequest) //nolint:errcheck // test fake: the clone has the same type
	f.fetches = append(f.fetches, c)
	return f.chain, nil
}

func (f *fakeRunner) FetchHostKey(context.Context, *runnerpb.FetchHostKeyRequest, ...grpc.CallOption) (*runnerpb.FetchHostKeyResponse, error) {
	return f.hostKey, nil
}

func (f *fakeRunner) PrePull(_ context.Context, req *runnerpb.PrePullRequest, _ ...grpc.CallOption) (*runnerpb.PrePullResponse, error) {
	return &runnerpb.PrePullResponse{ImageRef: "postgres:" + req.GetVersionKey(), AlreadyPresent: true}, nil
}

type svcEnv struct {
	svc     *Service
	runner  *fakeRunner
	auditor *audit.Auditor
	q       *sqlc.Queries
	pool    *pgxpool.Pool
	db      dbtest.DB
	actor   Actor
	alerts  []Alert
	mu      sync.Mutex
}

func newSvcEnv(t *testing.T) *svcEnv {
	t.Helper()
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	e := &svcEnv{runner: &fakeRunner{}, auditor: audit.New(pool, audit.Options{Registry: logging.NewRegistry()}), q: sqlc.New(pool), pool: pool, db: d}
	var err error
	e.svc, err = New(Options{
		DB: pool, Keyring: testKeyring(t), Auditor: e.auditor, Runner: e.runner,
		Limiter: func(string) (bool, time.Duration) { return true, 0 },
		Alerts: func(_ context.Context, a Alert) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.alerts = append(e.alerts, a)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	u, err := e.q.InsertUser(t.Context(), sqlc.InsertUserParams{Email: "admin@example.com", DisplayName: "A", Role: sqlc.UserRoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	e.actor = Actor{UserID: u.ID}
	return e
}

func (e *svcEnv) auditEntries(t *testing.T, action audit.Action) []map[string]any {
	t.Helper()
	page, err := e.auditor.List(t.Context(), audit.Filter{Action: action}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]map[string]any, 0, len(page.Entries))
	for i := range page.Entries {
		m := map[string]any{}
		if err := json.Unmarshal(page.Entries[i].Details, &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

// allAudit returns every audit detail as one string (secret search).
func (e *svcEnv) allAudit(t *testing.T) string {
	t.Helper()
	page, err := e.auditor.List(t.Context(), audit.Filter{}, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := range page.Entries {
		b.Write(page.Entries[i].Details)
	}
	return b.String()
}

const (
	dbPassword  = "DB-PASSWORD-VALUE-91a2"
	sshPassword = "SSH-PASSWORD-VALUE-3c4d"
)

func pinChain(leaf *testCert) *runnerpb.FetchServerCertificateResponse {
	return &runnerpb.FetchServerCertificateResponse{
		Chain:                []*runnerpb.PeerCertificate{{Der: leaf.cert.Raw}},
		SuggestedAnchorIndex: 0, LeafSpkiSha256: leaf.spki,
	}
}

func TestServiceLifecycle(t *testing.T) {
	e := newSvcEnv(t)
	ctx := t.Context()
	pw := []byte(dbPassword)
	c, err := e.svc.Create(ctx, e.actor, CreateInput{
		Name: "Prod", Engine: engine.KindPostgres, VersionKey: "16", ImageKey: "17", Host: "db.example.com",
		Username: "backup", Password: pw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Trim(string(pw), "\x00") != "" {
		t.Fatal("Create did not zero the password input")
	}
	if c.Port != 5432 || c.TLSMode != TLSPinned || !c.HasPassword || c.Ready || c.PinStatus != PinMissing || c.InsecureTransport {
		t.Fatalf("created: %+v", c)
	}
	row, err := e.q.GetConnection(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(row.SecretEnc, []byte(dbPassword)) {
		t.Fatal("the stored blob contains the password")
	}

	// Duplicate names (case-insensitive) conflict; the D-132 rule applies.
	if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "prod", Engine: engine.KindPostgres, VersionKey: "16", Host: "x.example.com", Username: "u"}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate: %v", err)
	}
	var fe *FieldError
	if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "IP", Engine: engine.KindPostgres, VersionKey: "16", Host: "10.1.2.3", Username: "u"}); !errors.As(err, &fe) || fe.Field != "host" {
		t.Fatalf("ip literal: %v", err)
	}

	// A test before a pin is not ready (no runner call).
	var nr *NotReadyError
	if _, err := e.svc.Test(ctx, e.actor, c.ID); !errors.As(err, &nr) {
		t.Fatalf("test without pin: %v", err)
	}

	// Pin through a fresh capture bound to the confirmed leaf.
	leaf := newCert(t, "db.example.com", false, nil, time.Now().Add(20*24*time.Hour))
	e.runner.chain = pinChain(leaf)
	idx := 0
	other := newCert(t, "db.example.com", false, nil, time.Now().Add(time.Hour))
	if _, err := e.svc.ConfirmPin(ctx, e.actor, c.ID, PinInput{CertIndex: &idx, LeafSPKI: base64.StdEncoding.EncodeToString(other.spki)}); !errors.As(err, &fe) {
		t.Fatalf("confirm with another leaf: %v", err)
	}
	c, err = e.svc.ConfirmPin(ctx, e.actor, c.ID, PinInput{CertIndex: &idx, LeafSPKI: base64.StdEncoding.EncodeToString(leaf.spki)})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Ready || c.Pin == nil || c.PinStatus != PinExpires30d || !bytes.Equal(c.Pin.LeafSPKI, leaf.spki) {
		t.Fatalf("pinned: %+v", c)
	}
	if got := e.auditEntries(t, audit.ActionConnectionPinConfirmed); len(got) != 1 || got[0]["source"] != "captured" {
		t.Fatalf("pin audit: %v", got)
	}

	// Stage a backup pin, then promote it.
	next := newCert(t, "db.example.com", false, nil, time.Now().Add(90*24*time.Hour))
	if _, err := e.svc.StageBackupPin(ctx, e.actor, c.ID, PinInput{CertificatePEM: leaf.pem, LeafSPKI: base64.StdEncoding.EncodeToString(leaf.spki)}); !errors.As(err, &fe) {
		t.Fatalf("staging the current pin: %v", err)
	}
	c, err = e.svc.StageBackupPin(ctx, e.actor, c.ID, PinInput{CertificatePEM: next.pem, LeafSPKI: base64.StdEncoding.EncodeToString(next.spki)})
	if err != nil || c.BackupPin == nil {
		t.Fatalf("stage: %v %+v", err, c)
	}

	// Test: the request carries both pins and the password; the service
	// zeroes its copy after the call.
	e.runner.probe = &runnerpb.ProbeResponse{ServerVersion: "16.4", DetectedVersionKey: "16", Databases: []string{"app"}, VersionKeyCompatible: true}
	res, err := e.svc.Test(ctx, e.actor, c.ID)
	if err != nil || !res.OK || res.ServerVersion != "16.4" || res.Databases != 1 {
		t.Fatalf("test: %v %+v", err, res)
	}
	sent := e.runner.probes[len(e.runner.probes)-1]
	if string(sent.GetTarget().GetPassword()) != dbPassword || len(sent.GetTarget().GetPins()) != 2 || sent.GetTarget().GetVersionKey() != "17" {
		t.Fatalf("probe request: %v", sent)
	}
	if live := e.runner.live[len(e.runner.live)-1]; len(live.GetTarget().GetPassword()) != 0 {
		t.Fatal("the request still holds the password after the call")
	}

	c, err = e.svc.ConfirmPin(ctx, e.actor, c.ID, PinInput{CertificatePEM: next.pem, LeafSPKI: base64.StdEncoding.EncodeToString(next.spki)})
	if err != nil || c.BackupPin != nil || c.PinStatus != PinValid {
		t.Fatalf("promote: %v %+v", err, c)
	}

	// A pin mismatch is audited, alerted and typed.
	st, err := status.New(codes.FailedPrecondition, "target certificate does not match the pinned certificate").WithDetails(
		&errdetails.ErrorInfo{Reason: string(runnerpb.ReasonPinMismatch), Domain: runnerpb.ErrorDomain, Metadata: map[string]string{"check": "leaf_spki"}})
	if err != nil {
		t.Fatal(err)
	}
	e.runner.probeErr = st.Err()
	var pm *PinMismatchError
	if _, err := e.svc.Test(ctx, e.actor, c.ID); !errors.As(err, &pm) || pm.Check != "leaf_spki" {
		t.Fatalf("mismatch: %v", err)
	}
	if got := e.auditEntries(t, audit.ActionConnectionPinMismatch); len(got) != 1 || got[0]["check"] != "leaf_spki" {
		t.Fatalf("mismatch audit: %v", got)
	}
	if len(e.alerts) != 1 || e.alerts[0].Kind != AlertTLSPinMismatch {
		t.Fatalf("alerts: %+v", e.alerts)
	}
	// Other target failures are a test result.
	e.runner.probeErr = runnerpb.ReasonAuthFailed.Status()
	res, err = e.svc.Test(ctx, e.actor, c.ID)
	if err != nil || res.OK || res.Code != string(runnerpb.ReasonAuthFailed) {
		t.Fatalf("auth failure: %v %+v", err, res)
	}
	if _, _, err := e.svc.ListDatabases(ctx, e.actor, c.ID); err == nil {
		t.Fatal("list databases ignored the failure")
	}
	e.runner.probeErr = nil

	// Moving the server removes the pins (audited as changed fields).
	c, err = e.svc.Update(ctx, e.actor, c.ID, UpdateInput{Host: ptr("db2.example.com"), Password: []byte("NEW-" + dbPassword)})
	if err != nil || c.Pin != nil || c.Ready {
		t.Fatalf("move: %v %+v", err, c)
	}
	upd := e.auditEntries(t, audit.ActionConnectionUpdated)
	if len(upd) != 1 || upd[0]["tls_pins_cleared"] != true || !strings.Contains(strings.Join(anyStrings(upd[0]["changed"]), ","), "password") {
		t.Fatalf("update audit: %v", upd)
	}

	// TLS downgrade: audited opt-out, alert, badge; pins kept for upgrades.
	c, err = e.svc.SetTLSMode(ctx, e.actor, c.ID, TLSUnverified)
	if err != nil || !c.InsecureTransport || c.PinStatus != PinNotRequired || !c.Ready {
		t.Fatalf("downgrade: %v %+v", err, c)
	}
	if got := e.auditEntries(t, audit.ActionSecurityOptOut); len(got) != 1 || got[0]["to"] != "unverified" {
		t.Fatalf("opt-out audit: %v", got)
	}
	if e.alerts[len(e.alerts)-1].Kind != AlertTLSDowngrade {
		t.Fatalf("alerts: %+v", e.alerts)
	}
	c, err = e.svc.SetTLSMode(ctx, e.actor, c.ID, TLSPinned)
	if err != nil || c.InsecureTransport || len(e.auditEntries(t, audit.ActionSecurityOptOut)) != 1 {
		t.Fatalf("upgrade: %v %+v", err, c)
	}

	// SSH: tunnel, host key, generated keypair.
	// Sec M3 (D-140): the route to the database changes, so the stored
	// database password must be entered again in the same update.
	if _, err := e.svc.Update(ctx, e.actor, c.ID, UpdateInput{SSH: &SSHInput{Host: "bastion.example.com", User: "tunnel", Password: []byte(sshPassword)}}); !errors.As(err, &fe) || fe.Field != "password" {
		t.Fatalf("tunnel without the database password: %v", err)
	}
	c, err = e.svc.Update(ctx, e.actor, c.ID, UpdateInput{
		SSH: &SSHInput{Host: "bastion.example.com", User: "tunnel", Password: []byte(sshPassword)}, Password: []byte("NEW-" + dbPassword),
	})
	if err != nil || c.SSH == nil || c.SSH.Auth != SSHAuthPassword || c.Ready {
		t.Fatalf("ssh: %v %+v", err, c)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp)))
	e.runner.hostKey = &runnerpb.FetchHostKeyResponse{Type: sp.Type(), FingerprintSha256: ssh.FingerprintSHA256(sp), AuthorizedKey: line}
	hk, err := e.svc.FetchHostKey(ctx, e.actor, c.ID)
	if err != nil || hk.AuthorizedKey != line {
		t.Fatalf("fetch host key: %v %+v", err, hk)
	}
	if hk.Host != "bastion.example.com" || hk.Port != 22 {
		t.Fatalf("fetched from %s:%d", hk.Host, hk.Port)
	}
	// Code M10: the pin is bound to the bastion the key was fetched from.
	if _, err := e.svc.ConfirmHostKey(ctx, e.actor, c.ID, line, hk.Host, 2222); !errors.Is(err, ErrConflict) {
		t.Fatalf("confirm for another bastion: %v", err)
	}
	if c, err = e.svc.ConfirmHostKey(ctx, e.actor, c.ID, line+" comment", hk.Host, hk.Port); err != nil || c.SSH.HostKey == nil {
		t.Fatalf("confirm host key: %v", err)
	}
	kp, c, err := e.svc.GenerateSSHKeypair(ctx, e.actor, c.ID)
	if err != nil || !strings.HasPrefix(kp.AuthorizedKey, "ssh-ed25519 ") || c.SSH.Auth != SSHAuthPrivateKey || c.SSH.PublicKey != kp.AuthorizedKey {
		t.Fatalf("keypair: %v %+v", err, c)
	}
	// The SSH password was replaced; the DB password is untouched.
	creds := e.openRow(t, c.ID)
	if creds.SSHPassword != nil || len(creds.SSHPrivateKey) == 0 || string(creds.Password) != "NEW-"+dbPassword {
		t.Fatal("credentials after keypair generation")
	}
	creds.Zero()

	// The bastion move clears the host key; disabling removes everything.
	// The private key may stay across the move; the database password
	// must be entered again (D-140).
	c, err = e.svc.Update(ctx, e.actor, c.ID, UpdateInput{SSH: &SSHInput{Host: "bastion2.example.com", User: "tunnel"}, Password: []byte("NEW-" + dbPassword)})
	if err != nil || c.SSH.HostKey != nil || c.SSH.Auth != SSHAuthPrivateKey {
		t.Fatalf("bastion move: %v %+v", err, c)
	}
	moves := e.auditEntries(t, audit.ActionConnectionUpdated)
	if got := anyStrings(moves[0]["reentered_for_new_endpoint"]); strings.Join(got, ",") != "password" {
		t.Fatalf("re-entry audit: %v", moves[0])
	}
	c, err = e.svc.Update(ctx, e.actor, c.ID, UpdateInput{DisableSSH: true, Password: []byte("NEW-" + dbPassword)})
	if err != nil || c.SSH != nil {
		t.Fatalf("disable ssh: %v", err)
	}
	if creds := e.openRow(t, c.ID); creds.SSHPrivateKey != nil {
		t.Fatal("SSH key kept after disabling the tunnel")
	}

	if res, err := e.svc.PrePull(ctx, e.actor, c.ID); err != nil || res.ImageRef != "postgres:17" {
		t.Fatalf("prepull: %v %+v", err, res)
	}

	// Secrets never reach the audit log.
	all := e.allAudit(t)
	for _, s := range []string{dbPassword, sshPassword, "PRIVATE KEY"} {
		if strings.Contains(all, s) {
			t.Fatalf("the audit log contains %q", s)
		}
	}
	if len(e.auditEntries(t, audit.ActionSecretChanged)) < 3 {
		t.Fatal("secret.changed entries missing")
	}

	// A connection backed up by a (non-deleted) job cannot be deleted
	// (task 3.5); once the job is deleted it can.
	jobID, err := dbtest.InsertJob(ctx, e.pool, dbtest.JobRow{ConnectionID: c.ID})
	if err != nil {
		t.Fatal(err)
	}
	var inUse *InUseError
	if err := e.svc.Delete(ctx, e.actor, c.ID); !errors.Is(err, ErrInUse) || !errors.As(err, &inUse) || len(inUse.Jobs) != 1 {
		t.Fatalf("delete while in use: %v", err)
	}
	if _, err := e.pool.Exec(ctx, "UPDATE jobs SET deleted_at = now() WHERE id = $1", jobID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(ctx, e.actor, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "Prod", Engine: engine.KindMySQL, VersionKey: "8.4", Host: "my.example.com", Username: "root"}); err != nil {
		t.Fatalf("reuse name: %v", err)
	}
}

func (e *svcEnv) openRow(t *testing.T, id uuid.UUID) *Credentials {
	t.Helper()
	row, err := e.q.GetConnection(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	c, err := openCredentials(e.svc.o.Keyring, row.ID, row.SecretEnc)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func anyStrings(v any) []string {
	l, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(l))
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestCreateUnpinnedIsOptOut(t *testing.T) {
	e := newSvcEnv(t)
	c, err := e.svc.Create(t.Context(), e.actor, CreateInput{
		Name: "Legacy", Engine: engine.KindMariaDB, VersionKey: "10.11", Host: "10.1.2.3", Username: "root", TLSMode: TLSDisabled,
	})
	if err != nil || !c.InsecureTransport || !c.Ready {
		t.Fatalf("create: %v %+v", err, c)
	}
	if got := e.auditEntries(t, audit.ActionSecurityOptOut); len(got) != 1 || got[0]["from"] != "pinned" || got[0]["to"] != "disabled" {
		t.Fatalf("opt-out: %v", got)
	}
	if len(e.alerts) != 1 || e.alerts[0].Kind != AlertTLSDowngrade {
		t.Fatalf("alerts: %+v", e.alerts)
	}
	// Back to pinned on an IP literal without a tunnel: D-132.
	var fe *FieldError
	if _, err := e.svc.SetTLSMode(t.Context(), e.actor, c.ID, TLSPinned); !errors.As(err, &fe) {
		t.Fatalf("upgrade on ip literal: %v", err)
	}
}

// TestRunLimit covers the per-connection run limit (task 3.13, D-156):
// bounds against the column range and workers.max_concurrent_runs (default
// 4 here), the effective value, clearing, and the audit by field name.
func TestRunLimit(t *testing.T) {
	e := newSvcEnv(t)
	ctx := t.Context()
	var fe *FieldError
	for _, bad := range []int{0, 65, 5} {
		n := bad
		_, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "bad", Engine: engine.KindPostgres, VersionKey: "16", Host: "db.example.com", Username: "u", MaxConcurrentRuns: &n})
		if !errors.As(err, &fe) || fe.Field != "maxConcurrentRuns" {
			t.Fatalf("create with %d: %v", bad, err)
		}
	}
	two := 2
	c, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "limited", Engine: engine.KindPostgres, VersionKey: "16", Host: "db.example.com", Username: "u", MaxConcurrentRuns: &two})
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxConcurrentRuns == nil || *c.MaxConcurrentRuns != 2 || c.EffectiveMaxConcurrentRuns != 2 {
		t.Fatalf("created: %v %d", c.MaxConcurrentRuns, c.EffectiveMaxConcurrentRuns)
	}
	got := e.auditEntries(t, audit.ActionConnectionCreated)
	if len(got) != 1 {
		t.Fatalf("create audit: %v", got)
	}
	if settings, ok := got[0]["settings"].(map[string]any); !ok || settings["max_concurrent_runs"] != float64(2) {
		t.Fatalf("create audit: %v", got)
	}
	for _, bad := range []int{0, 65, 5} {
		n := bad
		if _, err := e.svc.Update(ctx, e.actor, c.ID, UpdateInput{MaxConcurrentRuns: &n}); !errors.As(err, &fe) || fe.Field != "maxConcurrentRuns" {
			t.Fatalf("update with %d: %v", bad, err)
		}
	}
	one := 1
	if _, err := e.svc.Update(ctx, e.actor, c.ID, UpdateInput{MaxConcurrentRuns: &one, ClearMaxConcurrentRuns: true}); !errors.As(err, &fe) {
		t.Fatalf("set and clear: %v", err)
	}
	c, err = e.svc.Update(ctx, e.actor, c.ID, UpdateInput{MaxConcurrentRuns: &one})
	if err != nil || c.MaxConcurrentRuns == nil || *c.MaxConcurrentRuns != 1 || c.EffectiveMaxConcurrentRuns != 1 {
		t.Fatalf("update: %v %v", c.MaxConcurrentRuns, err)
	}
	upd := e.auditEntries(t, audit.ActionConnectionUpdated)
	if len(upd) != 1 || !strings.Contains(fmt.Sprint(upd[0]["changed"]), "max_concurrent_runs") {
		t.Fatalf("update audit: %v", upd)
	}
	values, ok := upd[0]["values"].(map[string]any)
	if !ok {
		t.Fatalf("update audit values: %v", upd[0])
	}
	if v, ok := values["max_concurrent_runs"].(map[string]any); !ok || v["old"] != float64(2) || v["new"] != float64(1) {
		t.Fatalf("update audit values: %v", upd[0]["values"])
	}
	c, err = e.svc.Update(ctx, e.actor, c.ID, UpdateInput{ClearMaxConcurrentRuns: true})
	if err != nil || c.MaxConcurrentRuns != nil || c.EffectiveMaxConcurrentRuns != 1 {
		t.Fatalf("clear: %v %v", c.MaxConcurrentRuns, err)
	}
	row, err := e.q.GetConnection(ctx, c.ID)
	if err != nil || row.MaxConcurrentRuns != nil {
		t.Fatalf("stored: %v %v", row.MaxConcurrentRuns, err)
	}
	// An unrelated update with no limit change is not audited as one.
	name := "renamed"
	if _, err := e.svc.Update(ctx, e.actor, c.ID, UpdateInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	upd = e.auditEntries(t, audit.ActionConnectionUpdated)
	for _, u := range upd {
		if u["name"] == "renamed" && strings.Contains(fmt.Sprint(u["changed"]), "max_concurrent_runs") {
			t.Fatalf("rename audited a limit change: %v", u)
		}
	}
}
