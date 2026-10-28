package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/scheduler"
)

// Test-restore settings (task 4.6). A job may designate a scratch target
// connection (permanent, the job's engine) into which internal/restore
// regularly restores the job's newest verified backup as a new database
// bekup_test_<id>, compares it with the backup's source statistics and
// always drops it again. Changing the settings is job.test_restore_edit:
// staff with a fresh second factor, because the scratch target receives a
// copy of the job's data like a restore does.
//
// Privileged scratch targets (D-068, D-170): designating a target probes
// its role through the runner. A privileged or unknown verdict (SQL in the
// tested dumps runs with that role's rights) needs an admin and the
// explicit AcknowledgePrivilegedTarget (maintainers: ErrPrivilegedTarget-
// AdminOnly; admins without it: VALIDATION); the acknowledgement is
// audited, alerted and stored with the verdict, and the test-restore
// worker re-probes the target before every run.

// ErrPrivilegedTargetAdminOnly reports that the scratch target's role is privileged or
// its privileges are unknown and the caller is not an admin (FORBIDDEN).
var ErrPrivilegedTargetAdminOnly = errors.New("jobs: only an admin can designate a scratch target whose role is privileged or unknown")

// AlertTestRestorePrivilegedTarget is raised when an admin designated a privileged (or
// unknown) scratch target with the acknowledgement.
const AlertTestRestorePrivilegedTarget AlertKind = "test_restore_privileged_target"

// ReasonProbeUnavailable is the reason when no prober is configured: the
// verdict is unknown (fail closed).
const ReasonProbeUnavailable = "probe_unavailable"

// ReasonTargetNotReady is the reason when the scratch connection is not
// ready, so its role could not be checked (unknown).
const ReasonTargetNotReady = "target_not_ready"

// TestTargetProber reads a scratch target's privilege verdict with one
// runner Probe (*connections.Service).
type TestTargetProber interface {
	ProbeTarget(ctx context.Context, actor connections.Actor, id uuid.UUID) (connections.TargetProbe, error)
}

// DefaultTestRestoreSchedule is the schedule of a job that never chose one:
// weekly, Sunday 04:00 UTC.
func DefaultTestRestoreSchedule() scheduler.Schedule {
	return scheduler.Schedule{Kind: scheduler.KindCron, Cron: "0 4 * * 0", Timezone: "UTC"}
}

// TestRestoreSettings are a job's test-restore settings as shown by the
// API (owners see them too: the scratch target's name and engine are not
// secret).
type TestRestoreSettings struct {
	Enabled             bool
	Schedule            scheduler.Schedule
	ScheduleDescription string
	// Target is nil when no scratch target is designated.
	Target *ConnectionInfo
	// NextAt is the next scheduled test restore (nil: none).
	NextAt *time.Time
	// TargetPrivileges is the verdict on the target's role when it was
	// designated (nil: no target, or designated before D-170: unknown).
	TargetPrivileges *connections.Privileges
	// PrivilegedAckAt / PrivilegedAckBy: an admin acknowledged the
	// privileged (or unknown) target role (nil: no acknowledgement).
	PrivilegedAckAt *time.Time
	PrivilegedAckBy *uuid.UUID
}

// TestRestoreInput replaces a job's test-restore settings. Schedule nil
// keeps the current one; TargetConnectionID nil removes the target (only
// allowed while disabled).
type TestRestoreInput struct {
	Enabled            bool
	Schedule           *scheduler.Schedule
	TargetConnectionID *uuid.UUID
	// AcknowledgePrivilegedTarget is the admin's acknowledgement that the
	// target role is privileged or its privileges are unknown (D-068).
	AcknowledgePrivilegedTarget bool
}

// testRestoreView reads the settings of a row.
func testRestoreView(ctx context.Context, q *sqlc.Queries, row *sqlc.Job) (TestRestoreSettings, error) {
	out := TestRestoreSettings{
		Enabled: row.TestRestoreEnabled, NextAt: row.TestRestoreNextAt,
		PrivilegedAckAt: row.TestRestorePrivilegedAckAt, PrivilegedAckBy: row.TestRestorePrivilegedAckBy,
	}
	if row.TestRestoreConnectionID != nil {
		p := StoredTestTargetPrivileges(row)
		out.TargetPrivileges = &p
	}
	sched, err := scheduler.Parse(row.TestRestoreSchedule, scheduler.Limits{MinInterval: scheduler.AbsoluteMinInterval})
	if err != nil {
		out.ScheduleDescription = "Invalid schedule"
	} else {
		out.Schedule, out.ScheduleDescription = sched, scheduler.Describe(sched)
	}
	if row.TestRestoreConnectionID != nil {
		c, err := q.GetJobConnectionInfo(ctx, *row.TestRestoreConnectionID)
		if err != nil {
			return out, fmt.Errorf("jobs: load the test-restore target of %s: %w", row.ID, err)
		}
		out.Target = &ConnectionInfo{ID: c.ID, Name: c.Name, Engine: engine.Kind(c.Engine), VersionKey: c.VersionKey, Deleted: c.DeletedAt != nil}
	}
	return out, nil
}

