package connections

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Temporary restore connections (task 4.4, D-163). An admin creates one
// with step-up inside the restore wizard; it is validated like any other
// connection (pinned TLS by default, D-132, the runner's target allowlist
// on every use) and is usable only as a restore target: List, Get,
// Update, Delete, SetTLSMode and Target never see it, and the jobs service
// refuses it. The runner, pin, host-key and SSH-key operations accept it
// when the actor has AllowTemporary (admins). It expires TempTTL after its
// creation (an admin may move the expiry up to TempMaxTTL after the
// creation); the cleanup worker then, or once the restores that used it
// have finished (plus RestoreGrace), destroys its credentials and soft
// deletes the row (restores keep referencing it).

// Defaults of restore.temp_connection_ttl / temp_connection_max_ttl.
const (
	DefaultTempTTL    = 24 * time.Hour
	DefaultTempMaxTTL = 7 * 24 * time.Hour
	// RestoreGrace is how long a temporary connection stays after the last
	// restore that used it finished (a retry can reuse it meanwhile).
	RestoreGrace = 15 * time.Minute
	// MaxActiveTemporary bounds the unexpired temporary connections.
	MaxActiveTemporary = 50
	// CleanupInterval is the period of the cleanup job.
	CleanupInterval  = 5 * time.Minute
	cleanupBatchSize = 100
)

// Reasons of an automatic deletion (audit detail "reason").
const (
	ReasonExpired         = "expired"
	ReasonRestoreFinished = "restore_finished"
	reasonManual          = "manual"
)

// ErrTooManyTemporary reports that MaxActiveTemporary unexpired temporary
// connections exist already.
var ErrTooManyTemporary = errors.New("connections: too many temporary restore connections")

// temporaryName is the default name of a temporary connection.
func temporaryName(host string) string {
	n := "Temporary: " + strings.TrimSpace(host)
	for utf8.RuneCountInString(n) > maxNameRunes {
		_, size := utf8.DecodeLastRuneInString(n)
		n = n[:len(n)-size]
	}
	return n
}

func (s *Service) checkTemporaryCap(ctx context.Context, q *sqlc.Queries) error {
	n, err := q.CountActiveTemporaryConnections(ctx, s.o.Now())
	if err != nil {
		return fmt.Errorf("connections: count temporary: %w", err)
	}
	if n >= MaxActiveTemporary {
		return ErrTooManyTemporary
	}
	return nil
}

// CreateTemporary validates and stores a temporary restore connection that
// expires TempTTL from now. The caller must have authorized
// connection.temporary_create (admin, step-up) and, for a TLS mode other
// than pinned, connection.tls_downgrade. Names may repeat (temporary
// connections never block a name); an empty name gets a default. The
// secret fields of in are zeroed before CreateTemporary returns.
func (s *Service) CreateTemporary(ctx context.Context, actor Actor, in CreateInput) (Connection, error) {
	return s.create(ctx, actor, in, &temporaryCreate{expiresAt: s.o.Now().Add(s.o.TempTTL).UTC()})
}

// ListTemporary returns the unexpired temporary connections, newest first
// (at most 200; the restore wizard of admins).
func (s *Service) ListTemporary(ctx context.Context) ([]Connection, error) {
	rows, err := sqlc.New(s.o.DB).ListTemporaryConnections(ctx, s.o.Now())
	if err != nil {
		return nil, fmt.Errorf("connections: list temporary: %w", err)
	}
	out := make([]Connection, 0, len(rows))
	for i := range rows {
		out = append(out, s.view(&rows[i]))
	}
	return out, nil
}

// TempMaxTTL is the cap on a temporary connection's lifetime.
func (s *Service) TempMaxTTL() time.Duration { return s.o.TempMaxTTL }

// ExtendTemporary moves the expiry of a temporary connection to until,
// which must lie in the future and at most TempMaxTTL after the
// connection's creation (so extensions can never keep it alive longer).
func (s *Service) ExtendTemporary(ctx context.Context, actor Actor, id uuid.UUID, until time.Time) (Connection, error) {
	now := s.o.Now()
	until = until.UTC().Truncate(time.Second)
	var out sqlc.Connection
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		row, err := s.loadTemporary(ctx, q, id)
		if err != nil {
			return err
		}
		if row.ExpiresAt == nil || !row.ExpiresAt.After(now) {
			return ErrNotFound // expired: the cleanup worker deletes it
		}
		latest := row.CreatedAt.Add(s.o.TempMaxTTL)
		switch {
		case !until.After(now):
			return fieldErr("expiresAt", "must be in the future")
		case until.After(latest):
			return fieldErr("expiresAt", "must not be later than %s (restore.temp_connection_max_ttl after the creation)",
				latest.UTC().Format(time.RFC3339))
		}
		out, err = q.SetTemporaryConnectionExpiry(ctx, sqlc.SetTemporaryConnectionExpiryParams{ID: id, ExpiresAt: &until})
		if err != nil {
			return fmt.Errorf("connections: extend temporary: %w", err)
		}
		return s.record(ctx, q, actor, audit.ActionConnectionTemporaryExtended, id, audit.Details{
			"name": row.Name, "from": row.ExpiresAt.UTC().Format(time.RFC3339), "to": until.Format(time.RFC3339),
		}, nil)
	})
	if err != nil {
		return Connection{}, err
	}
	return s.view(&out), nil
}

