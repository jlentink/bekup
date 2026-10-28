package restore

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/serviceutil"
)

func fieldOfErr(err error) string {
	if fe, ok := err.(*serviceutil.FieldError); ok { //nolint:errorlint // validateInput returns the type itself
		return fe.Field
	}
	return ""
}

func TestValidateInput(t *testing.T) {
	ok := RequestInput{ArtifactID: uuid.New(), TargetConnectionID: uuid.New(), Mode: ModeNewDB, TargetDatabase: "app_copy"}
	if err := validateInput(&ok); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		mod   func(*RequestInput)
		field string
	}{
		"mode":           {func(in *RequestInput) { in.Mode = "merge" }, "mode"},
		"no artifact":    {func(in *RequestInput) { in.ArtifactID = uuid.Nil }, "artifactId"},
		"no target":      {func(in *RequestInput) { in.TargetConnectionID = uuid.Nil }, "targetConnectionId"},
		"empty name":     {func(in *RequestInput) { in.TargetDatabase = "" }, "targetDatabase"},
		"quote":          {func(in *RequestInput) { in.TargetDatabase = `a"b` }, "targetDatabase"},
		"semicolon":      {func(in *RequestInput) { in.TargetDatabase = "a;drop" }, "targetDatabase"},
		"leading dash":   {func(in *RequestInput) { in.TargetDatabase = "-a" }, "targetDatabase"},
		"too long":       {func(in *RequestInput) { in.TargetDatabase = strings.Repeat("a", 64) }, "targetDatabase"},
		"temp marker":    {func(in *RequestInput) { in.TargetDatabase = "app__bekup_restore_0123" }, "targetDatabase"},
		"old marker":     {func(in *RequestInput) { in.TargetDatabase = "x__bekup_old_1" }, "targetDatabase"},
		"newline":        {func(in *RequestInput) { in.TargetDatabase = "a\nb" }, "targetDatabase"},
		"unicode letter": {func(in *RequestInput) { in.TargetDatabase = "dätabase" }, "targetDatabase"},
	}
	for name, c := range cases {
		in := ok
		c.mod(&in)
		if got := fieldOfErr(validateInput(&in)); got != c.field {
			t.Errorf("%s: field %q, want %q", name, got, c.field)
		}
	}
}

func FuzzValidateInput(f *testing.F) {
	for _, s := range []string{"app", "", "a__bekup_old_x", "a;b", "x.y-z$"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		in := RequestInput{ArtifactID: uuid.New(), TargetConnectionID: uuid.New(), Mode: ModeOverwrite, TargetDatabase: name}
		if validateInput(&in) != nil {
			return
		}
		// Anything accepted is a strict-charset name that quotes safely
		// and is not one of bekup's temporary names.
		if engine.ValidateDBName(name) != nil || isBekupTemporaryName(name) {
			t.Fatalf("accepted %q", name)
		}
		if _, err := engine.QuotePGIdent(name); err != nil {
			t.Fatalf("accepted %q that does not quote: %v", name, err)
		}
	})
}

func warnCodes(ws []Warning) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		if w.Message == "" {
			panic("warning without text: " + w.Code)
		}
		out = append(out, w.Code)
	}
	return out
}

func TestWarnings(t *testing.T) {
	job := uuid.New()
	src := Source{Engine: engine.KindPostgres, VersionKey: "16", JobConnectionID: job, Kind: "database", Encryption: encPassphrase}
	same := connections.Connection{ID: job, Engine: engine.KindPostgres, Ready: true}
	other := connections.Connection{ID: uuid.New(), Engine: engine.KindPostgres, Temporary: true, InsecureTransport: true, NotReadyReason: "pin", Ready: false}
	my := connections.Connection{ID: job, Engine: engine.KindMySQL, Ready: true}

	got := warnCodes(databaseWarnings(src, same, &RequestInput{Mode: ModeOverwrite, TargetDatabase: "app"}))
	if strings.Join(got, ",") != WarnOverwritePostgres+","+WarnRestoreHardening+","+WarnClientVersion {
		t.Fatalf("pg overwrite into the original: %v", got)
	}
	got = warnCodes(databaseWarnings(src, other, &RequestInput{Mode: ModeNewDB, TargetDatabase: "n", KeepOwners: true}))
	want := []string{WarnNewDatabasePartial, WarnRestoreHardening, WarnClientVersion, WarnCrossSource, WarnTemporaryTarget, WarnTargetNotReady, WarnInsecureTransport, WarnKeepOwners}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("new db elsewhere: %v", got)
	}
	src.Engine = engine.KindMySQL
	ws := databaseWarnings(src, my, &RequestInput{Mode: ModeOverwrite, TargetDatabase: "shop"})
	if ws[0].Code != WarnOverwriteMySQL || ws[0].Message != MySQLOverwriteWarning {
		t.Fatalf("mysql overwrite: %+v", ws[0])
	}
	if contains(warnCodes(ws), WarnRestoreHardening) {
		t.Fatal("MySQL restores are not neutralized")
	}
	src.Encryption = encRecipients
	src.JobConnectionID = uuid.Nil // deleted job: never "original"
	got = warnCodes(globalsWarnings(src, same))
	if got[0] != WarnGlobalsRoles || !contains(got, WarnCrossSource) || !contains(got, WarnIdentityRequired) {
		t.Fatalf("globals: %v", got)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestTargetLockKeyAndNames(t *testing.T) {
	c := uuid.New()
	db := "app"
	if targetLockKey(c, nil) == targetLockKey(c, &db) || targetLockKey(c, &db) == targetLockKey(uuid.New(), &db) {
		t.Fatal("lock keys collide")
	}
	if !isBekupTemporaryName("a__bekup_restore_1") || !isBekupTemporaryName("a__bekup_old_1") || isBekupTemporaryName("bekup") {
		t.Fatal("temporary names")
	}
	if GlobalsConfirmPhrase("Prod") != "restore globals into Prod" {
		t.Fatal("phrase")
	}
	if !strings.Contains(OutcomeWarningMessage(WarnOldDatabaseKept), "__bekup_old_") || OutcomeWarningMessage("x_y") != "Warning: x_y." {
		t.Fatal("outcome messages")
	}
	if ErrorCode(ErrAbandoned) != CodeAbandoned || !strings.Contains(ErrorMessage(CodeAbandoned), "restore timeout") {
		t.Fatal("abandoned code")
	}
}

func TestEvictOldest(t *testing.T) {
	now := time.Now()
	m := map[string]time.Time{"a": now, "b": now.Add(-time.Minute), "c": now.Add(time.Minute)}
	evictOldest(m, func(v time.Time) time.Time { return v })
	if _, ok := m["b"]; ok || len(m) != 2 {
		t.Fatalf("evicted %v", m)
	}
	evictOldest(map[string]time.Time{}, func(v time.Time) time.Time { return v })
}

func TestNewAPIValidates(t *testing.T) {
	if _, err := NewAPI(APIOptions{}); err == nil {
		t.Fatal("NewAPI without dependencies")
	}
}