// nextTestRestore is the first occurrence after now (nil for a manual
// schedule or disabled settings).
func (s *Service) nextTestRestore(sched scheduler.Schedule, enabled bool) (*time.Time, error) {
	if !enabled || sched.Kind == scheduler.KindManual {
		return nil, nil
	}
	t, err := scheduler.Next(sched, s.o.Now())
	if err != nil {
		return nil, scheduleErr(err)
	}
	t = t.UTC()
	return &t, nil
}

// SetTestRestore replaces a job's test-restore settings
// (job.test_restore_edit: admins and maintainers with a fresh second
// factor). The scratch target must be a permanent, non-deleted connection
// with the job's engine (MySQL and MariaDB are different engines) and is
// required while enabled; it may be the job's own connection (bekup only
// ever creates and drops bekup_test_<id> databases there). Audited as
// job.test_restore_updated.
func (s *Service) SetTestRestore(ctx context.Context, actor Actor, id uuid.UUID, in TestRestoreInput) (Job, error) {
	if err := s.authorize(ctx, actor, authz.OpJobTestRestoreEdit, authz.Job{ID: id}); err != nil {
		return Job{}, err
	}
	if in.Enabled && in.TargetConnectionID == nil {
		return Job{}, fieldErr("targetConnectionId", "choose the scratch connection test restores write to")
	}
	if in.TargetConnectionID == nil && in.AcknowledgePrivilegedTarget {
		return Job{}, fieldErr("acknowledgePrivilegedTarget", "there is no scratch connection to acknowledge")
	}
	// The verdict is read outside the transaction (a runner call) and
	// decided before anything changes.
	var priv *connections.Privileges
	if in.TargetConnectionID != nil {
		p, err := s.probeTestTarget(ctx, actor, *in.TargetConnectionID)
		if err != nil {
			return Job{}, err
		}
		if err := checkPrivilegedTarget(ctx, p, in.AcknowledgePrivilegedTarget); err != nil {
			return Job{}, err
		}
		priv = &p
	}
	var out Job
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		cur, err := q.GetJobForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("jobs: load: %w", err)
		}
		sched := DefaultTestRestoreSchedule()
		if in.Schedule != nil {
			sched = *in.Schedule
		} else if cs, err := scheduler.Parse(cur.TestRestoreSchedule, scheduler.Limits{MinInterval: scheduler.AbsoluteMinInterval}); err == nil {
			sched = cs
		}
		canon, enc, err := checkSchedule(sched, s.o.Limits)
		if err != nil {
			return err
		}
		details := audit.Details{"name": cur.Name, "enabled": in.Enabled, "schedule": scheduler.Describe(canon)}
		if in.TargetConnectionID != nil {
			target, err := lockConnection(ctx, q, *in.TargetConnectionID)
			if err != nil {
				return fieldErr("targetConnectionId", "the scratch connection must be an existing, permanent connection")
			}
			job, err := q.GetJobConnectionInfo(ctx, cur.ConnectionID)
			if err != nil {
				return fmt.Errorf("jobs: load connection of %s: %w", id, err)
			}
			if target.Engine != job.Engine {
				return fieldErr("targetConnectionId", "the scratch connection must run the job's engine (%s)", job.Engine)
			}
			details["target_connection_id"], details["target_connection"] = target.ID.String(), target.Name
			details["target_is_job_connection"] = target.ID == cur.ConnectionID
			details["target_privileges"] = string(priv.Status)
			details["target_privilege_reasons"] = slices.Clone(priv.Reasons)
			details["privileged_target_acknowledged"] = priv.Gated()
		}
		next, err := s.nextTestRestore(canon, in.Enabled)
		if err != nil {
			return err
		}
		params := sqlc.SetJobTestRestoreParams{
			ID: id, Enabled: in.Enabled, Schedule: enc, ConnectionID: in.TargetConnectionID, NextAt: next, UpdatedBy: &actor.UserID,
			PrivilegeReasons: []string{},
		}
		if priv != nil {
			st := string(priv.Status)
			params.TargetPrivileges, params.PrivilegeReasons = &st, slices.Clone(priv.Reasons)
			if priv.Gated() {
				// checkPrivilegedTarget admitted only an admin with the
				// acknowledgement.
				now := s.o.Now().UTC()
				params.PrivilegedAckBy, params.PrivilegedAckAt = &actor.UserID, &now
			}
		}
		row, err := q.SetJobTestRestore(ctx, params)
		if err != nil {
			return fmt.Errorf("jobs: set test-restore settings: %w", err)
		}
		if err := s.record(ctx, q, actor, audit.ActionJobTestRestoreUpdated, id, details); err != nil {
			return err
		}
		out, err = s.view(ctx, q, &row)
		return err
	})
	if err != nil {
		return Job{}, err
	}
	if priv != nil && priv.Gated() {
		s.o.Alerts(ctx, Alert{Kind: AlertTestRestorePrivilegedTarget, JobID: id, ActorID: actor.UserID, Details: map[string]any{
			"target_connection_id": in.TargetConnectionID.String(), "target_privileges": string(priv.Status),
			"reasons": slices.Clone(priv.Reasons),
		}})
	}
	return out, nil
}

