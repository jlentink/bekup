package resolvers

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/retention"
	"github.com/jlentink/bekup/internal/storage"
)

// Retention visibility (D-209): Job.retentionHolds, the retention estimate
// of previewSchedule and the unrecorded files of a destination.

// jobRetentionHolds returns the recorded holds of a job the parent field
// already authorized (job / jobs with JobScope), batch-loaded per page.
func (r *Resolver) jobRetentionHolds(ctx context.Context, obj *model.Job) ([]model.RetentionHold, error) {
	if r.RetentionHolds == nil {
		return []model.RetentionHold{}, nil
	}
	if err := r.Authz.Authorize(ctx, authz.OpJobList); err != nil {
		return nil, err
	}
	holds, err := r.loadRetentionHolds(ctx, obj.ID)
	if err != nil {
		return nil, err
	}
	out := make([]model.RetentionHold, 0, len(holds))
	for i := range holds {
		h := &holds[i]
		reason := model.RetentionHoldReasonNewerThanNewestComplete
		if h.Reason == retention.HoldNoCompleteRun {
			reason = model.RetentionHoldReasonNoCompleteRun
		}
		out = append(out, model.RetentionHold{
			DestinationID: h.DestinationID, DestinationName: h.DestinationName,
			DestinationType: destinationTypeModel(destinations.Type(h.DestinationType)), DestinationDeleted: h.DestinationDeleted,
			Reason: reason, HeldRuns: h.HeldRuns, Since: h.Since, NewestCompleteAt: h.NewestCompleteAt, CheckedAt: h.CheckedAt,
		})
	}
	return out, nil
}

// retentionEstimate computes SchedulePreview.retention.
func retentionEstimate(svc *jobs.Service, p jobs.Preview, in *model.JobRetentionInput) (*model.RetentionEstimate, error) {
	e, err := svc.EstimateRetention(p.Schedule, jobs.Retention{KeepLast: in.KeepLast, KeepDays: in.KeepDays})
	if err != nil {
		return nil, jobError(err)
	}
	out := &model.RetentionEstimate{ByKeepLast: e.ByKeepLast, AtLeast: e.AtLeast}
	if e.Known {
		kept, days := e.Kept, e.ByKeepDays
		out.Kept, out.ByKeepDays = &kept, &days
	}
	return out, nil
}

var (
	errNoDeleteMode = apierror.New(apierror.Validation,
		"the destination is in no-delete mode: bekup never deletes there (remove the file on the storage itself)")
	errUnrecordedGone = apierror.New(apierror.NotFound, "unrecorded file not found (it may have been removed or recorded meanwhile)")
	errNowRecorded    = apierror.New(apierror.Conflict, "the file belongs to a backup now; it was not deleted")
	errTooNew         = apierror.New(apierror.Conflict, "the file changed within the last 24 hours; it was not deleted")
)

func unrecordedError(err error) error {
	switch {
	case errors.Is(err, destinations.ErrNoDeleteMode):
		return errNoDeleteMode
	case errors.Is(err, destinations.ErrUnrecordedNotFound):
		return errUnrecordedGone
	case errors.Is(err, destinations.ErrUnrecordedNowRecorded):
		return errNowRecorded
	case errors.Is(err, destinations.ErrUnrecordedTooNew):
		return errTooNew
	default:
		return destinationError(err)
	}
}

func (r *Resolver) destinationUnrecordedFiles(ctx context.Context, destID uuid.UUID, first *int, after *string) (*model.UnrecordedFiles, error) {
	svc, _, _, err := r.destinationCaller(ctx)
	if err != nil {
		return nil, err
	}
	n := 0
	if first != nil {
		if n = *first; n < 1 || n > destinations.MaxUnrecordedPage {
			return nil, apierror.Invalid("first", "first must be between 1 and 200")
		}
	}
	cursor := ""
	if after != nil {
		cursor = strings.TrimSpace(*after)
	}
	page, err := svc.UnrecordedFiles(ctx, destID, n, cursor)
	if err != nil {
		return nil, unrecordedError(err)
	}
	out := &model.UnrecordedFiles{
		DestinationID: destID, ScannedAt: page.ScannedAt, TotalCount: page.TotalCount, TotalBytes: float64(page.TotalBytes),
		Truncated: page.Truncated, PrunedDirectories: page.PrunedDirs, Files: make([]model.UnrecordedFile, 0, len(page.Files)),
	}
	for _, f := range page.Files {
		out.Files = append(out.Files, model.UnrecordedFile{
			ID: f.ID, Key: f.Key, Size: float64(f.Size), ModifiedAt: f.ModifiedAt, FirstSeenAt: f.FirstSeenAt, LastSeenAt: f.LastSeenAt,
		})
	}
	if page.Next != "" {
		next := page.Next
		out.NextCursor = &next
	}
	return out, nil
}

func (r *Resolver) deleteUnrecordedFile(ctx context.Context, destID, fileID uuid.UUID) (*model.UnrecordedFileDeleteResult, error) {
	svc, _, actor, err := r.destinationCaller(ctx)
	if err != nil {
		return nil, err
	}
	code, err := svc.DeleteUnrecordedFile(ctx, actor, destID, fileID)
	if err != nil {
		return nil, unrecordedError(err)
	}
	return &model.UnrecordedFileDeleteResult{Ok: code == storage.CodeOK, Code: string(code), Message: code.Message()}, nil
}
