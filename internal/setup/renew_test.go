package setup

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/certs"
)

func renewOptions(dir string, now time.Time, sys *fakeSys) RenewOptions {
	return RenewOptions{
		Dir: dir, Ownership: OwnershipNone, Out: &bytes.Buffer{},
		Now:        func() time.Time { return now },
		Passphrase: func() ([]byte, error) { return nil, nil }, // fastCerts: unencrypted CA key
		sys:        sys.env(),
		rotateCA: func(o certs.RotateCAOptions) (*certs.Result, error) {
			o.AllowUnencrypted = len(o.Passphrase) == 0 // the fastCerts CA
			return certs.RotateCA(o)
		},
	}
}

func fingerprints(t *testing.T, dir string) map[string]string {
	t.Helper()
	p, err := InspectCertificates(dir, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, c := range p.Certificates {
		out[c.Component] = c.SHA256
	}
	return out
}

func TestRenewExpiryAndLeaves(t *testing.T) {
	dir := t.TempDir()
	mustInit(t, dir, testAnswers(t, ProxyTraefik))
	p, err := InspectCertificates(dir, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Rotation != RotationNone || !p.CAMounted || len(p.Due()) != 0 || p.WarnDays != certs.ExpiryWarnDays {
		t.Fatalf("fresh deployment: %+v", p)
	}
	var leafEnd time.Time
	for _, c := range p.Certificates {
		if c.Component == "serve" {
			leafEnd = c.NotAfter
		}
	}
	// 10 days before the leaves expire: all three are due, the CA is not.
	soon := leafEnd.Add(-10 * 24 * time.Hour)
	p, err = InspectCertificates(dir, soon, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.Due(), ","); got != "runner,serve,postgres" {
		t.Fatalf("due %q", got)
	}
	for _, c := range p.Certificates {
		if (c.Component == "ca") == c.Expiring {
			t.Errorf("%s expiring=%v", c.Component, c.Expiring)
		}
	}

	t.Run("list", func(t *testing.T) {
		o := renewOptions(dir, soon, &fakeSys{euid: 0})
		o.ListOnly = true
		before := snapshot(t, dir)
		if err := Renew(o); err != nil {
			t.Fatal(err)
		}
		out := bufOf(t, o.Out).String()
		if !strings.Contains(out, "EXPIRES SOON") || !strings.Contains(out, "CA rotation: none") {
			t.Fatalf("list:\n%s", out)
		}
		if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
			t.Fatalf("list changed files: %v", d)
		}
	})
	t.Run("nothing to renew", func(t *testing.T) {
		o := renewOptions(dir, time.Now(), &fakeSys{})
		if err := Renew(o); err != nil || !strings.Contains(bufOf(t, o.Out).String(), "Nothing to renew") {
			t.Fatalf("%v\n%s", err, bufOf(t, o.Out))
		}
	})
	t.Run("confirm aborts", func(t *testing.T) {
		o := renewOptions(dir, soon, &fakeSys{})
		before := snapshot(t, dir)
		o.Confirm = func(p *RenewPlan) error {
			if p.RestartCommand() != "docker compose up -d --force-recreate runner serve postgres" {
				t.Errorf("restart %q", p.RestartCommand())
			}
			return errors.New("not confirmed")
		}
		if err := Renew(o); err == nil || err.Error() != "not confirmed" {
			t.Fatal(err)
		}
		if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
			t.Fatalf("aborted renew changed files: %v", d)
		}
	})
	t.Run("one component, owners", func(t *testing.T) {
		before := fingerprints(t, dir)
		sys := &fakeSys{euid: 0}
		o := renewOptions(dir, time.Now(), sys)
		o.Ownership = OwnershipChown
		o.Components = []string{"serve"}
		if err := Renew(o); err != nil {
			t.Fatal(err)
		}
		after := fingerprints(t, dir)
		if after["serve"] == before["serve"] || after["runner"] != before["runner"] || after["ca"] != before["ca"] {
			t.Fatalf("fingerprints %v -> %v", before, after)
		}
		out := bufOf(t, o.Out).String()
		if !strings.Contains(out, "Next: docker compose up -d --force-recreate serve\n") || !strings.Contains(out, after["serve"]) {
			t.Fatalf("report:\n%s", out)
		}
		if !strings.Contains(strings.Join(sys.calls, "\n"), "chown secrets/certs/serve/serve.key 65532:65532") {
			t.Fatalf("owner not set: %v", sys.calls)
		}
		if fi := mustStat(t, pathIn(dir, "secrets/certs/serve/serve.key")); fi.Mode().Perm() != 0o400 {
			t.Fatalf("key mode %04o", fi.Mode().Perm())
		}
		if _, _, err := runCheck(t, dir, nil); err != nil {
			t.Fatalf("check after renew: %v", err)
		}
	})
	t.Run("due leaves", func(t *testing.T) {
		before := fingerprints(t, dir)
		o := renewOptions(dir, soon, &fakeSys{})
		if err := Renew(o); err != nil {
			t.Fatal(err)
		}
		after := fingerprints(t, dir)
		for _, c := range []string{"runner", "serve", "postgres"} {
			if after[c] == before[c] {
				t.Errorf("%s not renewed", c)
			}
		}
	})
	t.Run("bad component", func(t *testing.T) {
		o := renewOptions(dir, soon, &fakeSys{})
		o.Components = []string{"worker"}
		if err := Renew(o); err == nil || !strings.Contains(err.Error(), "this deployment has the certificates") {
			t.Fatal(err)
		}
	})
}

