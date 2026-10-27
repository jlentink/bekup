package authz_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/graph/apierror"
)

// checkGate asserts that exactly the allowed operations pass both the
// precheck and Authorize for the principal and every other one answers
// code (task 1.9 restricted principals).
func checkGate(t *testing.T, p auth.Principal, allowed map[authz.Operation]bool, code apierror.Code) {
	t.Helper()
	pol := authz.New(authz.Options{})
	ctx := auth.WithPrincipal(t.Context(), p)
	for _, op := range authz.Operations() {
		pre := pol.Precheck(ctx, op)
		if allowed[op] {
			if pre != nil {
				t.Errorf("%s: %v", op, pre)
			}
			continue
		}
		if !apierror.IsCode(pre, code) {
			t.Errorf("%s precheck: %v (want %s)", op, pre, code)
		}
		if err := pol.Authorize(ctx, op); !apierror.IsCode(err, code) {
			t.Errorf("%s authorize: %v (want %s)", op, err, code)
		}
	}
}

// A login waiting for its second factor can only complete it or log out,
// whatever the role.
func TestMFAPendingGate(t *testing.T) {
	for _, role := range auth.Roles() {
		checkGate(t, auth.Principal{
			UserID: uuid.New(), SessionID: uuid.New(), Role: role, AAL: auth.AAL1, MFAPending: true,
		}, map[authz.Operation]bool{
			authz.OpAccountSecondFactorLogin: true, authz.OpAccountLogout: true,
		}, apierror.SecondFactorRequired)
	}
	// The pending state wins over the other restrictions.
	checkGate(t, auth.Principal{
		UserID: uuid.New(), SessionID: uuid.New(), Role: auth.RoleAdmin, AAL: auth.AAL1,
		MFAPending: true, PasswordChangeRequired: true, EnrollmentRequired: true,
	}, map[authz.Operation]bool{
		authz.OpAccountSecondFactorLogin: true, authz.OpAccountLogout: true,
	}, apierror.SecondFactorRequired)
}

// After the grace period an account without the required factor can only
// enroll one (with a password step-up), see its account and log out.
func TestEnrollmentRequiredGate(t *testing.T) {
	for _, role := range auth.Roles() {
		checkGate(t, auth.Principal{
			UserID: uuid.New(), SessionID: uuid.New(), Role: role, AAL: auth.AAL1,
			StepUpAt: time.Now(), EnrollmentRequired: true,
		}, map[authz.Operation]bool{
			authz.OpAccountView: true, authz.OpAccountLogout: true, authz.OpAccountStepUp: true,
			authz.OpAccountSecondFactorEdit: true,
		}, apierror.Requires2FAEnrollment)
	}
}

// A temporary password and the enrollment requirement together allow only
// what both allow; the password change comes first.
func TestPasswordChangeBeforeEnrollment(t *testing.T) {
	pol := authz.New(authz.Options{})
	ctx := auth.WithPrincipal(t.Context(), auth.Principal{
		UserID: uuid.New(), SessionID: uuid.New(), Role: auth.RoleUser, AAL: auth.AAL1,
		StepUpAt: time.Now(), PasswordChangeRequired: true, EnrollmentRequired: true,
	})
	if err := pol.Precheck(ctx, authz.OpAccountSecondFactorEdit); !apierror.IsCode(err, apierror.PasswordChangeRequired) {
		t.Errorf("enroll before password change: %v", err)
	}
	if err := pol.Precheck(ctx, authz.OpAccountPasswordChange); !apierror.IsCode(err, apierror.Requires2FAEnrollment) {
		t.Errorf("password change while enrollment is required: %v", err)
	}
	for _, op := range []authz.Operation{authz.OpAccountView, authz.OpAccountLogout, authz.OpAccountStepUp} {
		if err := pol.Precheck(ctx, op); err != nil {
			t.Errorf("%s: %v", op, err)
		}
	}
}