// loadTemporary locks an unexpired temporary connection (ErrNotFound for
// permanent, deleted or expired ones).
func (s *Service) loadTemporary(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (sqlc.Connection, error) {
	row, err := q.GetTemporaryConnectionForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Connection{}, ErrNotFound
	}
	if err != nil {
		return sqlc.Connection{}, fmt.Errorf("connections: load temporary: %w", err)
	}
	return row, nil
}

// DeleteTemporary destroys a temporary connection's credentials and
// deletes it (an admin, before it expires). A restore that already holds
// its runner request keeps running; later restores cannot use it.
func (s *Service) DeleteTemporary(ctx context.Context, actor Actor, id uuid.UUID) error {
	return db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		row, err := s.loadTemporary(ctx, q, id)
		if err != nil {
			return err
		}
		return s.shred(ctx, q, &row, audit.UserActor(actor.UserID), actor, audit.ActionConnectionTemporaryDeleted, reasonManual)
	})
}

// storedCredentialFields names the credentials a row holds (audit).
func storedCredentialFields(row *sqlc.Connection) []string {
	var f []string
	if row.HasPassword {
		f = append(f, "password")
	}
	switch SSHAuth(row.SshAuth) {
	case SSHAuthPassword:
		f = append(f, "ssh_password")
	case SSHAuthPrivateKey:
		f = append(f, "ssh_private_key")
		if row.SshHasPassphrase {
			f = append(f, "ssh_private_key_passphrase")
		}
	case SSHAuthNone:
	}
	if row.ClientCertPem != nil {
		f = append(f, "client_key")
	}
	return f
}

// shred destroys the sealed credentials of a locked temporary row (the
// column and every flag describing it) and soft deletes it, audited in the
// same transaction (fail closed). The in-memory copy of the sealed blob is
// zeroed as well.
func (s *Service) shred(ctx context.Context, q *sqlc.Queries, row *sqlc.Connection, who audit.Actor, actor Actor, action audit.Action, reason string) error {
	defer crypto.Zero(row.SecretEnc)
	fields := storedCredentialFields(row)
	n, err := q.ShredTemporaryConnection(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("connections: shred temporary: %w", err)
	}
	if n != 1 {
		return ErrNotFound
	}
	ev := audit.Event{
		Actor: who, Action: action, TargetType: "connection", TargetID: row.ID.String(), IP: actor.IP,
		Details: audit.Details{
			"name": row.Name, "engine": string(row.Engine), "host": row.Host, "port": row.Port,
			"reason": reason, "destroyed_fields": fields,
		},
	}
	if row.ExpiresAt != nil {
		ev.Details["expires_at"] = row.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if _, err := s.o.Auditor.RecordTx(ctx, q, ev); err != nil {
		return err
	}
	if len(fields) == 0 {
		return nil
	}
	ev.Action = audit.ActionSecretChanged
	ev.Details = audit.Details{"fields": fields, "via": string(action), "destroyed": true}
	_, err = s.o.Auditor.RecordTx(ctx, q, ev)
	return err
}

// cleanupState is what the cleanup decides on.
type cleanupState struct {
	expiresAt      *time.Time
	activeRestores int64
	restores       int64
	lastFinished   time.Time
	slotHeld       bool
}

// cleanupReason says whether (and why) a temporary connection is deleted
// now: never while a run holds a slot on it; when it expired; when every
// restore that used it finished at least RestoreGrace ago.
func cleanupReason(st cleanupState, now time.Time) (string, bool) {
	switch {
	case st.slotHeld:
		return "", false
	case st.expiresAt == nil || !st.expiresAt.After(now):
		return ReasonExpired, true
	case st.restores > 0 && st.activeRestores == 0 && !st.lastFinished.Add(RestoreGrace).After(now):
		return ReasonRestoreFinished, true
	default:
		return "", false
	}
}

// CleanupResult counts the deletions of one cleanup pass.
type CleanupResult struct {
	Expired         int
	RestoreFinished int
}

// CleanupTemporary deletes (crypto-shreds and soft deletes) the temporary
// connections that expired or whose restores finished, each in its own
// transaction with the decision re-checked under the row lock, audited
// with the system actor. It stops at the first error (the job retries).
func (s *Service) CleanupTemporary(ctx context.Context) (CleanupResult, error) {
	var res CleanupResult
	now := s.o.Now()
	rows, err := sqlc.New(s.o.DB).ListTemporaryConnectionCleanup(ctx, sqlc.ListTemporaryConnectionCleanupParams{
		Now: now, MaxRows: cleanupBatchSize,
	})
	if err != nil {
		return res, fmt.Errorf("connections: list temporary cleanup: %w", err)
	}
	for _, c := range rows {
		if _, ok := cleanupReason(cleanupState{
			expiresAt: c.ExpiresAt, activeRestores: c.ActiveRestores, restores: c.Restores, lastFinished: c.LastRestoreFinishedAt,
		}, now); !ok {
			continue
		}
		var reason string
		err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
			row, err := q.GetTemporaryConnectionForUpdate(ctx, c.ID)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil // deleted in the meantime
			}
			if err != nil {
				return fmt.Errorf("connections: lock temporary: %w", err)
			}
			st, err := q.GetTemporaryConnectionCleanupState(ctx, c.ID)
			if err != nil {
				return fmt.Errorf("connections: temporary cleanup state: %w", err)
			}
			r, ok := cleanupReason(cleanupState{
				expiresAt: st.ExpiresAt, activeRestores: st.ActiveRestores, restores: st.Restores,
				lastFinished: st.LastRestoreFinishedAt, slotHeld: st.SlotHeld,
			}, s.o.Now())
			if !ok {
				return nil
			}
			reason = r
			return s.shred(ctx, q, &row, audit.SystemActor(), Actor{}, audit.ActionConnectionTemporaryExpired, r)
		})
		if err != nil {
			return res, err
		}
		switch reason {
		case ReasonExpired:
			res.Expired++
		case ReasonRestoreFinished:
			res.RestoreFinished++
		}
	}
	return res, nil
}

