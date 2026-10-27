package resolvers

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/auth/passkey"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
)

var errPasskeysNotConfigured = errors.New("resolvers: passkey service not configured")

// passkeyError maps passkey service errors to API errors; anything else
// stays an internal (masked) error.
func passkeyError(err error, rp passkey.RelyingParty) error {
	var ne *passkey.NameError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, passkey.ErrUnavailable):
		return apierror.New(apierror.Forbidden, rp.Reason)
	case errors.Is(err, passkey.ErrStepUpRequired):
		return apierror.New(apierror.StepUpRequired, "please confirm your identity again")
	case errors.As(err, &ne):
		return apierror.Invalid("name", "the passkey name "+ne.Message)
	case errors.Is(err, passkey.ErrNoCeremony):
		return apierror.Invalid("response", "no pending passkey registration for this session (expired or already used); start again")
	case errors.Is(err, passkey.ErrInvalidResponse), errors.Is(err, passkey.ErrVerification):
		return apierror.Invalid("response", "the passkey response could not be verified; start again")
	case errors.Is(err, passkey.ErrTooMany):
		return apierror.Newf(apierror.Conflict, "you already have the maximum of %d passkeys", passkey.MaxPasskeys)
	case errors.Is(err, passkey.ErrNotFound):
		return apierror.New(apierror.NotFound, "passkey not found")
	case errors.Is(err, auth.ErrLastFactor):
		return apierror.New(apierror.Conflict, "this is your last second factor and the 2FA policy requires one for your role; add another factor first")
	default:
		return err
	}
}

func (r *Resolver) passkeys(ctx context.Context) (*passkey.Service, auth.Principal, error) {
	if r.Passkeys == nil {
		return nil, auth.Principal{}, errPasskeysNotConfigured
	}
	p, err := caller(ctx)
	if err != nil {
		return nil, auth.Principal{}, err
	}
	return r.Passkeys, p, nil
}

func (r *Resolver) webauthnStatus(ctx context.Context) (*model.WebAuthnStatus, error) {
	svc, _, err := r.passkeys(ctx)
	if err != nil {
		return nil, err
	}
	rp := svc.Status()
	st := &model.WebAuthnStatus{WebauthnAvailable: rp.Available}
	if rp.Available {
		id := rp.ID
		st.RpID = &id
	} else {
		reason := rp.Reason
		st.Reason = &reason
	}
	return st, nil
}

func (r *Resolver) myPasskeys(ctx context.Context) ([]model.Passkey, error) {
	svc, p, err := r.passkeys(ctx)
	if err != nil {
		return nil, err
	}
	list, err := svc.List(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]model.Passkey, 0, len(list))
	for i := range list {
		out = append(out, passkeyModel(&list[i]))
	}
	return out, nil
}

func (r *Resolver) beginPasskeyRegistration(ctx context.Context) (model.JSON, error) {
	svc, p, err := r.passkeys(ctx)
	if err != nil {
		return nil, err
	}
	out, err := svc.BeginRegistration(ctx, p)
	if err != nil {
		return nil, passkeyError(err, svc.Status())
	}
	return model.JSON(out), nil
}

func (r *Resolver) finishPasskeyRegistration(ctx context.Context, name string, response model.JSON) (*model.Passkey, error) {
	svc, p, err := r.passkeys(ctx)
	if err != nil {
		return nil, err
	}
	pk, err := svc.FinishRegistration(ctx, p, name, response)
	if err != nil {
		return nil, passkeyError(err, svc.Status())
	}
	m := passkeyModel(&pk)
	return &m, nil
}

func (r *Resolver) renamePasskey(ctx context.Context, id uuid.UUID, name string) (*model.Passkey, error) {
	svc, p, err := r.passkeys(ctx)
	if err != nil {
		return nil, err
	}
	pk, err := svc.Rename(ctx, p, id, name)
	if err != nil {
		return nil, passkeyError(err, svc.Status())
	}
	m := passkeyModel(&pk)
	return &m, nil
}

func (r *Resolver) deletePasskey(ctx context.Context, id uuid.UUID) (bool, error) {
	svc, p, err := r.passkeys(ctx)
	if err != nil {
		return false, err
	}
	if err := svc.Delete(ctx, p, id); err != nil {
		return false, passkeyError(err, svc.Status())
	}
	return true, nil
}

func passkeyModel(pk *passkey.Passkey) model.Passkey {
	return model.Passkey{
		ID: pk.ID, Name: pk.Name, CreatedAt: pk.CreatedAt, LastUsedAt: pk.LastUsedAt,
		Transports: pk.Transports, Aaguid: pk.AAGUID,
		BackupEligible: pk.BackupEligible, BackedUp: pk.BackupState, UserVerified: pk.UserVerified,
		SignCountAnomalyAt: pk.SignCountAnomalyAt,
	}
}
