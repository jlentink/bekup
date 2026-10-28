package jobkeys

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/serviceutil"
)

const testRecipient = "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"

func TestValidatePassphrase(t *testing.T) {
	gen, err := crypto.GeneratePassphrase(crypto.MinPassphraseWords)
	if err != nil {
		t.Fatal(err)
	}
	defer gen.Destroy()
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{"generated", string(gen.Bytes()), true},
		{"strong", "correct horse battery staple umbrella quartz", true},
		{"short", "Tr0ub4dor&3x", false},
		{"weak but long", strings.Repeat("a", 30), false},
		{"control", "correct horse battery\tstaple umbrella quartz", false},
		{"leading space", " correct horse battery staple umbrella quartz", false},
		{"invalid utf8", "correct horse battery staple umbrella \xff\xfe", false},
		{"too long", strings.Repeat("abcdefgh ", 120), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePassphrase([]byte(tt.in))
			if (err == nil) != tt.ok {
				t.Fatalf("validatePassphrase = %v, want ok=%v", err, tt.ok)
			}
			var fe *serviceutil.FieldError
			if err != nil {
				if !errors.As(err, &fe) || fe.Field != "passphrase" {
					t.Fatalf("error %v is not a passphrase field error", err)
				}
				if strings.Contains(err.Error(), tt.in) {
					t.Fatal("error message contains the passphrase")
				}
			}
		})
	}
}

func TestParseRecipient(t *testing.T) {
	kp, err := crypto.GenerateAgeKeypair()
	if err != nil {
		t.Fatal(err)
	}
	defer kp.Identity.Destroy()
	tests := []struct {
		name, in, want string
	}{
		{"fixed", testRecipient, testRecipient},
		{"generated", kp.Recipient, kp.Recipient},
		{"spaces", "  " + testRecipient + "\n", testRecipient},
		{"empty", "", ""},
		{"identity", string(kp.Identity.Bytes()), ""},
		{"ssh", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl", ""},
		{"bad checksum", testRecipient[:len(testRecipient)-1] + "q", ""},
		{"too long", testRecipient + strings.Repeat("q", 100), ""},
		{"plugin", "age1yubikey1qwerty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, fp, err := parseRecipient(tt.in)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("parseRecipient(%q) accepted", tt.in)
				}
				if strings.Contains(err.Error(), "AGE-SECRET-KEY") {
					t.Fatal("error echoes the identity")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("canonical = %q, want %q", got, tt.want)
			}
			if fp != pipeline.RecipientFingerprint(tt.want) || fp != Fingerprint(tt.want) {
				t.Fatalf("fingerprint %q does not match the manifest fingerprint", fp)
			}
		})
	}
}

func FuzzParseRecipient(f *testing.F) {
	f.Add(testRecipient)
	f.Add("age1")
	f.Add("AGE-SECRET-KEY-1QQQ")
	f.Fuzz(func(t *testing.T, s string) {
		got, fp, err := parseRecipient(s)
		if err != nil {
			return
		}
		if !recipientRe.MatchString(got) || len(fp) != 64 {
			t.Fatalf("accepted %q as %q / %q", s, got, fp)
		}
	})
}

func TestCleanText(t *testing.T) {
	if got, err := cleanText("label", "  ops laptop  ", 0, MaxLabelRunes); err != nil || got != "ops laptop" {
		t.Fatalf("cleanText = %q, %v", got, err)
	}
	if _, err := cleanText("reason", "   ", 1, MaxReasonRunes); err == nil {
		t.Fatal("empty reason accepted")
	}
	if _, err := cleanText("reason", "a\x00b", 1, MaxReasonRunes); err == nil {
		t.Fatal("control character accepted")
	}
	if _, err := cleanText("label", strings.Repeat("é", MaxLabelRunes+1), 0, MaxLabelRunes); err == nil {
		t.Fatal("long label accepted")
	}
}

func TestPassphraseAAD(t *testing.T) {
	job := uuid.MustParse("6f1b8f1e-3c2a-4c1e-9d55-0a4b8f9b2c11")
	a := passphraseAAD(job, 7)
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if got, want := a.String(), "job_passphrases:"+job.String()+":v7"; got != want {
		t.Fatalf("AAD = %q, want %q", got, want)
	}
}