// The restore rule (plan "Restore 2FA", D-057): every restore needs a
// passkey/TOTP check within the step-up window on an AAL2 session,
// whatever the 2FA policy. A password step-up or a recovery-code step-up
// (step-up time only) is not enough; without a factor the answer is
// REQUIRES_2FA_ENROLLMENT.
func TestRestoreRequiresFreshSecondFactor(t *testing.T) {
	admin := authztest.Principal(auth.RoleAdmin)
	withFactor := authz.New(authz.Options{SecondFactors: authztest.SecondFactors{Enrolled: map[uuid.UUID]bool{admin.UserID: true}}})
	noFactor := authz.New(authz.Options{})
	now := time.Now()
	cases := []struct {
		name   string
		mutate func(*auth.Principal)
		pol    *authz.Policy
		want   apierror.Code
	}{
		{"fresh AAL2", func(*auth.Principal) {}, withFactor, ""},
		{"4m old second factor", func(p *auth.Principal) { p.AAL2At = now.Add(-4 * time.Minute) }, withFactor, ""},
		{"6m old second factor", func(p *auth.Principal) { p.AAL2At = now.Add(-6 * time.Minute) }, withFactor, apierror.StepUpRequired},
		{"recovery-code step-up (no second-factor time)", func(p *auth.Principal) { p.AAL2At = time.Time{} }, withFactor, apierror.StepUpRequired},
		{"password step-up on AAL1", func(p *auth.Principal) { p.AAL, p.AAL2At = auth.AAL1, time.Time{} }, withFactor, apierror.StepUpRequired},
		{"AAL1 with a planted second-factor time", func(p *auth.Principal) { p.AAL = auth.AAL1 }, withFactor, apierror.StepUpRequired},
		{"future second-factor time", func(p *auth.Principal) { p.AAL2At = now.Add(time.Minute) }, withFactor, apierror.StepUpRequired},
		{"password step-up without a factor", func(p *auth.Principal) { p.AAL, p.AAL2At = auth.AAL1, time.Time{} }, noFactor, apierror.Requires2FAEnrollment},
	}
	for _, c := range cases {
		p := admin
		p.StepUpAt = now
		c.mutate(&p)
		ctx := authztest.Context(t.Context(), p)
		for _, op := range []authz.Operation{authz.OpRestoreGlobals} {
			err := c.pol.Authorize(ctx, op)
			if c.want == "" {
				if err != nil {
					t.Errorf("%s %s: %v", c.name, op, err)
				}
				continue
			}
			if !apierror.IsCode(err, c.want) {
				t.Errorf("%s %s: %v (want %s)", c.name, op, err, c.want)
			}
		}
		target := authz.RestoreTarget{ConnectionID: uuid.New(), Database: "app"}
		err := c.pol.Authorize(ctx, authz.OpRestoreArtifact, target)
		if (c.want == "" && err != nil) || (c.want != "" && !apierror.IsCode(err, c.want)) {
			t.Errorf("%s restore.artifact: %v (want %q)", c.name, err, c.want)
		}
	}
}

// Introspection is for unrestricted principals only (D-050; M1 review L1).
func TestIntrospectionDeniedToRestrictedPrincipals(t *testing.T) {
	pol := authz.New(authz.Options{})
	base := authztest.Principal(auth.RoleAdmin)
	cases := []struct {
		name   string
		mutate func(*auth.Principal)
		want   bool
	}{
		{"unrestricted", func(*auth.Principal) {}, true},
		{"second factor pending", func(p *auth.Principal) { p.MFAPending, p.AAL = true, auth.AAL1 }, false},
		{"temporary password", func(p *auth.Principal) { p.PasswordChangeRequired = true }, false},
		{"enrollment required", func(p *auth.Principal) { p.EnrollmentRequired = true }, false},
	}
	for _, c := range cases {
		p := base
		c.mutate(&p)
		if got := pol.IntrospectionAllowed(auth.WithPrincipal(t.Context(), p)); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if pol.IntrospectionAllowed(t.Context()) {
		t.Error("anonymous caller may introspect")
	}
}
