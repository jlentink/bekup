package restore

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/artifactverify"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/storage"
)

// Errors. Compare with errors.Is; ErrorCode maps them to the sanitized
// restores.error_code.
var (
	// ErrNotFound: no such restore or artifact.
	ErrNotFound = errors.New("restore: not found")
	// ErrNotQueued: the restore is no longer queued (another attempt
	// already started it, or it finished).
	ErrNotQueued = errors.New("restore: the restore is not queued")
	// ErrUnavailable: the artifact is deleted, missing, unverified or not
	// covered by a signed manifest.
	ErrUnavailable = errors.New("restore: the artifact is not available")
	// ErrKeyUnavailable: the job key version of the artifact is gone.
	ErrKeyUnavailable = errors.New("restore: the key of this artifact is not available")
	// ErrIdentityRequired: a recipient-encrypted artifact without an
	// identity handoff.
	ErrIdentityRequired = errors.New("restore: an age identity is required")
	// ErrIdentityExpired: the identity handoff expired before the worker
	// took it.
	ErrIdentityExpired = errors.New("restore: the age identity handoff expired")
	// ErrIdentityMismatch: the identity does not decrypt the artifact.
	ErrIdentityMismatch = errors.New("restore: the age identity does not match the backup")
	// ErrDestination: the destination could not be read.
	ErrDestination = errors.New("restore: destination unavailable")
	// ErrInsufficientSpace: the scratch volume cannot hold the artifact
	// plus storage.scratch_reserve_bytes.
	ErrInsufficientSpace = errors.New("restore: not enough space on the scratch volume")
	// ErrVerification: the staged artifact failed the verification (see
	// *VerificationError).
	ErrVerification = errors.New("restore: artifact verification failed")
	// ErrAlreadyOpened: Staged.Open may be called once.
	ErrAlreadyOpened = errors.New("restore: the staged artifact was already opened")
	// ErrCleanedUp: the staging was removed.
	ErrCleanedUp = errors.New("restore: the staged artifact was cleaned up")
	// ErrPanic: staging panicked (the panic is re-raised after cleanup).
	ErrPanic = errors.New("restore: staging panicked")

	// Restore worker and engine restorer errors (tasks 4.2, 4.3).

	// ErrEngineMismatch: the target connection's engine differs from the
	// engine of the backup (signed manifest); MySQL and MariaDB are
	// different engines.
	ErrEngineMismatch = errors.New("restore: the target's engine differs from the backup's")
	// ErrUnsupported: the engine, kind, mode or dump format cannot be
	// restored this way (e.g. globals on MySQL, a globals restore with a
	// database name).
	ErrUnsupported = errors.New("restore: this restore is not supported")
	// ErrNoRestorer: no EngineRestorer is registered for the engine.
	ErrNoRestorer = errors.New("restore: no restorer for this engine")
	// ErrTargetUnavailable: the target connection was deleted or (a
	// temporary one) expired.
	ErrTargetUnavailable = errors.New("restore: the target connection is no longer available")
	// ErrTargetNotReady: the target connection lacks a pin, host key or
	// credential.
	ErrTargetNotReady = errors.New("restore: the target connection is not ready")
	// ErrRestoreFailed: the restore tool exited non-zero.
	ErrRestoreFailed = errors.New("restore: the restore tool failed")
	// ErrRestoreIncomplete: the runner stream ended without a result, or
	// the bytes delivered differ from the dump size.
	ErrRestoreIncomplete = errors.New("restore: the restore did not complete")
	// ErrInterrupted: the worker stopped (shutdown) during the restore.
	ErrInterrupted = errors.New("restore: the worker stopped during the restore")
	// ErrAuditFailed: the restore.started entry could not be recorded
	// (fail closed: the restore does not start).
	ErrAuditFailed = errors.New("restore: the audit entry could not be recorded")

	// ErrNeutralizeFailed: the restored PostgreSQL database could not be
	// neutralized (SECURITY DEFINER -> INVOKER, PUBLIC access revoked;
	// D-171); the restore stops before the swap.
	ErrNeutralizeFailed = errors.New("restore: the restored database could not be neutralized")
	// ErrPrivilegedTarget: the target role became privileged (or its
	// privileges unknown) after the request and the restore carries no
	// matching acknowledgement of an admin (D-173).
	ErrPrivilegedTarget = errors.New("restore: the target role is privileged and the restore was not acknowledged for it")
	// ErrSwapStateUnknown: the swap call failed and the state of the
	// target could not be determined afterwards (D-171).
	ErrSwapStateUnknown = errors.New("restore: the state of the swap is unknown")

	// Restore API errors (task 4.5).

	// ErrFinished: the restore already finished (cancelRestore).
	ErrFinished = errors.New("restore: the restore already finished")
	// ErrTargetBusy: another restore into the same target database (or,
	// for globals, the same connection) is queued or running.
	ErrTargetBusy = errors.New("restore: another restore into this target is queued or running")
	// ErrTargetChanged: the target connection changed (engine, temporary
	// flag) between the checks and the insert; retry.
	ErrTargetChanged = errors.New("restore: the target connection changed")
	// ErrAbandoned: the stuck-restore sweep failed a restore that no
	// worker finished within the restore timeout (worker crash).
	ErrAbandoned = errors.New("restore: the restore was abandoned")
)

