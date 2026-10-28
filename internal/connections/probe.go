package connections

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Runner operations: test, version detection, database listing, image
// pre-pull, certificate and host-key capture. They are rate limited per
// user, bounded in concurrency and time, and every result carries only
// normalized codes and fixed messages (the runner's D-134 reasons), so
// they are fit for maintainers. A pin or host-key mismatch is a security
// event: audited, alerted and returned as its own error type.

// prePullTimeout bounds PrePull (image pulls can take minutes; the runner
// also applies docker.pull_timeout).
const prePullTimeout = 10 * time.Minute

// TargetError is a normalized runner/target failure; Code is the runner's
// reason (or a serve-side code) and Message a fixed, presentable text.
type TargetError struct {
	Code    string
	Message string
}

func (e *TargetError) Error() string { return "connections: target: " + e.Code }

// PinMismatchError reports that the target's certificate does not match the pin
// (security event). Check is the runner's failed check (leaf_spki,
// untrusted_chain, hostname, expired, no_certificate).
type PinMismatchError struct{ Check string }

func (e *PinMismatchError) Error() string { return "connections: tls pin mismatch: " + e.Check }

// HostKeyMismatchError reports that the SSH bastion presented another host
// key than the pinned one (security event).
type HostKeyMismatchError struct{}

func (e *HostKeyMismatchError) Error() string { return "connections: ssh host key mismatch" }

// Codes of TargetError besides the runner reasons.
const (
	CodeTargetNotAllowed = "TARGET_NOT_ALLOWED"
	CodeInvalidRequest   = "INVALID_REQUEST"
	CodeRunnerFailed     = "RUNNER_FAILED"
)

// reasonMessages are the presentable texts of the runner reasons.
var reasonMessages = map[runnerpb.Reason]string{
	runnerpb.ReasonUnreachable:           "the database server could not be reached",
	runnerpb.ReasonAuthFailed:            "the database rejected the user name or password",
	runnerpb.ReasonTLSFailed:             "the TLS handshake with the database failed (does the server support TLS?)",
	runnerpb.ReasonTimeout:               "the database did not answer in time",
	runnerpb.ReasonPermission:            "the database user lacks a required privilege",
	runnerpb.ReasonUnsupportedServer:     "the server version is not supported by the allowlisted client images",
	runnerpb.ReasonDatabaseNotFound:      "the database does not exist on the server",
	runnerpb.ReasonDatabaseExists:        "the database already exists on the server",
	runnerpb.ReasonSSHAuthFailed:         "the SSH bastion rejected the user or key",
	runnerpb.ReasonSSHHostKeyUnsupported: "the SSH server's host key type is not accepted (ssh-ed25519, ECDSA or RSA ≥ 2048 bits required)",
	runnerpb.ReasonFailed:                "the database operation failed",
}

// acquire takes a token from the caller's limiter and a concurrency slot.
func (s *Service) acquire(actor Actor) (func(), error) {
	return s.limiter.Acquire(actor.UserID.String())
}

// begin loads the connection and acquires a slot; the returned context is
// bounded by OperationTimeout.
func (s *Service) begin(ctx context.Context, actor Actor, id uuid.UUID) (context.Context, sqlc.Connection, func(), error) {
	row, err := s.load(ctx, sqlc.New(s.o.DB), id, false)
	if err != nil {
		return nil, sqlc.Connection{}, nil, err
	}
	release, err := s.acquire(actor)
	if err != nil {
		return nil, sqlc.Connection{}, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.o.OperationTimeout)
	return ctx, row, func() { cancel(); release() }, nil
}

func runContext() *runnerpb.RunContext { return &runnerpb.RunContext{RunId: uuid.NewString()} }

// pinCheck reads the D-133 "check" metadata of a pin-mismatch status.
func pinCheck(err error) string {
	st, ok := status.FromError(err)
	if !ok {
		return ""
	}
	for _, d := range st.Details() {
		if ei, ok := d.(*errdetails.ErrorInfo); ok && ei.GetDomain() == runnerpb.ErrorDomain {
			return ei.GetMetadata()["check"]
		}
	}
	return ""
}

