package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func validPrincipal() Principal {
	return Principal{UserID: uuid.New(), SessionID: uuid.New(), Role: RoleUser, AAL: 1}
}

func TestFromContext(t *testing.T) {
	if _, ok := FromContext(t.Context()); ok {
		t.Fatal("empty context has a principal")
	}
	p := validPrincipal()
	got, ok := FromContext(WithPrincipal(t.Context(), p))
	if !ok || got != p {
		t.Fatalf("FromContext = %+v, %v", got, ok)
	}
	for name, bad := range map[string]Principal{
		"zero":       {},
		"no user":    {SessionID: uuid.New(), Role: RoleAdmin},
		"no session": {UserID: uuid.New(), Role: RoleAdmin},
		"bad role":   {UserID: uuid.New(), SessionID: uuid.New(), Role: "root"},
		"empty role": {UserID: uuid.New(), SessionID: uuid.New()},
	} {
		if _, ok := FromContext(WithPrincipal(t.Context(), bad)); ok {
			t.Errorf("%s: accepted as a principal", name)
		}
	}
}

func TestRoles(t *testing.T) {
	for _, r := range Roles() {
		if !r.Valid() {
			t.Errorf("%s not valid", r)
		}
	}
	if Role("Admin").Valid() || Role("").Valid() {
		t.Error("unknown roles are valid")
	}
}

func TestFreshness(t *testing.T) {
	now := time.Now()
	w := 5 * time.Minute
	cases := []struct {
		name string
		ts   time.Time
		w    time.Duration
		want bool
	}{
		{"zero", time.Time{}, w, false},
		{"just now", now, w, true},
		{"4m59s", now.Add(-w + time.Second), w, true},
		{"5m01s", now.Add(-w - time.Second), w, false},
		{"small future skew", now.Add(2 * time.Second), w, true},
		{"far future", now.Add(time.Hour), w, false},
		{"zero window", now, 0, false},
		{"negative window", now, -time.Second, false},
	}
	for _, c := range cases {
		if got := fresh(c.ts, c.w, now); got != c.want {
			t.Errorf("%s: fresh = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHasRecent(t *testing.T) {
	p := validPrincipal()
	p.StepUpAt = time.Now().Add(-4 * time.Minute)
	p.AAL2At = time.Now().Add(-4 * time.Minute)
	if !p.HasRecentStepUp(5 * time.Minute) {
		t.Error("4m old step-up is not recent")
	}
	if p.HasRecentStepUp(3 * time.Minute) {
		t.Error("4m old step-up is recent within 3m")
	}
	if p.HasRecentAAL2(5 * time.Minute) {
		t.Error("AAL1 session counts as recent AAL2")
	}
	p.AAL = 2
	if !p.HasRecentAAL2(5 * time.Minute) {
		t.Error("4m old AAL2 is not recent")
	}
	p.AAL2At = time.Now().Add(-6 * time.Minute)
	if p.HasRecentAAL2(5 * time.Minute) {
		t.Error("6m old AAL2 is recent")
	}
}
