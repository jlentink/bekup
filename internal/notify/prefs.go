package notify

import (
	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Channel is a delivery channel of a user.
type Channel string

// Channels a preference can address (the preference matrix columns are
// MatrixChannels).
const (
	ChannelInbox   Channel = "inbox"
	ChannelEmail   Channel = "email"
	ChannelWebhook Channel = "webhook"
)

// Pref is one stored preference of a user for an event: per event (JobID
// nil) or a per-job override.
type Pref struct {
	JobID   *uuid.UUID
	Channel Channel
	Enabled bool
}

// Default is the D-161 default of a channel for an event: the inbox for
// every visible event, e-mail for problem events, webhooks only when the
// user opts in.
func Default(m Meta, ch Channel) bool {
	switch ch {
	case ChannelInbox:
		return true
	case ChannelEmail:
		return m.Problem
	default:
		return false
	}
}

// Enabled resolves whether ch is on for an event of m about jobID (nil
// for events without a job): a per-job override beats the per-event
// preference, which beats Default. Mandatory events always keep the
// inbox and e-mail; preferences can only add channels to them.
func Enabled(m Meta, ch Channel, jobID *uuid.UUID, prefs []Pref) bool {
	if m.Mandatory && (ch == ChannelInbox || ch == ChannelEmail) {
		return true
	}
	var perEvent, perJob *bool
	for i := range prefs {
		p := &prefs[i]
		if p.Channel != ch {
			continue
		}
		switch {
		case p.JobID == nil:
			perEvent = &p.Enabled
		case jobID != nil && *p.JobID == *jobID:
			perJob = &p.Enabled
		}
	}
	switch {
	case perJob != nil:
		return *perJob
	case perEvent != nil:
		return *perEvent
	default:
		return Default(m, ch)
	}
}

// prefsByUser groups stored preference rows per user.
func prefsByUser(rows []sqlc.ListNotificationPrefsRow) map[uuid.UUID][]Pref {
	out := map[uuid.UUID][]Pref{}
	for _, r := range rows {
		out[r.UserID] = append(out[r.UserID], Pref{JobID: r.JobID, Channel: Channel(r.Channel), Enabled: r.Enabled})
	}
	return out
}

// sharedFilterMatches reports whether a shared channel's filter on a job
// accepts m: events listed by type or group; both lists empty accept every
// event. Security events never go to shared channels.
func sharedFilterMatches(m Meta, groups, events []string) bool {
	if m.Group == GroupSecurity {
		return false
	}
	if len(groups) == 0 && len(events) == 0 {
		return true
	}
	for _, g := range groups {
		if Group(g) == m.Group {
			return true
		}
	}
	for _, e := range events {
		if EventType(e) == m.Type {
			return true
		}
	}
	return false
}
