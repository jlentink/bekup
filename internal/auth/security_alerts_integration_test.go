//go:build integration

// External test package: internal/notify imports internal/auth, so the
// end-to-end security alert tests (task 5.5) live outside package auth.
package auth_test

import (
	"encoding/base32"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify"
)

// TestRecoveryCodeUseAlertsTheAccount: using a recovery code writes a
// security.account event for exactly that account, through serve's audit
// observer, in the audit entry's transaction.
func TestRecoveryCodeUseAlertsTheAccount(t *testing.T) {
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	ctx := auth.WithRequestMeta(t.Context(), auth.RequestMeta{IP: netip.MustParseAddr("192.0.2.10")})
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	auditor := audit.New(pool, audit.Options{Observer: notify.ObserveAudit})
	factors := auth.NewSecondFactors()
	svc, err := auth.NewTOTP(auth.TOTPOptions{DB: pool, Keyring: kr, Auditor: auditor, Factors: factors})
	if err != nil {
		t.Fatal(err)
	}
	factors.Register(svc)
	q := sqlc.New(pool)
	u, err := q.InsertUser(ctx, sqlc.InsertUserParams{Email: "ivy@example.com", DisplayName: "Ivy", Role: sqlc.UserRoleUser})
	if err != nil {
		t.Fatal(err)
	}
	other, err := q.InsertUser(ctx, sqlc.InsertUserParams{Email: "jo@example.com", DisplayName: "Jo", Role: sqlc.UserRoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	p := auth.Principal{UserID: u.ID, Role: auth.Role(u.Role), SessionID: uuid.New(), AAL: auth.AAL1, StepUpAt: time.Now()}
	en, err := svc.BeginTOTPEnrollment(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(en.ManualKey)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCodeCustom(base32.StdEncoding.EncodeToString(secret), time.Now(), totp.ValidateOpts{
		Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatal(err)
	}
	codes, err := svc.ConfirmTOTPEnrollment(ctx, p, code)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.UseRecoveryCode(ctx, u.ID, codes[0]); err != nil {
		t.Fatal(err)
	}
	count := func(user uuid.UUID, kind string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_events
			WHERE type = 'security.account' AND user_id = $1 AND detail->>'kind' = $2`, user, kind).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(u.ID, notify.KindRecoveryCodeUsed) != 1 || count(u.ID, notify.KindSecondFactorAdded) != 1 {
		t.Fatal("the recovery code use was not alerted to its account")
	}
	if count(other.ID, notify.KindRecoveryCodeUsed) != 0 {
		t.Fatal("another account was alerted")
	}
	// The event carries no code.
	var detail string
	if err := pool.QueryRow(ctx, `SELECT detail::text FROM notification_events WHERE detail->>'kind' = $1`, notify.KindRecoveryCodeUsed).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	for _, c := range codes {
		if strings.Contains(detail, c) {
			t.Fatal("a recovery code reached the event")
		}
	}
}

// TestFailedLoginBurstAlertsAdmins: repeated failed sign-ins from one
// address raise one security.system alert per window that names the
// address and the count, never an e-mail address.
func TestFailedLoginBurstAlertsAdmins(t *testing.T) {
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	ctx := t.Context()
	h, err := auth.NewHasher(auth.HasherOptions{Params: auth.Argon2Params{MemoryKiB: 8, Iterations: 1, Parallelism: 1}, MaxConcurrent: 4, WeakForTests: true})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.NewService(auth.ServiceOptions{
		DB: pool, Hasher: h, Sessions: auth.NewSessions(pool, auth.SessionOptions{}),
		Auditor: audit.New(pool, audit.Options{Observer: notify.ObserveAudit}), Factors: auth.NewSecondFactors(),
		Policy:       auth.NewPasswordPolicy(12, 3),
		LoginLimiter: func(netip.Addr) (bool, time.Duration) { return true, 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := auth.RequestMeta{IP: netip.MustParseAddr("203.0.113.77")}
	for i := range notify.BurstPerAddress + 5 {
		_, err := svc.Login(ctx, fmt.Sprintf("nobody-%d@example.com", i), "wrong-password-value", meta, uuid.Nil)
		if !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("login %d: %v", i, err)
		}
	}
	rows, err := pool.Query(ctx, `SELECT detail::text, severity FROM notification_events
		WHERE type = 'security.system' AND detail->>'kind' = $1`, notify.KindLoginBurstAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var det, sev string
		if err := rows.Scan(&det, &sev); err != nil {
			t.Fatal(err)
		}
		if sev != string(notify.SeverityError) {
			t.Fatalf("severity %s", sev)
		}
		details = append(details, det)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// One alert even if the window boundary is crossed mid-test: at most
	// two buckets.
	if len(details) < 1 || len(details) > 2 {
		t.Fatalf("%d burst alerts: %v", len(details), details)
	}
	if !strings.Contains(details[0], "203.0.113.77") || strings.Contains(details[0], "@") {
		t.Fatalf("burst alert detail %s", details[0])
	}
}
