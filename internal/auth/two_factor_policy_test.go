package auth

import (
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

func TestTwoFactorPolicyRequiredAndDeadline(t *testing.T) {
	def := DefaultTwoFactorPolicy()
	// Default (user decision, D-112): off for every role, 7-day grace once
	// an admin enables it.
	if def.RequireAll || len(def.Roles) != 0 || def.GraceDays != 7 {
		t.Fatalf("default %+v", def)
	}
	for _, r := range Roles() {
		if def.RequiredFor(r) {
			t.Errorf("default requires %s", r)
		}
	}
	all := TwoFactorPolicy{RequireAll: true}
	for _, r := range Roles() {
		if !all.RequiredFor(r) {
			t.Errorf("require all: %s not required", r)
		}
	}
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	since := created.Add(48 * time.Hour)
	p := TwoFactorPolicy{Roles: []Role{RoleUser}, GraceDays: 3, Since: map[Role]time.Time{RoleUser: since}}
	if got := p.DeadlineFor(RoleUser, created, nil); !got.Equal(since.Add(72 * time.Hour)) {
		t.Errorf("deadline from since: %v", got)
	}
	// A second-factor reset after the start restarts the grace period; an
	// older one does not matter.
	reset := since.Add(100 * time.Hour)
	if got := p.DeadlineFor(RoleUser, created, &reset); !got.Equal(reset.Add(72 * time.Hour)) {
		t.Errorf("deadline from reset: %v", got)
	}
	old := created.Add(-time.Hour)
	if got := p.DeadlineFor(RoleUser, created, &old); !got.Equal(since.Add(72 * time.Hour)) {
		t.Errorf("old reset: %v", got)
	}
	// An account created after the requirement started counts from its
	// creation.
	later := since.Add(24 * time.Hour)
	if got := p.DeadlineFor(RoleUser, later, nil); !got.Equal(later.Add(72 * time.Hour)) {
		t.Errorf("deadline from creation: %v", got)
	}
	p.GraceDays = 0
	if got := p.DeadlineFor(RoleUser, created, nil); !got.Equal(since) {
		t.Errorf("zero grace: %v", got)
	}
}

func TestTwoFactorPolicyWeakens(t *testing.T) {
	base := TwoFactorPolicy{Roles: []Role{RoleAdmin, RoleMaintainer}, GraceDays: 7}
	cases := []struct {
		name string
		next TwoFactorPolicy
		want bool
	}{
		{"same", base, false},
		{"stricter role", TwoFactorPolicy{Roles: []Role{RoleAdmin, RoleMaintainer, RoleUser}, GraceDays: 7}, false},
		{"shorter grace", TwoFactorPolicy{Roles: []Role{RoleAdmin, RoleMaintainer}, GraceDays: 1}, false},
		{"require all", TwoFactorPolicy{RequireAll: true, GraceDays: 7}, false},
		{"drop a role", TwoFactorPolicy{Roles: []Role{RoleAdmin}, GraceDays: 7}, true},
		{"longer grace", TwoFactorPolicy{Roles: []Role{RoleAdmin, RoleMaintainer}, GraceDays: 8}, true},
	}
	for _, c := range cases {
		if got := weakens(base, c.next); got != c.want {
			t.Errorf("%s: weakens = %v", c.name, got)
		}
	}
	if !weakens(TwoFactorPolicy{RequireAll: true}, TwoFactorPolicy{Roles: []Role{RoleAdmin, RoleMaintainer}}) {
		t.Error("dropping require-all keeps users required")
	}
}

func TestTwoFactorPolicyEncoding(t *testing.T) {
	since := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	p := TwoFactorPolicy{RequireAll: false, Roles: []Role{RoleAdmin, RoleUser}, GraceDays: 14, Since: map[Role]time.Time{RoleUser: since}}
	raw, err := encodeTwoFactorPolicy(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeTwoFactorPolicy(sqlc.Setting{Key: TwoFactorPolicySetting, Value: raw, UpdatedAt: since})
	if err != nil {
		t.Fatal(err)
	}
	if got.RequireAll || len(got.Roles) != 2 || got.GraceDays != 14 || !got.Since[RoleUser].Equal(since) || got.UpdatedAt == nil {
		t.Fatalf("round trip %+v", got)
	}
	for name, bad := range map[string]string{
		"not json":      `x`,
		"version":       `{"version":2,"roles":[],"grace_period_days":1}`,
		"grace":         `{"version":1,"roles":[],"grace_period_days":91}`,
		"negative":      `{"version":1,"roles":[],"grace_period_days":-1}`,
		"unknown role":  `{"version":1,"roles":["root"],"grace_period_days":1}`,
		"missing field": `{}`,
	} {
		if _, err := decodeTwoFactorPolicy(sqlc.Setting{Value: []byte(bad)}); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
}
