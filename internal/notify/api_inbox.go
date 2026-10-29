package notify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/pubsub"
)

// Inbox limits.
const (
	DefaultInboxPage = 50
	MaxInboxPage     = 200
	// MaxMarkRead caps the ids of one markNotificationsRead call.
	MaxMarkRead = 200
)

// inboxPayloadPrefix starts the pubsub payload of a new inbox row.
const inboxPayloadPrefix = "inbox:"

// InboxPayload is the pubsub.NotificationEvents payload of a new inbox
// row: identifiers only.
func InboxPayload(notificationID, userID uuid.UUID) string {
	return inboxPayloadPrefix + notificationID.String() + ":" + userID.String()
}

// ParseInboxPayload parses an InboxPayload.
func ParseInboxPayload(s string) (uuid.UUID, uuid.UUID, error) {
	rest, ok := strings.CutPrefix(s, inboxPayloadPrefix)
	if !ok {
		return uuid.Nil, uuid.Nil, errors.New("notify: not an inbox payload")
	}
	a, b, ok := strings.Cut(rest, ":")
	if !ok {
		return uuid.Nil, uuid.Nil, errors.New("notify: malformed inbox payload")
	}
	notificationID, err1 := uuid.Parse(a)
	userID, err2 := uuid.Parse(b)
	// Only the canonical form InboxPayload writes.
	if err1 != nil || err2 != nil || notificationID.String() != a || userID.String() != b {
		return uuid.Nil, uuid.Nil, errors.New("notify: malformed inbox payload")
	}
	return notificationID, userID, nil
}

// insertInbox writes an inbox row and announces it (IDs only) in the same
// transaction.
func (s *Service) insertInbox(ctx context.Context, q *sqlc.Queries, p sqlc.InsertInboxNotificationParams) error {
	ids, err := q.InsertInboxNotification(ctx, p)
	if err != nil {
		return fmt.Errorf("notify: insert inbox row: %w", err)
	}
	if len(ids) == 0 || s.o.Publisher == nil {
		return nil
	}
	return s.o.Publisher.PublishTx(ctx, q, pubsub.NotificationEvents.Name, InboxPayload(ids[0], p.UserID))
}

// InboxCursor is the position after which a page starts.
type InboxCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// InboxPage is one page of the own inbox, newest first.
type InboxPage struct {
	Items   []sqlc.Notification
	HasNext bool
}

// MaxInboxFilterEvents caps the event types of one inbox filter.
const MaxInboxFilterEvents = 64

// InboxFilter narrows the inbox (D-196); the zero value matches every
// notification.
type InboxFilter struct {
	UnreadOnly bool
	// Group keeps the events of one catalog group (nil: any).
	Group *Group
	// Events keeps these event types (catalog types or notices; empty:
	// any). With Group, an event must match both.
	Events []string
}

// types resolves the filter to the event types to match (nil: no type
// filter). An empty, non-nil result matches nothing.
func (f InboxFilter) types() ([]string, error) {
	if f.Group == nil && len(f.Events) == 0 {
		return nil, nil
	}
	if f.Group != nil && !slices.Contains(Groups(), *f.Group) {
		return nil, fieldErr("filter.group", "unknown group")
	}
	if len(f.Events) > MaxInboxFilterEvents {
		return nil, fieldErr("filter.events", "at most %d event types", MaxInboxFilterEvents)
	}
	var events []string
	for i, e := range f.Events {
		if _, ok := Lookup(EventType(e)); !ok && e != NoticeWebhookDisabled {
			return nil, fieldErr(fmt.Sprintf("filter.events.%d", i), "unknown event type")
		}
		if !slices.Contains(events, e) {
			events = append(events, e)
		}
	}
	if f.Group == nil {
		return events, nil
	}
	out := []string{}
	all := Catalog()
	for i := range all {
		if all[i].Group == *f.Group && (len(events) == 0 || slices.Contains(events, string(all[i].Type))) {
			out = append(out, string(all[i].Type))
		}
	}
	return out, nil
}

// inboxView is the read-time visibility of a reader's inbox (M5 review
// L5): rows stay stored, but the reader only sees what their current role
// and job ownership allow.
type inboxView struct {
	// hidden are the event types the reader's role may no longer see
	// (staff and admin audiences after a demotion, e.g. security.system
	// for a former admin).
	hidden []string
	// ownedOnly hides rows about jobs the reader does not own (role user).
	ownedOnly bool
}

