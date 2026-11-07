package resolvers

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/apiaccess"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// errAPITokensUnconfigured: the API token service is not wired.
var errAPITokensUnconfigured = errors.New("resolvers: API token service not configured")

// apiTokenError maps the token service's errors to API errors (D-220).
func apiTokenError(err error) error {
	var fe *serviceutil.FieldError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &fe):
		return apierror.Invalid(fe.Field, fe.Message)
	case errors.Is(err, apiaccess.ErrNotFound):
		return apierror.New(apierror.NotFound, "API token not found")
	case errors.Is(err, apiaccess.ErrRevoked):
		return apierror.New(apierror.Conflict, "the API token is already revoked")
	case errors.Is(err, apiaccess.ErrForbidden):
		return apierror.New(apierror.Forbidden, "not allowed")
	default:
		return err
	}
}

func (r *Resolver) createAPIToken(ctx context.Context, in model.CreateAPITokenInput) (*model.CreatedAPIToken, error) {
	if r.APITokens == nil {
		return nil, errAPITokensUnconfigured
	}
	ci := apiaccess.CreateInput{
		Name: in.Name, JobIDs: in.JobIds, NeverExpires: in.NeverExpires,
		AllowedCIDRs: in.AllowedCidrs, AllowAnyAddress: in.AllowAnyAddress,
	}
	if in.ExpiresAt != nil {
		ci.ExpiresAt = *in.ExpiresAt
	}
	c, err := r.APITokens.Create(ctx, ci)
	if err != nil {
		return nil, apiTokenError(err)
	}
	t := apiTokenModel(&c.Token)
	return &model.CreatedAPIToken{Token: &t, Plaintext: c.Plaintext}, nil
}

func (r *Resolver) apiTokens(ctx context.Context, jobID *uuid.UUID) ([]model.APIToken, error) {
	if r.APITokens == nil {
		return nil, errAPITokensUnconfigured
	}
	list, err := r.APITokens.List(ctx, jobID)
	if err != nil {
		return nil, apiTokenError(err)
	}
	out := make([]model.APIToken, len(list))
	for i := range list {
		out[i] = apiTokenModel(&list[i])
	}
	return out, nil
}

func (r *Resolver) revokeAPIToken(ctx context.Context, id uuid.UUID) (*model.APIToken, error) {
	if r.APITokens == nil {
		return nil, errAPITokensUnconfigured
	}
	t, err := r.APITokens.Revoke(ctx, id)
	if err != nil {
		return nil, apiTokenError(err)
	}
	m := apiTokenModel(&t)
	return &m, nil
}

func (r *Resolver) updateAPITokenAddresses(ctx context.Context, id uuid.UUID, cidrs []string, allowAny bool) (*model.APIToken, error) {
	if r.APITokens == nil {
		return nil, errAPITokensUnconfigured
	}
	t, err := r.APITokens.UpdateAddresses(ctx, id, cidrs, allowAny)
	if err != nil {
		return nil, apiTokenError(err)
	}
	m := apiTokenModel(&t)
	return &m, nil
}

// apiTokenModel converts a token view (never the secret or its hash).
func apiTokenModel(t *apiaccess.Token) model.APIToken {
	m := model.APIToken{
		ID: t.ID, Name: t.Name, Prefix: t.Prefix, Jobs: make([]model.APITokenJob, len(t.Jobs)),
		HiddenJobCount: t.HiddenJobs, CreatedByMe: t.CreatedByCaller, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt,
		NeverExpires: t.NeverExpires, RevokedAt: t.RevokedAt, AllowedCidrs: apiaccess.AddressStrings(t.AllowedCIDRs), AllowAnyAddress: t.AllowAnyAddress,
		LastUsedAt: t.LastUsedAt,
	}
	for i, j := range t.Jobs {
		m.Jobs[i] = model.APITokenJob{ID: j.ID, Name: j.Name, Deleted: j.Deleted}
	}
	if c := t.CreatedBy; c != nil {
		m.CreatedBy = &model.APITokenCreator{ID: c.ID, Email: c.Email, DisplayName: c.DisplayName, Deleted: c.Deleted}
	}
	if s := t.LastSeen; s != nil {
		at := s.At
		m.LastSeenAt = &at
		m.LastSeenRefused = s.Refused
		if s.IP != nil {
			ip := s.IP.String()
			m.LastSeenIP = &ip
		}
	}
	return m
}
