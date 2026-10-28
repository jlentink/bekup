package resolvers

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// setJobTestRestore's errors (D-170): maintainers designating a
// privileged scratch target are FORBIDDEN, admins without the
// acknowledgement VALIDATION, probe failures map like connection errors.
func TestSetTestRestoreErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		code apierror.Code
	}{
		{jobs.ErrPrivilegedTargetAdminOnly, apierror.Forbidden},
		{serviceutil.FieldErr("acknowledgePrivilegedTarget", "acknowledge"), apierror.Validation},
		{fmt.Errorf("jobs: probe the scratch connection: %w", &connections.NotReadyError{Reason: "pin required"}), apierror.Conflict},
		{fmt.Errorf("jobs: probe the scratch connection: %w", &connections.RateLimitedError{RetryAfter: time.Second}), apierror.RateLimited},
		{jobs.ErrNotFound, apierror.NotFound},
	}
	for _, c := range cases {
		var ae *apierror.Error
		if err := setTestRestoreError(c.err); !errors.As(err, &ae) || ae.Code != c.code {
			t.Errorf("%v -> %v, want %s", c.err, err, c.code)
		}
	}
}

func TestTestRestoreJobModelPrivileges(t *testing.T) {
	now := time.Now()
	m := testRestoreJobModel(&jobs.TestRestoreSettings{
		TargetPrivileges: &connections.Privileges{Status: connections.PrivilegeUnknown, Reasons: []string{"roles_granted"}},
		PrivilegedAckAt:  &now,
	})
	if m.TargetPrivileges == nil || m.TargetPrivileges.Status != model.TargetPrivilegeStatusUnknown || !m.TargetPrivileges.AcknowledgementRequired ||
		m.PrivilegedTargetAcknowledgedAt == nil {
		t.Fatalf("model %+v", m)
	}
	if m := testRestoreJobModel(&jobs.TestRestoreSettings{}); m.TargetPrivileges != nil || m.PrivilegedTargetAcknowledgedAt != nil {
		t.Fatalf("no target: %+v", m)
	}
}
