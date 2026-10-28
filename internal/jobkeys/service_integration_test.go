//go:build integration

package jobkeys

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/pipeline"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

type env struct {
	d        dbtest.DB
	pool     *pgxpool.Pool
	kr       *crypto.Keyring
	svc      *Service
	auditor  *audit.Auditor
	admin    Actor
	maint    Actor
	adminPr  auth.Principal
	adminCtx context.Context
	maintCtx context.Context
	mu       sync.Mutex
	alerts   []Alert
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{d: dbtest.Migrated(t)}
	e.pool = e.d.AppPool(t)
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	e.kr = kr
	e.auditor = audit.New(e.pool, audit.Options{Registry: logging.NewRegistry()})
	e.svc, err = New(Options{
		DB: e.pool, Keyring: kr, Auditor: e.auditor, Authorizer: authz.New(authz.Options{}),
		Alerts: func(_ context.Context, a Alert) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.alerts = append(e.alerts, a)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	q := sqlc.New(e.pool)
	mk := func(email string, role auth.Role) (Actor, auth.Principal) {
		u, err := q.InsertUser(t.Context(), sqlc.InsertUserParams{Email: email, DisplayName: "U", Role: sqlc.UserRole(role)})
		if err != nil {
			t.Fatal(err)
		}
		pr := authztest.Principal(role)
		pr.UserID = u.ID
		return Actor{UserID: u.ID}, pr
	}
	var maintPr auth.Principal
	e.admin, e.adminPr = mk("admin@example.com", auth.RoleAdmin)
	e.maint, maintPr = mk("maint@example.com", auth.RoleMaintainer)
	e.adminCtx = authztest.Context(t.Context(), e.adminPr)
	e.maintCtx = authztest.Context(t.Context(), maintPr)
	return e
}

// newJob inserts a jobs row (the key tables reference jobs since task 3.5).
func (e *env) newJob(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := dbtest.InsertJob(t.Context(), e.pool, dbtest.JobRow{})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) alertsOf(k AlertKind) []Alert {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Alert
	for _, a := range e.alerts {
		if a.Kind == k {
			out = append(out, a)
		}
	}
	return out
}

func (e *env) audits(t *testing.T, action audit.Action) []map[string]any {
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

// dump returns the text form of every row of the job key tables and the
// audit log (plaintext search).
func (e *env) dump(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, table := range []string{"job_encryption", "job_passphrases", "job_recipients", "audit_log"} {
		rows, err := e.pool.Query(t.Context(), fmt.Sprintf("SELECT t::text FROM %s t", table))
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			b.WriteString(s + "\n")
		}
		if rows.Err() != nil {
			t.Fatal(rows.Err())
		}
	}
	return b.String()
}

func generate(t *testing.T, e *env, job uuid.UUID) (int, string) {
	t.Helper()
	g, err := e.svc.GeneratePassphrase(e.adminCtx, e.admin, job)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Destroy()
	return g.Info.Version, string(g.Passphrase.Bytes())
}

func encryptWith(t *testing.T, enc pipeline.Encryption, plain string) []byte {
	t.Helper()
	var buf bytes.Buffer
	var (
		w   io.WriteCloser
		err error
	)
	switch enc.Mode {
	case pipeline.EncryptPassphrase:
		w, err = crypto.EncryptWithPassphrase(&buf, enc.Passphrase.Bytes(), enc.WorkFactor)
	case pipeline.EncryptRecipients:
		w, err = crypto.EncryptToRecipients(&buf, enc.Recipients...)
	default:
		t.Fatalf("mode %s", enc.Mode)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, plain); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readAll(t *testing.T, r io.Reader, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func isCode(err error, code apierror.Code) bool {
	var ae *apierror.Error
	return errors.As(err, &ae) && ae.Code == code
}

// TestPassphraseVersioning: new artifacts use the newest version, old
// versions stay retrievable, exactly one version is active.
func TestPassphraseVersioning(t *testing.T) {
	e := newEnv(t)
	job := e.newJob(t)
	v1, p1 := generate(t, e, job)
	chosen := "correct horse battery staple umbrella quartz"
	info2, err := e.svc.SetPassphrase(e.adminCtx, e.admin, job, crypto.NewSecret([]byte(chosen)))
	if err != nil {
		t.Fatal(err)
	}
	v3, p3 := generate(t, e, job)
	if v1 != 1 || info2.Version != 2 || v3 != 3 || p1 == p3 {
		t.Fatalf("versions %d %d %d", v1, info2.Version, v3)
	}
	if words := strings.Fields(p3); len(words) != crypto.MinPassphraseWords {
		t.Fatalf("generated passphrase has %d words", len(words))
	}
	for v, want := range map[int]string{1: p1, 2: chosen, 3: p3} {
		got, err := e.svc.PassphraseForVersion(t.Context(), job, v)
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Bytes()) != want {
			t.Fatalf("version %d: wrong passphrase", v)
		}
		got.Destroy()
	}
	if _, err := e.svc.PassphraseForVersion(t.Context(), job, 4); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("version 4: %v", err)
	}
	if _, err := e.svc.PassphraseForVersion(t.Context(), uuid.New(), 1); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("other job: %v", err)
	}
	list, err := e.svc.Passphrases(t.Context(), job)
	if err != nil {
		t.Fatal(err)
	}
	active := 0
	for _, p := range list {
		if p.RetiredAt == nil {
			active++
			if p.Version != 3 {
				t.Fatalf("active version %d", p.Version)
			}
		}
	}
	if len(list) != 3 || active != 1 {
		t.Fatalf("%d versions, %d active", len(list), active)
	}
	st, err := e.svc.Status(t.Context(), job)
	if err != nil || !st.Configured || st.Mode != ModePassphrase || st.Passphrase == nil || st.Passphrase.Version != 3 || st.OptedOut {
		t.Fatalf("status = %+v, %v", st, err)
	}

	// Round trip through the pipeline's configuration: an artifact written
	// now uses v3 and decrypts with PassphraseForVersion(3) only.
	enc, err := e.svc.Encryptor(job).BackupEncryption(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if enc.PassphraseVersion != 3 {
		t.Fatalf("new artifacts use version %d", enc.PassphraseVersion)
	}
	ct := encryptWith(t, enc, "dump v3")
	enc.Passphrase.Destroy()
	p, err := e.svc.PassphraseForVersion(t.Context(), job, 3)
	if err != nil {
		t.Fatal(err)
	}
	r, err := crypto.DecryptWithPassphrase(bytes.NewReader(ct), p.Bytes(), 0)
	if got := readAll(t, r, err); got != "dump v3" {
		t.Fatalf("decrypted %q", got)
	}
	p.Destroy()
	old, err := e.svc.PassphraseForVersion(t.Context(), job, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Destroy()
	if _, err := crypto.DecryptWithPassphrase(bytes.NewReader(ct), old.Bytes(), 0); err == nil {
		t.Fatal("an old version decrypted a new artifact")
	}

	if got := e.audits(t, audit.ActionJobEncryptionPassphraseSet); len(got) != 3 {
		t.Fatalf("%d passphrase_set entries", len(got))
	}
	if got := e.audits(t, audit.ActionSecretChanged); len(got) != 3 {
		t.Fatalf("%d secret.changed entries", len(got))
	}
}

// TestSealedWithAAD: the ciphertext is bound to job and version.
func TestSealedWithAAD(t *testing.T) {
	e := newEnv(t)
	job := e.newJob(t)
	generate(t, e, job)
	generate(t, e, job)
	q := sqlc.New(e.pool)
	row, err := q.GetJobPassphrase(t.Context(), sqlc.GetJobPassphraseParams{JobID: job, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := e.kr.Open(row.SealedPassphrase, passphraseAAD(job, 1)); err != nil {
		t.Fatal(err)
	} else {
		crypto.Zero(pt)
	}
	for name, aad := range map[string]crypto.AAD{
		"other version": passphraseAAD(job, 2),
		"other job":     passphraseAAD(uuid.New(), 1),
		"other field":   {Table: "job_passphrases", RowID: job.String(), Field: "sealed_passphrase"},
	} {
		if _, err := e.kr.Open(row.SealedPassphrase, aad); !errors.Is(err, crypto.ErrDecrypt) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Swapping the ciphertexts of two versions in the database (bypassing
	// the triggers as superuser) makes both unreadable.
	if err := e.d.SuperExec(t.Context(),
		"SET session_replication_role = replica",
		fmt.Sprintf(`UPDATE job_passphrases a SET sealed_passphrase = b.sealed_passphrase
			FROM job_passphrases b WHERE a.job_id = '%[1]s' AND b.job_id = '%[1]s' AND a.version <> b.version`, job),
	); err != nil {
		t.Fatal(err)
	}
	for _, v := range []int{1, 2} {
		if _, err := e.svc.PassphraseForVersion(t.Context(), job, v); !errors.Is(err, crypto.ErrDecrypt) {
			t.Errorf("swapped version %d: %v", v, err)
		}
	}
	if _, err := e.svc.KeyMaterialFor(t.Context(), job); !errors.Is(err, crypto.ErrDecrypt) {
		t.Errorf("KeyMaterialFor after the swap: %v", err)
	}
}

// TestGeneratedKeysNeverStoredInPlaintext scans every row for the
// generated passphrase, the chosen passphrase and the identity.
func TestGeneratedKeysNeverStoredInPlaintext(t *testing.T) {
	e := newEnv(t)
	job := e.newJob(t)
	_, p1 := generate(t, e, job)
	chosen := "correct horse battery staple umbrella quartz"
	if _, err := e.svc.SetPassphrase(e.adminCtx, e.admin, job, crypto.NewSecret([]byte(chosen))); err != nil {
		t.Fatal(err)
	}
	kp, err := e.svc.GenerateAgeKeypair(e.adminCtx, e.admin, job, "ops laptop")
	if err != nil {
		t.Fatal(err)
	}
	identity := string(kp.Identity.Bytes())
	if !strings.HasPrefix(identity, "AGE-SECRET-KEY-1") || !bytes.Contains(kp.IdentityFile.Bytes(), kp.Identity.Bytes()) {
		t.Fatal("the generated identity is not returned")
	}
	if !strings.HasSuffix(kp.FileName, "-v1.agekey") || kp.Info.Fingerprint != pipeline.RecipientFingerprint(kp.Info.Recipient) {
		t.Fatalf("keypair = %+v", kp.Info)
	}
	ex, err := e.svc.ExportPassphrases(e.adminCtx, e.admin, job)
	if err != nil {
		t.Fatal(err)
	}
	ex.Destroy()
	kp.Destroy()
	dump := e.dump(t)
	for name, s := range map[string]string{
		"generated passphrase": p1, "chosen passphrase": chosen, "identity": identity,
		"identity prefix": "AGE-SECRET-KEY", "first word": strings.Fields(p1)[0] + " " + strings.Fields(p1)[1],
	} {
		if strings.Contains(dump, s) || strings.Contains(strings.ToLower(dump), strings.ToLower(s)) {
			t.Errorf("the %s is stored in plaintext", name)
		}
	}
	if !strings.Contains(dump, kp.Info.Recipient) {
		t.Error("the recipient (public) should be stored")
	}
}

// TestRecipientKeypair: generated and supplied recipients, versioning and
// a round trip with the returned identity.
func TestRecipientKeypair(t *testing.T) {
	e := newEnv(t)
	job := e.newJob(t)
	generate(t, e, job)
	kp, err := e.svc.GenerateAgeKeypair(e.adminCtx, e.admin, job, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	defer kp.Destroy()
	km, err := e.svc.KeyMaterialFor(t.Context(), job)
	if err != nil {
		t.Fatal(err)
	}
	if km.Mode != ModeRecipient || km.RecipientVersion != 1 || km.Passphrase != nil || km.Recipients[0] != kp.Info.Recipient {
		t.Fatalf("key material = %+v", km)
	}
	enc, err := km.Pipeline()
	if err != nil {
		t.Fatal(err)
	}
	ct := encryptWith(t, enc, "dump r1")
	r, err := crypto.DecryptWithIdentities(bytes.NewReader(ct), kp.IdentityFile.Bytes())
	if got := readAll(t, r, err); got != "dump r1" {
		t.Fatalf("decrypted %q", got)
	}

	info, err := e.svc.SetRecipient(e.adminCtx, e.admin, job, "  "+testRecipient+" ", "yubikey")
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != 2 || info.Generated || info.Recipient != testRecipient {
		t.Fatalf("recipient = %+v", info)
	}
	if _, err := e.svc.SetRecipient(e.adminCtx, e.admin, job, "age1bogus", ""); err == nil {
		t.Fatal("invalid recipient accepted")
	}
	old, err := e.svc.RecipientForVersion(t.Context(), job, 1)
	if err != nil || old.RetiredAt == nil || !old.Generated || old.Label != "laptop" {
		t.Fatalf("recipient v1 = %+v, %v", old, err)
	}
	list, err := e.svc.Recipients(t.Context(), job)
	if err != nil || len(list) != 2 {
		t.Fatalf("recipients = %v, %v", list, err)
	}
	// Switching back to a passphrase keeps the recipients.
	v, _ := generate(t, e, job)
	st, err := e.svc.Status(t.Context(), job)
	if err != nil || st.Mode != ModePassphrase || st.Passphrase.Version != v || st.Recipient == nil || st.Recipient.Version != 2 {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if got := e.audits(t, audit.ActionJobEncryptionRecipientSet); len(got) != 2 || got[0]["recipient"] == logging.Redacted {
		t.Fatalf("recipient_set entries = %v", got)
	}
}

// TestOptOutAndOptIn: admin only with step-up, audited, alerted, badge.
func TestOptOutAndOptIn(t *testing.T) {
	e := newEnv(t)
	job := e.newJob(t)
	generate(t, e, job)

	if _, err := e.svc.OptOut(e.maintCtx, e.maint, job, "test db"); !isCode(err, apierror.Forbidden) {
		t.Fatalf("maintainer opt-out: %v", err)
	}
	stale := authztest.Context(t.Context(), authztest.Stale(e.adminPr))
	if _, err := e.svc.OptOut(stale, e.admin, job, "test db"); !isCode(err, apierror.StepUpRequired) {
		t.Fatalf("stale admin opt-out: %v", err)
	}
	if _, err := e.svc.OptOut(e.adminCtx, e.maint, job, "test db"); !errors.Is(err, ErrActorMismatch) {
		t.Fatalf("actor mismatch: %v", err)
	}
	if _, err := e.svc.OptOut(e.adminCtx, e.admin, job, "  "); err == nil {
		t.Fatal("opt-out without a reason accepted")
	}
	if len(e.alertsOf(AlertEncryptionOptOut)) != 0 || len(e.audits(t, audit.ActionSecurityOptOut)) != 0 {
		t.Fatal("a refused opt-out was alerted or audited")
	}

	st, err := e.svc.OptOut(e.adminCtx, e.admin, job, "scratch database")
	if err != nil {
		t.Fatal(err)
	}
	if !st.OptedOut || st.Mode != ModeNone || st.OptOutReason != "scratch database" || st.OptedOutBy == nil || *st.OptedOutBy != e.admin.UserID {
		t.Fatalf("status = %+v", st)
	}
	if _, err := e.svc.OptOut(e.adminCtx, e.admin, job, "again"); !errors.Is(err, ErrAlreadyOptedOut) {
		t.Fatalf("second opt-out: %v", err)
	}
	alerts := e.alertsOf(AlertEncryptionOptOut)
	if len(alerts) != 1 || alerts[0].JobID != job || alerts[0].ActorID != e.admin.UserID {
		t.Fatalf("alerts = %+v", alerts)
	}
	so := e.audits(t, audit.ActionSecurityOptOut)
	if len(so) != 1 || so[0]["setting"] != "job.encryption" || so[0]["reason"] != "scratch database" {
		t.Fatalf("security.opt_out = %v", so)
	}
	if got := e.audits(t, audit.ActionJobEncryptionOptedOut); len(got) != 1 || got[0]["from"] != "passphrase" {
		t.Fatalf("opted_out = %v", got)
	}
	enc, err := e.svc.Encryptor(job).BackupEncryption(t.Context())
	if err != nil || enc.Mode != pipeline.EncryptNone || !enc.OptedOut {
		t.Fatalf("opted-out encryption = %v, %v", enc, err)
	}

	if _, err := e.svc.OptIn(e.adminCtx, e.admin, job, ModeRecipient); !errors.Is(err, ErrNoActiveKey) {
		t.Fatalf("opt-in without a recipient: %v", err)
	}
	st, err = e.svc.OptIn(e.adminCtx, e.admin, job, ModePassphrase)
	if err != nil || st.OptedOut || st.Mode != ModePassphrase || st.OptOutReason != "" {
		t.Fatalf("opt-in = %+v, %v", st, err)
	}
	if _, err := e.svc.OptIn(e.adminCtx, e.admin, job, ModePassphrase); !errors.Is(err, ErrNotOptedOut) {
		t.Fatalf("second opt-in: %v", err)
	}
	// Setting a key while opted out opts in as well.
	if _, err := e.svc.OptOut(e.adminCtx, e.admin, job, "scratch database"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetRecipient(e.adminCtx, e.admin, job, testRecipient, ""); err != nil {
		t.Fatal(err)
	}
	if st, err := e.svc.Status(t.Context(), job); err != nil || st.Mode != ModeRecipient || st.OptedOut {
		t.Fatalf("status after setting a key = %+v, %v", st, err)
	}
	if got := e.audits(t, audit.ActionJobEncryptionOptedIn); len(got) != 2 {
		t.Fatalf("%d opted_in entries", len(got))
	}
}

// TestExportPassphrases: admin only with step-up, every version, audited
// and alerted.
func TestExportPassphrases(t *testing.T) {
	e := newEnv(t)
	job := e.newJob(t)
	if _, err := e.svc.ExportPassphrases(e.adminCtx, e.admin, job); !errors.Is(err, ErrNoPassphrases) {
		t.Fatalf("empty export: %v", err)
	}
	_, p1 := generate(t, e, job)
	_, p2 := generate(t, e, job)
	if _, err := e.svc.ExportPassphrases(e.maintCtx, e.maint, job); !isCode(err, apierror.Forbidden) {
		t.Fatalf("maintainer export: %v", err)
	}
	stale := authztest.Context(t.Context(), authztest.Stale(e.adminPr))
	if _, err := e.svc.ExportPassphrases(stale, e.admin, job); !isCode(err, apierror.StepUpRequired) {
		t.Fatalf("stale export: %v", err)
	}
	ex, err := e.svc.ExportPassphrases(e.adminCtx, e.admin, job)
	if err != nil {
		t.Fatal(err)
	}
	defer ex.Destroy()
	if len(ex.Versions) != 2 || string(ex.Versions[0].Passphrase.Bytes()) != p1 || string(ex.Versions[1].Passphrase.Bytes()) != p2 ||
		ex.Versions[0].RetiredAt == nil || ex.Versions[1].RetiredAt != nil {
		t.Fatal("export does not hold every version")
	}
	f := ex.File()
	defer f.Destroy()
	if !bytes.Contains(f.Bytes(), []byte("passphrase "+p1+"\n")) {
		t.Fatal("export file lacks version 1")
	}
	got := e.audits(t, audit.ActionJobPassphrasesExported)
	if len(got) != 1 || fmt.Sprint(got[0]["versions"]) != "[1 2]" {
		t.Fatalf("export audit = %v", got)
	}
	if a := e.alertsOf(AlertPassphraseExport); len(a) != 1 || a[0].JobID != job {
		t.Fatalf("export alerts = %v", a)
	}
}

// TestAppRoleCannotDeleteOrRewriteKeys: bekup_app can only retire keys.
func TestAppRoleCannotDeleteOrRewriteKeys(t *testing.T) {
	e := newEnv(t)
	job := e.newJob(t)
	generate(t, e, job)
	generate(t, e, job)
	if _, err := e.svc.SetRecipient(e.adminCtx, e.admin, job, testRecipient, ""); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"DELETE FROM job_passphrases",
		"DELETE FROM job_recipients",
		"DELETE FROM job_encryption",
		"TRUNCATE job_passphrases",
		"UPDATE job_passphrases SET sealed_passphrase = '\\x00'",
		"UPDATE job_passphrases SET version = version + 10",
		"UPDATE job_recipients SET recipient = recipient",
		// Reviving a retired key or moving its retirement time.
		"UPDATE job_passphrases SET retired_at = NULL WHERE version = 1",
		"UPDATE job_passphrases SET retired_at = now() WHERE version = 1",
	} {
		_, err := e.pool.Exec(t.Context(), stmt)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s: err = %v, want insufficient_privilege", stmt, err)
		}
	}
	list, err := e.svc.Passphrases(t.Context(), job)
	if err != nil || len(list) != 2 {
		t.Fatalf("passphrases = %v, %v", list, err)
	}
}

// TestInitTx: the job-creation path of task 3.5.
func TestInitTx(t *testing.T) {
	e := newEnv(t)
	job := e.newJob(t)
	if _, err := e.svc.KeyMaterialFor(t.Context(), job); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("unconfigured job: %v", err)
	}
	if st, err := e.svc.Status(t.Context(), job); err != nil || st.Configured {
		t.Fatalf("status = %+v, %v", st, err)
	}
	var res *InitResult
	err := db.Tx(t.Context(), e.pool, func(q *sqlc.Queries) error {
		var err error
		res, err = e.svc.InitTx(e.maintCtx, q, e.maint, job, Init{})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Destroy()
	if res.Passphrase == nil || res.Passphrase.Passphrase.Len() == 0 || res.Status.Mode != ModePassphrase || res.Status.Passphrase.Version != 1 {
		t.Fatalf("init = %+v", res)
	}
	err = db.Tx(t.Context(), e.pool, func(q *sqlc.Queries) error {
		_, err := e.svc.InitTx(e.maintCtx, q, e.maint, job, Init{})
		return err
	})
	if !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("second init: %v", err)
	}
	job2 := e.newJob(t)
	err = db.Tx(t.Context(), e.pool, func(q *sqlc.Queries) error {
		_, err := e.svc.InitTx(e.maintCtx, q, e.maint, job2, Init{Mode: ModeNone})
		return err
	})
	if err == nil {
		t.Fatal("a job was created opted out")
	}
	err = db.Tx(t.Context(), e.pool, func(q *sqlc.Queries) error {
		r, err := e.svc.InitTx(e.adminCtx, q, e.admin, job2, Init{Mode: ModeRecipient})
		if err == nil && (r.Keypair == nil || r.Keypair.Identity.Len() == 0 || r.Status.Mode != ModeRecipient) {
			err = fmt.Errorf("init = %+v", r)
		}
		r.Destroy()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// A rolled-back creation leaves nothing behind.
	job3 := e.newJob(t)
	errRollBack := errors.New("roll back")
	err = db.Tx(t.Context(), e.pool, func(q *sqlc.Queries) error {
		r, err := e.svc.InitTx(e.adminCtx, q, e.admin, job3, Init{Mode: ModeRecipient, Recipient: testRecipient})
		r.Destroy()
		if err != nil {
			return err
		}
		return errRollBack
	})
	if !errors.Is(err, errRollBack) {
		t.Fatal(err)
	}
	if st, err := e.svc.Status(t.Context(), job3); err != nil || st.Configured {
		t.Fatalf("rolled-back init left a configuration: %+v, %v", st, err)
	}
}

// TestKeyChangesAreAdminOnly (user decision, M3 review): maintainers can
// no longer set or generate keys or opt a job back in, and they cannot
// choose a key when they create a job; the default (a generated
// passphrase) still comes with job creation.
func TestKeyChangesAreAdminOnly(t *testing.T) {
	e := newEnv(t)
	job := e.newJob(t)
	initTx := func(ctx context.Context, actor Actor, j uuid.UUID, in Init) error {
		return db.Tx(ctx, e.pool, func(q *sqlc.Queries) error {
			r, err := e.svc.InitTx(ctx, q, actor, j, in)
			r.Destroy()
			return err
		})
	}
	for name, in := range map[string]Init{
		"chosen passphrase": {Passphrase: crypto.NewSecret([]byte("correct horse battery staple umbrella quartz"))},
		"recipient mode":    {Mode: ModeRecipient},
		"chosen recipient":  {Mode: ModeRecipient, Recipient: testRecipient},
	} {
		if err := initTx(e.maintCtx, e.maint, job, in); !isCode(err, apierror.Forbidden) {
			t.Fatalf("maintainer init with %s: %v", name, err)
		}
	}
	if err := initTx(e.maintCtx, e.maint, job, Init{}); err != nil {
		t.Fatalf("maintainer default init: %v", err)
	}
	checks := map[string]func() error{
		"SetPassphrase": func() error {
			_, err := e.svc.SetPassphrase(e.maintCtx, e.maint, job, crypto.NewSecret([]byte("correct horse battery staple umbrella quartz")))
			return err
		},
		"GeneratePassphrase": func() error {
			g, err := e.svc.GeneratePassphrase(e.maintCtx, e.maint, job)
			g.Destroy()
			return err
		},
		"SetRecipient": func() error { _, err := e.svc.SetRecipient(e.maintCtx, e.maint, job, testRecipient, ""); return err },
		"GenerateAgeKeypair": func() error {
			k, err := e.svc.GenerateAgeKeypair(e.maintCtx, e.maint, job, "")
			k.Destroy()
			return err
		},
		"OptIn": func() error { _, err := e.svc.OptIn(e.maintCtx, e.maint, job, ModePassphrase); return err },
	}
	for name, f := range checks {
		if err := f(); !isCode(err, apierror.Forbidden) {
			t.Errorf("maintainer %s: %v", name, err)
		}
	}
	// The service re-checks the role itself, whatever the policy says.
	allowAll := &Service{o: e.svc.o}
	allowAll.o.Authorizer = allowAllAuthorizer{}
	if _, err := allowAll.SetRecipient(e.maintCtx, e.maint, job, testRecipient, ""); !errors.Is(err, ErrAdminOnly) {
		t.Fatalf("service re-check: %v", err)
	}
	if _, err := e.svc.SetRecipient(e.adminCtx, e.admin, job, testRecipient, ""); err != nil {
		t.Fatalf("admin: %v", err)
	}
}

type allowAllAuthorizer struct{}

func (allowAllAuthorizer) Authorize(context.Context, authz.Operation, ...authz.Resource) error {
	return nil
}

// TestConcurrentChanges: concurrent generations get distinct versions and
// leave exactly one active key.
func TestConcurrentChanges(t *testing.T) {
	e := newEnv(t)
	job := e.newJob(t)
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, err := e.svc.GeneratePassphrase(e.adminCtx, e.admin, job)
			g.Destroy()
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	list, err := e.svc.Passphrases(t.Context(), job)
	if err != nil {
		t.Fatal(err)
	}
	active := 0
	for i, p := range list {
		if p.Version != i+1 {
			t.Fatalf("versions = %v", list)
		}
		if p.RetiredAt == nil {
			active++
		}
	}
	if len(list) != n || active != 1 {
		t.Fatalf("%d versions, %d active", len(list), active)
	}
}
