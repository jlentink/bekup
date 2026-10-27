package resolvers

import (
	"context"
	"errors"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/httpserver"
)

var errTOTPNotConfigured = errors.New("resolvers: TOTP service not configured")

// totpError maps second-factor service errors to API errors; anything else
// stays an internal (masked) error.
func totpError(err error) error {
	var locked *auth.SecondFactorLockedError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, auth.ErrStepUpRequired):
		return apierror.New(apierror.StepUpRequired, "a fresh step-up is required")
	case errors.Is(err, auth.ErrInvalidCode):
		return apierror.Invalid("code", "the code is not valid")
	case errors.As(err, &locked):
		return apierror.Newf(apierror.RateLimited, "too many failed attempts; try again in %d seconds",
			httpserver.RetryAfterSeconds(locked.RetryAfter))
	case errors.Is(err, auth.ErrTOTPAlreadyEnabled):
		return apierror.New(apierror.Conflict, "TOTP is already enabled; disable it first")
	case errors.Is(err, auth.ErrTOTPNotEnabled):
		return apierror.New(apierror.Conflict, "TOTP is not enabled")
	case errors.Is(err, auth.ErrNoEnrollmentPending):
		return apierror.New(apierror.Conflict, "no TOTP enrollment is pending; start again")
	case errors.Is(err, auth.ErrNoSecondFactor):
		return apierror.New(apierror.Conflict, "enroll a second factor first")
	case errors.Is(err, auth.ErrLastFactor):
		return apierror.New(apierror.Conflict, "this is your last second factor and the 2FA policy requires one for your role; add another factor first")
	default:
		return err
	}
}

func (r *Resolver) totpService(ctx context.Context) (*auth.TOTP, auth.Principal, error) {
	if r.TOTP == nil {
		return nil, auth.Principal{}, errTOTPNotConfigured
	}
	p, err := caller(ctx)
	if err != nil {
		return nil, auth.Principal{}, err
	}
	return r.TOTP, p, nil
}

func (r *Resolver) beginTotpEnrollment(ctx context.Context) (*model.TotpEnrollment, error) {
	svc, p, err := r.totpService(ctx)
	if err != nil {
		return nil, err
	}
	en, err := svc.BeginTOTPEnrollment(ctx, p)
	if err != nil {
		return nil, totpError(err)
	}
	return &model.TotpEnrollment{
		OtpauthURI: en.URI, QRCodeSVG: en.QRCodeSVG, ManualEntryKey: en.ManualKey, ExpiresAt: en.ExpiresAt,
	}, nil
}

func (r *Resolver) confirmTotpEnrollment(ctx context.Context, code string) (*model.RecoveryCodes, error) {
	svc, p, err := r.totpService(ctx)
	if err != nil {
		return nil, err
	}
	codes, err := svc.ConfirmTOTPEnrollment(ctx, p, code)
	if err != nil {
		return nil, totpError(err)
	}
	return &model.RecoveryCodes{Codes: codes}, nil
}

func (r *Resolver) disableTotp(ctx context.Context) (bool, error) {
	svc, p, err := r.totpService(ctx)
	if err != nil {
		return false, err
	}
	if err := svc.DisableTOTP(ctx, p); err != nil {
		return false, totpError(err)
	}
	return true, nil
}

func (r *Resolver) regenerateRecoveryCodes(ctx context.Context) (*model.RecoveryCodes, error) {
	svc, p, err := r.totpService(ctx)
	if err != nil {
		return nil, err
	}
	codes, err := svc.RegenerateRecoveryCodes(ctx, p)
	if err != nil {
		return nil, totpError(err)
	}
	return &model.RecoveryCodes{Codes: codes}, nil
}

func (r *Resolver) myTwoFactorStatus(ctx context.Context) (*model.TwoFactorStatus, error) {
	svc, p, err := r.totpService(ctx)
	if err != nil {
		return nil, err
	}
	st, err := svc.Status(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	return &model.TwoFactorStatus{
		TotpEnabled: st.TOTPEnabled, TotpEnabledAt: st.TOTPEnabledAt,
		TotpEnrollmentPending: st.TOTPEnrollmentPending, RecoveryCodesRemaining: st.RecoveryCodesRemaining,
	}, nil
}
