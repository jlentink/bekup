package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/i18n"
	"github.com/jlentink/bekup/internal/notify/email"
	"github.com/jlentink/bekup/internal/storage"
)

// Content is what every channel shows of an event: a title, one short
// sentence, a link into the UI (built from server.base_url) and the
// minimal data object of the webhook envelope and the inbox payload. It
// never contains secrets, connection strings or database content.
type Content struct {
	Title string
	Body  string
	Link  string
	Data  map[string]any
}

// names are the display names of an event's subjects.
type names struct {
	job, connection, destination string
	jobDeleted                   bool
	// jobGone: the job row no longer exists; render shows the
	// recipient's "(deleted job)" instead of a name.
	jobGone bool
	// summary is the content of a summary.periodic event (D-226), built
	// for its recipient; nil when it cannot be built.
	summary *SummaryContent
}

// DisplayName makes an admin-entered name safe for every channel: no
// control characters, no line or paragraph separators and no format
// characters (Unicode Cf: bidi overrides and isolates such as U+202E and
// U+2066-U+2069 that reorder the text around them, zero-width characters
// that hide a difference; a space shows where one was), at most 100
// characters.
func DisplayName(s string) string { return displayName(s) }

func displayName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 100 {
		s = string([]rune(s)[:99]) + "…"
	}
	if s == "" {
		return "(unnamed)"
	}
	return s
}

func (s *Service) loadNames(ctx context.Context, q *sqlc.Queries, ev *sqlc.NotificationEvent) (names, error) {
	var n names
	if ev.JobID != nil {
		j, err := q.GetNotifyJob(ctx, *ev.JobID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			n.job, n.jobDeleted, n.jobGone = "(deleted job)", true, true
		case err != nil:
			return n, fmt.Errorf("notify: load job: %w", err)
		default:
			n.job, n.jobDeleted = displayName(j.Name), j.DeletedAt != nil
		}
	}
	if ev.ConnectionID != nil {
		name, err := q.GetNotifyConnectionName(ctx, *ev.ConnectionID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return n, fmt.Errorf("notify: load connection: %w", err)
		}
		n.connection = displayName(name)
	}
	if ev.DestinationID != nil {
		name, err := q.GetNotifyDestinationName(ctx, *ev.DestinationID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return n, fmt.Errorf("notify: load destination: %w", err)
		}
		n.destination = displayName(name)
	}
	if EventType(ev.Type) == SummaryPeriodic && s.o.Summaries != nil {
		c, err := s.o.Summaries.Render(ctx, q, ev)
		if err != nil {
			return n, fmt.Errorf("notify: summary content: %w", err)
		}
		n.summary = c
	}
	return n, nil
}

func (s *Service) link(path string) string {
	u := *s.base
	u.Path += path
	return u.String()
}

func idPath(prefix string, id *uuid.UUID) string {
	if id == nil {
		return prefix
	}
	return prefix + "/" + id.String()
}

// detailOf decodes the stored detail (written by Emit, so trusted to be
// small and clean).
func detailOf(ev *sqlc.NotificationEvent) Detail {
	d := Detail{}
	if len(ev.Detail) == 0 {
		return d
	}
	if err := json.Unmarshal(ev.Detail, &d); err != nil {
		// A malformed detail renders without it.
		return Detail{}
	}
	return d
}

func str(d Detail, k string) string {
	if s, ok := d[k].(string); ok {
		return s
	}
	return ""
}

func num(d Detail, k string) int64 {
	if f, ok := d[k].(float64); ok {
		return int64(f)
	}
	return 0
}

// lowSpaceBody is the sentence of destination.low_space (D-227): the
// free space, the part of the D-225 rule that holds and, for a reminder,
// since when.
func lowSpaceBody(t textFunc, l i18n.Locale, dest string, d Detail) string {
	size := func(n int64) string { return i18n.LocalizeDecimal(l, storage.FormatSize(n)) }
	free := size(num(d, "free_bytes"))
	if _, ok := d["total_bytes"]; ok {
		free = t("body.low_space.of", free, size(num(d, "total_bytes")))
	}
	cause := t("body.low_space.fraction")
	if str(d, "cause") == "below_newest_set" {
		cause = t("body.low_space.below_newest_set", size(num(d, "newest_set_bytes")))
	}
	body := t("body.destination.low_space", dest, free, cause)
	if num(d, "reminder") > 0 {
		if since, err := time.Parse(time.RFC3339, str(d, "low_since")); err == nil {
			body += t("body.low_space.since", i18n.FormatTime(l, since.UTC(), "2 Jan 2006 15:04"))
		}
	}
	return body
}