// inboxVisibility derives the reader's inbox view from their current role
// in the database (not the session: the stream reloads with a user id
// only). An inactive account sees nothing (ErrNotFound).
func (a *API) inboxVisibility(ctx context.Context, q *sqlc.Queries, userID uuid.UUID) (inboxView, error) {
	u, err := q.GetNotifiableUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return inboxView{}, ErrNotFound
	}
	if err != nil {
		return inboxView{}, fmt.Errorf("notify: load reader: %w", err)
	}
	return visibilityFor(ctx, a.o.Policy, userID, auth.Role(u.Role))
}

// visibilityFor computes the view of a reader with role (pure apart from
// the policy decision; the same synthetic principal as delivery uses).
func visibilityFor(ctx context.Context, policy *authz.Policy, userID uuid.UUID, role auth.Role) (inboxView, error) {
	v := inboxView{hidden: []string{}}
	pctx := auth.WithPrincipal(ctx, auth.Principal{UserID: userID, Role: role, SessionID: deliverySession})
	all := Catalog()
	for i := range all {
		m := &all[i]
		switch m.Audience {
		case AudienceStaff, AudienceAdmins:
			err := policy.Authorize(pctx, m.Op)
			var ae *apierror.Error
			switch {
			case err == nil:
			case errors.As(err, &ae):
				v.hidden = append(v.hidden, string(m.Type))
			default:
				return inboxView{}, fmt.Errorf("notify: authorize reader: %w", err)
			}
		case AudienceJob, AudienceOwnAccount:
		}
	}
	scope, _, err := policy.JobScope(pctx)
	if err != nil {
		var ae *apierror.Error
		if !errors.As(err, &ae) {
			return inboxView{}, fmt.Errorf("notify: job scope of reader: %w", err)
		}
		scope = authz.ScopeOwned
	}
	v.ownedOnly = scope != authz.ScopeAll
	return v, nil
}

// Notifications lists the caller's inbox, newest first.
func (a *API) Notifications(ctx context.Context, f InboxFilter, after *InboxCursor, limit int) (InboxPage, error) {
	p, _, err := a.who(ctx, authz.OpNotificationInbox)
	if err != nil {
		return InboxPage{}, err
	}
	if limit < 1 || limit > MaxInboxPage {
		return InboxPage{}, fieldErr("first", "first must be between 1 and %d", MaxInboxPage)
	}
	types, err := f.types()
	if err != nil {
		return InboxPage{}, err
	}
	if types != nil && len(types) == 0 {
		return InboxPage{Items: []sqlc.Notification{}}, nil
	}
	q := sqlc.New(a.o.DB)
	v, err := a.inboxVisibility(ctx, q, p.UserID)
	if err != nil {
		return InboxPage{}, err
	}
	params := sqlc.ListUserNotificationsParams{
		UserID: p.UserID, UnreadOnly: f.UnreadOnly, Types: types, MaxRows: int32(limit + 1),
		HiddenTypes: v.hidden, OwnedJobsOnly: v.ownedOnly,
	}
	if after != nil {
		params.AfterCreated, params.AfterID = &after.CreatedAt, &after.ID
	}
	rows, err := q.ListUserNotifications(ctx, params)
	if err != nil {
		return InboxPage{}, fmt.Errorf("notify: list inbox: %w", err)
	}
	out := InboxPage{Items: rows}
	if len(rows) > limit {
		out.Items, out.HasNext = rows[:limit], true
	}
	return out, nil
}

// UnreadCount counts the caller's unread notifications.
func (a *API) UnreadCount(ctx context.Context) (int, error) {
	p, _, err := a.who(ctx, authz.OpNotificationInbox)
	if err != nil {
		return 0, err
	}
	return a.countUnread(ctx, sqlc.New(a.o.DB), p.UserID)
}

func (a *API) countUnread(ctx context.Context, q *sqlc.Queries, userID uuid.UUID) (int, error) {
	v, err := a.inboxVisibility(ctx, q, userID)
	if err != nil {
		return 0, err
	}
	n, err := q.CountUnreadNotifications(ctx, sqlc.CountUnreadNotificationsParams{
		UserID: userID, HiddenTypes: v.hidden, OwnedJobsOnly: v.ownedOnly,
	})
	if err != nil {
		return 0, fmt.Errorf("notify: count unread: %w", err)
	}
	return int(n), nil
}

