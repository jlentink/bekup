package connections

import (
	"slices"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/runnerpb"
)

func TestPrivilegesFromRunner(t *testing.T) {
	long := strings.Repeat("x", 65)
	many := make([]string, 0, 40)
	for i := range 40 {
		many = append(many, "global:P"+strings.Repeat("A", i))
	}
	for _, c := range []struct {
		name    string
		in      *runnerpb.TargetPrivileges
		status  PrivilegeStatus
		reasons []string
		gated   bool
	}{
		{"nil", nil, PrivilegeUnknown, []string{ReasonNoVerdict}, true},
		{"unspecified", &runnerpb.TargetPrivileges{Reasons: []string{"superuser"}}, PrivilegeUnknown, []string{ReasonNoVerdict}, true},
		{"plain", &runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_NOT_PRIVILEGED}, PrivilegeNotPrivileged, []string{}, false},
		{
			"plain with reasons is inconsistent",
			&runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_NOT_PRIVILEGED, Reasons: []string{"superuser"}},
			PrivilegeUnknown, []string{"superuser"}, true,
		},
		{
			"privileged", &runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED,
				Reasons: []string{"superuser", "global:ALL PRIVILEGES", "superuser"}},
			PrivilegePrivileged, []string{"superuser", "global:ALL PRIVILEGES"}, true,
		},
		{
			"global data and read-all classes", &runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED,
				Reasons: []string{"global_data:CREATE TEMPORARY TABLES", "member_of:pg_read_all_data"}},
			PrivilegePrivileged, []string{"global_data:CREATE TEMPORARY TABLES", "member_of:pg_read_all_data"}, true,
		},
		{
			"privileged without reasons", &runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED},
			PrivilegePrivileged, []string{ReasonNoVerdict}, true,
		},
		{
			"odd reasons", &runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_UNKNOWN,
				Reasons: []string{"<script>", long, "", "roles_granted"}},
			PrivilegeUnknown, []string{ReasonInvalidReason, "roles_granted"}, true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := PrivilegesFromRunner(c.in)
			if got.Status != c.status || !slices.Equal(got.Reasons, c.reasons) || got.Gated() != c.gated {
				t.Fatalf("got %+v (gated %v)", got, got.Gated())
			}
		})
	}
	got := PrivilegesFromRunner(&runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED, Reasons: many})
	if len(got.Reasons) != maxPrivilegeReasons {
		t.Fatalf("reasons not bounded: %d", len(got.Reasons))
	}
}
