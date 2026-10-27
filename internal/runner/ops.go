package runner

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/dbprobe"
	"github.com/jlentink/bekup/internal/runner/executor"
	"github.com/jlentink/bekup/internal/runner/pinning"
	"github.com/jlentink/bekup/internal/runner/preamble"
	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/runner/validate"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/tunnel"
)

// Per-RPC time caps (D-134). The interceptor already clamps every deadline
// to limits.max_run_duration; these bound the short operations further.
const (
	probeTimeout  = time.Minute
	createTimeout = 2 * time.Minute
	swapTimeout   = 5 * time.Minute
	// swapAttempts is how often the PostgreSQL swap transaction is tried
	// (terminated backends may need a moment to exit).
	swapAttempts = 3
	swapRetryGap = 2 * time.Second
	// maxGlobalsBuffer bounds the buffered output of the first DumpGlobals
	// attempt (the --no-role-passwords fallback must not follow streamed
	// data). Real globals are a few KiB; beyond the cap the output is
	// streamed and no fallback is possible.
	maxGlobalsBuffer = 4 << 20
	// maxResultLogs bounds the log lines returned in a VerifyDump response.
	maxResultLogs = 200
)

// Warning codes the runner adds to results (stable strings).
const (
	warnNonInnoDB        = "non_innodb_tables"
	warnSkippedNames     = "databases_with_unsupported_names_skipped"
	warnServerTooOld     = "server_not_supported"
	warnNoClient         = "no_allowlisted_client_for_server"
	warnRolesNoPasswords = "roles_without_passwords"
)

// runExecutor is the part of *executor.Executor the RPCs use (a fake in
// unit tests).
type runExecutor interface {
	Run(ctx context.Context, rs executor.RunSpec) (executor.Result, error)
	PrePull(ctx context.Context, entry engine.Entry) (bool, error)
}

// operations implements the runner RPCs of task 2.7. Every request goes
// validate -> target allowlist (validator) -> pin pre-flight + Go-driver
// checks -> executor; credentials are copied only into buffers that are
// zeroed after use, and the request itself is zeroed by the interceptor.
type operations struct {
	val  *validate.Validator
	exec runExecutor
	pins pinning.Verifier
	log  *slog.Logger
	now  func() time.Time
	// connect opens the upstream (pinning.Connect; replaced by tests).
	connect func(ctx context.Context, u pinning.Upstream) (pinning.DialFunc, func(), error)
	// open opens a Go-driver connection (dbprobe.Open; replaced by tests).
	open func(ctx context.Context, p dbprobe.Params) (dbprobe.Conn, error)
	// clientTLS builds the pinned TLS config of the Go drivers.
	clientTLS func(host string, pins []validate.Pin) (*tls.Config, error)
	// swapGap is the delay between swap attempts (tests shorten it).
	swapGap time.Duration
	// hostKey captures an SSH host key (tunnel.FetchHostKey; replaced by
	// tests).
	hostKey func(ctx context.Context, host string, port int, d tunnel.Dialer) (tunnel.HostKeyInfo, error)
	// fetch captures a target's certificate chain (pinning.Checker.Fetch;
	// fails closed without a checker).
	fetch func(ctx context.Context, t pinning.Target) ([]*x509.Certificate, error)
	// warned remembers bastions already reported as unrestricted
	// (bounded by maxWarnedBastions).
	warnedMu sync.Mutex
	warned   map[string]bool
}

// chainFetcher is implemented by *pinning.Checker.
type chainFetcher interface {
	Fetch(ctx context.Context, t pinning.Target) ([]*x509.Certificate, error)
}