// MarkRead marks the given (or, with all, every) unread notification of
// the caller read and returns how many changed and the remaining unread
// count.
func (a *API) MarkRead(ctx context.Context, ids []uuid.UUID, all bool) (marked, unread int, err error) {
	p, _, err := a.who(ctx, authz.OpNotificationInbox)
	if err != nil {
		return 0, 0, err
	}
	switch {
	case all && len(ids) > 0:
		return 0, 0, fieldErr("ids", "give either ids or all")
	case !all && len(ids) == 0:
		return 0, 0, fieldErr("ids", "give ids or all")
	case len(ids) > MaxMarkRead:
		return 0, 0, fieldErr("ids", "at most %d ids", MaxMarkRead)
	}
	q := sqlc.New(a.o.DB)
	now := a.o.Now()
	var n int64
	if all {
		n, err = q.MarkAllUserNotificationsRead(ctx, sqlc.MarkAllUserNotificationsReadParams{UserID: p.UserID, At: now})
	} else {
		n, err = q.MarkUserNotificationsRead(ctx, sqlc.MarkUserNotificationsReadParams{UserID: p.UserID, Ids: ids, At: now})
	}
	if err != nil {
		return 0, 0, fmt.Errorf("notify: mark read: %w", err)
	}
	c, err := a.countUnread(ctx, q, p.UserID)
	if err != nil {
		return 0, 0, err
	}
	return int(n), c, nil
}

// errSecurityNotDeletable is the VALIDATION answer to deleting a
// security.* notification.
const errSecurityNotDeletable = "security notifications cannot be deleted; mark them read instead"

// DeleteNotification deletes one of the caller's notifications. Security
// notifications (security.account, security.system) are kept as a record
// for the account holder and the admins: they can only be marked read
// (VALIDATION; M5 review L2).
func (a *API) DeleteNotification(ctx context.Context, id uuid.UUID) error {
	p, _, err := a.who(ctx, authz.OpNotificationInbox)
	if err != nil {
		return err
	}
	q := sqlc.New(a.o.DB)
	typ, err := q.GetUserNotificationType(ctx, sqlc.GetUserNotificationTypeParams{ID: id, UserID: p.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("notify: load notification: %w", err)
	}
	if isSecurityType(typ) {
		return fieldErr("id", errSecurityNotDeletable)
	}
	// The query refuses security rows as well (a concurrent change of the
	// type is impossible, but the rule holds in SQL too).
	n, err := q.DeleteUserNotification(ctx, sqlc.DeleteUserNotificationParams{ID: id, UserID: p.UserID})
	if err != nil {
		return fmt.Errorf("notify: delete notification: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// isSecurityType reports whether an inbox row type is a security event.
func isSecurityType(t string) bool { return strings.HasPrefix(t, "security.") }

// Notification loads one of userID's notifications (the stream re-loads
// every announced row scoped to its own user; ErrNotFound otherwise).
func (a *API) Notification(ctx context.Context, userID, id uuid.UUID) (sqlc.Notification, error) {
	q := sqlc.New(a.o.DB)
	v, err := a.inboxVisibility(ctx, q, userID)
	if err != nil {
		return sqlc.Notification{}, err
	}
	n, err := q.GetUserNotification(ctx, sqlc.GetUserNotificationParams{
		ID: id, UserID: userID, HiddenTypes: v.hidden, OwnedJobsOnly: v.ownedOnly,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return n, ErrNotFound
	}
	if err != nil {
		return n, fmt.Errorf("notify: load notification: %w", err)
	}
	return n, nil
}

// NotificationsSince lists userID's notifications created at or after
// since, oldest first (stream resync after possibly lost messages).
func (a *API) NotificationsSince(ctx context.Context, userID uuid.UUID, since time.Time, limit int) ([]sqlc.Notification, error) {
	q := sqlc.New(a.o.DB)
	v, err := a.inboxVisibility(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListUserNotificationsSince(ctx, sqlc.ListUserNotificationsSinceParams{
		UserID: userID, Since: since, MaxRows: int32(min(max(limit, 1), MaxInboxPage)),
		HiddenTypes: v.hidden, OwnedJobsOnly: v.ownedOnly,
	})
	if err != nil {
		return nil, fmt.Errorf("notify: list inbox: %w", err)
	}
	return rows, nil
}
