package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Subject identifies what an event is about (identifiers only; delivery
// re-loads names and re-checks access with them).
type Subject struct {
	JobID         uuid.UUID
	RunID         uuid.UUID
	RestoreID     uuid.UUID
	TestRestoreID uuid.UUID
	ConnectionID  uuid.UUID
	DestinationID uuid.UUID
	ArtifactID    uuid.UUID
	// UserID is the account a security.account event is about.
	UserID uuid.UUID
}

// Detail is a small set of fixed, non-secret facts of an event (a status,
// an error code, counts). Keys are snake_case; values are strings (at most
// MaxDetailString characters, no control characters), booleans or
// integers. Never put secrets, connection strings, server output or
// database content here.
type Detail map[string]any

// Limits of an event.
const (
	MaxDetailKeys   = 16
	MaxDetailString = 200
	MaxDetailBytes  = 2048
	maxDedupKey     = 200
)

// Event is one emitted notification event.
type Event struct {
	Type    EventType
	Subject Subject
	// Severity overrides the catalog's severity when set.
	Severity Severity
	Detail   Detail
	// DedupKey makes the emit idempotent: a second event with the same
	// key is dropped (daily digests, one sign-in alert per address and
	// day).
	DedupKey string
}

// Errors of Emit.
var (
	ErrUnknownEvent   = errors.New("notify: unknown event type")
	ErrMissingSubject = errors.New("notify: the event lacks its subject")
	ErrInvalidDetail  = errors.New("notify: invalid event detail")
)

var (
	detailKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	// sensitiveKeyRe refuses detail keys that name secrets (defense in
	// depth: values are never secrets by contract).
	sensitiveKeyRe = regexp.MustCompile(`password|passphrase|pwd|secret|token|credential|private|cookie|dsn|identity|hmac|signature`)
	dedupRe        = regexp.MustCompile(`^[a-z][a-z0-9_.:-]*$`)
)

// validate checks ev against the catalog and returns its metadata.
func (ev *Event) validate() (Meta, error) {
	m, ok := Lookup(ev.Type)
	if !ok {
		return Meta{}, fmt.Errorf("%w %q", ErrUnknownEvent, ev.Type)
	}
	switch m.Subject {
	case SubjectJob:
		if ev.Subject.JobID == uuid.Nil {
			return Meta{}, fmt.Errorf("%w: %s needs a job", ErrMissingSubject, ev.Type)
		}
	case SubjectConnection:
		if ev.Subject.ConnectionID == uuid.Nil {
			return Meta{}, fmt.Errorf("%w: %s needs a connection", ErrMissingSubject, ev.Type)
		}
	case SubjectDestination:
		if ev.Subject.DestinationID == uuid.Nil {
			return Meta{}, fmt.Errorf("%w: %s needs a destination", ErrMissingSubject, ev.Type)
		}
	case SubjectUser:
		if ev.Subject.UserID == uuid.Nil {
			return Meta{}, fmt.Errorf("%w: %s needs a user", ErrMissingSubject, ev.Type)
		}
	case SubjectNone:
	}
	if ev.Severity != "" && !ev.Severity.Valid() {
		return Meta{}, fmt.Errorf("%w: severity", ErrInvalidDetail)
	}
	if ev.DedupKey != "" && (len(ev.DedupKey) > maxDedupKey || !dedupRe.MatchString(ev.DedupKey)) {
		return Meta{}, fmt.Errorf("%w: dedup key", ErrInvalidDetail)
	}
	return m, nil
}

// ValidateDetail checks d (see Detail) and returns its JSON.
func ValidateDetail(d Detail) ([]byte, error) {
	if len(d) > MaxDetailKeys {
		return nil, fmt.Errorf("%w: more than %d keys", ErrInvalidDetail, MaxDetailKeys)
	}
	for k, v := range d {
		if !detailKeyRe.MatchString(k) || sensitiveKeyRe.MatchString(k) || strings.HasSuffix(k, "key") {
			return nil, fmt.Errorf("%w: key %q", ErrInvalidDetail, k)
		}
		switch x := v.(type) {
		case string:
			if !cleanText(x, MaxDetailString) {
				return nil, fmt.Errorf("%w: value of %q", ErrInvalidDetail, k)
			}
		case bool, int, int32, int64:
		default:
			return nil, fmt.Errorf("%w: value of %q has an unsupported type", ErrInvalidDetail, k)
		}
	}
	if d == nil {
		d = Detail{}
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidDetail, err)
	}
	if len(b) > MaxDetailBytes {
		return nil, fmt.Errorf("%w: larger than %d bytes", ErrInvalidDetail, MaxDetailBytes)
	}
	return b, nil
}

// cleanText reports whether s is valid UTF-8 of at most n characters
// without control or line-separator characters.
func cleanText(s string, n int) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > n {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
	})
}

func optID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// Emit adds ev to the notification outbox with q, which should be bound to
// the producer's transaction (db.Tx): the event commits or rolls back with
// the state change it reports. The dispatcher enqueues its fan-out after
// commit; recipients and channels are resolved (and access re-checked) at
// fan-out and delivery time, never here.
//
// It returns the event id, or uuid.Nil when DedupKey was already emitted.
// Errors are validation errors (the event is malformed: a programming
// error) or database errors (the transaction is then aborted).
func Emit(ctx context.Context, q *sqlc.Queries, ev Event) (uuid.UUID, error) {
	m, err := ev.validate()
	if err != nil {
		return uuid.Nil, err
	}
	detail, err := ValidateDetail(ev.Detail)
	if err != nil {
		return uuid.Nil, err
	}
	sev := ev.Severity
	if sev == "" {
		sev = m.Severity
	}
	var dedup *string
	if ev.DedupKey != "" {
		dedup = &ev.DedupKey
	}
	s := ev.Subject
	ids, err := q.InsertNotificationEvent(ctx, sqlc.InsertNotificationEventParams{
		Type: string(ev.Type), Severity: string(sev),
		JobID: optID(s.JobID), RunID: optID(s.RunID), RestoreID: optID(s.RestoreID),
		TestRestoreID: optID(s.TestRestoreID), ConnectionID: optID(s.ConnectionID),
		DestinationID: optID(s.DestinationID), ArtifactID: optID(s.ArtifactID), UserID: optID(s.UserID),
		Detail: detail, DedupKey: dedup,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("notify: insert event: %w", err)
	}
	if len(ids) == 0 {
		return uuid.Nil, nil
	}
	return ids[0], nil
}
