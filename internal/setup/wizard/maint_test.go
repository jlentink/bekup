package wizard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/imageref"
	"github.com/jlentink/bekup/internal/setup"
)

func TestUpgradeConfirm(t *testing.T) {
	sum := &setup.UpgradeSummary{
		OldVersion: "1.2.3", NewVersion: "1.3.0", OldImage: "a", NewImage: "b",
		ComposeMode: setup.ComposeGenerated, Variant: setup.VariantTraefik, DB: setup.DBExternal,
		ComposeDiff: "  + new line\n  - old line\n  + other\n", Changed: []string{"compose.yaml"},
		NewSecretGroups: []string{"certs"}, BackedUp: []string{"deploy/x"},
	}
	s := &Script{Answers: []Answer{
		{Title: "Upgrade"}, {Title: "compose.yaml changes"},
		{Title: "Backup", Value: "yes"}, {Title: "Upgrade now?", Value: "yes"},
	}}
	if err := UpgradeConfirm(s)(sum); err != nil {
		t.Fatal(err)
	}
	text := s.Text.String()
	for _, want := range []string{"1.2.3 -> 1.3.0", "your own PostgreSQL", "rewritten (+2 -1 lines)", "New secrets: certs", "1 of yours backed up", "docker compose pull && docker compose up -d"} {
		if !strings.Contains(text, want) {
			t.Errorf("summary lacks %q:\n%s", want, text)
		}
	}
	// No backup: nothing is written.
	s = &Script{Answers: []Answer{{Title: "Upgrade"}, {Title: "compose.yaml changes"}, {Title: "Backup", Value: "no"}}}
	if err := UpgradeConfirm(s)(sum); !errors.Is(err, ErrAborted) || !strings.Contains(err.Error(), "back up first") {
		t.Fatalf("no backup: %v", err)
	}
	// Compose-first: no diff screen.
	sum.ComposeMode, sum.ComposeDiff, sum.ComposeLevel = setup.ComposeOperator, "", setup.RecognizedExact
	s = &Script{Answers: []Answer{{Title: "Upgrade"}, {Title: "Backup", Value: "yes"}, {Title: "Upgrade now?", Value: "no"}}}
	if err := UpgradeConfirm(s)(sum); !errors.Is(err, ErrAborted) {
		t.Fatalf("declined: %v", err)
	}
	if !strings.Contains(s.Text.String(), "your release file (exact)") {
		t.Error(s.Text.String())
	}
}

func TestRunRenew(t *testing.T) {
	// A deployment with the bundled database (written by setup itself).
	dir := t.TempDir()
	o := setup.Options{
		Dir: dir, Version: "1.2.3", Ownership: setup.OwnershipNone, WriteCompose: true,
		CAPassphrase: func() ([]byte, error) { return []byte("correct horse battery staple x"), nil },
	}
	img := mustImage(t)
	o.Answers = setup.Answers{Image: img, Variant: setup.VariantFull, Proxy: setup.ProxyNone, Domain: "b.example.com", TrustedProxies: []string{"10.213.250.6/32"}, DockerGID: 999}
	if err := setup.Init(o); err != nil {
		t.Fatal(err)
	}
	p, err := setup.InspectCertificates(dir, nowForTest(), 0)
	if err != nil {
		t.Fatal(err)
	}
	s := &Script{Answers: []Answer{{Title: "Certificates"}, {Title: "Renew", Value: "all"}}}
	opts, done, err := RunRenew(s, dir, nowForTest(), 0)
	if err != nil || done || !opts.All || opts.CAPhase != "" {
		t.Fatalf("all: %+v %v %v", opts, done, err)
	}
	if !strings.Contains(s.Text.String(), "CA rotation: none") || !strings.Contains(s.Text.String(), "postgres") {
		t.Fatalf("list:\n%s", s.Text.String())
	}
	// The hooks: confirm shows the plan, the passphrase is asked.
	s.Answers = append(s.Answers, Answer{Title: "Renew now?", Value: "yes"}, Answer{Title: "Passphrase of the CA key", Value: "pw"})
	plan := &setup.RenewPlan{Renew: p.Components, Restart: []string{"runner", "serve", "postgres"}}
	if err := opts.Confirm(plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Text.String(), "--force-recreate runner serve postgres") {
		t.Error("restart command not shown")
	}
	if pw, err := opts.Passphrase(); err != nil || string(pw) != "pw" {
		t.Fatalf("passphrase %q %v", pw, err)
	}
	s = &Script{Answers: []Answer{{Title: "Certificates"}, {Title: "Renew", Value: "phase"}}}
	if opts, _, err = RunRenew(s, dir, nowForTest(), 0); err != nil || opts.CAPhase != certs.CAStage {
		t.Fatalf("phase: %+v %v", opts, err)
	}
	s = &Script{Answers: []Answer{{Title: "Certificates"}, {Title: "Renew"}}}
	if _, done, err = RunRenew(s, dir, nowForTest(), 0); err != nil || !done {
		t.Fatalf("default is quit when nothing expires: %v %v", done, err)
	}
	// --warn-days reaches the plan: with a period beyond the leaves'
	// validity everything is due.
	s = &Script{Answers: []Answer{{Title: "Certificates"}, {Title: "Renew"}}}
	if opts, done, err = RunRenew(s, dir, nowForTest(), 100000); err != nil || done || opts.WarnDays != 100000 || opts.All || opts.CAPhase != "" {
		t.Fatalf("warn days: %+v %v %v", opts, done, err)
	}
	// A missing leaf counts as due and is named.
	if err := os.Remove(filepath.Join(dir, "secrets", "certs", "serve", "serve.crt")); err != nil {
		t.Fatal(err)
	}
	s = &Script{Answers: []Answer{{Title: "Certificates"}, {Title: "Renew"}}}
	if opts, done, err = RunRenew(s, dir, nowForTest(), 0); err != nil || done || opts.All {
		t.Fatalf("missing leaf: %+v %v %v", opts, done, err)
	}
	if text := s.Text.String(); !strings.Contains(text, "serve    MISSING") || !strings.Contains(text, "1 expiring or missing certificate(s): serve") {
		t.Fatalf("missing leaf not shown:\n%s", text)
	}
}

func mustImage(t *testing.T) imageref.Ref {
	t.Helper()
	r, err := imageref.ParsePinned(testImage)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func nowForTest() time.Time { return time.Now() }
