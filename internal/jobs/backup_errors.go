package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/storage"
)

// Failure codes of backup runs and items (runs.error_code). The messages
// are fixed texts: never server output, DSNs, paths or secrets.
const (
	CodeJobMissing          = "job_missing"
	CodeJobDeleted          = "job_deleted"
	CodeJobDisabled         = "job_disabled"
	CodeConnectionDeleted   = "connection_deleted"
	CodeEncryption          = "encryption_unavailable"
	CodeDestinations        = "destinations_unavailable"
	CodeDestinationDropped  = "destination_dropped"
	CodeAllDestinations     = "all_destinations_failed"
	CodeNoDatabases         = "no_databases"
	CodeDatabaseMissing     = "database_missing"
	CodeDumpFailed          = "dump_failed"
	CodeDumpIncomplete      = "dump_incomplete"
	CodeVerifyFailed        = "verify_failed"
	CodeManifestFailed      = "manifest_failed"
	CodeIncomplete          = "incomplete"
	CodeTimeout             = "timeout"
	CodeCanceled            = runstate.CodeCanceled
	CodeInterrupted         = runstate.CodeInterrupted
	CodeRunnerUnavailable   = "runner_unavailable"
	CodeRunnerBusy          = "runner_busy"
	CodeRunnerRejected      = "runner_rejected"
	CodeTargetNotAllowed    = "target_not_allowed"
	CodeRunnerFailed        = "runner_failed"
	CodeStorageFailed       = "storage_failed"
	CodeInternal            = "internal"
	CodeRolesNoPasswords    = "roles_without_passwords"
	CodeSlotLost            = "slot_lost"
	CodeSecurityAuditFailed = "security_audit_failed"
)

// Run-context cancellation causes.
var (
	errRunCanceled = errors.New("jobs: the run was canceled")
	errJobTimeout  = errors.New("jobs: the job timeout expired")
	errSlotLost    = errors.New("jobs: the run slot expired")
)

func fail(code, format string, args ...any) *runstate.Failure {
	return &runstate.Failure{Code: code, Message: fmt.Sprintf(format, args...)}
}

// stopFailure describes why the run context ended (nil while it is live).
func stopFailure(ctx context.Context) *runstate.Failure {
	if ctx.Err() == nil {
		return nil
	}
	cause := context.Cause(ctx)
	switch {
	case errors.Is(cause, errRunCanceled), errors.Is(cause, errCanceledRemotely):
		return fail(CodeCanceled, "the run was canceled")
	case errors.Is(cause, errJobTimeout), errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fail(CodeTimeout, "the run exceeded its timeout")
	case errors.Is(cause, errSlotLost):
		return fail(CodeSlotLost, "the run lost its concurrency slot (no heartbeat) and stopped")
	default:
		return fail(CodeInterrupted, "the worker stopped (shutdown) before the run finished")
	}
}

// runnerFailure maps an error of a runner call to a sanitized failure. The
// runner's normalized reasons (D-134) carry fixed messages; everything else
// is classified by gRPC code only.
func runnerFailure(ctx context.Context, err error) *runstate.Failure {
	if f := stopFailure(ctx); f != nil {
		return f
	}
	if r := runnerpb.ErrorReason(err); r != "" {
		msg, ok := connections.ReasonMessage(r)
		if r == runnerpb.ReasonPinMismatch {
			msg, ok = "the database server's TLS certificate does not match the pinned certificate (security alert raised)", true
		}
		if r == runnerpb.ReasonSSHHostKey {
			msg, ok = "the SSH bastion presented another host key than the pinned one (security alert raised)", true
		}
		if !ok {
			msg = "the database operation failed"
		}
		return &runstate.Failure{Code: strings.ToLower(string(r)), Message: msg}
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.Unimplemented:
		return fail(CodeRunnerUnavailable, "the runner could not be reached")
	case codes.ResourceExhausted:
		return fail(CodeRunnerBusy, "the runner is busy (too many concurrent runs)")
	case codes.PermissionDenied:
		return fail(CodeTargetNotAllowed, "the runner's target allowlist (runner.toml [targets]) does not allow this host")
	case codes.InvalidArgument:
		return fail(CodeRunnerRejected, "the runner rejected the request")
	case codes.DeadlineExceeded:
		return fail(CodeTimeout, "the runner operation timed out")
	case codes.Canceled:
		return fail(CodeCanceled, "the runner operation was canceled")
	default:
		return fail(CodeRunnerFailed, "the runner could not perform the operation")
	}
}

// retryable reports failures a later attempt may overcome (the runner was
// down or saturated). Only runs that stored nothing are retried.
func retryable(f *runstate.Failure) bool {
	return f != nil && (f.Code == CodeRunnerUnavailable || f.Code == CodeRunnerBusy || f.Code == CodeInterrupted)
}

// securityReason reports runner errors that are security events.
func securityReason(err error) bool {
	r := runnerpb.ErrorReason(err)
	return r == runnerpb.ReasonPinMismatch || r == runnerpb.ReasonSSHHostKey
}

// destinationWarning describes a dropped destination (fixed texts).
func destinationWarning(db, dest, reason string, code storage.ErrorCode) runstate.Warning {
	msg := "the destination was dropped: " + code.Message()
	switch reason {
	case "lag":
		msg = "the destination was dropped: it did not keep up (lag threshold exceeded)"
	case "verify":
		msg = "the destination was dropped: the stored object did not match the streamed bytes"
	case "unavailable":
		msg = "the destination could not be opened: " + code.Message()
	}
	return runstate.Warning{Code: CodeDestinationDropped, Message: msg, Database: db, DestinationID: dest}
}

// runnerWarnings maps the runner's stable warning codes to messages.
var runnerWarnings = map[string]string{
	"non_innodb_tables":                        "the database has non-transactional (non-InnoDB) tables: the dump may be inconsistent if they were written during the backup",
	"databases_with_unsupported_names_skipped": "the server has databases whose names bekup does not support; they were skipped",
	"server_not_supported":                     "the server version is not supported by the allowlisted clients",
	"no_allowlisted_client_for_server":         "no allowlisted client image matches the server version exactly",
	"roles_without_passwords":                  "the globals were dumped without role passwords (--no-role-passwords): the backup role cannot read pg_authid",
	"stats_unavailable":                        "the source statistics could not be captured; the backup has no completeness evidence",
}

// runnerWarning converts one runner warning code (anything else is
// replaced by a generic text; the code is kept only when well-formed).
func runnerWarning(code, db string) runstate.Warning {
	msg, ok := runnerWarnings[code]
	if !ok {
		msg = "the runner reported a warning"
		if !codeShape.MatchString(code) {
			code = "runner_warning"
		}
	}
	return runstate.Warning{Code: code, Message: msg, Database: db}
}
