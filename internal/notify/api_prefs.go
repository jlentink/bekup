package notify

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
)

// MatrixChannels are the channel columns of the preference matrix (push
// joins in phase 2).
func MatrixChannels() []Channel {
	return []Channel{ChannelInbox, ChannelEmail, ChannelWebhook}
}

// VisibleTo reports whether events of m can reach an account of role at
// all (the catalog and matrix filter): job events reach every role (users
// for the jobs they own), staff events admins and maintainers, admin
// events admins, own-account events everyone. Delivery still checks the
// authz policy with the subject at that moment.
func VisibleTo(role auth.Role, m Meta) bool {
	switch m.Audience {
	case AudienceJob, AudienceOwnAccount:
		return role == auth.RoleAdmin || role == auth.RoleMaintainer || role == auth.RoleUser
	case AudienceStaff:
		return role == auth.RoleAdmin || role == auth.RoleMaintainer
	case AudienceAdmins:
		return role == auth.RoleAdmin
	default:
		return false
	}
}

// GroupLabel is the display name of a group.
func GroupLabel(g Group) string {
	switch g {
	case GroupBackup:
		return "Backups"
	case GroupRestore:
		return "Restores"
	case GroupRetention:
		return "Retention & storage"
	case GroupConfig:
		return "Jobs & configuration"
	case GroupSecurity:
		return "Security"
	default:
		return string(g)
	}
}

// CatalogGroup is one group of the catalog with the events the caller's
// role can receive.
type CatalogGroup struct {
	Group  Group
	Events []Meta
}

// CatalogFor returns the catalog grouped in display order, filtered to
// the events role can receive; empty groups are left out.
func CatalogFor(role auth.Role) []CatalogGroup {
	var out []CatalogGroup
	all := Catalog()
	for _, g := range Groups() {
		cg := CatalogGroup{Group: g}
		for i := range all {
			if all[i].Group == g && VisibleTo(role, all[i]) {
				cg.Events = append(cg.Events, all[i])
			}
		}
		if len(cg.Events) > 0 {
			out = append(out, cg)
		}
	}
	return out
}

// DefaultChannels are the matrix channels on by default for m (D-161).
func DefaultChannels(m Meta) []Channel {
	var out []Channel
	for _, ch := range MatrixChannels() {
		if (m.Mandatory && (ch == ChannelInbox || ch == ChannelEmail)) || Default(m, ch) {
			out = append(out, ch)
		}
	}
	return out
}

// Catalog returns the catalog for the caller's role.
func (a *API) Catalog(ctx context.Context) ([]CatalogGroup, error) {
	p, _, err := a.who(ctx, authz.OpNotificationPrefsView)
	if err != nil {
		return nil, err
	}
	return CatalogFor(p.Role), nil
}

// Source says where an effective preference comes from.
type Source string

// Sources.
const (
	SourceDefault   Source = "default"
	SourceOverride  Source = "override"
	SourceMandatory Source = "mandatory"
)

// ChannelPref is one cell of the matrix.
type ChannelPref struct {
	Channel Channel
	Enabled bool
	Source  Source
	// Locked cells cannot be changed (mandatory security channels).
	Locked bool
}

// JobOverride is a per-job override of one channel.
type JobOverride struct {
	JobID   uuid.UUID
	JobName string
	Channel Channel
	Enabled bool
}

// EventPrefs is one row of the matrix.
type EventPrefs struct {
	Event     Meta
	Channels  []ChannelPref
	Overrides []JobOverride
}

// PrefGroup is a group of matrix rows.
type PrefGroup struct {
	Group  Group
	Events []EventPrefs
}

// Preferences is the caller's effective preference matrix.
type Preferences struct {
	Groups         []PrefGroup
	EmailVerified  bool
	ActiveWebhooks int
}

// userPrefs are the stored rows of one user by event.
type userPrefs map[EventType][]Pref

// cells resolves the matrix row of m for stored prefs (per-event rows
// only; per-job overrides are listed separately).
func cells(m Meta, prefs []Pref) []ChannelPref {
	out := make([]ChannelPref, 0, len(MatrixChannels()))
	for _, ch := range MatrixChannels() {
		c := ChannelPref{Channel: ch, Enabled: Enabled(m, ch, nil, prefs), Source: SourceDefault}
		if m.Mandatory && (ch == ChannelInbox || ch == ChannelEmail) {
			c.Source, c.Locked = SourceMandatory, true
		} else {
			for i := range prefs {
				if prefs[i].JobID == nil && prefs[i].Channel == ch {
					c.Source = SourceOverride
				}
			}
		}
		out = append(out, c)
	}
	return out
}

// jobVisible authorizes job.view of a job for the caller (denials are
// false).
func (a *API) jobVisible(ctx context.Context, id uuid.UUID) (bool, error) {
	err := a.o.Policy.Authorize(ctx, authz.OpJobView, authz.Job{ID: id})
	if err == nil {
		return true, nil
	}
	var ae *apierror.Error
	if errors.As(err, &ae) {
		return false, nil
	}
	return false, err
}

// MyPreferences returns the caller's effective matrix: every event the
// role can receive × inbox / e-mail / webhook with the source of each
// value, plus the per-job overrides of jobs the caller may still see.
func (a *API) MyPreferences(ctx context.Context) (Preferences, error) {
	p, _, err := a.who(ctx, authz.OpNotificationPrefsView)
	if err != nil {
		return Preferences{}, err
	}
	return a.preferences(ctx, sqlc.New(a.o.DB), p)
}

