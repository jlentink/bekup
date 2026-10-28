package resolvers

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/jobs"
)

// Run resolvers (task 3.6, D-147). cancelRun is staff-only (run.cancel,
// D-106): owners may start runs of their jobs but not cancel them.

// runError maps the run errors of internal/jobs.
func runError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, jobs.ErrRunNotFound):
		return apierror.New(apierror.NotFound, "run not found")
	case errors.Is(err, jobs.ErrRunFinished):
		return apierror.New(apierror.Conflict, "the run already finished")
	default:
		return jobError(err)
	}
}

func (r *Resolver) cancelRun(ctx context.Context, id uuid.UUID) (*model.RunCancellation, error) {
	svc, actor, err := r.jobCaller(ctx)
	if err != nil {
		return nil, err
	}
	if err := r.Authz.Authorize(ctx, authz.OpRunCancel); err != nil {
		return nil, err
	}
	c, err := svc.CancelRun(ctx, actor, id)
	if err != nil {
		return nil, runError(err)
	}
	st := model.RunStatus(strings.ToUpper(c.Status))
	if !st.IsValid() {
		st = model.RunStatusCanceled
	}
	return &model.RunCancellation{RunID: c.RunID, JobID: c.JobID, Status: st, RequestedAt: c.RequestedAt, RetryCanceled: c.RetryCanceled}, nil
}
