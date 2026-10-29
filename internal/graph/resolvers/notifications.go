package resolvers

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/graph/streams"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// Notification resolvers (task 5.3b, D-191): shared channels (admin), own
// channels, the preference matrix, the inbox and its part of accountEvents.
// Everything personal is scoped to the caller inside internal/notify;
// webhook URLs are only shown as their display form and signing keys only
// once, right after they were generated.

var (
	errNotificationsNotConfigured = errors.New("resolvers: notifications not configured")
	errNotificationNotFound       = apierror.New(apierror.NotFound, "not found")
)

func (r *Resolver) notifications() (*notify.API, error) {
	if r.Notifications == nil {
		return nil, errNotificationsNotConfigured
	}
	return r.Notifications, nil
}

// notifyError maps service errors to API errors.
func notifyError(err error) error {
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
		return apierror.Newf(apierror.RateLimited, "too many test sends; try again in %d seconds",
			httpserver.RetryAfterSeconds(rl.RetryAfter))
	case errors.Is(err, serviceutil.ErrBusy):
		return errBusy
	case errors.Is(err, notify.ErrNotFound):
		return errNotificationNotFound
	case errors.Is(err, notify.ErrNameTaken):
		return apierror.Invalid("name", "another shared channel already uses this name")
	case errors.Is(err, notify.ErrTooManyWebhooks):
		return apierror.New(apierror.Validation, "you already have 5 personal webhooks; delete one first")
	case errors.Is(err, notify.ErrUnavailable):
		return apierror.New(apierror.Conflict, "sending is not set up on this server; ask an administrator")
	default:
		return err
	}
}

// keyString returns the one-time display form of a signing key and
// zeroes the bytes.
func keyString(k []byte) string {
	s := string(k)
	crypto.Zero(k)
	return s
}

// --- enums ---------------------------------------------------------------------

func groupModel(g notify.Group) model.NotificationGroup {
	switch g {
	case notify.GroupBackup:
		return model.NotificationGroupBackup
	case notify.GroupRestore:
		return model.NotificationGroupRestore
	case notify.GroupRetention:
		return model.NotificationGroupRetention
	case notify.GroupConfig:
		return model.NotificationGroupConfig
	default:
		return model.NotificationGroupSecurity
	}
}

func groupFromModel(g model.NotificationGroup) notify.Group {
	switch g {
	case model.NotificationGroupBackup:
		return notify.GroupBackup
	case model.NotificationGroupRestore:
		return notify.GroupRestore
	case model.NotificationGroupRetention:
		return notify.GroupRetention
	case model.NotificationGroupConfig:
		return notify.GroupConfig
	case model.NotificationGroupSecurity:
		return notify.GroupSecurity
	default:
		return notify.Group(g)
	}
}

func channelModel(c notify.Channel) model.NotificationChannelType {
	switch c {
	case notify.ChannelEmail:
		return model.NotificationChannelTypeEmail
	case notify.ChannelWebhook:
		return model.NotificationChannelTypeWebhook
	default:
		return model.NotificationChannelTypeInbox
	}
}

func channelFromModel(c model.NotificationChannelType) (notify.Channel, bool) {
	switch c {
	case model.NotificationChannelTypeInbox:
		return notify.ChannelInbox, true
	case model.NotificationChannelTypeEmail:
		return notify.ChannelEmail, true
	case model.NotificationChannelTypeWebhook:
		return notify.ChannelWebhook, true
	default:
		return "", false
	}
}

func severityModel(s string) model.NotificationSeverity {
	switch notify.Severity(s) {
	case notify.SeverityWarning:
		return model.NotificationSeverityWarning
	case notify.SeverityError:
		return model.NotificationSeverityError
	case notify.SeverityCritical:
		return model.NotificationSeverityCritical
	default:
		return model.NotificationSeverityInfo
	}
}

