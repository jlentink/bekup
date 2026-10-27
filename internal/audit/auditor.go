package audit

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

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
	db  sqlc.DBTX
	reg *logging.Registry
}

// Options configures New.
type Options struct {
	// Registry holds secret values scrubbed from details
	// (nil: logging.DefaultRegistry).
	Registry *logging.Registry
}

// New returns an Auditor writing through db (normally the bekup_app pool).
func New(db sqlc.DBTX, opts Options) *Auditor {
	reg := opts.Registry
	if reg == nil {
		reg = logging.DefaultRegistry
	}
	return &Auditor{db: db, reg: reg}
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
	return row, nil
}

func (a *Auditor) params(ev Event) (sqlc.InsertAuditLogParams, error) {
	if !ev.Action.Valid() {
		return sqlc.InsertAuditLogParams{}, fmt.Errorf("%w: unknown action %q", ErrInvalidEvent, ev.Action)
	}
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
	if p.Details, err = Redact(ev.Details, a.reg); err != nil {
		return p, err
	}
	return p, nil
}

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