// Verification failure codes (VerificationError.Code, audit reason).
const (
	VerifyStoredDigest       = "stored_digest_mismatch"
	VerifyDumpDigest         = "dump_digest_mismatch"
	VerifyManifestSignature  = "manifest_bad_signature"
	VerifyManifestUnknownKey = "manifest_unknown_key"
	VerifyManifestReplayed   = "manifest_replayed"
	VerifyManifestBinding    = "manifest_binding_mismatch"
	VerifyManifestMalformed  = "manifest_malformed"
	VerifyManifestInvalid    = "manifest_invalid"
	VerifyDecrypt            = "decrypt_failed"
)

// VerificationError is a failed verification of a staged artifact: a
// possible tampering of the stored object or its manifest. It matches
// ErrVerification and unwraps to the cause (pipeline.ErrReplay, ...).
type VerificationError struct {
	Code string
	Err  error
}

func (e *VerificationError) Error() string {
	return "restore: artifact verification failed (" + e.Code + ")"
}

func (e *VerificationError) Unwrap() error { return e.Err }

// Is matches ErrVerification.
func (e *VerificationError) Is(target error) bool { return target == ErrVerification }

// manifestFailure classifies an artifactverify.ErrManifest error.
func manifestFailure(err error) *VerificationError {
	code := VerifyManifestInvalid
	switch {
	case errors.Is(err, pipeline.ErrReplay):
		code = VerifyManifestReplayed
	case errors.Is(err, pipeline.ErrBindingMismatch):
		code = VerifyManifestBinding
	case errors.Is(err, pipeline.ErrManifestBadSignature):
		code = VerifyManifestSignature
	case errors.Is(err, pipeline.ErrManifestUnknownKey):
		code = VerifyManifestUnknownKey
	case errors.Is(err, pipeline.ErrManifestMalformed):
		code = VerifyManifestMalformed
	}
	return &VerificationError{Code: code, Err: err}
}

// Error codes of restores.error_code (fixed, sanitized).
const (
	CodeArtifactUnavailable = "artifact_unavailable"
	CodeKeyUnavailable      = "key_unavailable"
	CodeIdentityRequired    = "identity_required"
	CodeIdentityExpired     = "identity_expired"
	CodeIdentityMismatch    = "identity_mismatch"
	CodeDestination         = "destination_unavailable"
	CodeInsufficientSpace   = "insufficient_scratch_space"
	CodeVerification        = "verification_failed"
	CodeHoldLost            = "artifact_hold_lost"
	CodeTimeout             = "timeout"
	CodeCanceled            = "canceled"
	CodeInternal            = "internal"

	CodeEngineMismatch    = "engine_mismatch"
	CodeUnsupported       = "unsupported_restore"
	CodeNoRestorer        = "no_restorer"
	CodeTargetUnavailable = "target_unavailable"
	CodeTargetNotReady    = "target_not_ready"
	CodeRestoreFailed     = "restore_failed"
	CodeRestoreIncomplete = "restore_incomplete"
	CodeInterrupted       = "interrupted"
	CodeSlotLost          = "slot_lost"
	CodeAuditFailed       = "audit_failed"
	CodeAbandoned         = "abandoned"
	CodeNeutralizeFailed  = "neutralize_failed"
	CodePrivilegedTarget  = "privileged_target"
	CodeSwapStateUnknown  = "swap_state_unknown"
	// Runner failures without a target reason (gRPC code only).
	CodeRunnerUnavailable = "runner_unavailable"
	CodeRunnerBusy        = "runner_busy"
	CodeTargetNotAllowed  = "target_not_allowed"
	CodeRunnerRejected    = "runner_rejected"
	CodeRunnerFailed      = "runner_failed"
)

