package restore

import (
	"context"

	"github.com/google/uuid"
)

// NotifyEvent is a restore notification event of the D-161 catalog.
type NotifyEvent string

// Restore lifecycle events (D-161). A canceled restore is reported as
// failed (status canceled).
const (
	NotifyStarted   NotifyEvent = "restore.started"
	NotifyCompleted NotifyEvent = "restore.completed"
	NotifyFailed    NotifyEvent = "restore.failed"
)

// Notification is one restore event (identifiers and fixed codes only).
type Notification struct {
	Event     NotifyEvent
	RestoreID uuid.UUID
	JobID     uuid.UUID
	// Status and ErrorCode are set for completed / failed.
	Status    Status
	ErrorCode string
}

// Notifier is the emit hook for restore notifications. Task 5.3 delivers
// them; until then the worker uses NoNotify. Emit must not block for long
// and cannot fail the restore.
type Notifier interface {
	Emit(ctx context.Context, n Notification)
}

// NoNotify discards notifications.
type NoNotify struct{}

// Emit implements Notifier.
func (NoNotify) Emit(context.Context, Notification) {}
