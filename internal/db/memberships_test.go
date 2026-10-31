package db

import (
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

func TestSplitMemberships(t *testing.T) {
	ms := []sqlc.RoleMembershipsRow{
		{RoleName: OwnerRole, MemberName: "admin", AdminOption: true},
		{RoleName: OwnerRole, MemberName: AppRole},
		{RoleName: AppRole, MemberName: "acct"},
		{RoleName: OwnerRole, MemberName: "mallory", AdminOption: true},
		{RoleName: AppRole, MemberName: "postgres", MemberSuperuser: true},
	}
	got := splitMemberships(ms, "admin", "")
	names := make([]string, 0, len(got))
	for _, m := range got {
		names = append(names, m.MemberName)
	}
	if strings.Join(names, ",") != "acct,mallory,postgres" {
		t.Fatalf("foreign = %v", names)
	}
	if w := membershipWarning(ms, "acct"); !strings.Contains(w, "admin is a member of bekup_owner WITH ADMIN OPTION") ||
		!strings.Contains(w, "mallory") || !strings.Contains(w, "postgres is a member of bekup_app (a superuser)") || strings.Contains(w, "acct is") {
		t.Fatalf("warning %q", w)
	}
	if w := membershipWarning(ms[1:3], "acct"); w != "" {
		t.Fatalf("expected no warning, got %q", w)
	}
	if h := revokeMembershipHint(sqlc.RoleMembershipsRow{RoleName: OwnerRole, MemberName: `we"ird`}); h != `REVOKE bekup_owner FROM "we""ird";` {
		t.Fatalf("hint %q", h)
	}
}

func TestCheckSystemUser(t *testing.T) {
	for _, tc := range []struct {
		id, login, want string
	}{
		{"scram-sha-256:bekup_app", "bekup_app", ""},
		{"md5:bekup_app", "bekup_app", ""},
		{"scram-sha-256:postgres", "bekup_app", "another role"},
		{"md5:postgres", "bekup_app", "another role"},
		{"cert:bekup_app", "bekup_app", `method "cert"`},
		{"", "bekup_app", `method "none"`},
		{"trust", "bekup_app", `method "trust"`},
		{"we!rd:bekup_app", "bekup_app", `method "unknown"`},
		{"scram-sha-256:acct", "acct", ""},
	} {
		err := checkSystemUser(tc.id, tc.login)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%q: %v", tc.id, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%q: got %v, want %q", tc.id, err, tc.want)
		case err != nil && strings.Contains(err.Error(), "postgres"):
			t.Errorf("%q: error echoes the foreign identity: %v", tc.id, err)
		}
	}
}