func newOperations(val *validate.Validator, exec runExecutor, pins pinning.Verifier, log *slog.Logger) *operations {
	if pins == nil {
		pins = pinning.FailClosed()
	}
	fetch := func(context.Context, pinning.Target) ([]*x509.Certificate, error) { return nil, pinning.ErrNoVerifier }
	if f, ok := pins.(chainFetcher); ok {
		fetch = f.Fetch
	}
	return &operations{
		fetch:  fetch,
		warned: map[string]bool{},
		val:    val, exec: exec, pins: pins, log: log, now: time.Now,
		connect: pinning.Connect,
		open:    dbprobe.Open,
		clientTLS: func(host string, pins []validate.Pin) (*tls.Config, error) {
			return pinning.ClientTLSConfig(host, pins, nil)
		},
		swapGap: swapRetryGap,
		hostKey: tunnel.FetchHostKey,
	}
}

// remaining is the time left until ctx's deadline (0 = none).
func remaining(ctx context.Context) time.Duration {
	if dl, ok := ctx.Deadline(); ok {
		return max(time.Until(dl), time.Millisecond)
	}
	return 0
}

// --- target sessions --------------------------------------------------------

// checkOpts selects the Go-driver checks before an operation.
type checkOpts struct {
	// needDB must be in the server-reported list (D-033).
	needDB []string
	// absentDB must not exist yet (CreateDatabase).
	absentDB string
	// nonInnoDB counts non-InnoDB tables of this database (MySQL/MariaDB
	// dumps warn about them).
	nonInnoDB string
}

// checked is what the pre-checks found out.
type checked struct {
	raw       string
	version   engine.Version
	databases []string
	skipped   int
	warnings  []string
}

// tunnelCreds copies the request's SSH credentials into fresh secrets
// (tunnel.Dial destroys them).
func tunnelCreds(st *runnerpb.SshTunnel) tunnel.Auth {
	var a tunnel.Auth
	if st == nil {
		return a
	}
	if b := st.GetPrivateKeyPem(); len(b) > 0 {
		a.PrivateKey = crypto.NewSecret(bytes.Clone(b))
		if p := st.GetPassphrase(); len(p) > 0 {
			a.Passphrase = crypto.NewSecret(bytes.Clone(p))
		}
		return a
	}
	a.Password = crypto.NewSecret(bytes.Clone(st.GetPassword()))
	return a
}

// maxWarnedBastions bounds the remembered unrestricted bastions.
const maxWarnedBastions = 256

// warnUnrestrictedBastion logs once per bastion that it has no
// targets.bastion_targets list, so it may forward to any host it reaches
// (D-138).
func (o *operations) warnUnrestrictedBastion(ctx context.Context, s *validate.SSH) {
	if s == nil || o.val.Guard().HasBastionTargets(s.Bastion.Host, s.Bastion.Addrs) {
		return
	}
	o.warnedMu.Lock()
	seen := o.warned[s.Bastion.Host]
	if !seen && len(o.warned) < maxWarnedBastions {
		o.warned[s.Bastion.Host] = true
	}
	o.warnedMu.Unlock()
	if !seen {
		o.log.LogAttrs(ctx, slog.LevelWarn, "runner_bastion_unrestricted",
			slog.String("bastion", s.Bastion.Host),
			slog.String("hint", "add targets.bastion_targets entries for this bastion"))
	}
}

// runTunnel is the executor's tunnel configuration for t (nil without SSH).
func (o *operations) runTunnel(t *validate.Target, st *runnerpb.SshTunnel) *tunnel.Config {
	if t.SSH == nil {
		return nil
	}
	return &tunnel.Config{
		Host: t.SSH.Bastion.Host, Port: t.SSH.Bastion.Port, User: t.SSH.User,
		Auth: tunnelCreds(st), HostKeyPin: t.SSH.Pin,
		TargetHost: t.Conn.Host, TargetPort: t.Conn.Port,
		Logger: o.log,
	}
}

