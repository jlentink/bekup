package restore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// MySQL and MariaDB restores (task 4.3, D-165). Both engines restore a
// plain SQL dump (mysqldump / mariadb-dump without --databases, so the
// dump carries no CREATE DATABASE or USE) through the runner's Restore
// into the engine's hardened client, which the runner builds itself:
//
//	mysql   --system-command=OFF --commands=OFF --local-infile=0 --default-character-set=utf8mb4 --database=<db>
//	mariadb --sandbox --local-infile=0 --default-character-set=utf8mb4 --database=<db>
//
// The client stops at the first failing statement (no --force).
//
//   - ModeNewDB: CreateDatabase (the runner requires the database to be
//     absent), then Restore into it. A failed restore leaves the new,
//     partially restored database behind (there is no API to drop a
//     non-temporary database; the log says so).
//   - ModeOverwrite: in place, after the explicit warning of task 4.5
//     (MySQL and MariaDB have no RENAME DATABASE, so there is no temp
//     database and swap). The restore stream starts with a fixed prefix
//     built here from the validated name: it reads the database's default
//     character set and collation from information_schema.SCHEMATA (a
//     catalog view, never a user table, D-152/D-153), prepares the
//     CREATE DATABASE, drops the database, recreates it with the same
//     defaults and USEs it, all in the client's one session; then the dump
//     follows. Objects that are not in the backup (extra tables, views,
//     routines, events) are therefore removed. Grants on the database
//     survive (MySQL/MariaDB keep them in mysql.db). A failure after the
//     DROP leaves the database empty or partially restored.
//   - KindGlobals is refused (PostgreSQL only), and the source, the target
//     connection and this restorer must be the same engine: a MySQL dump
//     is never restored into MariaDB or the other way round (D-036).
//
// The stream goes through RunRestoreStream (runner_calls.go), which holds
// the last chunk back until src ended cleanly; this restorer adds the
// "-- Dump completed" trailer check to that end, so a dump without it is
// canceled before the client reads its last chunk.

func init() {
	RegisterEngine(string(engine.KindMySQL), mysqlRestorer{kind: engine.KindMySQL})
	RegisterEngine(string(engine.KindMariaDB), mysqlRestorer{kind: engine.KindMariaDB})
}

// MySQLOverwriteWarning is the explicit warning the restore API and UI
// (task 4.5) show before an in-place MySQL/MariaDB overwrite, together
// with the type-to-confirm of the database name.
const MySQLOverwriteWarning = "MySQL and MariaDB databases are overwritten in place: the target database is " +
	"dropped and recreated (same character set and collation), then the backup is restored into it. " +
	"Every table, view, routine, trigger and event that is not in the backup is permanently removed. " +
	"If the restore fails, the database stays empty or partially restored; " +
	"there is no automatic rollback. Grants on the database are kept."

// WarnPartialNewDatabase is the warning (EngineResult.Warnings) of a
// failed new-database restore that left the created database behind.
const WarnPartialNewDatabase = "partial_new_database"

// errMySQLNoTrailer: the dump does not end with the trailer.
var errMySQLNoTrailer = fmt.Errorf("%w: the dump does not end with the \"-- Dump completed\" trailer", ErrRestoreIncomplete)

// mysqlRestorer is the EngineRestorer of one of the two engines.
type mysqlRestorer struct{ kind engine.Kind }

// MySQLFamilyRestorer returns the restorer of MySQL or MariaDB (the same
// type that init registers; the integration tests drive it directly).
func MySQLFamilyRestorer(k engine.Kind) (EngineRestorer, bool) {
	if k != engine.KindMySQL && k != engine.KindMariaDB {
		return nil, false
	}
	return mysqlRestorer{kind: k}, true
}

// mysqlMaxDump mirrors the runner's total_bytes bound (64 TiB) minus room
// for the overwrite prefix.
const mysqlMaxDump = 64<<40 - 4096

// check validates a request before anything reaches the runner. Errors
// match both this package's sentinel (ErrorCode) and the engine one.
func (m mysqlRestorer) check(req *EngineRequest) error {
	if req.Kind == KindGlobals {
		return fmt.Errorf("%w: %s has no globals restore: %w", ErrUnsupported, m.kind, engine.ErrUnsupported)
	}
	if req.Kind != KindDatabase {
		return fmt.Errorf("%w: unknown restore kind: %w", ErrUnsupported, engine.ErrUnsupported)
	}
	if req.SourceEngine != m.kind || req.TargetEngine != m.kind {
		// MySQL and MariaDB are strictly separate engines (D-036).
		return fmt.Errorf("%w: the %s restorer got a %.16s dump for a %.16s target: %w",
			ErrEngineMismatch, m.kind, req.SourceEngine, req.TargetEngine, engine.ErrWrongEngine)
	}
	if req.Mode != ModeNewDB && req.Mode != ModeOverwrite {
		return fmt.Errorf("%w: unknown restore mode: %w", ErrUnsupported, engine.ErrUnsupported)
	}
	if err := engine.ValidateDBName(req.TargetDB); err != nil {
		return fmt.Errorf("%w: target database: %w", ErrUnsupported, err)
	}
	e, err := engine.ForKind(m.kind)
	if err != nil {
		return err
	}
	if e.IsSystemDatabase(req.TargetDB) {
		return fmt.Errorf("%w: target database: %w", ErrUnsupported, engine.ErrSystemDatabase)
	}
	if req.Runner == nil || req.Target == nil {
		return errors.New("restore: runner and target are required")
	}
	if req.TotalBytes == 0 || req.TotalBytes > mysqlMaxDump {
		return fmt.Errorf("%w: invalid dump size", ErrUnsupported)
	}
	return nil
}