// --- restore targets (tasks 4.1-4.5) ----------------------------------------

// GetForRestore returns a connection that can be a restore target: a
// permanent one or an unexpired temporary one (Connection.Temporary tells
// which; temporary targets are admin only, authz.RestoreTarget.Temporary).
func (s *Service) GetForRestore(ctx context.Context, id uuid.UUID) (Connection, error) {
	row, err := s.loadAs(ctx, sqlc.New(s.o.DB), id, false, true)
	if err != nil {
		return Connection{}, err
	}
	return s.view(&row), nil
}

// LockRestoreTargetTx share-locks a restore target inside the transaction
// that records the restore (task 4.5), so the cleanup worker cannot delete
// a temporary target while the restore is being created.
func (s *Service) LockRestoreTargetTx(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (Connection, error) {
	row, err := q.GetConnectionAnyForShare(ctx, sqlc.GetConnectionAnyForShareParams{ID: id, Now: s.o.Now()})
	if errors.Is(err, pgx.ErrNoRows) {
		return Connection{}, ErrNotFound
	}
	if err != nil {
		return Connection{}, fmt.Errorf("connections: lock restore target: %w", err)
	}
	return s.view(&row), nil
}

// RunningRestoreTarget is RestoreTarget for a restore that holds a run
// slot on the connection (holder: the slot's holder id): a temporary
// target that expired after the restore started stays usable until the
// restore ends (the cleanup worker never deletes a connection while a slot
// is held), a deleted one never is (D-173). Call release right after the
// RPC.
func (s *Service) RunningRestoreTarget(ctx context.Context, id, holder uuid.UUID) (*runnerpb.Target, func(), error) {
	row, err := sqlc.New(s.o.DB).GetConnectionForSlotHolder(ctx, sqlc.GetConnectionForSlotHolderParams{ID: id, Now: s.o.Now(), HolderID: holder})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("connections: load restore target: %w", err)
	}
	return s.target(&row)
}

// RestoreTarget is Target for restore runs: it also accepts an unexpired
// temporary restore connection. Backup runs use Target, which never does.
// Call release right after the RPC.
func (s *Service) RestoreTarget(ctx context.Context, id uuid.UUID) (*runnerpb.Target, func(), error) {
	row, err := s.loadAs(ctx, sqlc.New(s.o.DB), id, false, true)
	if err != nil {
		return nil, nil, err
	}
	return s.target(&row)
}