// driverTLS is the Go-driver TLS configuration of the target's TLS mode.
// A client certificate is parsed here (crypto/tls needs the key); the
// parsed key cannot be zeroed and is left to the garbage collector
// (documented limitation, D-134).
func (o *operations) driverTLS(t *validate.Target, cc *runnerpb.ClientCertificate) (*tls.Config, error) {
	var cfg *tls.Config
	switch t.Conn.TLS {
	case engine.TLSPinned:
		c, err := o.clientTLS(t.Conn.Host, t.Pins)
		if err != nil {
			return nil, err
		}
		cfg = c
	case engine.TLSUnverified:
		cfg = dbprobe.UnverifiedTLS(t.Conn.Host)
	default:
		return nil, nil
	}
	if t.Conn.ClientCert && cc != nil {
		pair, err := tls.X509KeyPair(cc.GetCertPem(), cc.GetKeyPem())
		if err != nil {
			return nil, runnerpb.ReasonTLSFailed.Status()
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return cfg, nil
}

// session runs the pin pre-flight (TLS mode PINNED) and opens a Go-driver
// connection to db (PostgreSQL; "" = the maintenance database) through a
// fresh upstream (target allowlist dialer or SSH tunnel). The returned
// close function must always be called.
func (o *operations) session(ctx context.Context, t *validate.Target, rt *runnerpb.Target, db string) (dbprobe.Conn, []string, func(), error) {
	o.warnUnrestrictedBastion(ctx, t.SSH)
	dial, done, err := o.connect(ctx, pinning.Upstream{
		Host: t.Conn.Host, Port: t.Conn.Port, Guard: o.val.Guard(), SSH: t.SSH,
		Tunnel: rt.GetSshTunnel(), Logger: o.log,
	})
	if err != nil {
		done()
		return nil, nil, func() {}, err
	}
	var warns []string
	if t.Conn.TLS == engine.TLSPinned {
		rep, err := o.pins.Preflight(ctx, pinning.Target{Kind: t.Kind, Host: t.Conn.Host, Dial: dial}, t.Pins)
		if err != nil {
			done()
			return nil, nil, func() {}, err
		}
		warns = append(warns, rep.Warnings...)
	}
	tlsCfg, err := o.driverTLS(t, rt.GetClientCertificate())
	if err != nil {
		done()
		return nil, nil, func() {}, err
	}
	conn, err := o.open(ctx, dbprobe.Params{
		Kind: t.Kind, Host: t.Conn.Host, Port: t.Conn.Port, User: t.Conn.User,
		Password: rt.GetPassword(), Database: db, TLS: tlsCfg,
		Dial: dbprobe.DialFunc(dial),
	})
	if err != nil {
		done()
		return nil, nil, func() {}, err
	}
	return conn, warns, func() {
		discardErr(conn.Close())
		done()
	}, nil
}

// precheck connects with the Go driver and checks the server version and
// the databases before a container operation (D-033): the database names
// must be in the server-reported list, and the allowlisted client must be
// able to handle the server (PostgreSQL: client major >= server major).
func (o *operations) precheck(ctx context.Context, t *validate.Target, rt *runnerpb.Target, co checkOpts) (*checked, error) {
	conn, warns, done, err := o.session(ctx, t, rt, "")
	defer done()
	if err != nil {
		return nil, err
	}
	c := &checked{warnings: warns}
	if c.raw, c.version, err = conn.ServerVersion(ctx); err != nil {
		return nil, err
	}
	if c.databases, c.skipped, err = conn.Databases(ctx); err != nil {
		return nil, err
	}
	for _, db := range co.needDB {
		if !slices.Contains(c.databases, db) {
			return nil, runnerpb.ReasonDatabaseNotFound.Status()
		}
	}
	if co.absentDB != "" && slices.Contains(c.databases, co.absentDB) {
		return nil, runnerpb.ReasonDatabaseExists.Status()
	}
	if co.nonInnoDB != "" && t.Kind != engine.KindPostgres {
		n, err := conn.NonInnoDBTables(ctx, co.nonInnoDB)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			c.warnings = append(c.warnings, warnNonInnoDB)
		}
	}
	return c, nil
}

// compatWarnings checks the request's client entry against the server.
func compatWarnings(e engine.Entry, server engine.Version) ([]string, error) {
	ws, err := engine.CheckDumpCompat(e, server)
	if err != nil {
		return nil, runnerpb.ReasonUnsupportedServer.Status()
	}
	return warningCodes(ws), nil
}

func warningCodes(ws []engine.Warning) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, string(w.Code))
	}
	return out
}