func audienceModel(a notify.Audience) model.NotificationAudience {
	switch a {
	case notify.AudienceStaff:
		return model.NotificationAudienceStaff
	case notify.AudienceAdmins:
		return model.NotificationAudienceAdmins
	case notify.AudienceOwnAccount:
		return model.NotificationAudienceOwnAccount
	default:
		return model.NotificationAudienceJob
	}
}

func sourceModel(s notify.Source) model.NotificationPreferenceSource {
	switch s {
	case notify.SourceOverride:
		return model.NotificationPreferenceSourceOverride
	case notify.SourceMandatory:
		return model.NotificationPreferenceSourceMandatory
	default:
		return model.NotificationPreferenceSourceDefault
	}
}

// --- models ----------------------------------------------------------------------

func eventInfoModel(m notify.Meta) *model.NotificationEventInfo {
	out := &model.NotificationEventInfo{
		Type: string(m.Type), Label: m.Label, Group: groupModel(m.Group), Audience: audienceModel(m.Audience),
		Problem: m.Problem, Mandatory: m.Mandatory, Priority: model.NotificationPriorityNormal,
		Severity: severityModel(string(m.Severity)), PerJob: m.Subject == notify.SubjectJob,
		DefaultChannels: []model.NotificationChannelType{},
	}
	if m.Priority == notify.PriorityLow {
		out.Priority = model.NotificationPriorityLow
	}
	for _, c := range notify.DefaultChannels(m) {
		out.DefaultChannels = append(out.DefaultChannels, channelModel(c))
	}
	return out
}

func stateModel(s notify.ChannelState) *model.NotificationChannelState {
	return &model.NotificationChannelState{
		Enabled: s.Enabled, DisabledAt: s.DisabledAt, DisabledReason: nonEmpty(s.DisabledReason),
		DisabledMessage: nonEmpty(s.DisabledMessage), ConsecutiveFailures: s.ConsecutiveFailures,
		PermanentFailures: s.PermanentFailures, LastFailureAt: s.LastFailureAt, LastFailureCode: nonEmpty(s.LastFailureCode),
	}
}

