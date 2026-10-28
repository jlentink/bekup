package restore

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Engine-specific restores (tasks 4.2, 4.3). The restore worker stages and
// verifies the artifact (Stage), then hands the plaintext dump to the
// EngineRestorer registered for the target's engine: PostgreSQL in
// postgres.go (task 4.2), MySQL and MariaDB in mysql.go (task 4.3). An
// engine restorer only talks to the runner (CreateDatabase, Restore,
// RenameSwapDatabase, DropTempDatabase); it never opens a database
// connection itself.

// Kind is what a restore writes.
type Kind string

// Restore kinds. A globals restore (PostgreSQL roles and tablespaces) is a
// separate, admin-only action (task 4.5 authorizes it) and never mixes with
// a database restore.
const (
	KindDatabase Kind = "database"
	KindGlobals  Kind = "globals"
)

// Log levels of LogSink.Line.
const (
	LogInfo  = "info"
	LogWarn  = "warn"
	LogError = "error"
)

// LogSink receives the log lines of a restore (worker events and the
// runner's tool output). The worker's sink sanitizes, redacts (the target's
// credentials and credentials in URLs/DSNs), truncates, caps and batches
// them into restore_logs; Line never blocks. Never pass secrets or dump
// contents.
type LogSink interface {
	Line(level, msg string)
}

// TargetBuilder returns the runner request target of the restore's target
// connection with its credentials unsealed (connections.RestoreTarget).
// Build a fresh target for every RPC and call release right after the RPC
// returned (or the stream was opened): it zeroes the credentials.
type TargetBuilder func(ctx context.Context) (target *runnerpb.Target, release func(), err error)

// EngineRequest is a verified restore handed to an engine restorer.
type EngineRequest struct {
	RestoreID uuid.UUID
	// Runner is the runner API (mTLS).
	Runner runnerpb.RunnerServiceClient
	// Target builds the runner target (see TargetBuilder).
	Target TargetBuilder
	// TargetEngine is the target connection's engine; the worker has
	// checked that it equals SourceEngine.
	TargetEngine engine.Kind
	// TargetDB is the database to write ("" for KindGlobals).
	TargetDB string
	Mode     Mode
	Kind     Kind
	// SourceEngine / SourceVersion are the engine and the catalog version
	// key of the dump client, from the verified, signed manifest. The
	// runner picks the restore client max(SourceVersion, target server)
	// itself (D-036).
	SourceEngine  engine.Kind
	SourceVersion string
	// Format is the dump format (PostgreSQL databases: custom archives;
	// globals and MySQL/MariaDB: SQL).
	Format pipeline.DumpFormat
	// TotalBytes is the exact plaintext dump size (the runner's
	// RestoreHeader.total_bytes).
	TotalBytes uint64
	// NoOwner: PostgreSQL pg_restore --no-owner (restores.no_owner,
	// default on).
	NoOwner bool
	// OnNeutralized is called by the PostgreSQL restorer after it
	// neutralized the restored database (NeutralizeRestored) and before
	// the swap or the success (D-171); an error stops the restore there
	// (the worker audits restore.secdef_neutralized, fail closed). nil:
	// nothing is called.
	OnNeutralized func(ctx context.Context, n Neutralized) error
}

// RunContext is the runner RunContext of the restore (its id is the
// runner's audit run_id).
func (r *EngineRequest) RunContext() *runnerpb.RunContext {
	return &runnerpb.RunContext{RunId: r.RestoreID.String()}
}

// TempID is the restore's temp_id for the runner ([a-z0-9]{12}): the
// first 48 bits of the restore id in hex. The runner derives the temporary
// database names from it (<db>__bekup_restore_<id>, <db>__bekup_old_<id>,
// D-123).
func (r *EngineRequest) TempID() string { return TempID(r.RestoreID) }

// TempID is EngineRequest.TempID for an id.
func TempID(id uuid.UUID) string { return hex.EncodeToString(id[:6]) }

// EngineResult is the outcome of a successful engine restore.
type EngineResult struct {
	// Warnings are stable codes ([a-z][a-z0-9_]{0,63}; the runner's
	// warnings and the engine's own, e.g. old_database_kept).
	Warnings []string
	// Neutralized is set when the PostgreSQL restorer neutralized the
	// restored database (D-171).
	Neutralized *Neutralized
}

// EngineRestorer performs the engine-specific part of a restore once the
// artifact is staged and verified. src is the plaintext dump stream; its
// final Read returns a verification error instead of io.EOF when the
// staged copy does not match the records (do not complete the restore
// then: RunRestoreStream holds the last chunk back until src ended
// cleanly). Errors are mapped to the sanitized restores.error_code by
// ErrorCode; never put server output into them.
type EngineRestorer interface {
	Restore(ctx context.Context, req EngineRequest, src io.Reader, logs LogSink) (EngineResult, error)
}

var (
	enginesMu sync.RWMutex
	engines   = map[string]EngineRestorer{}
)

// RegisterEngine registers the restorer of an engine ("postgres", "mysql",
// "mariadb"; engine.Kind values). Call it from an init function; it panics
// on an unknown engine, a nil restorer or a second registration.
func RegisterEngine(name string, r EngineRestorer) {
	switch engine.Kind(name) {
	case engine.KindPostgres, engine.KindMySQL, engine.KindMariaDB:
	default:
		panic(fmt.Sprintf("restore: RegisterEngine: unknown engine %q", name))
	}
	if r == nil {
		panic("restore: RegisterEngine: nil restorer for " + name)
	}
	enginesMu.Lock()
	defer enginesMu.Unlock()
	if _, dup := engines[name]; dup {
		panic("restore: RegisterEngine: engine registered twice: " + name)
	}
	engines[name] = r
}

// restorerFor returns the restorer of an engine.
func restorerFor(k engine.Kind) (EngineRestorer, bool) {
	enginesMu.RLock()
	defer enginesMu.RUnlock()
	r, ok := engines[string(k)]
	return r, ok
}

// RegisteredEngines lists the engines with a restorer (sorted).
func RegisteredEngines() []string {
	enginesMu.RLock()
	defer enginesMu.RUnlock()
	out := make([]string, 0, len(engines))
	for k := range engines {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