// --- container material -----------------------------------------------------

// material builds the preamble input for a command connecting to ep. It
// copies every credential; the executor zeroes the result.
func (o *operations) material(t *validate.Target, rt *runnerpb.Target, ep executor.ClientEndpoint) (*preamble.Material, error) {
	m := &preamble.Material{}
	ok := false
	defer func() {
		if !ok {
			for _, b := range [][]byte{m.PGPass, m.MyCnf, m.CA, m.ClientCert, m.ClientKey} {
				crypto.Zero(b)
			}
		}
	}()
	var err error
	if t.Kind == engine.KindPostgres {
		if m.PGPass, err = engine.PGPassLine(ep.Host, ep.Port, t.Conn.User, rt.GetPassword()); err != nil {
			return nil, err
		}
	} else if m.MyCnf, err = engine.MySQLOptionFile(rt.GetPassword()); err != nil {
		return nil, err
	}
	if t.Conn.TLS == engine.TLSPinned {
		if m.CA, err = o.pins.TrustAnchor(t.Pins); err != nil {
			return nil, err
		}
	}
	if t.Conn.ClientCert {
		cc := rt.GetClientCertificate()
		m.ClientCert = bytes.Clone(cc.GetCertPem())
		m.ClientKey = bytes.Clone(cc.GetKeyPem())
	}
	ok = true
	return m, nil
}

// connTarget is the engine target for the endpoint the executor chose.
func connTarget(t *validate.Target, ep executor.ClientEndpoint, server engine.Version) engine.Target {
	c := t.Conn
	c.Host, c.Port, c.ServerVersion = ep.Host, ep.Port, server
	return c
}

// runSpec is the common executor spec of a container that connects to t.
// build returns the command for the endpoint; the material is created for
// the same endpoint.
func (o *operations) runSpec(runID string, entry engine.Entry, t *validate.Target, rt *runnerpb.Target,
	build func(engine.Target) (engine.Command, error), server engine.Version,
) executor.RunSpec {
	mat := &preamble.Material{}
	rs := executor.RunSpec{
		RunID:  runID,
		Entry:  entry,
		Target: executor.Target{Host: t.Conn.Host, Port: t.Conn.Port, VerifyHost: t.Conn.TLS == engine.TLSPinned},
		Tunnel: o.runTunnel(t, rt.GetSshTunnel()),
		Command: func(ep executor.ClientEndpoint) (engine.Command, error) {
			m, err := o.material(t, rt, ep)
			if err != nil {
				return engine.Command{}, err
			}
			*mat = *m
			return build(connTarget(t, ep, server))
		},
		Material: mat,
		Redact:   redactions(rt),
	}
	return rs
}

// redactions are the request's secret values removed from tool output.
func redactions(rt *runnerpb.Target) [][]byte {
	var out [][]byte
	if pw := rt.GetPassword(); len(pw) >= 4 {
		out = append(out, pw)
	}
	if st := rt.GetSshTunnel(); st != nil && len(st.GetPassword()) >= 4 {
		out = append(out, st.GetPassword())
	}
	return out
}

// --- errors -----------------------------------------------------------------