func TestExportFile(t *testing.T) {
	job := uuid.New()
	created := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	retired := created.Add(time.Hour)
	ex := &Export{JobID: job, ExportedAt: created.Add(2 * time.Hour), Versions: []ExportedPassphrase{
		{PassphraseInfo{Version: 1, CreatedAt: created, RetiredAt: &retired}, crypto.NewSecret([]byte("old words here"))},
		{PassphraseInfo{Version: 2, CreatedAt: retired}, crypto.NewSecret([]byte("new words here"))},
	}}
	f := ex.File()
	defer f.Destroy()
	want := exportHeader + "\n" +
		"# bekup job passphrases for offline recovery. SECRET: anyone with this file can decrypt\n" +
		"# the job's passphrase-encrypted backups. Keep it offline; see docs/guides/encryption.md.\n" +
		"job " + job.String() + "\n" +
		"exported 2026-10-01T14:00:00Z\n" +
		"version 1 created 2026-10-01T12:00:00Z retired 2026-10-01T13:00:00Z\n" +
		"passphrase old words here\n" +
		"version 2 created 2026-10-01T13:00:00Z\n" +
		"passphrase new words here\n"
	if string(f.Bytes()) != want {
		t.Fatalf("export file:\n%s\nwant:\n%s", f.Bytes(), want)
	}
	if !strings.HasSuffix(ex.FileName(), "-passphrases.txt") {
		t.Fatal(ex.FileName())
	}
	ex.Destroy()
	for _, v := range ex.Versions {
		if v.Passphrase.Len() != 0 {
			t.Fatal("Destroy left a passphrase")
		}
	}
}

