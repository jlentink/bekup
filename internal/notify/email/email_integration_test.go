//go:build integration

package email

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/netguard"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// mailpitImage is the digest-pinned image of docker-compose.dev.yml.
const mailpitImage = "axllent/mailpit:v1.32.0@sha256:0790692be935ea4420d085694f7985b1f692ca6a8771ab0e0bd8ba75ac180498"

const (
	relayUser     = "bekup"
	relayPassword = "relay-password-7f3c9a"
	testBase      = "https://bekup.test"
)

type mailpit struct {
	smtpPort int
	api      string
	caPEM    []byte
}

// mpOptions configure startMailpit.
type mpOptions struct {
	// starttls offers (and requires) STARTTLS with a certificate for
	// 127.0.0.1 from a fresh test CA and requires AUTH.
	starttls bool
	// implicit speaks TLS from the first byte (same certificate and AUTH).
	implicit bool
	// plainAuth requires AUTH without offering TLS at all.
	plainAuth bool
}

// startMailpit starts mailpit; withTLS: STARTTLS + AUTH (see mpOptions).
func startMailpit(t *testing.T, withTLS bool) *mailpit {
	t.Helper()
	return startMailpitWith(t, mpOptions{starttls: withTLS})
}

func startMailpitWith(t *testing.T, o mpOptions) *mailpit {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        mailpitImage,
		ExposedPorts: []string{"1025/tcp", "8025/tcp"},
		Env:          map[string]string{"MP_DATABASE": "/tmp/mailpit.db"},
		WaitingFor:   wait.ForHTTP("/livez").WithPort("8025/tcp").WithStartupTimeout(90 * time.Second),
	}
	mp := &mailpit{}
	if o.starttls || o.implicit {
		ca, cert := testPKI(t)
		mp.caPEM = ca
		keyDER, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
		if err != nil {
			t.Fatal(err)
		}
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
		req.Files = []testcontainers.ContainerFile{
			{Reader: bytes.NewReader(certPEM), ContainerFilePath: "/tmp/smtp.crt", FileMode: 0o644},
			{Reader: bytes.NewReader(keyPEM), ContainerFilePath: "/tmp/smtp.key", FileMode: 0o644},
		}
		req.Env["MP_SMTP_TLS_CERT"] = "/tmp/smtp.crt"
		req.Env["MP_SMTP_TLS_KEY"] = "/tmp/smtp.key"
		if o.implicit {
			req.Env["MP_SMTP_REQUIRE_TLS"] = "true"
		} else {
			req.Env["MP_SMTP_REQUIRE_STARTTLS"] = "true"
		}
		req.Env["MP_SMTP_AUTH"] = relayUser + ":" + relayPassword
	}
	if o.plainAuth {
		req.Env["MP_SMTP_AUTH"] = relayUser + ":" + relayPassword
		req.Env["MP_SMTP_AUTH_ALLOW_INSECURE"] = "true"
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start mailpit: %v", err)
	}
	sp, err := c.MappedPort(ctx, "1025/tcp")
	if err != nil {
		t.Fatal(err)
	}
	ap, err := c.MappedPort(ctx, "8025/tcp")
	if err != nil {
		t.Fatal(err)
	}
	mp.smtpPort, err = strconv.Atoi(sp.Port())
	if err != nil {
		t.Fatal(err)
	}
	mp.api = fmt.Sprintf("http://127.0.0.1:%s/api/v1", ap.Port())
	return mp
}

type mpMessage struct {
	ID      string
	Subject string
	From    struct{ Address, Name string }
	To      []struct{ Address string }
	Text    string
	HTML    string
}

// waitMessage polls mailpit for a message to `to` and returns it in full.
func (mp *mailpit) waitMessage(t *testing.T, to string) mpMessage {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var list struct{ Messages []mpMessage }
		mp.get(t, "/search?query="+url.QueryEscape("to:"+to), &list)
		if len(list.Messages) > 0 {
			var full mpMessage
			mp.get(t, "/message/"+list.Messages[0].ID, &full)
			return full
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no message to %s", to)
	return mpMessage{}
}

func (mp *mailpit) count(t *testing.T, to string) int {
	t.Helper()
	var list struct{ Messages []mpMessage }
	mp.get(t, "/search?query="+url.QueryEscape("to:"+to), &list)
	return len(list.Messages)
}

func (mp *mailpit) get(t *testing.T, path string, out any) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, mp.api+path, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { discard(resp.Body.Close()) }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mailpit %s: %d %s", path, resp.StatusCode, b)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}
}

