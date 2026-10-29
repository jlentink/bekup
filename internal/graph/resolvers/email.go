package resolvers

import (
	"context"
	"errors"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/notify/email"
)

// E-mail resolvers (task 5.1, D-178): SMTP settings (admin; the password is
// write-only), the admin test e-mail and the own e-mail verification.

// EmailServices are the e-mail services behind the resolvers.
type EmailServices struct {
	Settings *email.Settings
	Test     *email.TestMailer
	Verifier *email.Verifier
}

var (
	errEmailNotConfigured = errors.New("resolvers: e-mail services not configured")
	errInvalidVerifyCode  = apierror.Invalid("code", "the verification link is invalid or has expired; request a new one")
)

// emailError maps service errors to API errors. Delivery failures of
// non-admin operations never reveal SMTP details.
func emailError(err error) error {
	var (
		fe *email.FieldError
		rl *email.RateLimitedError
		se *email.SendError
	)
	switch {
	case err == nil:
		return nil
	case errors.As(err, &fe):
		return apierror.Invalid(fe.Field, fe.Message)
	case errors.As(err, &rl):
		return apierror.Newf(apierror.RateLimited, "too many e-mail requests; try again in %d seconds",
			httpserver.RetryAfterSeconds(rl.RetryAfter))
	case errors.Is(err, email.ErrBusy):
		return errBusy
	case errors.Is(err, email.ErrConflict):
		return apierror.New(apierror.Conflict, "the SMTP settings were changed in the meantime; reload and try again")
	case errors.Is(err, email.ErrAlreadyVerified):
		return apierror.New(apierror.Conflict, "your e-mail address is already verified")
	case errors.Is(err, email.ErrCooldown):
		return apierror.New(apierror.RateLimited, "a verification link was sent less than a minute ago; check your mailbox or try again later")
	case errors.Is(err, email.ErrInvalidCode):
		return errInvalidVerifyCode
	case errors.Is(err, email.ErrNoAccount):
		return errUnauthenticated
	case errors.As(err, &se) && se.Code == email.CodeNotConfigured:
		return apierror.New(apierror.Conflict, "e-mail is not set up on this server; ask an administrator")
	case errors.As(err, &se):
		return apierror.New(apierror.ConnectionFailed, "the e-mail could not be sent; try again later")
	default:
		return err
	}
}

func emailActor(ctx context.Context) (email.Actor, error) {
	p, err := caller(ctx)
	if err != nil {
		return email.Actor{}, err
	}
	return email.Actor{UserID: p.UserID, IP: auth.RequestMetaFrom(ctx).IP}, nil
}

func smtpModeModel(m email.Mode) model.SMTPMode {
	switch m {
	case email.ModeSTARTTLS:
		return model.SMTPModeStarttls
	case email.ModeImplicitTLS:
		return model.SMTPModeImplicitTLS
	case email.ModeNone:
		return model.SMTPModeNone
	default:
		return model.SMTPModeAuto
	}
}

func smtpModeFromModel(m model.SMTPMode) (email.Mode, bool) {
	switch m {
	case model.SMTPModeAuto:
		return email.ModeAuto, true
	case model.SMTPModeStarttls:
		return email.ModeSTARTTLS, true
	case model.SMTPModeImplicitTLS:
		return email.ModeImplicitTLS, true
	case model.SMTPModeNone:
		return email.ModeNone, true
	default:
		return "", false
	}
}

func smtpSettingsModel(v *email.View) *model.SMTPSettings {
	return &model.SMTPSettings{
		Configured: v.Configured, Enabled: v.Enabled, Host: v.Host, Port: v.Port, Mode: smtpModeModel(v.Mode),
		Username: nonEmpty(v.Username), HasPassword: v.HasPassword, FromAddress: v.FromAddress,
		FromName: nonEmpty(v.FromName), CaBundlePem: nonEmpty(v.CABundlePEM),
		ConnectTimeoutSeconds: v.ConnectTimeoutSeconds, SendTimeoutSeconds: v.SendTimeoutSeconds,
		MayBeUnencrypted: v.MayBeUnencrypted, UpdatedAt: v.UpdatedAt,
	}
}