// Restore implements EngineRestorer.
func (m mysqlRestorer) Restore(ctx context.Context, req EngineRequest, src io.Reader, logs LogSink) (EngineResult, error) {
	if err := m.check(&req); err != nil {
		return EngineResult{}, err
	}
	var prefix []byte
	switch req.Mode {
	case ModeNewDB:
		if err := CreateDatabase(ctx, &req, req.TargetDB); err != nil {
			return EngineResult{}, fmt.Errorf("restore: create database: %w", err)
		}
		logs.Line(LogInfo, "created the new database "+req.TargetDB)
	case ModeOverwrite:
		p, err := MySQLOverwritePrefix(req.TargetDB)
		if err != nil {
			return EngineResult{}, err
		}
		prefix = p
		logs.Line(LogWarn, "overwriting database "+req.TargetDB+" in place: it is dropped, recreated with its "+
			"character set and collation and restored from the backup; objects not in the backup are removed")
	}
	sreq := req
	sreq.TotalBytes += uint64(len(prefix))
	tr := &mysqlTrailerReader{r: src}
	res, err := RunRestoreStream(ctx, &sreq, StreamOptions{Database: req.TargetDB, Kind: runnerpb.DumpKind_DUMP_KIND_DATABASE},
		io.MultiReader(bytes.NewReader(prefix), tr), logs)
	if err == nil && res == nil {
		// Defensive: a stream the runner ended early without a result we
		// could check is never a success.
		err = fmt.Errorf("%w: no restore result", ErrRestoreIncomplete)
	}
	out := EngineResult{Warnings: append([]string(nil), res.GetWarnings()...)}
	if err != nil {
		switch {
		case errors.Is(err, errMySQLNoTrailer):
			logs.Line(LogError, "the dump does not end with the \"-- Dump completed\" line; the restore was canceled")
		case errors.Is(err, ErrRestoreFailed):
			logs.Line(LogError, fmt.Sprintf("the %s client exited with code %d", m.kind, res.GetExitCode()))
		}
		switch req.Mode {
		case ModeNewDB:
			logs.Line(LogError, "the restore failed; the new database "+req.TargetDB+" was left behind partially restored")
			out.Warnings = append(out.Warnings, WarnPartialNewDatabase)
		case ModeOverwrite:
			logs.Line(LogError, "the restore failed; database "+req.TargetDB+" may be empty or partially restored")
		}
		return out, err
	}
	logs.Line(LogInfo, "restore of database "+req.TargetDB+" completed")
	return out, nil
}

// mysqlTrailerReader passes the dump through and replaces its io.EOF with
// errMySQLNoTrailer when the dump does not end with the trailer.
type mysqlTrailerReader struct {
	r    io.Reader
	tail []byte
}

func (t *mysqlTrailerReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.tail = append(t.tail, p[:n]...)
		if len(t.tail) > engine.DumpTrailerWindow {
			t.tail = append(t.tail[:0], t.tail[len(t.tail)-engine.DumpTrailerWindow:]...)
		}
	}
	if errors.Is(err, io.EOF) && !engine.DumpTrailerOK(t.tail) {
		return n, errMySQLNoTrailer
	}
	return n, err
}

// MySQLOverwritePrefix is the SQL that precedes the dump of an in-place
// overwrite of db (the client is connected with --database=db). It
// captures the database defaults and prepares the CREATE before the DROP,
// so a missing database or an unreadable catalog fails before anything is
// dropped. db must be a valid, non-system name; validated names contain no
// quotes, backticks or backslashes, so the identifier is safe inside the
// string literal under every sql_mode.
func MySQLOverwritePrefix(db string) ([]byte, error) {
	q, err := engine.QuoteMySQLIdent(db)
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(q[1:len(q)-1], "`'\\\"") {
		return nil, fmt.Errorf("restore: %w", engine.ErrInvalidName)
	}
	var b strings.Builder
	b.WriteString("-- bekup: in-place overwrite of the target database (task 4.3)\n")
	b.WriteString("SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME INTO @bekup_cs, @bekup_co " +
		"FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = DATABASE();\n")
	b.WriteString("SET @bekup_create = CONCAT('CREATE DATABASE " + q + " CHARACTER SET ', @bekup_cs, ' COLLATE ', @bekup_co);\n")
	b.WriteString("PREPARE bekup_create FROM @bekup_create;\n")
	b.WriteString("DROP DATABASE " + q + ";\n")
	b.WriteString("EXECUTE bekup_create;\n")
	b.WriteString("DEALLOCATE PREPARE bekup_create;\n")
	b.WriteString("SET @bekup_create = NULL, @bekup_cs = NULL, @bekup_co = NULL;\n")
	b.WriteString("USE " + q + ";\n")
	return []byte(b.String()), nil
}
