//go:build integration

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/logging"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

func TestAuditCommands(t *testing.T) {
	ctx := t.Context()
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	a := audit.New(pool, audit.Options{Registry: logging.NewRegistry()})
	for range 3 {
		if _, err := a.Record(ctx, audit.Event{Actor: audit.SystemActor(), Action: audit.ActionSettingChanged}); err != nil {
			t.Fatal(err)
		}
	}
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer kr.Destroy()
	krText, err := kr.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, "bekup.toml", fmt.Sprintf(`
[database]
url = %q
tls_mode = "verify-full"
tls_ca_file = %q
[security]
master_key = '''
%s'''
`, d.AppURL, dbtest.CAFile(), krText))

	out, _, err := run(t, "--dev", "--config", cfg, "audit", "verify")
	if err != nil || !strings.Contains(out, "3 rows verified") || !strings.Contains(out, "no signed chain head") {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	head := filepath.Join(t.TempDir(), "head.json")
	out, _, err = run(t, "--dev", "--config", cfg, "audit", "sign-head", "--export-file", head)
	if err != nil || !strings.Contains(out, "signed audit chain head: id 4") {
		t.Fatalf("sign-head: %v\n%s", err, out)
	}
	out, _, err = run(t, "--dev", "--config", cfg, "audit", "verify", "--json", "--head-file", head, "--from-id", "2")
	if err != nil {
		t.Fatalf("verify --json: %v\n%s", err, out)
	}
	var rep audit.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil || !rep.OK || len(rep.Heads) != 2 || !rep.Partial {
		t.Fatalf("json report: %v %+v", err, rep)
	}

	if err := d.SuperExec(ctx, "ALTER TABLE audit_log DISABLE TRIGGER USER", "UPDATE audit_log SET action = 'user.deleted' WHERE id = 2"); err != nil {
		t.Fatal(err)
	}
	out, _, err = run(t, "--dev", "--config", cfg, "audit", "verify")
	if !errors.Is(err, audit.ErrTampered) || !strings.Contains(out, "BROKEN at id 2") || !strings.Contains(out, "TAMPERING DETECTED") {
		t.Fatalf("tampered verify: %v\n%s", err, out)
	}
}
