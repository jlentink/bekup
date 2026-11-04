package audit

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
)

// Errors.
var (
	// ErrRecordFailed wraps every failure to write an audit entry. The
	// audited operation must fail with it (fail closed).
	ErrRecordFailed = errors.New("audit entry could not be recorded")
	// ErrInvalidEvent reports an event that can never be stored.
	ErrInvalidEvent = errors.New("invalid audit event")
	// ErrDetailsTooLarge reports details over MaxDetailsBytes.
	ErrDetailsTooLarge = errors.New("audit details too large")
)

// Auditor records audit entries. It is safe for concurrent use.
type Auditor struct {
	db       sqlc.DBTX
	reg      *logging.Registry
	observer Observer
}

// Observer is told about every recorded entry with the q of the entry, so
// it writes in the same transaction (task 5.3: audited actions that are
// notification events go to the notification outbox atomically with the
// entry). An error fails the record (and so the audited operation): it
// must only fail on database errors.
type Observer func(ctx context.Context, q *sqlc.Queries, ev Event) error

// Options configures New.
type Options struct {
	// Registry holds secret values scrubbed from details
	// (nil: logging.DefaultRegistry).
	Registry *logging.Registry
	// Observer is called after every recorded entry (nil: none).
	Observer Observer
}

// New returns an Auditor writing through db (normally the bekup_app pool).
func New(db sqlc.DBTX, opts Options) *Auditor {
	reg := opts.Registry
	if reg == nil {
		reg = logging.DefaultRegistry
	}
	return &Auditor{db: db, reg: reg, observer: opts.Observer}
}

// Entry is a stored audit row as returned by Record.
type Entry = sqlc.AuditLog

// Record appends ev in its own statement (autocommit). An error means the
// entry was not stored; the caller must fail the audited operation.
func (a *Auditor) Record(ctx context.Context, ev Event) (Entry, error) {
	return a.RecordTx(ctx, sqlc.New(a.db), ev)
}

// RecordTx appends ev with q, which should be bound to the caller's
// transaction (db.Tx) so the entry commits or rolls back with the action.
// The transaction must be READ COMMITTED (the trigger refuses others) and
// should be short: the insert holds the chain lock until commit.
func (a *Auditor) RecordTx(ctx context.Context, q *sqlc.Queries, ev Event) (Entry, error) {
	p, err := a.params(ev)
	if err != nil {
		return Entry{}, fmt.Errorf("%w: %w", ErrRecordFailed, err)
	}
	row, err := q.InsertAuditLog(ctx, p)
	if err != nil {
		return Entry{}, fmt.Errorf("%w: %s: %w", ErrRecordFailed, ev.Action, err)
	}
	if a.observer != nil {
		if err := a.observer(ctx, q, ev); err != nil {
			return Entry{}, fmt.Errorf("%w: %s: observer: %w", ErrRecordFailed, ev.Action, err)
		}
	}
	return row, nil
}

func (a *Auditor) params(ev Event) (sqlc.InsertAuditLogParams, error) {
	if !ev.Action.Valid() {
		return sqlc.InsertAuditLogParams{}, fmt.Errorf("%w: unknown action %q", ErrInvalidEvent, ev.Action)
	}
	if ev.Actor.API != nil && ev.Actor.Kind != ActorAPI {
		return sqlc.InsertAuditLogParams{}, fmt.Errorf("%w: %s actor with an API token", ErrInvalidEvent, ev.Actor.Kind)
	}
	details := ev.Details
	switch ev.Actor.Kind {
	case ActorUser:
		if ev.Actor.UserID == nil {
			return sqlc.InsertAuditLogParams{}, fmt.Errorf("%w: user actor without user id", ErrInvalidEvent)
		}
	case ActorSystem:
		if ev.Actor.UserID != nil {
			return sqlc.InsertAuditLogParams{}, fmt.Errorf("%w: system actor with a user id", ErrInvalidEvent)
		}
	case ActorCLI:
	case ActorAPI:
		var err error
		if details, err = apiDetails(ev.Action, ev.Actor, ev.Details); err != nil {
			return sqlc.InsertAuditLogParams{}, err
		}
	default:
		return sqlc.InsertAuditLogParams{}, fmt.Errorf("%w: unknown actor kind %q", ErrInvalidEvent, ev.Actor.Kind)
	}
	p := sqlc.InsertAuditLogParams{
		ActorUserID: ev.Actor.UserID,
		ActorKind:   string(ev.Actor.Kind),
		Action:      string(ev.Action),
	}
	var err error
	if p.TargetType, err = optText("target type", ev.TargetType, 128, a); err != nil {
		return p, err
	}
	if p.TargetID, err = optText("target id", ev.TargetID, 256, a); err != nil {
		return p, err
	}
	if ev.IP.IsValid() {
		ip := ev.IP.WithZone("").Unmap()
		p.Ip = &ip
	}
	if p.Details, err = Redact(details, a.reg); err != nil {
		return p, err
	}
	return p, nil
}

// apiDetails validates an ActorAPI actor (no user id; the token id, a
// name of 1-100 characters and the request id, which only the actions in
// apiActionsWithoutRequest may leave out) and returns d with the token id,
// name and request id added. A caller's detail of the same name is
// refused rather than overwritten.
func apiDetails(action Action, actor Actor, d Details) (Details, error) {
	r := actor.API
	if actor.UserID != nil {
		return nil, fmt.Errorf("%w: API actor with a user id", ErrInvalidEvent)
	}
	if r == nil || r.TokenID == uuid.Nil || (r.RequestID == uuid.Nil && !apiActionsWithoutRequest[action]) {
		return nil, fmt.Errorf("%w: API actor without token or request id", ErrInvalidEvent)
	}
	if !utf8.ValidString(r.TokenName) || r.TokenName == "" || utf8.RuneCountInString(r.TokenName) > maxAPITokenName {
		return nil, fmt.Errorf("%w: API token name must be valid UTF-8 of 1 to %d characters", ErrInvalidEvent, maxAPITokenName)
	}
	out := make(Details, len(d)+3)
	for k, v := range d {
		out[k] = v
	}
	add := map[string]string{DetailAPITokenID: r.TokenID.String(), DetailAPITokenName: r.TokenName, DetailRequestID: ""}
	if r.RequestID != uuid.Nil {
		add[DetailRequestID] = r.RequestID.String()
	}
	for k, v := range add {
		if _, taken := out[k]; taken {
			return nil, fmt.Errorf("%w: detail %q is reserved for API actors", ErrInvalidEvent, k)
		}
		if v != "" {
			out[k] = v
		}
	}
	return out, nil
}

// apiActionsWithoutRequest are the ActorAPI actions that concern the
// token, not a run request: an access denial (D-220 amendment 1).
var apiActionsWithoutRequest = map[Action]bool{ActionAPIAddressDenied: true}

// optText validates an optional text column and scrubs registered secrets.
func optText(name, s string, maxChars int, a *Auditor) (*string, error) {
	if s == "" {
		return nil, nil
	}
	s = redactString(s, a.reg)
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxChars {
		return nil, fmt.Errorf("%w: %s must be valid UTF-8 of at most %d characters", ErrInvalidEvent, name, maxChars)
	}
	return &s, nil
}
