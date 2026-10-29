package resolvers

import (
	"context"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/notify"
)

// jobSharedChannelAttachments lists the shared channels attached to a job
// (D-198): staff only, null for backup owners (like Job.owners), batch-
// loaded for every hinted job of the page. The job itself was loaded with
// the caller's rights by the parent field.
func (r *Resolver) jobSharedChannelAttachments(ctx context.Context, obj *model.Job) ([]model.JobSharedChannelAttachment, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	if err := r.Authz.Authorize(ctx, authz.OpSharedChannelAttachmentView); err != nil {
		return nil, nil //nolint:nilerr // not visible to this role: null, not an error
	}
	atts, err := batchLoad(ctx, loaderFrom(ctx), func(l *runLoader) map[uuid.UUID][]notify.JobAttachment { return l.channels },
		jobHintsOf, obj.ID, api.JobAttachments)
	if err != nil {
		return nil, notifyError(err)
	}
	out := make([]model.JobSharedChannelAttachment, 0, len(atts))
	for _, a := range atts {
		m := model.JobSharedChannelAttachment{
			ChannelID: a.ChannelID, Name: a.Name, Type: model.SharedChannelTypeWebhook, DisplayURL: nonEmpty(a.DisplayURL),
			Enabled: a.Enabled, Groups: make([]model.NotificationGroup, 0, len(a.Groups)), Events: make([]string, 0, len(a.Events)),
		}
		if a.Type == sqlc.SharedChannelTypeEmailList {
			m.Type = model.SharedChannelTypeEmailList
		}
		for _, g := range a.Groups {
			m.Groups = append(m.Groups, groupModel(g))
		}
		for _, e := range a.Events {
			m.Events = append(m.Events, string(e))
		}
		out = append(out, m)
	}
	return out, nil
}