// codeShape is the syntax of error and warning codes.
var codeShape = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

var codeMessages = map[string]string{
	CodeArtifactUnavailable: "The backup is no longer available (deleted, missing, unverified or unsigned).",
	CodeKeyUnavailable:      "The encryption key of this backup is no longer available.",
	CodeIdentityRequired:    "This backup is encrypted to an age recipient; the matching identity is required.",
	CodeIdentityExpired:     "The age identity handed to the restore expired before the restore started.",
	CodeIdentityMismatch:    "The age identity does not match the recipient of this backup.",
	CodeDestination:         "The destination holding the backup could not be read.",
	CodeInsufficientSpace:   "Not enough free space on the scratch volume to stage the backup.",
	CodeVerification:        "The backup failed verification (digest or signed manifest mismatch); it may have been tampered with.",
	CodeHoldLost:            "The backup could no longer be protected against retention.",
	CodeTimeout:             "The restore timed out.",
	CodeCanceled:            "The restore was canceled.",
	CodeInternal:            "Internal error.",
	CodeEngineMismatch:      "The target database server runs another engine than the backup (MySQL and MariaDB are different engines).",
	CodeUnsupported:         "This backup cannot be restored this way (engine, kind or format).",
	CodeNoRestorer:          "Restores for this engine are not available in this version.",
	CodeTargetUnavailable:   "The target connection was deleted or has expired.",
	CodeTargetNotReady:      "The target connection is not ready (TLS pin, SSH host key or credentials missing).",
	CodeRestoreFailed:       "The restore tool reported an error; see the restore log. An overwritten PostgreSQL database was left unchanged.",
	CodeRestoreIncomplete:   "The restore did not complete (the runner stream ended early or the size did not match).",
	CodeInterrupted:         "The worker stopped before the restore finished.",
	CodeSlotLost:            "The restore lost its concurrency slot (no heartbeat) and stopped.",
	CodeAuditFailed:         "The audit log could not be written; the restore was stopped (fail closed).",
	CodeAbandoned: "No worker finished the restore within the restore timeout (worker crash or restart); it was marked failed. " +
		"A temporary or partially restored database may remain on the target; an overwritten PostgreSQL database is only replaced at the final swap.",
	CodeNeutralizeFailed: "The restored database could not be hardened (SECURITY DEFINER functions switched to SECURITY INVOKER, " +
		"PUBLIC access revoked), or another role's session was connected to it during the restore; the restore was stopped. " +
		"An overwritten PostgreSQL database was left unchanged; a new database was left on the server without PUBLIC access.",
	CodePrivilegedTarget: "The target connection's role became privileged (or its privileges unknown) after the restore was requested, " +
		"and the restore was not acknowledged by an admin for these privileges; it did not start.",
	CodeSwapStateUnknown: "The swap into the target database reported an error and bekup could not determine whether it happened: " +
		"check the target database and any __bekup_old_ / __bekup_restore_ databases on the server.",
	CodeRunnerUnavailable: "The runner could not be reached.",
	CodeRunnerBusy:        "The runner is busy (too many concurrent runs).",
	CodeTargetNotAllowed:  "The runner's target allowlist does not allow this host.",
	CodeRunnerRejected:    "The runner rejected the request.",
	CodeRunnerFailed:      "The runner could not perform the operation.",
	// Normalized target reasons of the runner (D-134).
	"target_unreachable":        "The target database server could not be reached.",
	"target_auth_failed":        "The target database server rejected the credentials.",
	"target_tls_failed":         "The TLS connection to the target database server failed.",
	"tls_pin_mismatch":          "The target database server's TLS certificate does not match the pinned certificate (security alert raised).",
	"target_timeout":            "The target database server did not answer in time.",
	"target_permission_denied":  "The target connection's role lacks a privilege the restore needs (e.g. CREATEDB).",
	"target_unsupported_server": "The target server version is not supported by the allowlisted clients.",
	"target_database_not_found": "The target database does not exist.",
	"target_database_exists":    "The database to create already exists on the target server.",
	"ssh_host_key_mismatch":     "The SSH bastion presented another host key than the pinned one (security alert raised).",
	"ssh_auth_failed":           "The SSH bastion rejected the credentials.",
	"ssh_host_key_unsupported":  "The SSH bastion's host key type is not supported.",
	"target_failed":             "The database operation on the target failed; see the restore log.",
}

