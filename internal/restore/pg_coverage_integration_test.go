//go:build integration

package restore_test

// More real-engine PostgreSQL restores on the task 4.2 harness
// (startPGEnv): a recipient-encrypted (age X25519) artifact restored with
// the identity handed off at request time (sealed, used once, gone after
// staging), and an artifact staged from an SFTP destination.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// requestAcked requests a new-database restore of artifact into the
// PostgreSQL 17 target as the admin, handing off identity (if any). The
// harness role is a superuser, so the request carries that verdict and the
// admin's acknowledgement (D-068) of the probed verdict, as the API would.
func (e *pgiEnv) requestAcked(artifact uuid.UUID, dbName, identity string) uuid.UUID {
	e.t.Helper()
	priv := e.targetPrivileges(e.dstConn)
	var id uuid.UUID
	err := db.Tx(e.t.Context(), e.pool, func(q *sqlc.Queries) error {
		in := restore.CreateInput{
			ArtifactID: artifact, TargetConnectionID: e.dstConn, TargetDBName: &dbName, Mode: restore.ModeNewDB,
			RequestedBy: e.admin, Privileges: &priv, PrivilegedAck: priv.Gated(),
		}
		if identity != "" {
			in.Identity = []byte(identity)
		}
		r, err := e.svc.CreateTx(e.t.Context(), q, in)
		id = r.ID
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// targetPrivileges probes the role of connection id through the runner,
// as the API does for a request.
func (e *pgiEnv) targetPrivileges(id uuid.UUID) connections.Privileges {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(e.t.Context(), 2*time.Minute)
	defer cancel()
	target, release, err := e.conns.Target(ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	resp, err := e.rc.RPC().Probe(ctx, &runnerpb.ProbeRequest{Ctx: &runnerpb.RunContext{RunId: uuid.NewString()}, Target: target})
	release()
	if err != nil {
		e.t.Fatal(err)
	}
	return connections.PrivilegesFromRunner(resp.GetPrivileges())
}

func TestIntegrationPGRestoreCoverage(t *testing.T) {
	e := startPGEnv(t)
	w := e.worker(e.rc.RPC())
	pgCustom := seedSpec{format: pipeline.FormatCustom, engine: "postgres", versionKey: "16"}
	appDump := e.dump("app")

	t.Run("recipient-encrypted artifact with the identity handoff", func(t *testing.T) {
		j := e.job(jobkeys.ModeRecipient, e.diskID)
		if j.identity == "" {
			t.Fatal("recipient job without an identity")
		}
		e.seq++
		s := e.seedAs(j, e.diskID, e.disk, appDump, time.Now().Add(time.Duration(e.seq)*time.Minute), pgCustom)
		var mode string
		if err := e.pool.QueryRow(t.Context(), "SELECT encryption_mode FROM artifacts WHERE id = $1", s.artifact).Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if mode != string(pipeline.EncryptRecipients) {
			t.Fatalf("artifact encryption %q", mode)
		}
		id := e.requestAcked(s.artifact, "from_recipient", j.identity)
		if n := e.count("SELECT count(*) FROM restore_identity_handoff WHERE restore_id = $1", id); n != 1 {
			t.Fatalf("handoffs before the run: %d", n)
		}
		if err := e.work(t.Context(), w, id); err != nil {
			t.Fatal(err)
		}
		if st, code := e.status(id); st != "succeeded" {
			t.Fatalf("status %s %s\n%s", st, code, (&wenv{env: e.env}).logs(id))
		}
		if n := e.dk.psql(e.dst, "from_recipient", "SELECT count(*) FROM items"); n != "1000" {
			t.Fatalf("rows = %s", n)
		}
		if n := e.count("SELECT count(*) FROM restore_identity_handoff WHERE restore_id = $1", id); n != 0 {
			t.Fatalf("handoff kept after the run: %d", n)
		}
		if n := e.count(`SELECT count(*) FROM restore_logs WHERE line LIKE '%AGE-SECRET-KEY%'`); n != 0 {
			t.Fatalf("%d restore log lines carry an identity", n)
		}
	})

	t.Run("staged from an SFTP destination", func(t *testing.T) {
		sd := startSFTPDest(t, "pg-restores")
		sftpID := e.destRow("sftp")
		e.op.add(sftpID, sd.open)
		dest, err := sd.open()
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := dest.Close(); err != nil {
				t.Error(err)
			}
		}()
		j := e.job(jobkeys.ModePassphrase, sftpID)
		e.seq++
		s := e.seedAs(j, sftpID, dest, appDump, time.Now().Add(time.Duration(e.seq)*time.Minute), pgCustom)
		id := e.requestAcked(s.artifact, "from_sftp", "")
		if err := e.work(t.Context(), w, id); err != nil {
			t.Fatal(err)
		}
		if st, code := e.status(id); st != "succeeded" {
			t.Fatalf("status %s %s\n%s", st, code, (&wenv{env: e.env}).logs(id))
		}
		if n := e.dk.psql(e.dst, "from_sftp", "SELECT count(*) FROM items"); n != "1000" {
			t.Fatalf("rows = %s", n)
		}
		if len(e.scratchEntries()) != 0 {
			t.Fatal("scratch not cleaned up")
		}
	})

	if n := e.count(`SELECT count(*) FROM restore_logs WHERE line LIKE '%' || $1 || '%'`, pgiPass); n != 0 {
		t.Fatalf("%d restore log lines carry the password", n)
	}
}