// probeTestTarget reads the scratch target's privilege verdict: one runner
// Probe (the per-user runner limiter applies). A connection that is not
// ready, and a missing prober, are unknown (fail closed); other probe
// errors (runner down, pin or host-key mismatch, rate limit) are returned.
func (s *Service) probeTestTarget(ctx context.Context, actor Actor, id uuid.UUID) (connections.Privileges, error) {
	if s.o.TestTargets == nil {
		return connections.Privileges{Status: connections.PrivilegeUnknown, Reasons: []string{ReasonProbeUnavailable}}, nil
	}
	tp, err := s.o.TestTargets.ProbeTarget(ctx, connections.Actor{UserID: actor.UserID, IP: actor.IP}, id)
	var nr *connections.NotReadyError
	switch {
	case errors.As(err, &nr):
		return connections.Privileges{Status: connections.PrivilegeUnknown, Reasons: []string{ReasonTargetNotReady}}, nil
	case errors.Is(err, connections.ErrNotFound):
		return connections.Privileges{}, fieldErr("targetConnectionId", "the scratch connection must be an existing, permanent connection")
	case err != nil:
		return connections.Privileges{}, fmt.Errorf("jobs: probe the scratch connection: %w", err)
	}
	return tp.Privileges, nil
}

// checkPrivilegedTarget applies D-068 to a gated verdict: admins only
// (ErrPrivilegedTargetAdminOnly otherwise), then the acknowledgement
// (VALIDATION without it).
func checkPrivilegedTarget(ctx context.Context, p connections.Privileges, ack bool) error {
	if !p.Gated() {
		return nil
	}
	pr, ok := auth.FromContext(ctx)
	if !ok || pr.Role != auth.RoleAdmin {
		return ErrPrivilegedTargetAdminOnly
	}
	if !ack {
		return fieldErr("acknowledgePrivilegedTarget",
			"the scratch connection's role is privileged or its privileges are unknown: acknowledge that SQL in the tested backups runs with its rights")
	}
	return nil
}

// StoredTestTargetPrivileges is the verdict stored with a job's scratch
// target (a target designated before D-170 has none: unknown).
func StoredTestTargetPrivileges(row *sqlc.Job) connections.Privileges {
	p := connections.Privileges{Status: connections.PrivilegeUnknown, Reasons: slices.Clone(row.TestRestorePrivilegeReasons)}
	if row.TestRestoreTargetPrivileges != nil {
		switch st := connections.PrivilegeStatus(*row.TestRestoreTargetPrivileges); st {
		case connections.PrivilegeNotPrivileged, connections.PrivilegePrivileged, connections.PrivilegeUnknown:
			p.Status = st
		default:
		}
	} else {
		p.Reasons = append(p.Reasons, connections.ReasonNoVerdict)
	}
	if p.Reasons == nil {
		p.Reasons = []string{}
	}
	return p
}

// TestTargetCovered reports whether a test restore may run into a target
// whose role currently has verdict cur (D-170): a not-privileged role
// always; a gated one only with a stored admin acknowledgement whose
// reasons include every current reason (a role that gained privileges
// since needs a new acknowledgement).
func TestTargetCovered(row *sqlc.Job, cur connections.Privileges) bool {
	if !cur.Gated() {
		return true
	}
	if row.TestRestorePrivilegedAckAt == nil {
		return false
	}
	stored := StoredTestTargetPrivileges(row)
	if !stored.Gated() {
		return false
	}
	for _, r := range cur.Reasons {
		if !slices.Contains(stored.Reasons, r) {
			return false
		}
	}
	return true
}

// CanonicalStats turns a runner stats message into the canonical stats
// object of D-151 (the backup_stats / manifest form) without storing it:
// the test-restore comparison (task 4.6) reads a scratch database's stats
// in exactly the form the backup recorded. The engine decides the object
// counts kept; database is the database (database stats only).
func CanonicalStats(eng engine.Kind, database string, st *runnerpb.DumpStats) (json.RawMessage, error) {
	if st == nil {
		return nil, errors.New("jobs: no stats")
	}
	ev, err := encodeStats(StatsInput{Engine: eng, Kind: pipeline.KindDatabase, Database: database}, st, nil)
	if err != nil {
		return nil, err
	}
	return ev.canonical, nil
}