// runnerError maps a runner error to the service's error types. Security
// events (pin and host-key mismatches) are audited and alerted first.
func (s *Service) runnerError(ctx context.Context, actor Actor, row *sqlc.Connection, op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return err
	}
	reason := runnerpb.ErrorReason(err)
	switch reason {
	case runnerpb.ReasonPinMismatch:
		check := pinCheck(err)
		if aerr := s.securityEvent(ctx, actor, row, audit.ActionConnectionPinMismatch, AlertTLSPinMismatch, op,
			map[string]any{"check": check, "host": row.Host, "port": row.Port}); aerr != nil {
			return aerr
		}
		return &PinMismatchError{Check: check}
	case runnerpb.ReasonSSHHostKey:
		if aerr := s.securityEvent(ctx, actor, row, audit.ActionConnectionHostKeyMismatch, AlertSSHHostKeyMismatch, op,
			map[string]any{"ssh_host": deref(row.SshHost), "ssh_port": deref(row.SshPort), "pinned_fingerprint": deref(row.SshHostKeyFingerprint)}); aerr != nil {
			return aerr
		}
		return &HostKeyMismatchError{}
	case "":
	default:
		if msg, ok := reasonMessages[reason]; ok {
			return &TargetError{Code: string(reason), Message: msg}
		}
	}
	code := status.Code(err)
	s.o.Logger.InfoContext(ctx, "runner call failed", slog.String("op", op), slog.String("code", code.String()))
	switch code {
	case codes.PermissionDenied:
		return &TargetError{Code: CodeTargetNotAllowed, Message: "the runner's target allowlist (runner.toml [targets]) does not allow this host"}
	case codes.InvalidArgument:
		return &TargetError{Code: CodeInvalidRequest, Message: "the runner rejected the connection settings"}
	case codes.ResourceExhausted:
		return ErrBusy
	case codes.DeadlineExceeded:
		return &TargetError{Code: string(runnerpb.ReasonTimeout), Message: reasonMessages[runnerpb.ReasonTimeout]}
	case codes.Unavailable, codes.Unimplemented:
		return ErrRunnerUnavailable
	case codes.FailedPrecondition, codes.NotFound, codes.AlreadyExists:
		return &TargetError{Code: CodeRunnerFailed, Message: "the runner could not perform the operation"}
	case codes.Canceled:
		return context.Canceled
	default:
		return fmt.Errorf("connections: runner %s: %w", op, err)
	}
}

// ReasonMessage returns the presentable, fixed text of a runner reason
// (false for reasons without one).
func ReasonMessage(r runnerpb.Reason) (string, bool) {
	m, ok := reasonMessages[r]
	return m, ok
}

// RecordRunFailure handles the security events of a runner call a worker
// made for connection id (backup runs, task 3.6): a TLS pin or SSH host-key
// mismatch is audited with the system actor (fail closed: the audit error
// is returned) and alerted, exactly like an interactive test. Other errors
// are ignored (nil); the caller classifies them itself.
func (s *Service) RecordRunFailure(ctx context.Context, id uuid.UUID, op string, err error) error {
	switch runnerpb.ErrorReason(err) {
	case runnerpb.ReasonPinMismatch, runnerpb.ReasonSSHHostKey:
	default:
		return nil
	}
	row, lerr := s.load(context.WithoutCancel(ctx), sqlc.New(s.o.DB), id, false)
	if lerr != nil {
		return lerr
	}
	mapped := s.runnerError(context.WithoutCancel(ctx), Actor{}, &row, op, err)
	var pm *PinMismatchError
	var hk *HostKeyMismatchError
	if errors.As(mapped, &pm) || errors.As(mapped, &hk) {
		return nil
	}
	return mapped
}

// securityEvent audits a mismatch (fail closed: an audit failure is
// returned) and raises the alert.
func (s *Service) securityEvent(ctx context.Context, actor Actor, row *sqlc.Connection, action audit.Action, kind AlertKind, op string, details map[string]any) error {
	d := audit.Details{"name": row.Name, "operation": op}
	for k, v := range details {
		d[k] = v
	}
	ev := audit.UserActor(actor.UserID)
	if actor.UserID == uuid.Nil {
		ev = audit.SystemActor() // a worker's run (RecordRunFailure)
	}
	if _, err := s.o.Auditor.Record(context.WithoutCancel(ctx), audit.Event{
		Actor: ev, Action: action, TargetType: "connection",
		TargetID: row.ID.String(), IP: actor.IP, Details: d,
	}); err != nil {
		return err
	}
	s.o.Alerts(ctx, Alert{Kind: kind, ConnectionID: row.ID, ConnectionName: row.Name, Details: details})
	return nil
}

// probe runs the runner's Probe with the stored credentials.
func (s *Service) probe(ctx context.Context, actor Actor, row *sqlc.Connection, op string) (*runnerpb.ProbeResponse, error) {
	t, release, err := s.target(row)
	if err != nil {
		return nil, err
	}
	defer release()
	defer registerTarget(t)()
	resp, err := s.o.Runner.Probe(ctx, &runnerpb.ProbeRequest{Ctx: runContext(), Target: t})
	if err != nil {
		return nil, s.runnerError(ctx, actor, row, op, err)
	}
	return resp, nil
}

// registerTarget registers the request's credentials with the log
// redaction registry for the duration of the call.
func registerTarget(t *runnerpb.Target) func() {
	c := &Credentials{Password: t.GetPassword(), ClientKey: t.GetClientCertificate().GetKeyPem()}
	if st := t.GetSshTunnel(); st != nil {
		c.SSHPassword, c.SSHPrivateKey, c.SSHPassphrase = st.GetPassword(), st.GetPrivateKeyPem(), st.GetPassphrase()
	}
	return registerSecrets(c)
}