// logBuffer collects log output (to assert that no secret is logged).
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type env struct {
	pool     *pgxpool.Pool
	d        dbtest.DB
	kr       *crypto.Keyring
	auditor  *audit.Auditor
	guard    *netguard.Guard
	logs     *logBuffer
	logger   *slog.Logger
	settings *Settings
	sender   *Sender
	admin    Actor
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	g, err := netguard.New(netguard.Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.1/32"}})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{pool: pool, d: d, kr: kr, guard: g, logs: &logBuffer{}, auditor: audit.New(pool, audit.Options{Registry: logging.NewRegistry()})}
	e.logger = slog.New(slog.NewJSONHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e.settings, err = NewSettings(SettingsOptions{DB: pool, Keyring: kr, Auditor: e.auditor, Guard: g, Logger: e.logger})
	if err != nil {
		t.Fatal(err)
	}
	e.sender, err = NewSender(SenderOptions{Settings: e.settings, Guard: g, HeloName: "bekup.test", Logger: e.logger})
	if err != nil {
		t.Fatal(err)
	}
	u, err := sqlc.New(pool).InsertUser(t.Context(), sqlc.InsertUserParams{Email: "admin@example.com", DisplayName: "A", Role: sqlc.UserRoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	e.admin = Actor{UserID: u.ID}
	return e
}

func (e *env) auditDetails(t *testing.T, action audit.Action) []string {
	t.Helper()
	page, err := e.auditor.List(t.Context(), audit.Filter{Action: action}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(page.Entries))
	for i := range page.Entries {
		out = append(out, string(page.Entries[i].Details))
	}
	return out
}

func (e *env) allAudit(t *testing.T) string {
	t.Helper()
	page, err := e.auditor.List(t.Context(), audit.Filter{}, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for i := range page.Entries {
		sb.Write(page.Entries[i].Details)
	}
	return sb.String()
}

func TestSMTPSendSTARTTLSWithCABundle(t *testing.T) {
	mp := startMailpit(t, true)
	e := newEnv(t)
	in := UpdateInput{
		Enabled: true, Host: "127.0.0.1", Port: mp.smtpPort, Mode: ModeSTARTTLS,
		Username: relayUser, Password: []byte(relayPassword), FromAddress: "bekup@example.com", FromName: "bekup <ops>",
		CABundlePEM: string(mp.caPEM),
	}
	v, err := e.settings.Update(t.Context(), e.admin, in)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Configured || !v.HasPassword || v.MayBeUnencrypted || v.Username != relayUser {
		t.Fatalf("view %+v", v)
	}
	// The password is sealed under settings:smtp:password.
	var sealed []byte
	if err := e.pool.QueryRow(t.Context(), "SELECT secret_enc FROM settings WHERE key = 'smtp'").Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(relayPassword)) {
		t.Fatal("password stored in plain text")
	}
	if pt, err := e.kr.Open(sealed, crypto.AAD{Table: "settings", RowID: "smtp", Field: "password"}); err != nil || string(pt) != relayPassword {
		t.Fatalf("open sealed password: %v", err)
	}
	if got := e.auditDetails(t, audit.ActionSettingChanged); len(got) != 1 || !strings.Contains(got[0], `"password"`) || !strings.Contains(got[0], `"host"`) {
		t.Fatalf("setting.changed %v", got)
	}
	if got := e.auditDetails(t, audit.ActionSecretChanged); len(got) != 1 || !strings.Contains(got[0], `"smtp"`) {
		t.Fatalf("secret.changed %v", got)
	}

	c, err := Render(Notice{
		Subject: "Backup failed: prod <db>", Heading: "Backup failed",
		Paragraphs: []string{"The job <b>prod</b> failed."},
		Action:     &Action{Label: "Open run", URL: testBase + "/runs/1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.sender.Send(t.Context(), NewMessage([]string{"ops@example.com"}, c)); err != nil {
		t.Fatal(err)
	}
	m := mp.waitMessage(t, "ops@example.com")
	if m.Subject != "Backup failed: prod <db>" || m.From.Address != "bekup@example.com" || m.From.Name != "bekup <ops>" {
		t.Fatalf("message %+v", m)
	}
	if !strings.Contains(m.Text, "The job <b>prod</b> failed.") || !strings.Contains(m.HTML, "The job &lt;b&gt;prod&lt;/b&gt; failed.") {
		t.Fatalf("bodies text=%q html=%q", m.Text, m.HTML)
	}

	// Without the CA bundle the self-signed relay is not trusted (system
	// roots). Changing the CA bundle needs the password again (M5 review
	// L3, D-198): refused without it, accepted with it.
	in.Password, in.CABundlePEM = nil, ""
	var fe *FieldError
	if _, err := e.settings.Update(t.Context(), e.admin, in); !errors.As(err, &fe) || fe.Field != "password" {
		t.Fatalf("CA bundle removed without the password: %v", err)
	}
	in.Password = []byte(relayPassword)
	if _, err := e.settings.Update(t.Context(), e.admin, in); err != nil {
		t.Fatal(err)
	}
	err = e.sender.Send(t.Context(), NewMessage([]string{"ops2@example.com"}, c))
	if codeOf(t, err) != CodeTLS {
		t.Fatalf("untrusted relay: %v", err)
	}

	// A wrong password is reported as auth_failed.
	in.CABundlePEM, in.Password = string(mp.caPEM), []byte("wrong-password")
	if _, err := e.settings.Update(t.Context(), e.admin, in); err != nil {
		t.Fatal(err)
	}
	if err := e.sender.Send(t.Context(), NewMessage([]string{"ops3@example.com"}, c)); codeOf(t, err) != CodeAuthFailed {
		t.Fatalf("wrong password: %v", err)
	}

	for _, s := range []string{relayPassword, "wrong-password"} {
		if strings.Contains(e.logs.String(), s) || strings.Contains(e.allAudit(t), s) {
			t.Fatalf("password in logs or audit")
		}
	}
}

// TestSMTPModesPlainServer: a relay without any TLS that requires AUTH
// (D-180): auto and none send unencrypted (AUTH included), starttls is
// refused before AUTH.
func TestSMTPModesPlainServer(t *testing.T) {
	mp := startMailpitWith(t, mpOptions{plainAuth: true})
	e := newEnv(t)
	in := UpdateInput{
		Enabled: true, Host: "127.0.0.1", Port: mp.smtpPort, Mode: ModeAuto,
		Username: relayUser, Password: []byte(relayPassword), FromAddress: "bekup@example.com",
	}
	v, err := e.settings.Update(t.Context(), e.admin, in)
	if err != nil {
		t.Fatal(err)
	}
	if !v.MayBeUnencrypted || v.Mode != ModeAuto {
		t.Fatalf("view %+v", v)
	}
	if !strings.Contains(e.logs.String(), "smtp_may_be_unencrypted") {
		t.Fatal("no warning on save")
	}
	if got := e.auditDetails(t, audit.ActionSettingChanged); len(got) != 1 ||
		!strings.Contains(got[0], `"mode": "auto"`) || !strings.Contains(got[0], `"may_be_unencrypted": true`) {
		t.Fatalf("setting.changed %v", got)
	}
	tm, err := NewTestMailer(TestMailerOptions{Sender: e.sender, Auditor: e.auditor})
	if err != nil {
		t.Fatal(err)
	}
	d, err := tm.SendTest(t.Context(), e.admin, "auto@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if d.Encrypted || d.TLSVersion != "" {
		t.Fatalf("delivery %+v", d)
	}
	if m := mp.waitMessage(t, "auto@example.com"); m.Subject != "bekup test e-mail" {
		t.Fatalf("message %+v", m)
	}
	if got := e.auditDetails(t, audit.ActionSMTPTestSent); len(got) != 1 || !strings.Contains(got[0], `"encrypted": false`) {
		t.Fatalf("smtp.test_sent %v", got)
	}
	// Once per process and server.
	if err := e.sender.Send(t.Context(), &Message{To: []string{"auto2@example.com"}, Subject: "s", Text: "t"}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(e.logs.String(), "smtp_unencrypted_session"); n != 1 {
		t.Fatalf("unencrypted warnings %d", n)
	}

	// AUTH really happens: a wrong password is refused.
	in.Password = []byte("wrong-password")
	if _, err := e.settings.Update(t.Context(), e.admin, in); err != nil {
		t.Fatal(err)
	}
	if err := e.sender.Send(t.Context(), &Message{To: []string{"x@example.com"}, Subject: "s", Text: "t"}); codeOf(t, err) != CodeAuthFailed {
		t.Fatalf("wrong password: %v", err)
	}

	// none: plain text as well.
	in.Mode, in.Password = ModeNone, []byte(relayPassword)
	if _, err := e.settings.Update(t.Context(), e.admin, in); err != nil {
		t.Fatal(err)
	}
	if d, err := e.sender.SendDelivery(t.Context(), &Message{To: []string{"none@example.com"}, Subject: "s", Text: "t"}); err != nil || d.Encrypted {
		t.Fatalf("none: %+v %v", d, err)
	}
	mp.waitMessage(t, "none@example.com")

	// starttls: refused before AUTH or MAIL.
	in.Mode, in.Password = ModeSTARTTLS, nil
	if _, err := e.settings.Update(t.Context(), e.admin, in); err != nil {
		t.Fatal(err)
	}
	if err := e.sender.Send(t.Context(), &Message{To: []string{"strict@example.com"}, Subject: "s", Text: "t"}); codeOf(t, err) != CodeNoSTARTTLS {
		t.Fatalf("starttls: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if mp.count(t, "strict@example.com") != 0 {
		t.Fatal("delivered without STARTTLS")
	}

	for _, s := range []string{relayPassword, "wrong-password"} {
		if strings.Contains(e.logs.String(), s) || strings.Contains(e.allAudit(t), s) {
			t.Fatal("password in logs or audit")
		}
	}
}

// TestSMTPAutoUsesSTARTTLS: auto upgrades when the server offers STARTTLS
// (verified against the CA bundle).
func TestSMTPAutoUsesSTARTTLS(t *testing.T) {
	mp := startMailpit(t, true)
	e := newEnv(t)
	in := UpdateInput{
		Enabled: true, Host: "127.0.0.1", Port: mp.smtpPort, Mode: ModeAuto,
		Username: relayUser, Password: []byte(relayPassword), FromAddress: "bekup@example.com",
		CABundlePEM: string(mp.caPEM),
	}
	if _, err := e.settings.Update(t.Context(), e.admin, in); err != nil {
		t.Fatal(err)
	}
	tm, err := NewTestMailer(TestMailerOptions{Sender: e.sender, Auditor: e.auditor})
	if err != nil {
		t.Fatal(err)
	}
	d, err := tm.SendTest(t.Context(), e.admin, "tls@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Encrypted || !strings.HasPrefix(d.TLSVersion, "TLS 1.") {
		t.Fatalf("delivery %+v", d)
	}
	mp.waitMessage(t, "tls@example.com")
	if strings.Contains(e.logs.String(), "smtp_unencrypted_session") {
		t.Fatal("unencrypted warning for a TLS session")
	}
	if got := e.auditDetails(t, audit.ActionSMTPTestSent); len(got) != 1 || !strings.Contains(got[0], `"encrypted": true`) {
		t.Fatalf("smtp.test_sent %v", got)
	}
	// Without the bundle the certificate is not trusted: an error, never a
	// fallback to plain text. (Changing the bundle needs the password.)
	in.Password, in.CABundlePEM = []byte(relayPassword), ""
	if _, err := e.settings.Update(t.Context(), e.admin, in); err != nil {
		t.Fatal(err)
	}
	if err := e.sender.Send(t.Context(), &Message{To: []string{"tls2@example.com"}, Subject: "s", Text: "t"}); codeOf(t, err) != CodeTLS {
		t.Fatalf("untrusted: %v", err)
	}
}

func TestSMTPImplicitTLS(t *testing.T) {
	mp := startMailpitWith(t, mpOptions{implicit: true})
	e := newEnv(t)
	if _, err := e.settings.Update(t.Context(), e.admin, UpdateInput{
		Enabled: true, Host: "127.0.0.1", Port: mp.smtpPort, Mode: ModeImplicitTLS,
		Username: relayUser, Password: []byte(relayPassword), FromAddress: "bekup@example.com",
		CABundlePEM: string(mp.caPEM),
	}); err != nil {
		t.Fatal(err)
	}
	d, err := e.sender.SendDelivery(t.Context(), &Message{To: []string{"smtps@example.com"}, Subject: "s", Text: "t"})
	if err != nil || !d.Encrypted || d.TLSVersion == "" {
		t.Fatalf("implicit TLS: %+v %v", d, err)
	}
	mp.waitMessage(t, "smtps@example.com")
}

// TestSMTPLegacyPlaintextMode: settings stored with the 5.1 dev-only mode
// are read as none (D-180).
func TestSMTPLegacyPlaintextMode(t *testing.T) {
	e := newEnv(t)
	val := `{"enabled":true,"host":"127.0.0.1","port":1025,"mode":"plaintext","from_address":"bekup@example.com","connect_timeout_seconds":10,"send_timeout_seconds":60}`
	if _, err := sqlc.New(e.pool).PutSetting(t.Context(), sqlc.PutSettingParams{Key: SettingKey, Value: []byte(val), UpdatedBy: &e.admin.UserID}); err != nil {
		t.Fatal(err)
	}
	v, err := e.settings.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if v.Mode != ModeNone || !v.MayBeUnencrypted {
		t.Fatalf("view %+v", v)
	}
}

func TestSMTPGuardBlocksPrivateHost(t *testing.T) {
	mp := startMailpit(t, false)
	e := newEnv(t)
	strict, err := netguard.New(netguard.Options{LoopbackAllowable: true}) // dev mode, nothing allowlisted
	if err != nil {
		t.Fatal(err)
	}
	settings, err := NewSettings(SettingsOptions{DB: e.pool, Keyring: e.kr, Auditor: e.auditor, Guard: strict})
	if err != nil {
		t.Fatal(err)
	}
	in := UpdateInput{Enabled: true, Host: "127.0.0.1", Port: mp.smtpPort, Mode: ModeNone, FromAddress: "bekup@example.com"}
	var fe *FieldError
	if _, err := settings.Update(t.Context(), e.admin, in); !errors.As(err, &fe) || fe.Field != "host" || !strings.Contains(fe.Message, "allowlist") {
		t.Fatalf("save: %v", err)
	}
	// Saved through an allowlisting guard, a sender with the strict guard
	// still refuses at dial time.
	if _, err := e.settings.Update(t.Context(), e.admin, in); err != nil {
		t.Fatal(err)
	}
	s, err := NewSender(SenderOptions{Settings: e.settings, Guard: strict})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Send(t.Context(), &Message{To: []string{"blocked@example.com"}, Subject: "s", Text: "t"})
	if codeOf(t, err) != CodeNotAllowed {
		t.Fatalf("send: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if mp.count(t, "blocked@example.com") != 0 {
		t.Fatal("message delivered to a blocked host")
	}
}

func TestEmailVerificationFlow(t *testing.T) {
	mp := startMailpit(t, false)
	e := newEnv(t)
	if _, err := e.settings.Update(t.Context(), e.admin, UpdateInput{
		Enabled: true, Host: "127.0.0.1", Port: mp.smtpPort, Mode: ModeNone, FromAddress: "bekup@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifier(VerifierOptions{
		DB: e.pool, Keyring: e.kr, Auditor: e.auditor, Mailer: e.sender, BaseURL: testBase + "/",
		Limiter: func(string) (bool, time.Duration) { return true, 0 }, Logger: e.logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	q := sqlc.New(e.pool)
	u, err := q.InsertUser(t.Context(), sqlc.InsertUserParams{Email: "pat@example.com", DisplayName: "Pat", Role: sqlc.UserRoleUser})
	if err != nil {
		t.Fatal(err)
	}
	other, err := q.InsertUser(t.Context(), sqlc.InsertUserParams{Email: "eve@example.com", DisplayName: "Eve", Role: sqlc.UserRoleUser})
	if err != nil {
		t.Fatal(err)
	}
	pat, eve := Actor{UserID: u.ID}, Actor{UserID: other.ID}

	if r, err := v.IsVerified(t.Context(), u.ID); err != nil || r.Verified || r.Email != "pat@example.com" {
		t.Fatalf("before: %+v %v", r, err)
	}
	res, err := v.Request(t.Context(), pat)
	if err != nil {
		t.Fatal(err)
	}
	if res.SentTo != "pat@example.com" || time.Until(res.ExpiresAt) < 23*time.Hour {
		t.Fatalf("request %+v", res)
	}
	if _, err := v.Request(t.Context(), pat); !errors.Is(err, ErrCooldown) {
		t.Fatalf("second request: %v", err)
	}
	code := linkCode(t, mp.waitMessage(t, "pat@example.com"))
	// The database holds only the MAC.
	var mac []byte
	if err := e.pool.QueryRow(t.Context(), "SELECT code_mac FROM email_verifications WHERE user_id = $1", u.ID).Scan(&mac); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mac), code) {
		t.Fatal("code stored")
	}

	// Another account cannot redeem it; a wrong code is refused.
	if err := v.Verify(t.Context(), eve, code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("other account: %v", err)
	}
	if err := v.Verify(t.Context(), pat, code[:len(code)-2]+"AA"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("wrong code: %v", err)
	}
	if err := v.Verify(t.Context(), pat, code); err != nil {
		t.Fatal(err)
	}
	if r, err := v.IsVerified(t.Context(), u.ID); err != nil || !r.Verified {
		t.Fatalf("after: %+v %v", r, err)
	}
	// Single use.
	if err := v.Verify(t.Context(), pat, code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("reuse: %v", err)
	}
	if _, err := v.Request(t.Context(), pat); !errors.Is(err, ErrAlreadyVerified) {
		t.Fatalf("request when verified: %v", err)
	}
	if n := len(e.auditDetails(t, audit.ActionUserEmailVerified)); n != 1 {
		t.Fatalf("verified audits %d", n)
	}
	if n := len(e.auditDetails(t, audit.ActionUserEmailVerificationFailed)); n != 3 {
		t.Fatalf("failure audits %d", n)
	}

	// An address change clears the verification (users trigger) and voids
	// pending links for the old address.
	if _, err := e.pool.Exec(t.Context(), "UPDATE users SET email = 'pat@example.org' WHERE id = $1", u.ID); err != nil {
		t.Fatal(err)
	}
	if r, err := v.IsVerified(t.Context(), u.ID); err != nil || r.Verified || r.Email != "pat@example.org" {
		t.Fatalf("after address change: %+v", r)
	}
	if _, err := v.Request(t.Context(), pat); err != nil {
		t.Fatal(err)
	}
	code2 := linkCode(t, mp.waitMessage(t, "pat@example.org"))
	if _, err := e.pool.Exec(t.Context(), "UPDATE users SET email = 'pat@example.net' WHERE id = $1", u.ID); err != nil {
		t.Fatal(err)
	}
	if err := v.Verify(t.Context(), pat, code2); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("code for an old address: %v", err)
	}

	// Expiry.
	if _, err := v.Request(t.Context(), pat); err != nil {
		t.Fatal(err)
	}
	code3 := linkCode(t, mp.waitMessage(t, "pat@example.net"))
	if err := e.d.SuperExec(t.Context(), fmt.Sprintf(
		"UPDATE email_verifications SET created_at = now() - interval '2 days', expires_at = now() - interval '1 day' WHERE user_id = '%s'", u.ID)); err != nil {
		t.Fatal(err)
	}
	if err := v.Verify(t.Context(), pat, code3); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("expired: %v", err)
	}
	if r, err := v.IsVerified(t.Context(), u.ID); err != nil || r.Verified {
		t.Fatal("verified with an expired code")
	}

	// A link that cannot be delivered is voided (and lifts the cooldown).
	if _, err := e.settings.Update(t.Context(), e.admin, UpdateInput{
		Enabled: false, Host: "127.0.0.1", Port: mp.smtpPort, Mode: ModeNone, FromAddress: "bekup@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Request(t.Context(), eve); codeOf(t, err) != CodeNotConfigured {
		t.Fatalf("request without SMTP: %v", err)
	}
	var n int
	if err := e.pool.QueryRow(t.Context(), "SELECT count(*) FROM email_verifications WHERE user_id = $1", other.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("undelivered link kept: %d %v", n, err)
	}
}

// linkCode extracts the code from the verification link in the text part.
func linkCode(t *testing.T, m mpMessage) string {
	t.Helper()
	prefix := testBase + VerificationPath + "#code="
	i := strings.Index(m.Text, prefix)
	if i < 0 {
		t.Fatalf("no link in %q", m.Text)
	}
	rest := m.Text[i+len(prefix):]
	if j := strings.IndexAny(rest, "\r\n "); j >= 0 {
		rest = rest[:j]
	}
	if len(rest) != 43 {
		t.Fatalf("code %q", rest)
	}
	if !strings.Contains(m.HTML, prefix+rest) {
		t.Fatal("HTML link differs")
	}
	return rest
}