// ErrorCode maps an error of this package (or of the restore worker) to
// the sanitized restores.error_code. Unknown errors are "internal".
func ErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrVerification):
		return CodeVerification
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrNotFound):
		return CodeArtifactUnavailable
	case errors.Is(err, ErrKeyUnavailable):
		return CodeKeyUnavailable
	case errors.Is(err, ErrIdentityRequired):
		return CodeIdentityRequired
	case errors.Is(err, ErrIdentityExpired):
		return CodeIdentityExpired
	case errors.Is(err, ErrIdentityMismatch):
		return CodeIdentityMismatch
	case errors.Is(err, ErrInsufficientSpace), errors.Is(err, storage.ErrInsufficientSpace):
		return CodeInsufficientSpace
	case errors.Is(err, runstate.ErrHoldLost):
		return CodeHoldLost
	case errors.Is(err, ErrDestination), errors.Is(err, artifactverify.ErrDestination):
		return CodeDestination
	case errors.Is(err, ErrEngineMismatch):
		return CodeEngineMismatch
	case errors.Is(err, ErrUnsupported):
		return CodeUnsupported
	case errors.Is(err, ErrNoRestorer):
		return CodeNoRestorer
	case errors.Is(err, ErrTargetUnavailable):
		return CodeTargetUnavailable
	case errors.Is(err, ErrTargetNotReady):
		return CodeTargetNotReady
	case errors.Is(err, ErrRestoreFailed):
		return CodeRestoreFailed
	case errors.Is(err, ErrRestoreIncomplete):
		return CodeRestoreIncomplete
	case errors.Is(err, ErrInterrupted):
		return CodeInterrupted
	case errors.Is(err, runstate.ErrSlotLost):
		return CodeSlotLost
	case errors.Is(err, ErrAuditFailed):
		return CodeAuditFailed
	case errors.Is(err, ErrAbandoned):
		return CodeAbandoned
	case errors.Is(err, ErrPrivilegedTarget):
		return CodePrivilegedTarget
	case errors.Is(err, ErrSwapStateUnknown):
		return CodeSwapStateUnknown
	case errors.Is(err, ErrNeutralizeFailed):
		// Security events of the runner call keep their own code.
		if r := runnerpb.ErrorReason(err); r == runnerpb.ReasonPinMismatch || r == runnerpb.ReasonSSHHostKey {
			return runnerCode(err)
		}
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return CodeTimeout
		case errors.Is(err, context.Canceled):
			return CodeCanceled
		}
		return CodeNeutralizeFailed
	case errors.Is(err, context.DeadlineExceeded):
		return CodeTimeout
	case errors.Is(err, context.Canceled):
		return CodeCanceled
	default:
		return runnerCode(err)
	}
}

// runnerCode maps a runner (gRPC) error: the normalized target reason when
// present (D-134), else the gRPC code; anything else is internal.
func runnerCode(err error) string {
	if r := runnerpb.ErrorReason(err); r != "" {
		code := strings.ToLower(string(r))
		if _, ok := codeMessages[code]; ok {
			return code
		}
		return CodeRunnerFailed
	}
	st, ok := status.FromError(err)
	if !ok {
		return CodeInternal
	}
	switch st.Code() {
	case codes.Unavailable, codes.Unimplemented:
		return CodeRunnerUnavailable
	case codes.ResourceExhausted:
		return CodeRunnerBusy
	case codes.PermissionDenied:
		return CodeTargetNotAllowed
	case codes.InvalidArgument:
		return CodeRunnerRejected
	case codes.DeadlineExceeded:
		return CodeTimeout
	case codes.Canceled:
		return CodeCanceled
	default:
		return CodeRunnerFailed
	}
}

// ErrorMessage is the fixed text of an error code.
func ErrorMessage(code string) string {
	if m, ok := codeMessages[code]; ok {
		return m
	}
	return codeMessages[CodeInternal]
}
