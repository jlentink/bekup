package runstate

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/pubsub"
)

// Event kinds of the bekup_run_events payload.
const (
	// EventRun: the run row, one of its items or artifacts changed.
	EventRun = "run"
	// EventLog: log lines were appended to the run.
	EventLog = "log"
)

// Ref identifies a run and its job (the payload of every run event).
type Ref struct {
	RunID uuid.UUID
	JobID uuid.UUID
}

// Payload is the run event payload: "<kind>:<run id>:<job id>".
func Payload(kind string, r Ref) string {
	return kind + ":" + r.RunID.String() + ":" + r.JobID.String()
}

// ErrBadPayload reports an undecodable run event payload.
var ErrBadPayload = errors.New("runstate: invalid run event payload")

// ParsePayload decodes a run event payload (task 3.7 listeners).
func ParsePayload(p string) (kind string, r Ref, err error) {
	const idLen = 36
	switch {
	case len(p) == len(EventRun)+2+2*idLen && p[:len(EventRun)+1] == EventRun+":":
		kind = EventRun
	case len(p) == len(EventLog)+2+2*idLen && p[:len(EventLog)+1] == EventLog+":":
		kind = EventLog
	default:
		return "", Ref{}, ErrBadPayload
	}
	rest := p[len(kind)+1:]
	if rest[idLen] != ':' {
		return "", Ref{}, ErrBadPayload
	}
	if r.RunID, err = uuid.Parse(rest[:idLen]); err != nil {
		return "", Ref{}, ErrBadPayload
	}
	if r.JobID, err = uuid.Parse(rest[idLen+1:]); err != nil {
		return "", Ref{}, ErrBadPayload
	}
	return kind, r, nil
}

// publish sends a run event in q's transaction.
func publish(ctx context.Context, pub *pubsub.Publisher, q *sqlc.Queries, kind string, r Ref) error {
	if pub == nil {
		pub = &pubsub.Publisher{}
	}
	return pub.PublishTx(ctx, q, pubsub.RunEvents.Name, Payload(kind, r))
}