func TestIdentityFileParses(t *testing.T) {
	kp, err := crypto.GenerateAgeKeypair()
	if err != nil {
		t.Fatal(err)
	}
	defer kp.Identity.Destroy()
	f := identityFile(uuid.New(), RecipientInfo{Version: 3, Recipient: kp.Recipient}, kp.Identity.Bytes(), time.Now())
	defer f.Destroy()
	var ct bytes.Buffer
	w, err := crypto.EncryptToRecipients(&ct, kp.Recipient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("dump")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := crypto.DecryptWithIdentities(&ct, f.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(r); err != nil || out.String() != "dump" {
		t.Fatalf("round trip = %q, %v", out.String(), err)
	}
	if !bytes.Contains(f.Bytes(), []byte("# public key: "+kp.Recipient)) {
		t.Fatal("identity file lacks the public key comment")
	}
}

func TestKeyMaterialPipeline(t *testing.T) {
	km := &KeyMaterial{Mode: ModePassphrase, Passphrase: crypto.NewSecret([]byte("p p p p")), PassphraseVersion: 4}
	enc, err := km.Pipeline()
	if err != nil {
		t.Fatal(err)
	}
	if enc.Mode != pipeline.EncryptPassphrase || enc.PassphraseVersion != 4 || string(enc.Passphrase.Bytes()) != "p p p p" {
		t.Fatalf("passphrase encryption = %v", enc)
	}
	if km.Passphrase != nil {
		t.Fatal("the passphrase did not move to the pipeline configuration")
	}
	enc.Passphrase.Destroy()

	enc, err = (&KeyMaterial{Mode: ModeRecipient, Recipients: []string{testRecipient}, RecipientVersion: 1}).Pipeline()
	if err != nil || enc.Mode != pipeline.EncryptRecipients || len(enc.Recipients) != 1 || enc.RecipientVersion != 1 {
		t.Fatalf("recipient encryption = %v, %v", enc, err)
	}
	enc, err = (&KeyMaterial{Mode: ModeNone}).Pipeline()
	if err != nil || enc.Mode != pipeline.EncryptNone || !enc.OptedOut {
		t.Fatalf("opted-out encryption = %v, %v", enc, err)
	}
	if _, err := (&KeyMaterial{Mode: ModePassphrase}).Pipeline(); !errors.Is(err, pipeline.ErrEncryptionConfig) {
		t.Fatalf("empty passphrase: %v", err)
	}
	if _, err := (&KeyMaterial{Mode: "bogus"}).Pipeline(); !errors.Is(err, pipeline.ErrEncryptionConfig) {
		t.Fatalf("unknown mode: %v", err)
	}
	var nilKM *KeyMaterial
	if _, err := nilKM.Pipeline(); err == nil {
		t.Fatal("nil key material accepted")
	}
	nilKM.Destroy()
}

type denyAll struct{}

func (denyAll) Authorize(context.Context, authz.Operation, ...authz.Resource) error {
	return errors.New("denied")
}

func TestNewValidates(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("New accepted empty options")
	}
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer kr.Destroy()
	base := Options{DB: nilDB{}, Keyring: kr, Auditor: &audit.Auditor{}, Authorizer: denyAll{}}
	o := base
	o.PassphraseWords = 3
	if _, err := New(o); err == nil {
		t.Fatal("New accepted 3-word passphrases")
	}
	if _, err := New(base); err != nil {
		t.Fatal(err)
	}
}

// TestChangesAreAuthorized: every change is refused when the authorizer
// denies, before touching the database (nilDB panics on use).
func TestChangesAreAuthorized(t *testing.T) {
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer kr.Destroy()
	s, err := New(Options{DB: nilDB{}, Keyring: kr, Auditor: &audit.Auditor{}, Authorizer: denyAll{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, job, actor := t.Context(), uuid.New(), Actor{UserID: uuid.New()}
	pass := crypto.NewSecret([]byte("correct horse battery staple umbrella quartz"))
	calls := map[string]func() error{
		"SetPassphrase": func() error { _, err := s.SetPassphrase(ctx, actor, job, pass); return err },
		"GeneratePassphrase": func() error {
			g, err := s.GeneratePassphrase(ctx, actor, job)
			g.Destroy()
			return err
		},
		"SetRecipient": func() error { _, err := s.SetRecipient(ctx, actor, job, testRecipient, ""); return err },
		"GenerateAgeKeypair": func() error {
			g, err := s.GenerateAgeKeypair(ctx, actor, job, "")
			g.Destroy()
			return err
		},
		"OptOut": func() error { _, err := s.OptOut(ctx, actor, job, "test"); return err },
		"OptIn":  func() error { _, err := s.OptIn(ctx, actor, job, ModePassphrase); return err },
		"ExportPassphrases": func() error {
			e, err := s.ExportPassphrases(ctx, actor, job)
			e.Destroy()
			return err
		},
		"InitTx": func() error {
			r, err := s.InitTx(ctx, nil, actor, job, Init{})
			r.Destroy()
			return err
		},
	}
	for name, call := range calls {
		if err := call(); err == nil || err.Error() != "denied" {
			t.Errorf("%s: err = %v, want the authorizer's denial", name, err)
		}
	}
	if pass.Len() != 0 {
		t.Fatal("SetPassphrase did not destroy the passphrase it was given")
	}
}

// nilDB fails the test run on any database use.
type nilDB struct{}

func (nilDB) Begin(context.Context) (pgx.Tx, error) { panic("database used") }
func (nilDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	panic("database used")
}
func (nilDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) { panic("database used") }
func (nilDB) QueryRow(context.Context, string, ...interface{}) pgx.Row        { panic("database used") }

type allowAll struct{}

func (allowAll) Authorize(context.Context, authz.Operation, ...authz.Resource) error { return nil }

// TestActorMustBeCaller: an actor other than the authenticated caller is
// refused even when the policy allows the operation.
func TestActorMustBeCaller(t *testing.T) {
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer kr.Destroy()
	s, err := New(Options{DB: nilDB{}, Keyring: kr, Auditor: &audit.Auditor{}, Authorizer: allowAll{}})
	if err != nil {
		t.Fatal(err)
	}
	pr := authztest.Principal(auth.RoleAdmin)
	for name, ctx := range map[string]context.Context{
		"no principal":    t.Context(),
		"other principal": authztest.Context(t.Context(), pr),
	} {
		if _, err := s.OptOut(ctx, Actor{UserID: uuid.New()}, uuid.New(), "reason"); !errors.Is(err, ErrActorMismatch) {
			t.Errorf("%s: err = %v, want ErrActorMismatch", name, err)
		}
	}
	if _, err := s.OptOut(authztest.Context(t.Context(), pr), Actor{}, uuid.New(), "reason"); !errors.Is(err, ErrActorMismatch) {
		t.Errorf("nil actor: err = %v", err)
	}
}
