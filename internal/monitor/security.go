package monitor

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/queue"
)

// Periodic security checks (task 5.5, D-190): conditions that no single
// action reports become security.system alerts (mandatory: every admin's
// inbox and e-mail).
//
//   - Certificate expiry of the internal certificates (runner mTLS client
//     and CA, app-database CA and client certificate, the runner's server
//     certificate from its Health RPC): one alert per certificate when 30
//     and when 7 days or less remain, and one when it expired.
//   - Expiry of the pinned target-database certificates, the same way.
//   - Audit chain verification: incremental from the last verified row
//     (anchored on the remembered hash, so a rewrite of the verified part is
//     caught), the whole chain at the first run of a process and then every
//     SecurityFullVerifyEvery; a failure is recorded as audit.verify.failed
//     (the audit observer raises the critical alert, once per finding).
//     Every completed verification is stored as the latest result
//     (audit.SaveVerification, Query.auditChainStatus; D-196).
//   - Weakened posture (plain HTTP outside dev mode, weakened app-database
//     TLS): a daily reminder, sharing the dedup key with the startup alert.
const (
	// SecurityCheckInterval is how often the checks run.
	SecurityCheckInterval = time.Hour
	// SecurityFullVerifyEvery is how often the whole audit chain is
	// re-verified (in between, only new rows are).
	SecurityFullVerifyEvery = 24 * time.Hour
)

// ExpiryThresholds are the days-left thresholds of the expiry alerts
// (descending). The first is certs.ExpiryWarnDays, which `bekup setup check`
// and `bekup setup renew` share.
var ExpiryThresholds = []int{certs.ExpiryWarnDays, 7}

// SecurityOptions configure NewSecurityChecker.
type SecurityOptions struct {
	// DB is the app pool (bekup_app); required.
	DB sqlc.DBTX
	// Auditor records audit.verify.failed (its observer must be
	// notify.ObserveAudit); required.
	Auditor *audit.Auditor
	// Certs are the certificate files to watch (as for Health).
	Certs []CertFile
	// Runner reports the runner's server certificate (nil: not checked).
	Runner RunnerHealth
	// Posture returns the posture kinds that are weakened now
	// (notify.KindInsecureTransport, notify.KindInsecureDBTLS,
	// notify.KindInsecureDBRoles).
	Posture func() []string
	Now     func() time.Time
	Logger  *slog.Logger
}

// SecurityChecker runs the periodic security checks. It is safe for
// concurrent use (runs are serialized).
type SecurityChecker struct {
	o  SecurityOptions
	mu sync.Mutex
	// The last verified audit row and its hash (0: verify everything).
	verifiedID   int64
	verifiedHash []byte
	lastFull     time.Time
	lastFinding  string
}