// jobBodies are the event types whose sentence names only the job.
var jobBodies = map[EventType]bool{
	BackupSucceeded: true, BackupSucceededWithWarnings: true, BackupPartial: true, BackupFailed: true, BackupCanceled: true,
	RestoreStarted: true, RestoreCompleted: true, RestoreFailed: true,
	JobCreated: true, JobUpdated: true, JobDisabled: true, JobEnabled: true, JobDeleted: true, JobEncryptionChanged: true,
}

// render builds the content of ev in l (the recipient's language).
func (s *Service) render(ev *sqlc.NotificationEvent, m Meta, n names, l i18n.Locale) Content {
	t := textsFor(l)
	d := detailOf(ev)
	if n.jobGone {
		n.job = t("name.deleted_job")
	}
	job := n.job
	typ := EventType(ev.Type)
	var body, path string
	switch m.Group {
	case GroupBackup:
		path = idPath("/runs", ev.RunID)
		switch typ {
		case BackupStarted, BackupStartedEarly:
			body = t("body.backup.started", job)
		case BackupMissed:
			body = t("body.backup.missed", job)
			path = idPath("/jobs", ev.JobID)
		default:
			if jobBodies[typ] {
				body = t("body."+string(typ), job)
			}
		}
	case GroupRestore:
		path = idPath("/restores", ev.RestoreID)
		switch typ {
		case TestRestorePassed, TestRestoreWarnings, TestRestoreFailed:
			body, path = t("body."+string(typ), job), idPath("/jobs", ev.JobID)
		default:
			if jobBodies[typ] {
				body = t("body."+string(typ), job)
			}
		}
	case GroupRetention:
		switch typ {
		case RetentionSummary:
			body = t("body.retention.summary", t.n("n.backups", num(d, "runs")), t.n("n.files", num(d, "files")),
				num(d, "bytes"), job, str(d, "date"))
			path = idPath("/jobs", ev.JobID)
		case RetentionBlocked:
			cause := t("body.retention.blocked.no_complete")
			if c := str(d, "newest_complete"); c != "" {
				cause = t("body.retention.blocked.incomplete", c)
			}
			body = t("body.retention.blocked", t.n("n.incomplete_backups", num(d, "held_runs")), job, n.destination,
				str(d, "since"), cause)
			path = idPath("/jobs", ev.JobID)
		case ArtifactMissing:
			body = t("body.artifact.missing", job)
			path = idPath("/jobs", ev.JobID)
		case DestinationTestFailed:
			body = t("body.destination.test_failed", n.destination)
			path = idPath("/destinations", ev.DestinationID)
		case DestinationLowSpace:
			body, path = lowSpaceBody(t, l, n.destination, d), idPath("/destinations", ev.DestinationID)
		default:
		}
	case GroupConfig:
		if m.Subject == SubjectConnection {
			path = idPath("/connections", ev.ConnectionID)
			switch typ {
			case ConnectionPinChanged, ConnectionTLSDowngraded, ConnectionHostKeyChanged:
				body = t("body."+string(typ), n.connection)
			default:
			}
		} else {
			path = idPath("/jobs", ev.JobID)
			key := "body." + string(typ)
			if !jobBodies[typ] {
				key = "body." + string(JobUpdated)
			}
			body = t(key, job)
			if n.jobDeleted {
				path = "/jobs"
			}
		}
	case GroupSummary:
		path = "/"
		body = t("body.summary.fallback")
		if n.summary != nil {
			body = n.summary.Body
		}
	case GroupSecurity:
		if typ == SecurityAccount {
			path = "/profile/security"
			kind := str(d, "kind")
			body = accountMessage(t, kind)
			switch kind {
			case KindAPIAddressDenied:
				body = apiDenialBody(t, d)
			case KindAPITokenAddressesWidened:
				body = apiWidenedBody(t, d)
			case KindRiskAcceptanceExpiring, KindRiskAcceptanceEnded:
				body = riskBody(t, kind, d)
			default:
			}
		} else {
			path = "/admin/audit"
			body = systemBody(t, l, d)
		}
		if body == "" {
			body = t("body.security.fallback")
		}
	}
	title := m.Label
	if _, ok := texts[i18n.EN]["title."+ev.Type]; ok {
		title = t("title." + ev.Type)
	}
	if job != "" && m.Subject == SubjectJob {
		title = t("title.with_job", title, job)
	}
	if n.summary != nil {
		title = n.summary.Title
	}
	if code := str(d, "error_code"); code != "" {
		body += t("body.reason", code)
	}
	if ev.CollapsedCount > 0 {
		body += t.n("body.collapsed", int64(ev.CollapsedCount))
	}
	c := Content{Title: clampRunes(title, 200), Body: clampRunes(body, 1000)}
	if path != "" {
		c.Link = s.link(path)
	}
	c.Data = s.data(ev, m, n, d, c)
	return c
}

func clampRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// data is the minimal webhook / inbox payload.
func (s *Service) data(ev *sqlc.NotificationEvent, m Meta, n names, d Detail, c Content) map[string]any {
	out := map[string]any{
		"event":       ev.Type,
		"group":       string(m.Group),
		"severity":    ev.Severity,
		"title":       c.Title,
		"summary":     c.Body,
		"occurred_at": ev.CreatedAt.UTC().Format(time.RFC3339),
	}
	if c.Link != "" {
		out["link"] = c.Link
	}
	if ev.JobID != nil {
		name := n.job
		if n.jobGone {
			// Payload values other than title/summary stay language-neutral
			// (D-237): the deleted-job marker is always the English one.
			name = texts.T(i18n.EN, "name.deleted_job")
		}
		out["job"] = map[string]any{"id": ev.JobID.String(), "name": name}
	}
	if ev.ConnectionID != nil {
		out["connection"] = map[string]any{"id": ev.ConnectionID.String(), "name": n.connection}
	}
	if ev.DestinationID != nil {
		out["destination"] = map[string]any{"id": ev.DestinationID.String(), "name": n.destination}
	}
	for k, id := range map[string]*uuid.UUID{
		"run_id": ev.RunID, "restore_id": ev.RestoreID, "test_restore_id": ev.TestRestoreID, "artifact_id": ev.ArtifactID,
	} {
		if id != nil {
			out[k] = id.String()
		}
	}
	if len(d) > 0 {
		out["detail"] = d
	}
	if ev.CollapsedCount > 0 {
		out["collapsed"] = ev.CollapsedCount
	}
	return out
}

// emailFor renders the e-mail of c (the 5.1 layout) in l.
func emailFor(ev *sqlc.NotificationEvent, c Content, n names, l i18n.Locale) (email.Content, error) {
	if n.summary != nil {
		// The periodic summary has its own layout (D-226).
		return email.Render(n.summary.Notice)
	}
	t := textsFor(l)
	if n.jobGone {
		n.job = t("name.deleted_job")
	}
	details := []email.Detail{{Label: t("email.event"), Value: ev.Type}}
	if n.job != "" {
		details = append(details, email.Detail{Label: t("email.job"), Value: n.job})
	}
	if n.connection != "" {
		details = append(details, email.Detail{Label: t("email.connection"), Value: n.connection})
	}
	if n.destination != "" {
		details = append(details, email.Detail{Label: t("email.destination"), Value: n.destination})
	}
	details = append(details, email.Detail{Label: t("email.time"), Value: i18n.FormatTime(l, ev.CreatedAt.UTC(), "2006-01-02 15:04:05 UTC")})
	notice := email.Notice{
		Subject: t("email.subject", clampRunes(c.Title, 180)), Heading: c.Title, Paragraphs: []string{c.Body}, Details: details,
		Locale: l,
	}
	if c.Link != "" {
		notice.Action = &email.Action{Label: t("email.open"), URL: c.Link}
	}
	return email.Render(notice)
}

// floodEmail is the one summary e-mail of the per-user flood rule.
func (s *Service) floodEmail(limit int, l i18n.Locale) (email.Content, error) {
	t := textsFor(l)
	return email.Render(email.Notice{
		Subject: t("flood.subject"),
		Heading: t("flood.heading"),
		Paragraphs: []string{
			t("flood.received", limit),
			t("flood.further"),
		},
		Action: &email.Action{Label: t("flood.open_inbox"), URL: s.link("/")},
		Locale: l,
	})
}

// floodData is the webhook payload of the flood summary.
func (s *Service) floodData(limit int, l i18n.Locale) map[string]any {
	return map[string]any{
		"summary": textsFor(l)("flood.webhook", limit),
		"link":    s.link("/"),
	}
}

// FloodSummaryType is the webhook event type of the flood summary.
const FloodSummaryType = "notification.flood_summary"
