package runstate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// SlotKind is what holds a run slot.
type SlotKind string

// Slot kinds: backups and restores share the global limit.
const (
	SlotBackup  SlotKind = "backup"
	SlotRestore SlotKind = "restore"
)

// Slot lease policy (D-147).
const (
	// SlotLease is how long a slot survives without a heartbeat (a crashed
	// worker's slot is free again after it).
	SlotLease = 2 * time.Minute
	// SlotHeartbeat is how often a holder refreshes its slot.
	SlotHeartbeat = 30 * time.Second
)

// SlotLimits are workers.max_concurrent_runs and
// workers.max_runs_per_connection.
type SlotLimits struct {
	Global int
	// PerConnection is the default per-connection limit: a connection's
	// own max_concurrent_runs (task 3.13, D-156) replaces it when set.
	PerConnection int
}

// ErrSlotLost reports that the slot expired (no heartbeat for SlotLease) and was taken
// over; the holder must stop.
var ErrSlotLost = errors.New("runstate: the run slot expired")

// Slots is the DB-backed run semaphore: one run_slots row per running
// backup or restore. Acquiring serializes on a transaction-scoped advisory
// lock (no session locks on pooled connections), removes stale slots and
// inserts the holder only when both limits leave room. Safe across any
// number of serve and worker processes; each process applies its own
// configured limits.
type Slots struct {
	db DB
}

// NewSlots returns the semaphore on pool.
func NewSlots(pool DB) *Slots { return &Slots{db: pool} }

// TryAcquire takes a slot for holder (the run id) against connection. The
// per-connection limit is the connection's max_concurrent_runs when set,
// else l.PerConnection; it is read under the slot lock, so a changed limit
// applies to the next acquisition (lowering it never stops active runs,
// new runs wait until the count is below it). l.Global always applies.
func (s *Slots) TryAcquire(ctx context.Context, holder, connection uuid.UUID, kind SlotKind, l SlotLimits) (bool, error) {
	if l.Global < 1 || l.PerConnection < 1 {
		return false, errors.New("runstate: slot limits must be positive")
	}
	var ok bool
	err := db.Tx(ctx, s.db, func(q *sqlc.Queries) error {
		if err := q.LockRunSlots(ctx); err != nil {
			return fmt.Errorf("runstate: lock run slots: %w", err)
		}
		if _, err := q.DeleteStaleRunSlots(ctx, SlotLease.Seconds()); err != nil {
			return fmt.Errorf("runstate: expire run slots: %w", err)
		}
		n, err := q.TryInsertRunSlot(ctx, sqlc.TryInsertRunSlotParams{
			HolderID: holder, Kind: string(kind), ConnectionID: connection,
			MaxGlobal: int64(l.Global), MaxPerConnection: int64(l.PerConnection),
		})
		if err != nil {
			return fmt.Errorf("runstate: take run slot: %w", err)
		}
		ok = n == 1
		return nil
	})
	return ok, err
}

// Heartbeat refreshes holder's slot; ErrSlotLost when it is gone.
func (s *Slots) Heartbeat(ctx context.Context, holder uuid.UUID) error {
	n, err := sqlc.New(s.db).HeartbeatRunSlot(ctx, holder)
	if err != nil {
		return fmt.Errorf("runstate: run slot heartbeat: %w", err)
	}
	if n == 0 {
		return ErrSlotLost
	}
	return nil
}

// Release frees holder's slot (idempotent).
func (s *Slots) Release(ctx context.Context, holder uuid.UUID) error {
	if err := sqlc.New(s.db).DeleteRunSlot(ctx, holder); err != nil {
		return fmt.Errorf("runstate: release run slot: %w", err)
	}
	return nil
}
