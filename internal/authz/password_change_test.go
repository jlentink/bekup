package authz_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/graph/apierror"
)

// A session signed in with a temporary password (task 1.6) may only view
// its account and sessions, log out, step up and change the password; everything
// else answers PASSWORD_CHANGE_REQUIRED, even for an admin with a fresh
// step-up.
func TestPasswordChangeRequiredGate(t *testing.T) {
	pol := authz.New(authz.Options{})
	ctx := auth.WithPrincipal(t.Context(), auth.Principal{
		UserID: uuid.New(), SessionID: uuid.New(), Role: auth.RoleAdmin, AAL: auth.AAL1,
		StepUpAt: time.Now(), PasswordChangeRequired: true,
	})
	allowed := map[authz.Operation]bool{
		authz.OpAccountView: true, authz.OpAccountLogout: true, authz.OpAccountPasswordChange: true,
		authz.OpAccountSessionsView: true, authz.OpAccountSessionsRevoke: true, authz.OpAccountStepUp: true,
	}
	for _, op := range authz.Operations() {
		pre := pol.Precheck(ctx, op)
		if allowed[op] {
			if pre != nil {
				t.Errorf("%s: %v", op, pre)
			}
			continue
		}
		if !apierror.IsCode(pre, apierror.PasswordChangeRequired) {
			t.Errorf("%s precheck: %v", op, pre)
		}
		if err := pol.Authorize(ctx, op); !apierror.IsCode(err, apierror.PasswordChangeRequired) {
			t.Errorf("%s authorize: %v", op, err)
		}
	}
}