func (r *Resolver) smtpSettings(ctx context.Context) (*model.SMTPSettings, error) {
	if r.Email.Settings == nil {
		return nil, errEmailNotConfigured
	}
	v, err := r.Email.Settings.Get(ctx)
	if err != nil {
		return nil, emailError(err)
	}
	return smtpSettingsModel(&v), nil
}

func (r *Resolver) updateSMTPSettings(ctx context.Context, in model.UpdateSMTPSettingsInput) (*model.SMTPSettings, error) {
	if r.Email.Settings == nil {
		return nil, errEmailNotConfigured
	}
	actor, err := emailActor(ctx)
	if err != nil {
		return nil, err
	}
	mode, ok := smtpModeFromModel(in.Mode)
	if !ok {
		return nil, apierror.Invalid("mode", "unknown mode")
	}
	ui := email.UpdateInput{
		Enabled: in.Enabled, Host: in.Host, Port: in.Port, Mode: mode,
		Username: derefString(in.Username), Password: secretBytes(in.Password),
		ClearPassword: in.ClearPassword != nil && *in.ClearPassword,
		FromAddress:   in.FromAddress, FromName: derefString(in.FromName), CABundlePEM: derefString(in.CaBundlePem),
	}
	if in.ConnectTimeoutSeconds != nil {
		ui.ConnectTimeoutSeconds = *in.ConnectTimeoutSeconds
	}
	if in.SendTimeoutSeconds != nil {
		ui.SendTimeoutSeconds = *in.SendTimeoutSeconds
	}
	v, err := r.Email.Settings.Update(ctx, actor, ui)
	if err != nil {
		return nil, emailError(err)
	}
	return smtpSettingsModel(&v), nil
}

// sendTestEmail returns delivery failures as a result (admins may see the
// code, its fixed message and the admin detail; never server text).
func (r *Resolver) sendTestEmail(ctx context.Context, to string) (*model.SMTPTestResult, error) {
	if r.Email.Test == nil {
		return nil, errEmailNotConfigured
	}
	actor, err := emailActor(ctx)
	if err != nil {
		return nil, err
	}
	d, err := r.Email.Test.SendTest(ctx, actor, to)
	var se *email.SendError
	switch {
	case err == nil:
		return &model.SMTPTestResult{Ok: true, Encrypted: d.Encrypted, TLSVersion: nonEmpty(d.TLSVersion)}, nil
	case errors.As(err, &se):
		code, msg := string(se.Code), se.Code.Message()
		return &model.SMTPTestResult{
			Ok: false, Code: &code, Message: &msg, Detail: nonEmpty(se.Detail),
			Encrypted: d.Encrypted, TLSVersion: nonEmpty(d.TLSVersion),
		}, nil
	default:
		return nil, emailError(err)
	}
}

func (r *Resolver) requestEmailVerification(ctx context.Context) (*model.EmailVerificationRequest, error) {
	if r.Email.Verifier == nil {
		return nil, errEmailNotConfigured
	}
	actor, err := emailActor(ctx)
	if err != nil {
		return nil, err
	}
	res, err := r.Email.Verifier.Request(ctx, actor)
	if err != nil {
		return nil, emailError(err)
	}
	return &model.EmailVerificationRequest{SentTo: res.SentTo, ExpiresAt: res.ExpiresAt}, nil
}

func (r *Resolver) verifyEmail(ctx context.Context, code string) (*model.User, error) {
	if r.Email.Verifier == nil {
		return nil, errEmailNotConfigured
	}
	actor, err := emailActor(ctx)
	if err != nil {
		return nil, err
	}
	if len(code) > 128 {
		return nil, errInvalidVerifyCode
	}
	if err := r.Email.Verifier.Verify(ctx, actor, code); err != nil {
		return nil, emailError(err)
	}
	return r.me(ctx)
}