func TestRenewNeedsMountedCA(t *testing.T) {
	dir := t.TempDir()
	a := externalAnswers(t, dir, DBAuthBootstrap)
	mustInit(t, dir, a)
	// The CA went offline.
	if err := removeAll(pathIn(dir, "secrets/certs/ca")); err != nil {
		t.Fatal(err)
	}
	o := renewOptions(dir, time.Now(), &fakeSys{})
	o.All = true
	if err := Renew(o); err == nil || !strings.Contains(err.Error(), "mount the offline CA media") {
		t.Fatalf("err %v", err)
	}
	p, err := InspectCertificates(dir, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Rotation != RotationUnknown || p.CAMounted || strings.Join(p.Components, ",") != "runner,serve" {
		t.Fatalf("external database deployment: %+v", p)
	}
}

func TestRenewCARotation(t *testing.T) {
	dir := t.TempDir()
	mustInit(t, dir, testAnswers(t, ProxyTraefik))
	start := fingerprints(t, dir)
	run := func(phase certs.CAPhase) (string, error) {
		o := renewOptions(dir, time.Now(), &fakeSys{})
		o.CAPhase = phase
		err := Renew(o)
		return bufOf(t, o.Out).String(), err
	}
	if _, err := run(certs.CAPromote); err == nil || !strings.Contains(err.Error(), "it starts with --ca stage") {
		t.Fatalf("promote first: %v", err)
	}
	out, err := run(certs.CAStage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "--force-recreate runner serve postgres") || !strings.Contains(out, "bekup setup renew --ca promote") {
		t.Fatalf("stage report:\n%s", out)
	}
	if p := inspectT(t, dir); p.Rotation != RotationStaged {
		t.Fatalf("after stage: %s", p.Rotation)
	}
	o := renewOptions(dir, time.Now(), &fakeSys{})
	o.All = true
	if err := Renew(o); err == nil || !strings.Contains(err.Error(), "--ca promote") {
		t.Fatalf("leaf renewal during a rotation: %v", err)
	}
	if _, err := run(certs.CAFinish); err == nil || !strings.Contains(err.Error(), "the next phase is --ca promote") {
		t.Fatalf("finish while staged: %v", err)
	}
	if _, _, err := runCheck(t, dir, nil); err != nil {
		t.Fatalf("check while staged: %v", err)
	}
	if out, err = run(certs.CAPromote); err != nil || !strings.Contains(out, "--ca finish") {
		t.Fatalf("promote: %v\n%s", err, out)
	}
	if p := inspectT(t, dir); p.Rotation != RotationPromoted {
		t.Fatalf("after promote: %s", p.Rotation)
	}
	if _, err = run(certs.CAFinish); err != nil {
		t.Fatal(err)
	}
	end := fingerprints(t, dir)
	if p := inspectT(t, dir); p.Rotation != RotationNone {
		t.Fatalf("after finish: %s", p.Rotation)
	}
	for _, c := range []string{"ca", "runner", "serve", "postgres"} {
		if start[c] == end[c] {
			t.Errorf("%s unchanged by the CA rotation", c)
		}
	}
	if _, _, err := runCheck(t, dir, nil); err != nil {
		t.Fatalf("check after the rotation: %v", err)
	}
}

func removeAll(p string) error { return os.RemoveAll(p) }

func inspectT(t *testing.T, dir string) *RenewPlan {
	t.Helper()
	p, err := InspectCertificates(dir, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