func (a *API) preferences(ctx context.Context, q *sqlc.Queries, p auth.Principal) (Preferences, error) {
	rows, err := q.ListUserNotificationPrefs(ctx, p.UserID)
	if err != nil {
		return Preferences{}, fmt.Errorf("notify: list preferences: %w", err)
	}
	stored := userPrefs{}
	var jobIDs []uuid.UUID
	for _, r := range rows {
		stored[EventType(r.Event)] = append(stored[EventType(r.Event)], Pref{JobID: r.JobID, Channel: Channel(r.Channel), Enabled: r.Enabled})
		if r.JobID != nil && !slices.Contains(jobIDs, *r.JobID) {
			jobIDs = append(jobIDs, *r.JobID)
		}
	}
	names := map[uuid.UUID]string{}
	if len(jobIDs) > 0 {
		jobs, err := q.ListJobNamesByIDs(ctx, jobIDs)
		if err != nil {
			return Preferences{}, fmt.Errorf("notify: load jobs: %w", err)
		}
		for _, j := range jobs {
			ok, err := a.jobVisible(ctx, j.ID)
			if err != nil {
				return Preferences{}, err
			}
			if ok {
				names[j.ID] = displayName(j.Name)
			}
		}
	}
	out := Preferences{}
	for _, g := range CatalogFor(p.Role) {
		pg := PrefGroup{Group: g.Group}
		for i := range g.Events {
			m := g.Events[i]
			prefs := stored[m.Type]
			row := EventPrefs{Event: m, Channels: cells(m, prefs), Overrides: []JobOverride{}}
			for _, pr := range prefs {
				if pr.JobID == nil || pr.Channel == ChannelPush {
					continue
				}
				name, ok := names[*pr.JobID]
				if !ok {
					continue
				}
				row.Overrides = append(row.Overrides, JobOverride{JobID: *pr.JobID, JobName: name, Channel: pr.Channel, Enabled: pr.Enabled})
			}
			pg.Events = append(pg.Events, row)
		}
		out.Groups = append(out.Groups, pg)
	}
	u, err := q.GetUserContact(ctx, p.UserID)
	if err == nil {
		out.EmailVerified = u.EmailVerifiedAt != nil
	}
	n, err := q.CountEnabledUserWebhooks(ctx, p.UserID)
	if err != nil {
		return Preferences{}, fmt.Errorf("notify: count webhooks: %w", err)
	}
	out.ActiveWebhooks = int(n)
	return out, nil
}

// SetPreference stores the caller's preference for (event, channel), per
// event or, with jobID, as an override for one job the caller may see.
// Mandatory security channels (inbox and e-mail) cannot be turned off; it
// returns the updated matrix row.
func (a *API) SetPreference(ctx context.Context, event EventType, ch Channel, enabled bool, jobID *uuid.UUID) (EventPrefs, error) {
	p, _, err := a.who(ctx, authz.OpNotificationPrefsEdit)
	if err != nil {
		return EventPrefs{}, err
	}
	m, ok := Lookup(event)
	if !ok || !VisibleTo(p.Role, m) {
		return EventPrefs{}, fieldErr("event", "unknown event")
	}
	if !slices.Contains(MatrixChannels(), ch) {
		return EventPrefs{}, fieldErr("channel", "unknown channel")
	}
	if m.Mandatory && (ch == ChannelInbox || ch == ChannelEmail) {
		return EventPrefs{}, fieldErr("enabled", "security alerts always go to the inbox and by e-mail")
	}
	if jobID != nil {
		if m.Subject != SubjectJob {
			return EventPrefs{}, fieldErr("jobId", "this event does not concern a job")
		}
		ok, err := a.jobVisible(ctx, *jobID)
		if err != nil {
			return EventPrefs{}, err
		}
		if !ok {
			return EventPrefs{}, ErrNotFound
		}
	}
	var out EventPrefs
	err = db.Tx(ctx, a.o.DB, func(q *sqlc.Queries) error {
		if err := q.UpsertNotificationPref(ctx, sqlc.UpsertNotificationPrefParams{
			UserID: p.UserID, Event: string(event), JobID: jobID, Channel: sqlc.NotificationChannel(ch), Enabled: enabled,
		}); err != nil {
			var pe *pgconn.PgError
			if errors.As(err, &pe) && pe.Code == "23503" {
				return ErrNotFound
			}
			return fmt.Errorf("notify: store preference: %w", err)
		}
		all, err := a.preferences(ctx, q, p)
		if err != nil {
			return err
		}
		for _, g := range all.Groups {
			for i := range g.Events {
				if g.Events[i].Event.Type == event {
					out = g.Events[i]
				}
			}
		}
		return nil
	})
	return out, err
}

// ResetPreferences removes the caller's stored preferences (back to the
// D-161 defaults), or with jobID only the overrides of that job.
func (a *API) ResetPreferences(ctx context.Context, jobID *uuid.UUID) (Preferences, error) {
	p, _, err := a.who(ctx, authz.OpNotificationPrefsEdit)
	if err != nil {
		return Preferences{}, err
	}
	var out Preferences
	err = db.Tx(ctx, a.o.DB, func(q *sqlc.Queries) error {
		var err error
		if jobID != nil {
			_, err = q.DeleteUserJobNotificationPrefs(ctx, sqlc.DeleteUserJobNotificationPrefsParams{UserID: p.UserID, JobID: *jobID})
		} else {
			_, err = q.DeleteUserNotificationPrefs(ctx, p.UserID)
		}
		if err != nil {
			return fmt.Errorf("notify: reset preferences: %w", err)
		}
		out, err = a.preferences(ctx, q, p)
		return err
	})
	return out, err
}