// NewSecurityChecker returns the checker.
func NewSecurityChecker(o SecurityOptions) (*SecurityChecker, error) {
	if o.DB == nil || o.Auditor == nil {
		return nil, errors.New("monitor: the security checker needs the database and the auditor")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &SecurityChecker{o: o}, nil
}

// Run runs every check once. A failing check does not stop the others;
// the joined error reports operational failures (never findings).
func (c *SecurityChecker) Run(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.o.Now().UTC()
	q := sqlc.New(c.o.DB)
	var errs []error
	if err := c.checkCerts(ctx, q, now); err != nil {
		errs = append(errs, err)
	}
	if err := c.checkPins(ctx, q, now); err != nil {
		errs = append(errs, err)
	}
	if err := c.checkPosture(ctx, q, now); err != nil {
		errs = append(errs, err)
	}
	if err := c.verifyAudit(ctx, now); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// expiryKind returns the alert of a certificate expiring at notAfter: the
// kind, the threshold crossed (0 for expired) and whether one is due.
func expiryKind(notAfter, now time.Time, expiring, expired string) (string, int, bool) {
	left := notAfter.Sub(now)
	if left <= 0 {
		return expired, 0, true
	}
	due := 0
	for _, t := range ExpiryThresholds {
		if left <= time.Duration(t)*24*time.Hour {
			due = t
		}
	}
	if due == 0 {
		return "", 0, false
	}
	return expiring, due, true
}

// expiryEvent builds an expiry alert, deduplicated per object, expiry and
// threshold: it is emitted once at 30 days, once at 7 and once expired.
func expiryEvent(kind string, threshold int, object string, notAfter time.Time) notify.Event {
	d := notify.Detail{"kind": kind, "not_after": notAfter.UTC().Format("2006-01-02")}
	if threshold > 0 {
		d["threshold_days"] = threshold
	}
	sev := notify.SystemSeverity(kind)
	if threshold > 0 && threshold <= 7 {
		sev = notify.SeverityError
	}
	return notify.Event{
		Type: notify.SecuritySystem, Severity: sev, Detail: d,
		DedupKey: "security.system:" + kind + ":" + object + ":" + strconv.FormatInt(notAfter.Unix(), 10) + ":" + strconv.Itoa(threshold),
	}
}

func (c *SecurityChecker) checkCerts(ctx context.Context, q *sqlc.Queries, now time.Time) error {
	type cert struct {
		component string
		notAfter  time.Time
	}
	var list []cert
	for _, cf := range c.o.Certs {
		if cf.Path == "" {
			continue
		}
		infos, err := certs.InspectFile(cf.Path)
		if err != nil || len(infos) == 0 {
			c.o.Logger.WarnContext(ctx, "security check: reading a certificate file failed", slog.String("component", cf.Component))
			continue
		}
		earliest := infos[0].NotAfter
		for i := range infos {
			if infos[i].NotAfter.Before(earliest) {
				earliest = infos[i].NotAfter
			}
		}
		list = append(list, cert{cf.Component, earliest})
	}
	if c.o.Runner != nil {
		rctx, cancel := context.WithTimeout(ctx, runnerTimeout)
		resp, err := c.o.Runner.Health(rctx)
		cancel()
		if err == nil {
			if ts := resp.GetCertificateNotAfter(); ts != nil && ts.IsValid() {
				list = append(list, cert{CertRunnerServer, ts.AsTime()})
			}
		} else {
			// The error text may carry addresses; the event is enough.
			c.o.Logger.WarnContext(ctx, "security check: runner health check failed")
		}
	}
	for _, ce := range list {
		kind, t, ok := expiryKind(ce.notAfter, now, notify.KindCertExpiring, notify.KindCertExpired)
		if !ok {
			continue
		}
		ev := expiryEvent(kind, t, ce.component, ce.notAfter)
		ev.Detail["component"] = ce.component
		if _, err := notify.Emit(ctx, q, ev); err != nil {
			return fmt.Errorf("monitor: certificate expiry alert: %w", err)
		}
	}
	return nil
}

func (c *SecurityChecker) checkPins(ctx context.Context, q *sqlc.Queries, now time.Time) error {
	rows, err := q.ListExpiringConnectionPins(ctx, now.Add(time.Duration(ExpiryThresholds[0])*24*time.Hour))
	if err != nil {
		return fmt.Errorf("monitor: list expiring pins: %w", err)
	}
	for _, r := range rows {
		kind, t, ok := expiryKind(r.NotAfter, now, notify.KindPinExpiring, notify.KindPinExpired)
		if !ok {
			continue
		}
		ev := expiryEvent(kind, t, r.ID.String(), r.NotAfter)
		ev.Subject.ConnectionID = r.ID
		if _, err := notify.Emit(ctx, q, ev); err != nil {
			return fmt.Errorf("monitor: pin expiry alert: %w", err)
		}
	}
	return nil
}

func (c *SecurityChecker) checkPosture(ctx context.Context, q *sqlc.Queries, now time.Time) error {
	if c.o.Posture == nil {
		return nil
	}
	for _, kind := range c.o.Posture() {
		if _, err := notify.Emit(ctx, q, notify.Event{
			Type: notify.SecuritySystem, Severity: notify.SystemSeverity(kind),
			Detail:   notify.Detail{"kind": kind, "reminder": true},
			DedupKey: notify.PostureDedupKey(kind, now),
		}); err != nil {
			return fmt.Errorf("monitor: posture alert: %w", err)
		}
	}
	return nil
}

// verifyAudit verifies the audit chain (incrementally when possible) and
// records a failure once per finding.
func (c *SecurityChecker) verifyAudit(ctx context.Context, now time.Time) error {
	full := c.verifiedID == 0 || now.Sub(c.lastFull) >= SecurityFullVerifyEvery
	opts := audit.VerifyOptions{}
	if !full {
		opts.FromID = c.verifiedID + 1
		anchor, err := sqlc.New(c.o.DB).GetAuditLogChainAnchor(ctx, opts.FromID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return c.finding(ctx, "anchor_missing", c.verifiedID, full)
		case err != nil:
			return fmt.Errorf("monitor: read audit anchor: %w", err)
		case anchor.ID != c.verifiedID || !bytes.Equal(anchor.Hash, c.verifiedHash):
			// The last verified row was deleted or rewritten.
			return c.finding(ctx, "anchor_rewritten", c.verifiedID, full)
		}
	}
	rep, err := audit.Verify(ctx, c.o.DB, opts)
	if err != nil {
		return fmt.Errorf("monitor: verify audit chain: %w", err)
	}
	if !rep.OK {
		if rep.Broken != nil {
			return c.finding(ctx, string(rep.Broken.Kind), rep.Broken.ID, full)
		}
		for _, h := range rep.Heads {
			if !h.OK {
				return c.finding(ctx, "signed_head_"+h.Source, h.ChainID, full)
			}
		}
		return c.finding(ctx, "unknown", 0, full)
	}
	if rep.Rows > 0 {
		h, err := hex.DecodeString(rep.LastHash)
		if err != nil {
			return fmt.Errorf("monitor: audit chain hash: %w", err)
		}
		c.verifiedID, c.verifiedHash = rep.LastID, h
	}
	if full {
		c.lastFull = now
	}
	c.lastFinding = ""
	return c.saveVerification(ctx, audit.Verification{CheckedAt: now, OK: true, Full: full, VerifiedThroughID: c.verifiedID})
}

// saveVerification stores the outcome for Query.auditChainStatus (D-196).
func (c *SecurityChecker) saveVerification(ctx context.Context, v audit.Verification) error {
	if err := audit.SaveVerification(ctx, sqlc.New(c.o.DB), v); err != nil {
		return fmt.Errorf("monitor: %w", err)
	}
	return nil
}

// finding records audit.verify.failed (system actor) unless the same
// finding was already recorded by this process; the audit observer raises
// the critical security.system alert, deduplicated per finding across
// processes. The verification checkpoint is not advanced.
func (c *SecurityChecker) finding(ctx context.Context, kind string, id int64, full bool) error {
	f := kind + ":" + strconv.FormatInt(id, 10)
	c.o.Logger.ErrorContext(ctx, "audit chain verification failed", slog.String("finding", f))
	saveErr := c.saveVerification(ctx, audit.Verification{
		CheckedAt: c.o.Now().UTC(), Full: full, Finding: f, VerifiedThroughID: c.verifiedID,
	})
	if f == c.lastFinding {
		return saveErr
	}
	if _, err := c.o.Auditor.Record(ctx, audit.Event{
		Actor: audit.SystemActor(), Action: audit.ActionAuditVerifyFailed,
		TargetType: "audit_log", TargetID: strconv.FormatInt(id, 10),
		Details: audit.Details{"finding": f, "problem": kind, "row_id": id, "full": full},
	}); err != nil {
		return errors.Join(fmt.Errorf("monitor: record audit verification failure: %w", err), saveErr)
	}
	c.lastFinding = f
	return saveErr
}

// SecurityWorker runs the checks (River kind security_check, maintenance
// queue, every SecurityCheckInterval).
type SecurityWorker struct {
	river.WorkerDefaults[queue.SecurityCheckArgs]
	Checker *SecurityChecker
}

// Work implements river.Worker.
func (w *SecurityWorker) Work(ctx context.Context, _ *river.Job[queue.SecurityCheckArgs]) error {
	return w.Checker.Run(ctx)
}

// RegisterSecurityChecks registers the worker and its periodic job.
func RegisterSecurityChecks(ws *queue.Workers, c *SecurityChecker) error {
	if c == nil {
		return errors.New("monitor: the security worker needs the checker")
	}
	if err := queue.Register(ws, &SecurityWorker{Checker: c}); err != nil {
		return err
	}
	return ws.AddPeriodic(SecurityCheckInterval, queue.SecurityCheckArgs{})
}