// TestResult is the normalized outcome of Test.
type TestResult struct {
	OK bool
	// Code and Message describe a failure (runner reason, fixed text).
	Code    string
	Message string
	// ServerVersion is the version the server reported.
	ServerVersion string
	// DetectedVersionKey is the catalog key matching the server ("" when
	// none is allowlisted).
	DetectedVersionKey string
	// Compatible: the connection's image can back up this server.
	Compatible bool
	// Legacy: the detected (or configured) series is end-of-life.
	Legacy    bool
	Warnings  []string
	Databases int
}

// Test connects with the stored credentials through the runner (Probe).
// Target failures are a result (OK false), not an error; pin and host-key
// mismatches are errors (*PinMismatchError, *HostKeyMismatchError).
func (s *Service) Test(ctx context.Context, actor Actor, id uuid.UUID) (TestResult, error) {
	ctx, row, done, err := s.begin(ctx, actor, id)
	if err != nil {
		return TestResult{}, err
	}
	defer done()
	resp, err := s.probe(ctx, actor, &row, "test")
	var te *TargetError
	switch {
	case errors.As(err, &te):
		return TestResult{Code: te.Code, Message: te.Message}, nil
	case err != nil:
		return TestResult{}, err
	}
	res := TestResult{
		OK: true, ServerVersion: resp.GetServerVersion(), DetectedVersionKey: resp.GetDetectedVersionKey(),
		Compatible: resp.GetVersionKeyCompatible(), Warnings: resp.GetWarnings(), Databases: len(resp.GetDatabases()),
	}
	res.Legacy = s.legacy(engine.Kind(row.Engine), row.ImageKey, res.DetectedVersionKey)
	if !res.Compatible {
		res.OK = false
		res.Code = "INCOMPATIBLE_CLIENT"
		res.Message = "the connection's client image cannot back up this server version; choose the detected version"
	}
	return res, nil
}

func (s *Service) legacy(k engine.Kind, keys ...string) bool {
	for _, key := range keys {
		if e, ok := s.o.Catalog.Lookup(k, key); ok && e.Legacy() {
			return true
		}
	}
	return false
}

// VersionInfo is the result of DetectVersion.
type VersionInfo struct {
	ServerVersion string
	// SuggestedKey is the catalog key for both version_key and image_key
	// ("" when no allowlisted client fits the server).
	SuggestedKey string
	// Legacy: the suggested series is end-of-life.
	Legacy bool
	// CurrentCompatible: the connection's current image fits the server.
	CurrentCompatible bool
	Warnings          []string
}

// DetectVersion asks the server for its version (Probe) and suggests the
// catalog key.
func (s *Service) DetectVersion(ctx context.Context, actor Actor, id uuid.UUID) (VersionInfo, error) {
	ctx, row, done, err := s.begin(ctx, actor, id)
	if err != nil {
		return VersionInfo{}, err
	}
	defer done()
	resp, err := s.probe(ctx, actor, &row, "detect_version")
	if err != nil {
		return VersionInfo{}, err
	}
	key := resp.GetDetectedVersionKey()
	return VersionInfo{
		ServerVersion: resp.GetServerVersion(), SuggestedKey: key, Legacy: key != "" && s.legacy(engine.Kind(row.Engine), key),
		CurrentCompatible: resp.GetVersionKeyCompatible(), Warnings: resp.GetWarnings(),
	}, nil
}

// ListDatabases returns the server's databases (system databases removed
// by the runner) and warning codes.
func (s *Service) ListDatabases(ctx context.Context, actor Actor, id uuid.UUID) ([]string, []string, error) {
	ctx, row, done, err := s.begin(ctx, actor, id)
	if err != nil {
		return nil, nil, err
	}
	defer done()
	resp, err := s.probe(ctx, actor, &row, "list_databases")
	if err != nil {
		return nil, nil, err
	}
	return resp.GetDatabases(), resp.GetWarnings(), nil
}

// PrePullResult is the result of PrePull.
type PrePullResult struct {
	ImageRef       string
	AlreadyPresent bool
}

// PrePull pulls the connection's client image on the runner (by digest).
func (s *Service) PrePull(ctx context.Context, actor Actor, id uuid.UUID) (PrePullResult, error) {
	row, err := s.load(ctx, sqlc.New(s.o.DB), id, false)
	if err != nil {
		return PrePullResult{}, err
	}
	release, err := s.acquire(actor)
	if err != nil {
		return PrePullResult{}, err
	}
	defer release()
	eng, err := engineOf(engine.Kind(row.Engine))
	if err != nil {
		return PrePullResult{}, err
	}
	// Pulls can take minutes; the runner bounds them with docker.pull_timeout.
	ctx, cancel := context.WithTimeout(ctx, max(s.o.OperationTimeout, prePullTimeout))
	defer cancel()
	resp, err := s.o.Runner.PrePull(ctx, &runnerpb.PrePullRequest{Engine: eng, VersionKey: row.ImageKey})
	if err != nil {
		return PrePullResult{}, s.runnerError(ctx, actor, &row, "pre_pull", err)
	}
	return PrePullResult{ImageRef: resp.GetImageRef(), AlreadyPresent: resp.GetAlreadyPresent()}, nil
}