func sharedChannelModel(c *notify.SharedChannel) *model.SharedChannel {
	out := &model.SharedChannel{
		ID: c.ID, Name: c.Name, Type: model.SharedChannelTypeWebhook, Addresses: c.Addresses,
		DisplayURL: nonEmpty(c.DisplayURL), State: stateModel(c.State),
		Attachments: make([]model.SharedChannelAttachment, 0, len(c.Attachments)),
		CreatedAt:   c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	if c.Type == sqlc.SharedChannelTypeEmailList {
		out.Type = model.SharedChannelTypeEmailList
	}
	if out.Addresses == nil {
		out.Addresses = []string{}
	}
	for _, a := range c.Attachments {
		m := model.SharedChannelAttachment{JobID: a.JobID, JobName: a.JobName, Groups: []model.NotificationGroup{}, Events: []string{}}
		for _, g := range a.Groups {
			m.Groups = append(m.Groups, groupModel(g))
		}
		for _, e := range a.Events {
			m.Events = append(m.Events, string(e))
		}
		out.Attachments = append(out.Attachments, m)
	}
	return out
}

func personalWebhookModel(w *notify.PersonalWebhook) *model.PersonalWebhook {
	return &model.PersonalWebhook{ID: w.ID, Label: w.Label, DisplayURL: w.DisplayURL, State: stateModel(w.State), CreatedAt: w.CreatedAt}
}

func testResultModel(t notify.TestResult) *model.NotificationTestResult {
	out := &model.NotificationTestResult{Ok: t.OK, Code: nonEmpty(t.Code), Message: nonEmpty(t.Message)}
	if t.StatusCode > 0 {
		sc := t.StatusCode
		out.StatusCode = &sc
	}
	return out
}

func eventPrefsModel(e *notify.EventPrefs) model.NotificationEventPreference {
	out := model.NotificationEventPreference{
		Event:        eventInfoModel(e.Event),
		Channels:     make([]model.NotificationChannelPreference, 0, len(e.Channels)),
		JobOverrides: make([]model.NotificationJobOverride, 0, len(e.Overrides)),
	}
	for _, c := range e.Channels {
		out.Channels = append(out.Channels, model.NotificationChannelPreference{
			Channel: channelModel(c.Channel), Enabled: c.Enabled, Source: sourceModel(c.Source), Locked: c.Locked,
		})
	}
	for _, o := range e.Overrides {
		out.JobOverrides = append(out.JobOverrides, model.NotificationJobOverride{
			JobID: o.JobID, JobName: o.JobName, Channel: channelModel(o.Channel), Enabled: o.Enabled,
		})
	}
	return out
}

func preferencesModel(p *notify.Preferences) *model.NotificationPreferences {
	out := &model.NotificationPreferences{
		Groups: make([]model.NotificationPreferenceGroup, 0, len(p.Groups)), EmailVerified: p.EmailVerified,
		ActiveWebhooks: p.ActiveWebhooks,
	}
	for _, g := range p.Groups {
		mg := model.NotificationPreferenceGroup{
			Group: groupModel(g.Group), Label: notify.GroupLabel(g.Group),
			Events: make([]model.NotificationEventPreference, 0, len(g.Events)),
		}
		for i := range g.Events {
			mg.Events = append(mg.Events, eventPrefsModel(&g.Events[i]))
		}
		out.Groups = append(out.Groups, mg)
	}
	return out
}

func notificationModel(n *sqlc.Notification) *model.Notification {
	out := &model.Notification{
		ID: n.ID, Type: n.Type, Severity: severityModel(n.Severity), Title: n.Title, Body: n.Body, Link: n.Link,
		CreatedAt: n.CreatedAt, ReadAt: n.ReadAt,
	}
	if m, ok := notify.Lookup(notify.EventType(n.Type)); ok {
		g := groupModel(m.Group)
		out.Group = &g
	}
	var obj map[string]any
	if len(n.Payload) > 0 && json.Unmarshal(n.Payload, &obj) == nil && len(obj) > 0 {
		out.Data = model.JSON(n.Payload)
	}
	return out
}

// --- shared channels -----------------------------------------------------------------

func (r *Resolver) sharedChannels(ctx context.Context) ([]model.SharedChannel, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	list, err := api.SharedChannels(ctx)
	if err != nil {
		return nil, notifyError(err)
	}
	out := make([]model.SharedChannel, 0, len(list))
	for i := range list {
		out = append(out, *sharedChannelModel(&list[i]))
	}
	return out, nil
}

func sharedTypeFromModel(t model.SharedChannelType) sqlc.SharedChannelType {
	if t == model.SharedChannelTypeEmailList {
		return sqlc.SharedChannelTypeEmailList
	}
	if t == model.SharedChannelTypeWebhook {
		return sqlc.SharedChannelTypeWebhook
	}
	return sqlc.SharedChannelType(t)
}

func (r *Resolver) keyedShared(k notify.KeyedSharedChannel) *model.SharedChannelWithKey {
	out := &model.SharedChannelWithKey{Channel: sharedChannelModel(&k.Channel)}
	if k.Key != nil {
		s := keyString(k.Key)
		out.SigningKey = &s
	}
	return out
}

func (r *Resolver) createSharedChannel(ctx context.Context, in model.CreateSharedChannelInput) (*model.SharedChannelWithKey, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	k, err := api.CreateSharedChannel(ctx, notify.SharedChannelInput{
		Name: in.Name, Type: sharedTypeFromModel(in.Type), Addresses: in.Addresses, URL: derefString(in.URL),
	})
	if err != nil {
		return nil, notifyError(err)
	}
	return r.keyedShared(k), nil
}

func (r *Resolver) updateSharedChannel(ctx context.Context, id uuid.UUID, in model.UpdateSharedChannelInput) (*model.SharedChannel, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	c, err := api.UpdateSharedChannel(ctx, id, notify.SharedChannelUpdate{Name: in.Name, Addresses: in.Addresses, URL: in.URL})
	if err != nil {
		return nil, notifyError(err)
	}
	return sharedChannelModel(&c), nil
}

func (r *Resolver) rotateSharedChannelKey(ctx context.Context, id uuid.UUID) (*model.SharedChannelWithKey, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	k, err := api.RotateSharedChannelKey(ctx, id)
	if err != nil {
		return nil, notifyError(err)
	}
	return r.keyedShared(k), nil
}

func (r *Resolver) deleteSharedChannel(ctx context.Context, id uuid.UUID) (bool, error) {
	api, err := r.notifications()
	if err != nil {
		return false, err
	}
	if err := api.DeleteSharedChannel(ctx, id); err != nil {
		return false, notifyError(err)
	}
	return true, nil
}

func (r *Resolver) setSharedChannelEnabled(ctx context.Context, id uuid.UUID, enabled bool) (*model.SharedChannel, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	c, err := api.SetSharedChannelEnabled(ctx, id, enabled)
	if err != nil {
		return nil, notifyError(err)
	}
	return sharedChannelModel(&c), nil
}

func (r *Resolver) testSharedChannel(ctx context.Context, id uuid.UUID) (*model.NotificationTestResult, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	res, err := api.TestSharedChannel(ctx, id)
	if err != nil {
		return nil, notifyError(err)
	}
	return testResultModel(res), nil
}

func (r *Resolver) attachSharedChannel(ctx context.Context, jobID, channelID uuid.UUID, f *model.NotificationFilterInput) (*model.SharedChannel, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	var groups []notify.Group
	var events []notify.EventType
	if f != nil {
		for _, g := range f.Groups {
			groups = append(groups, groupFromModel(g))
		}
		for _, e := range f.Events {
			events = append(events, notify.EventType(e))
		}
	}
	c, err := api.AttachSharedChannel(ctx, jobID, channelID, groups, events)
	if err != nil {
		return nil, notifyError(err)
	}
	return sharedChannelModel(&c), nil
}

func (r *Resolver) detachSharedChannel(ctx context.Context, jobID, channelID uuid.UUID) (*model.SharedChannel, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	c, err := api.DetachSharedChannel(ctx, jobID, channelID)
	if err != nil {
		return nil, notifyError(err)
	}
	return sharedChannelModel(&c), nil
}

// --- own channels ----------------------------------------------------------------------

func (r *Resolver) myNotificationChannels(ctx context.Context) (*model.MyNotificationChannels, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	c, err := api.MyChannels(ctx)
	if err != nil {
		return nil, notifyError(err)
	}
	out := &model.MyNotificationChannels{
		Email: c.Email, EmailVerified: c.EmailVerifiedAt != nil, EmailVerifiedAt: c.EmailVerifiedAt,
		Webhooks: make([]model.PersonalWebhook, 0, len(c.Webhooks)), MaxWebhooks: notify.MaxPersonalWebhooks,
	}
	for i := range c.Webhooks {
		out.Webhooks = append(out.Webhooks, *personalWebhookModel(&c.Webhooks[i]))
	}
	return out, nil
}

func keyedPersonal(k notify.KeyedPersonalWebhook) *model.PersonalWebhookWithKey {
	return &model.PersonalWebhookWithKey{Webhook: personalWebhookModel(&k.Webhook), SigningKey: keyString(k.Key)}
}

func (r *Resolver) addMyWebhook(ctx context.Context, url, label string) (*model.PersonalWebhookWithKey, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	k, err := api.AddMyWebhook(ctx, url, label)
	if err != nil {
		return nil, notifyError(err)
	}
	return keyedPersonal(k), nil
}

func (r *Resolver) rotateMyWebhookKey(ctx context.Context, id uuid.UUID) (*model.PersonalWebhookWithKey, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	k, err := api.RotateMyWebhookKey(ctx, id)
	if err != nil {
		return nil, notifyError(err)
	}
	return keyedPersonal(k), nil
}

func (r *Resolver) deleteMyWebhook(ctx context.Context, id uuid.UUID) (bool, error) {
	api, err := r.notifications()
	if err != nil {
		return false, err
	}
	if err := api.DeleteMyWebhook(ctx, id); err != nil {
		return false, notifyError(err)
	}
	return true, nil
}

func (r *Resolver) testMyWebhook(ctx context.Context, id uuid.UUID) (*model.NotificationTestResult, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	res, err := api.TestMyWebhook(ctx, id)
	if err != nil {
		return nil, notifyError(err)
	}
	return testResultModel(res), nil
}

func (r *Resolver) reenableMyWebhook(ctx context.Context, id uuid.UUID) (*model.PersonalWebhook, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	w, err := api.ReenableMyWebhook(ctx, id)
	if err != nil {
		return nil, notifyError(err)
	}
	return personalWebhookModel(&w), nil
}

// --- preferences ------------------------------------------------------------------------

func (r *Resolver) notificationCatalog(ctx context.Context) ([]model.NotificationEventGroup, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	groups, err := api.Catalog(ctx)
	if err != nil {
		return nil, notifyError(err)
	}
	out := make([]model.NotificationEventGroup, 0, len(groups))
	for _, g := range groups {
		mg := model.NotificationEventGroup{
			Group: groupModel(g.Group), Label: notify.GroupLabel(g.Group),
			Events: make([]model.NotificationEventInfo, 0, len(g.Events)),
		}
		for i := range g.Events {
			mg.Events = append(mg.Events, *eventInfoModel(g.Events[i]))
		}
		out = append(out, mg)
	}
	return out, nil
}

func (r *Resolver) myNotificationPreferences(ctx context.Context) (*model.NotificationPreferences, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	p, err := api.MyPreferences(ctx)
	if err != nil {
		return nil, notifyError(err)
	}
	return preferencesModel(&p), nil
}

func (r *Resolver) setNotificationPreference(ctx context.Context, event string, ch model.NotificationChannelType, enabled bool, jobID *uuid.UUID) (*model.NotificationEventPreference, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	c, ok := channelFromModel(ch)
	if !ok {
		return nil, apierror.Invalid("channel", "unknown channel")
	}
	if len(event) > 64 {
		return nil, apierror.Invalid("event", "unknown event")
	}
	row, err := api.SetPreference(ctx, notify.EventType(event), c, enabled, jobID)
	if err != nil {
		return nil, notifyError(err)
	}
	m := eventPrefsModel(&row)
	return &m, nil
}

func (r *Resolver) resetNotificationPreferences(ctx context.Context, jobID *uuid.UUID) (*model.NotificationPreferences, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	p, err := api.ResetPreferences(ctx, jobID)
	if err != nil {
		return nil, notifyError(err)
	}
	return preferencesModel(&p), nil
}

// --- inbox -------------------------------------------------------------------------------

const inboxCursorPrefix = "n:"

// inboxFilter merges the unreadOnly argument and the filter input.
func inboxFilter(unreadOnly *bool, in *model.NotificationInboxFilter) notify.InboxFilter {
	f := notify.InboxFilter{UnreadOnly: unreadOnly != nil && *unreadOnly}
	if in == nil {
		return f
	}
	if in.UnreadOnly != nil && *in.UnreadOnly {
		f.UnreadOnly = true
	}
	if in.Group != nil {
		g := groupFromModel(*in.Group)
		f.Group = &g
	}
	f.Events = in.Events
	return f
}

func (r *Resolver) inbox(ctx context.Context, unreadOnly *bool, filter *model.NotificationInboxFilter, first *int, after *string) (*model.NotificationConnection, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	limit, err := runPageSize(first, notify.DefaultInboxPage, notify.MaxInboxPage)
	if err != nil {
		return nil, err
	}
	t, id, ok, err := decodeTimeCursor(inboxCursorPrefix, after)
	if err != nil {
		return nil, err
	}
	var cur *notify.InboxCursor
	if ok {
		cur = &notify.InboxCursor{CreatedAt: t, ID: id}
	}
	page, err := api.Notifications(ctx, inboxFilter(unreadOnly, filter), cur, limit)
	if err != nil {
		return nil, notifyError(err)
	}
	out := &model.NotificationConnection{
		Edges:    make([]model.NotificationEdge, 0, len(page.Items)),
		PageInfo: &model.PageInfo{HasNextPage: page.HasNext, HasPreviousPage: after != nil},
	}
	for i := range page.Items {
		n := &page.Items[i]
		out.Edges = append(out.Edges, model.NotificationEdge{
			Cursor: encodeTimeCursor(inboxCursorPrefix, n.CreatedAt, n.ID), Node: notificationModel(n),
		})
	}
	if len(out.Edges) > 0 {
		out.PageInfo.StartCursor = &out.Edges[0].Cursor
		out.PageInfo.EndCursor = &out.Edges[len(out.Edges)-1].Cursor
	}
	return out, nil
}

func (r *Resolver) unreadNotificationCount(ctx context.Context) (int, error) {
	api, err := r.notifications()
	if err != nil {
		return 0, err
	}
	n, err := api.UnreadCount(ctx)
	if err != nil {
		return 0, notifyError(err)
	}
	return n, nil
}

func (r *Resolver) markNotificationsRead(ctx context.Context, ids []uuid.UUID, all *bool) (*model.MarkNotificationsReadResult, error) {
	api, err := r.notifications()
	if err != nil {
		return nil, err
	}
	marked, unread, err := api.MarkRead(ctx, ids, all != nil && *all)
	if err != nil {
		return nil, notifyError(err)
	}
	return &model.MarkNotificationsReadResult{Marked: marked, UnreadCount: unread}, nil
}

func (r *Resolver) deleteNotification(ctx context.Context, id uuid.UUID) (bool, error) {
	api, err := r.notifications()
	if err != nil {
		return false, err
	}
	if err := api.DeleteNotification(ctx, id); err != nil {
		return false, notifyError(err)
	}
	return true, nil
}

// --- inbox stream (accountEvents) ------------------------------------------------------------------

const (
	// inboxEventBuffer is the bus buffer of one stream (overflow: resync).
	inboxEventBuffer = 64
	// inboxRetryGap and inboxRetries re-load an announced row that is not
	// visible yet (the local bus may announce it before the commit).
	inboxRetryGap = 250 * time.Millisecond
	inboxRetries  = 8
	// inboxMaxPending caps the rows waiting for a retry; beyond it the
	// stream resyncs.
	inboxMaxPending = 64
	// inboxSeen is how many delivered ids a stream remembers (the bus and
	// NOTIFY may both announce a row).
	inboxSeen = 512
	// inboxResyncMax caps the rows re-sent after a resync.
	inboxResyncMax = 100
)

// inboxEvent is one inbox row loaded for the stream's own account.
type inboxEvent struct{ row sqlc.Notification }

// inboxSource loads inbox rows of one account (*notify.API).
type inboxSource interface {
	Notification(ctx context.Context, userID, id uuid.UUID) (sqlc.Notification, error)
	NotificationsSince(ctx context.Context, userID uuid.UUID, since time.Time, limit int) ([]sqlc.Notification, error)
}

// inboxFeed streams new rows of the caller's own inbox (the
// notification half of accountEvents). The payload's ids only
// pre-filter; each row is loaded scoped to the stream's account and
// re-authorized per event (notification.inbox with the stream's current
// principal, which must still be that account). The source runs until
// ctx ends.
func (r *Resolver) inboxFeed(ctx context.Context, user uuid.UUID) (streams.Feed[*inboxEvent, *model.Notification], error) {
	var none streams.Feed[*inboxEvent, *model.Notification]
	api, err := r.notifications()
	if err != nil {
		return none, err
	}
	if r.Bus == nil {
		return none, errNotificationsNotConfigured
	}
	sub := r.Bus.Subscribe(pubsub.NotificationEvents.Name, inboxEventBuffer)
	src := make(chan *inboxEvent)
	go pumpInboxEvents(ctx, sub, src, api, user, inboxRetryGap)
	check := streams.Authorize(r.Authz, authz.OpNotificationInbox, func(ctx context.Context, ev *inboxEvent) ([]authz.Resource, error) {
		p, ok := auth.FromContext(ctx)
		if !ok || p.UserID != ev.row.UserID {
			return nil, errNotificationNotFound
		}
		return nil, nil
	})
	conv := func(_ context.Context, ev *inboxEvent) (*model.Notification, bool) {
		return notificationModel(&ev.row), true
	}
	return streams.Feed[*inboxEvent, *model.Notification]{Src: src, Check: check, Conv: conv}, nil
}

// seenSet remembers the last n ids.
type seenSet struct {
	n     int
	order []uuid.UUID
	set   map[uuid.UUID]bool
}

func newSeenSet(n int) *seenSet { return &seenSet{n: n, set: make(map[uuid.UUID]bool, n)} }

func (s *seenSet) has(id uuid.UUID) bool { return s.set[id] }

func (s *seenSet) add(id uuid.UUID) {
	if s.set[id] {
		return
	}
	if len(s.order) >= s.n {
		delete(s.set, s.order[0])
		s.order = s.order[1:]
	}
	s.order = append(s.order, id)
	s.set[id] = true
}

// pumpInboxEvents turns the bus messages for user into loaded inbox rows
// until ctx ends: a row not visible yet is retried a few times (it may
// be announced before its commit), each row is sent once, and after
// possibly lost messages (bus overflow, listener reconnect, too many
// pending retries) the rows created since the last resync are re-sent.
func pumpInboxEvents(ctx context.Context, sub *pubsub.Subscription, src chan<- *inboxEvent, api inboxSource, user uuid.UUID, gap time.Duration) {
	defer close(src)
	defer sub.Close()
	since := time.Now()
	seen := newSeenSet(inboxSeen)
	type pending struct {
		id    uuid.UUID
		tries int
	}
	var retries []pending
	timer := time.NewTimer(gap)
	timer.Stop()
	defer timer.Stop()
	armed := false
	send := func(n sqlc.Notification) bool {
		if seen.has(n.ID) {
			return true
		}
		seen.add(n.ID)
		select {
		case src <- &inboxEvent{row: n}:
			return true
		case <-ctx.Done():
			return false
		}
	}
	resync := func() bool {
		now := time.Now()
		rows, err := api.NotificationsSince(ctx, user, since.Add(-resyncMargin), inboxResyncMax)
		if ctx.Err() != nil {
			return false
		}
		if err != nil {
			return true
		}
		since = now
		for i := range rows {
			if !send(rows[i]) {
				return false
			}
		}
		return true
	}
	try := func(id uuid.UUID, tries int) bool {
		if seen.has(id) {
			return true
		}
		n, err := api.Notification(ctx, user, id)
		switch {
		case err == nil:
			return send(n)
		case ctx.Err() != nil:
			return false
		case tries < inboxRetries && len(retries) < inboxMaxPending:
			retries = append(retries, pending{id: id, tries: tries + 1})
			if !armed {
				timer.Reset(gap)
				armed = true
			}
		case tries < inboxRetries:
			return resync()
		}
		return true
	}
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-sub.C():
			id, uid, err := notify.ParseInboxPayload(m.Payload)
			if err != nil || uid != user {
				continue
			}
			if !try(id, 0) {
				return
			}
		case <-timer.C:
			armed = false
			due := retries
			retries = nil
			for _, p := range due {
				if !try(p.id, p.tries) {
					return
				}
			}
		case <-sub.Resync():
			if !resync() {
				return
			}
		}
	}
}