// targetStatus maps an error of a session, pre-flight, probe or run to a
// normalized gRPC status (static message, ErrorInfo reason). Connection
// failures before the database answered are all ReasonUnreachable.
func targetStatus(err error) error {
	if err == nil {
		return nil
	}
	if me, ok := dbprobe.MismatchCause(err); ok {
		return runnerpb.PinMismatchStatus(me.Reason)
	}
	var me *pinning.MismatchError
	if errors.As(err, &me) {
		return runnerpb.PinMismatchStatus(me.Reason)
	}
	if r := dbprobe.ReasonOf(err); r != "" {
		if r == runnerpb.ReasonFailed && errors.Is(err, context.Canceled) {
			return status.Error(codes.Canceled, "run canceled")
		}
		return r.Status()
	}
	if s, ok := status.FromError(err); ok && s.Code() != codes.Unknown {
		return err
	}
	var te *tunnel.Error
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "run canceled")
	case errors.Is(err, tunnel.ErrHostKeyMismatch):
		return runnerpb.ReasonSSHHostKey.Status()
	case errors.Is(err, tunnel.ErrUnsupportedHostKey):
		return runnerpb.ReasonSSHHostKeyUnsupported.Status()
	case errors.Is(err, tunnel.ErrAuthFailed):
		return runnerpb.ReasonSSHAuthFailed.Status()
	case errors.As(err, &te):
		return runnerpb.ReasonUnreachable.Status()
	case errors.Is(err, targets.ErrDenied):
		return status.Error(codes.PermissionDenied, targets.ErrDenied.Error())
	case errors.Is(err, pinning.ErrNoTLS):
		return runnerpb.ReasonTLSFailed.Status()
	case errors.Is(err, pinning.ErrNoVerifier):
		return status.Error(codes.FailedPrecondition, "certificate pinning is not available on this runner")
	case errors.Is(err, pinning.ErrUnreachable), errors.Is(err, pinning.ErrProtocol),
		errors.Is(err, pinning.ErrServerRejected), errors.Is(err, pinning.ErrHandshake),
		errors.Is(err, targets.ErrResolve), errors.Is(err, context.DeadlineExceeded):
		// Refused, filtered, timed out and non-database endpoints look the
		// same (no port scanning through the runner).
		return runnerpb.ReasonUnreachable.Status()
	default:
		return executor.Status(err)
	}
}

// --- audit ------------------------------------------------------------------

// auditEntry is one runner-log audit line per RPC (D-134): identifiers and
// outcome only, never payloads, secrets or server messages.
type auditEntry struct {
	rpc      string
	runID    string
	engine   engine.Kind
	version  string
	database string
	tls      string
	ssh      bool
	start    time.Time
	exitCode *int
	bytes    *uint64
	extra    []slog.Attr
}

func newAudit(rpc, runID string) *auditEntry {
	return &auditEntry{rpc: rpc, runID: runID, start: time.Now()}
}

func (a *auditEntry) target(t *validate.Target) {
	if t == nil {
		return
	}
	a.engine, a.version, a.tls, a.ssh = t.Kind, t.Entry.Key(), t.Conn.TLS.String(), t.SSH != nil
}

func (o *operations) audit(ctx context.Context, a *auditEntry, err error) {
	attrs := []slog.Attr{
		slog.String("rpc", a.rpc),
		slog.String("run_id", a.runID),
		slog.String("caller", CallerFromContext(ctx)),
		slog.String("code", status.Code(err).String()),
		slog.Duration("duration", time.Since(a.start)),
	}
	if a.engine != "" {
		attrs = append(attrs, slog.String("engine", string(a.engine)), slog.String("version_key", a.version))
	}
	if a.tls != "" {
		attrs = append(attrs, slog.String("tls_mode", a.tls), slog.Bool("ssh_tunnel", a.ssh))
	}
	if a.database != "" {
		attrs = append(attrs, slog.String("database", a.database))
	}
	if r := runnerpb.ErrorReason(err); r != "" {
		attrs = append(attrs, slog.String("reason", string(r)))
	}
	if a.exitCode != nil {
		attrs = append(attrs, slog.Int("exit_code", *a.exitCode))
	}
	if a.bytes != nil {
		attrs = append(attrs, slog.Uint64("bytes", *a.bytes))
	}
	attrs = append(attrs, a.extra...)
	o.log.LogAttrs(ctx, slog.LevelInfo, "runner_rpc_audit", attrs...)
}

// logCause logs the internal classification of a failure (never the
// driver's message, which may echo server text).
func (o *operations) logCause(ctx context.Context, rpc string, err error) {
	if err == nil {
		return
	}
	var pe *dbprobe.Error
	if errors.As(err, &pe) {
		o.log.LogAttrs(ctx, slog.LevelInfo, "runner_target_error",
			slog.String("rpc", rpc), slog.String("reason", string(pe.Reason)), slog.String("detail", pe.Detail))
	}
}
