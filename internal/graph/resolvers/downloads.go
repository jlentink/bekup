package resolvers

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/downloads"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// downloadError maps the download service's errors to API errors.
func downloadError(err error) error {
	var (
		fe *serviceutil.FieldError
		rl *serviceutil.RateLimitedError
	)
	switch {
	case err == nil:
		return nil
	case errors.As(err, &fe):
		return apierror.Invalid(fe.Field, fe.Message)
	case errors.As(err, &rl):
		return apierror.New(apierror.RateLimited, "too many open download tickets; use or let them expire first")
	case errors.Is(err, downloads.ErrNotFound):
		return apierror.New(apierror.NotFound, "artifact not found")
	case errors.Is(err, downloads.ErrUnavailable):
		return apierror.New(apierror.Conflict, "the artifact is no longer available")
	case errors.Is(err, downloads.ErrUnverified):
		return apierror.New(apierror.Conflict, "the artifact failed its verification and cannot be downloaded")
	case errors.Is(err, downloads.ErrUnsigned):
		return apierror.New(apierror.Conflict, "the artifact is not covered by a signed manifest and cannot be downloaded")
	case errors.Is(err, downloads.ErrKeyUnavailable):
		return apierror.New(apierror.Conflict, "the key of this artifact is not available")
	default:
		return err
	}
}

func (r *Resolver) createDownloadTicket(ctx context.Context, artifactID uuid.UUID, mode model.DownloadMode, ageIdentity *string) (*model.DownloadTicket, error) {
	if r.Downloads == nil {
		return nil, errors.New("resolvers: downloads service not configured")
	}
	in := downloads.CreateInput{ArtifactID: artifactID, Mode: downloads.ModeRaw}
	if mode == model.DownloadModePlain {
		in.Mode = downloads.ModePlain
	}
	if ageIdentity != nil && *ageIdentity != "" {
		in.Identity = []byte(*ageIdentity)
		defer crypto.Zero(in.Identity)
	}
	t, err := r.Downloads.CreateTicket(ctx, in)
	if err != nil {
		return nil, downloadError(err)
	}
	return &model.DownloadTicket{
		URL: t.URL, ExpiresAt: t.ExpiresAt, Mode: mode, FileName: t.FileName, Format: t.Format,
		RestoreCommand: t.RestoreCommand,
	}, nil
}
