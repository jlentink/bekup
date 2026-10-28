package jobs

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/serviceutil"
)

func strp(s string) *string { return &s }

func TestTestTargetCovered(t *testing.T) {
	now := time.Now()
	conn := uuid.New()
	not := connections.Privileges{Status: connections.PrivilegeNotPrivileged, Reasons: []string{}}
	super := connections.Privileges{Status: connections.PrivilegePrivileged, Reasons: []string{"superuser"}}
	unknown := connections.Privileges{Status: connections.PrivilegeUnknown, Reasons: []string{"roles_granted"}}
	acked := sqlc.Job{TestRestoreConnectionID: &conn, TestRestoreTargetPrivileges: strp("privileged"),
		TestRestorePrivilegeReasons: []string{"superuser", "createrole"}, TestRestorePrivilegedAckAt: &now}
	plain := sqlc.Job{TestRestoreConnectionID: &conn, TestRestoreTargetPrivileges: strp("not_privileged"), TestRestorePrivilegeReasons: []string{}}
	legacy := sqlc.Job{TestRestoreConnectionID: &conn}
	tests := []struct {
		name string
		row  sqlc.Job
		cur  connections.Privileges
		want bool
	}{
		{"not privileged always runs", plain, not, true},
		{"not privileged runs without a stored verdict", legacy, not, true},
		{"became privileged without an acknowledgement", plain, super, false},
		{"became unknown without an acknowledgement", plain, unknown, false},
		{"legacy designation now privileged", legacy, super, false},
		{"acknowledged reasons cover the verdict", acked, super, true},
		{"a subset of the acknowledged reasons", acked, connections.Privileges{Status: connections.PrivilegePrivileged, Reasons: []string{"createrole"}}, true},
		{"a new reason needs a new acknowledgement", acked, connections.Privileges{Status: connections.PrivilegePrivileged, Reasons: []string{"superuser", "replication"}}, false},
		{"unknown with other reasons", acked, unknown, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TestTargetCovered(&tt.row, tt.cur); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
	// An acknowledgement next to a not-privileged stored verdict (which
	// the CHECK forbids) never counts.
	bad := plain
	bad.TestRestorePrivilegedAckAt = &now
	if TestTargetCovered(&bad, super) {
		t.Fatal("inconsistent row covered")
	}
}

func TestStoredTestTargetPrivileges(t *testing.T) {
	if p := StoredTestTargetPrivileges(&sqlc.Job{}); p.Status != connections.PrivilegeUnknown || len(p.Reasons) != 1 || p.Reasons[0] != connections.ReasonNoVerdict {
		t.Fatalf("no verdict: %+v", p)
	}
	if p := StoredTestTargetPrivileges(&sqlc.Job{TestRestoreTargetPrivileges: strp("bogus")}); p.Status != connections.PrivilegeUnknown {
		t.Fatalf("bogus: %+v", p)
	}
	if p := StoredTestTargetPrivileges(&sqlc.Job{TestRestoreTargetPrivileges: strp("not_privileged")}); p.Gated() || p.Reasons == nil {
		t.Fatalf("plain: %+v", p)
	}
}

func TestCheckPrivilegedTarget(t *testing.T) {
	gated := connections.Privileges{Status: connections.PrivilegeUnknown, Reasons: []string{"roles_granted"}}
	admin := authztest.Context(t.Context(), authztest.Principal(auth.RoleAdmin))
	maint := authztest.Context(t.Context(), authztest.Principal(auth.RoleMaintainer))
	if err := checkPrivilegedTarget(maint, connections.Privileges{Status: connections.PrivilegeNotPrivileged}, false); err != nil {
		t.Fatalf("not privileged: %v", err)
	}
	if err := checkPrivilegedTarget(maint, gated, true); !errors.Is(err, ErrPrivilegedTargetAdminOnly) {
		t.Fatalf("maintainer: %v", err)
	}
	if err := checkPrivilegedTarget(t.Context(), gated, true); !errors.Is(err, ErrPrivilegedTargetAdminOnly) {
		t.Fatalf("anonymous: %v", err)
	}
	var fe *serviceutil.FieldError
	if err := checkPrivilegedTarget(admin, gated, false); !errors.As(err, &fe) || fe.Field != "acknowledgePrivilegedTarget" {
		t.Fatalf("admin without ack: %v", err)
	}
	if err := checkPrivilegedTarget(admin, gated, true); err != nil {
		t.Fatalf("admin with ack: %v", err)
	}
}
