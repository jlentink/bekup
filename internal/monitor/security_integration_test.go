//go:build integration

package monitor

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/notify"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// writeCert writes a self-signed certificate expiring at notAfter.
func writeCert(t *testing.T, notAfter time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"},
		NotBefore: notAfter.Add(-365 * 24 * time.Hour), NotAfter: notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "client.crt")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func systemEvents(t *testing.T, pool *pgxpool.Pool, kind string) []map[string]any {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT detail, severity FROM notification_events
		WHERE type = 'security.system' AND detail->>'kind' = $1 ORDER BY created_at`, kind)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var d map[string]any
		var sev string
		if err := rows.Scan(&d, &sev); err != nil {
			t.Fatal(err)
		}
		d["_severity"] = sev
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestSecurityCheckExpiry: a certificate with 6 days left raises the 7-day
// alert exactly once over repeated runs; a pinned database certificate
// with 20 days left raises the 30-day alert once; posture reminders are
// daily.
func TestSecurityCheckExpiry(t *testing.T) {
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	now := time.Now().UTC()
	conn := uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO connections (id, name, engine, version_key, image_key, host, port, username, tls_pin_cert_pem, tls_pin_not_after, tls_pinned_at)
		VALUES ($1, 'pinned', 'postgres', '17', '17', 'db.example.test', 5432, 'backup', 'pem', $2, now())`, conn, now.Add(20*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	clock := now
	c, err := NewSecurityChecker(SecurityOptions{
		DB: pool, Auditor: audit.New(pool, audit.Options{Observer: notify.ObserveAudit}),
		Certs:   []CertFile{{Component: CertRunnerClient, Path: writeCert(t, now.Add(6*24*time.Hour))}},
		Posture: func() []string { return []string{notify.KindInsecureTransport} },
		Now:     func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := c.Run(t.Context()); err != nil {
			t.Fatal(err)
		}
		clock = clock.Add(time.Hour)
	}
	got := systemEvents(t, pool, notify.KindCertExpiring)
	if len(got) != 1 || got[0]["component"] != CertRunnerClient || got[0]["threshold_days"] != float64(7) || got[0]["_severity"] != "error" {
		t.Fatalf("certificate alerts: %v", got)
	}
	pins := systemEvents(t, pool, notify.KindPinExpiring)
	if len(pins) != 1 || pins[0]["threshold_days"] != float64(30) {
		t.Fatalf("pin alerts: %v", pins)
	}
	var subject uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT connection_id FROM notification_events WHERE detail->>'kind' = $1`, notify.KindPinExpiring).Scan(&subject); err != nil || subject != conn {
		t.Fatalf("pin alert subject %v %v", subject, err)
	}
	if n := len(systemEvents(t, pool, notify.KindInsecureTransport)); n < 1 || n > 2 {
		t.Fatalf("%d posture reminders in 3 hours", n)
	}
}

// TestSecurityCheckAuditTamper: a tampered audit row raises one critical
// security.system alert (recorded as audit.verify.failed), also when the
// tampering is in the already verified part of the chain.
func TestSecurityCheckAuditTamper(t *testing.T) {
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	ctx := t.Context()
	a := audit.New(pool, audit.Options{Observer: notify.ObserveAudit})
	for range 5 {
		if _, err := a.Record(ctx, audit.Event{Actor: audit.SystemActor(), Action: audit.ActionSettingChanged, TargetType: "setting", TargetID: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	c, err := NewSecurityChecker(SecurityOptions{DB: pool, Auditor: a})
	if err != nil {
		t.Fatal(err)
	}
	// A clean chain: nothing, and the checkpoint advances.
	if err := c.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(systemEvents(t, pool, notify.KindAuditVerifyFailed)); n != 0 {
		t.Fatalf("%d alerts on a clean chain", n)
	}
	// The result is stored for Query.auditChainStatus (D-196).
	st, err := a.ChainStatus(ctx)
	if err != nil || st.LastVerification == nil || !st.LastVerification.OK || !st.LastVerification.Full ||
		st.LastVerification.VerifiedThroughID != st.HeadID || st.HeadID == 0 || len(st.HeadHash) != 64 {
		t.Fatalf("chain status after a clean run: %+v %v", st, err)
	}
	// Tamper with an already verified row: the incremental run must catch
	// it through the anchor or the (later) full run through the hash.
	if err := d.SuperExec(ctx, "ALTER TABLE audit_log DISABLE TRIGGER USER",
		"UPDATE audit_log SET details = '{\"forged\": true}', hash = sha256('x') WHERE id = (SELECT max(id) FROM audit_log)",
		"ALTER TABLE audit_log ENABLE TRIGGER USER"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := c.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got := systemEvents(t, pool, notify.KindAuditVerifyFailed)
	if len(got) != 1 || got[0]["_severity"] != "critical" {
		t.Fatalf("tamper alerts: %v", got)
	}
	var entries int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'audit.verify.failed'`).Scan(&entries); err != nil || entries != 1 {
		t.Fatalf("audit.verify.failed entries %d %v", entries, err)
	}
	st, err = a.ChainStatus(ctx)
	if err != nil || st.LastVerification == nil || st.LastVerification.OK || st.LastVerification.Finding == "" {
		t.Fatalf("chain status after tampering: %+v %v", st.LastVerification, err)
	}

	// A fresh process verifies the whole chain and reports the row itself
	// (hash_mismatch, a different finding than the anchor): one more alert,
	// and no more on its next run.
	c2, err := NewSecurityChecker(SecurityOptions{DB: pool, Auditor: a})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := c2.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := systemEvents(t, pool, notify.KindAuditVerifyFailed); len(got) != 2 {
		t.Fatalf("%d alerts after a full run: %v", len(got), got)
	}
}
