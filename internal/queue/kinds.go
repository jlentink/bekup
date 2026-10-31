package queue

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// Job kinds. Add a kind here together with its args type and catalog entry;
// the worker that executes it registers with Register.
const (
	// KindBackupRun runs one backup of a job (worker: task 3.6).
	KindBackupRun = "backup_run"
	// KindScheduleSkipCleanup deletes old schedule_skips rows (periodic,
	// maintenance queue).
	KindScheduleSkipCleanup = "schedule_skip_cleanup"
	// KindRetentionSweep applies a job's retention (worker: task 3.8,
	// retention queue); without a job id it is the daily fan-out.
	KindRetentionSweep = "retention_sweep"
	// KindTemporaryConnectionCleanup deletes expired temporary restore
	// connections and those whose restore finished, destroying their
	// credentials (task 4.4; periodic, maintenance queue).
	KindTemporaryConnectionCleanup = "temporary_connection_cleanup"
	// KindRestore runs one restore (worker: internal/restore, task 4.2;
	// restore queue, never retried).
	KindRestore = "restore"
	// KindRestoreStuckSweep fails restores that no worker will finish
	// (crashed worker, discarded job) and releases what they held (task
	// 4.5; periodic, maintenance queue).
	KindRestoreStuckSweep = "restore_stuck_sweep"
	// KindTestRestore runs one test restore of a job (worker:
	// internal/restore, task 4.6; restore queue, never retried).
	KindTestRestore = "test_restore"
	// KindTestRestoreSchedule enqueues due test restores, fails test
	// restores no worker will finish and retries dropping leftover scratch
	// databases (task 4.6; periodic, maintenance queue).
	KindTestRestoreSchedule = "test_restore_schedule"
	// KindNotifyFanout resolves the recipients of one notification event
	// and writes its inbox rows and deliveries (internal/notify, task 5.3;
	// notify queue).
	KindNotifyFanout = "notify_fanout"
	// KindNotifyDeliver sends one e-mail or webhook delivery (task 5.3;
	// notify queue, retried up to MaxAttemptsNotify).
	KindNotifyDeliver = "notify_deliver"
	// KindNotifyRetentionDigest emits the daily retention.summary events
	// (task 5.3; periodic, maintenance queue).
	KindNotifyRetentionDigest = "notify_retention_digest"
	// KindNotifyCleanup prunes old notification events, deliveries and
	// inbox rows (task 5.3; periodic, maintenance queue).
	KindNotifyCleanup = "notify_cleanup"
	// KindSecurityCheck runs the periodic security checks: certificate
	// and pinned-certificate expiry, incremental audit chain verification
	// and weakened-posture reminders (internal/monitor, task 5.5;
	// periodic, maintenance queue).
	KindSecurityCheck = "security_check"
	// KindMissedBackupCheck emits backup.missed for jobs without a
	// successful backup within their expected window (internal/notify/missed,
	// task 5.4; periodic, maintenance queue).
	KindMissedBackupCheck = "missed_backup_check"
)

// Trigger says why a backup run was enqueued.
type Trigger string

// Triggers: the scheduler's (schedule, catch_up), runJobNow's (manual,
// task 3.5) and startNextRunEarly's (early, task 3.12, D-156: the next
// planned occurrence pulled forward; ScheduledFor is that occurrence).
const (
	TriggerSchedule Trigger = "schedule"
	TriggerCatchUp  Trigger = "catch_up"
	TriggerManual   Trigger = "manual"
	TriggerEarly    Trigger = "early"
)

// BackupRunArgs are the args of KindBackupRun. Unique by JobID: a job has at
// most one queued, running or retry-pending run (no overlap).
type BackupRunArgs struct {
	JobID uuid.UUID `json:"job_id" river:"unique"`
	// ScheduledFor is the occurrence the run stands for (the stored
	// next_run_at, D-143; for early runs the original occurrence pulled
	// forward); zero for manual runs.
	ScheduledFor time.Time `json:"scheduled_for"`
	Trigger      Trigger   `json:"trigger"`
	// TriggeredBy is the user who requested a manual or early run (nil
	// for scheduled runs).
	TriggeredBy *uuid.UUID `json:"triggered_by,omitempty"`
}

// Kind implements river.JobArgs.
func (BackupRunArgs) Kind() string { return KindBackupRun }

// ScheduleSkipCleanupArgs are the (empty) args of KindScheduleSkipCleanup.
type ScheduleSkipCleanupArgs struct{}

// Kind implements river.JobArgs.
func (ScheduleSkipCleanupArgs) Kind() string { return KindScheduleSkipCleanup }

// TemporaryConnectionCleanupArgs are the (empty) args of
// KindTemporaryConnectionCleanup.
type TemporaryConnectionCleanupArgs struct{}

// Kind implements river.JobArgs.
func (TemporaryConnectionCleanupArgs) Kind() string { return KindTemporaryConnectionCleanup }

// RestoreArgs are the args of KindRestore: the restore id only (D-145;
// the restore row holds everything else, an age identity travels only
// sealed in restore_identity_handoff). Unique by RestoreID.
type RestoreArgs struct {
	RestoreID uuid.UUID `json:"restore_id" river:"unique"`
}

// Kind implements river.JobArgs.
func (RestoreArgs) Kind() string { return KindRestore }

// RestoreStuckSweepArgs are the (empty) args of KindRestoreStuckSweep.
type RestoreStuckSweepArgs struct{}

// Kind implements river.JobArgs.
func (RestoreStuckSweepArgs) Kind() string { return KindRestoreStuckSweep }

// TestRestoreArgs are the args of KindTestRestore: the test restore id
// only (D-145). Unique by TestRestoreID.
type TestRestoreArgs struct {
	TestRestoreID uuid.UUID `json:"test_restore_id" river:"unique"`
}

// Kind implements river.JobArgs.
func (TestRestoreArgs) Kind() string { return KindTestRestore }

// TestRestoreScheduleArgs are the (empty) args of KindTestRestoreSchedule.
type TestRestoreScheduleArgs struct{}

// Kind implements river.JobArgs.
func (TestRestoreScheduleArgs) Kind() string { return KindTestRestoreSchedule }

// RetentionReason says why a retention sweep was enqueued.
type RetentionReason string

// Retention reasons: after a backup run finished (success or partial), the
// daily sweep (periodic fan-out and the per-job sweeps it enqueues), or a
// changed retention policy of the job (D-209).
const (
	RetentionPostRun       RetentionReason = "post_run"
	RetentionDaily         RetentionReason = "daily"
	RetentionPolicyChanged RetentionReason = "policy_changed"
)

// RetentionSweepArgs are the args of KindRetentionSweep. Unique by JobID: a
// job has at most one queued or running sweep (two sweeps of one job never
// run at the same time); the daily fan-out (JobID nil) is unique as well.
type RetentionSweepArgs struct {
	// JobID is the job to sweep; nil = the daily fan-out, which enqueues a
	// sweep for every job with present artifacts and cleans up leftover
	// temporary files on disk and SFTP destinations.
	JobID  *uuid.UUID      `json:"job_id,omitempty" river:"unique"`
	Reason RetentionReason `json:"reason"`
	// DryRun computes and logs the plan without deleting anything (also
	// forced by retention.dry_run).
	DryRun bool `json:"dry_run,omitempty"`
}

// Kind implements river.JobArgs.
func (RetentionSweepArgs) Kind() string { return KindRetentionSweep }

// NotifyFanoutArgs are the args of KindNotifyFanout: the event id only
// (the outbox row holds everything else). Unique by EventID.
type NotifyFanoutArgs struct {
	EventID uuid.UUID `json:"event_id" river:"unique"`
}

// Kind implements river.JobArgs.
func (NotifyFanoutArgs) Kind() string { return KindNotifyFanout }

// NotifyDeliverArgs are the args of KindNotifyDeliver: the delivery id
// only. Unique by DeliveryID.
type NotifyDeliverArgs struct {
	DeliveryID uuid.UUID `json:"delivery_id" river:"unique"`
}

// Kind implements river.JobArgs.
func (NotifyDeliverArgs) Kind() string { return KindNotifyDeliver }

// NotifyRetentionDigestArgs are the (empty) args of
// KindNotifyRetentionDigest.
type NotifyRetentionDigestArgs struct{}

// Kind implements river.JobArgs.
func (NotifyRetentionDigestArgs) Kind() string { return KindNotifyRetentionDigest }

// NotifyCleanupArgs are the (empty) args of KindNotifyCleanup.
type NotifyCleanupArgs struct{}

// Kind implements river.JobArgs.
func (NotifyCleanupArgs) Kind() string { return KindNotifyCleanup }

// SecurityCheckArgs are the (empty) args of KindSecurityCheck.
type SecurityCheckArgs struct{}

// Kind implements river.JobArgs.
func (SecurityCheckArgs) Kind() string { return KindSecurityCheck }

// MissedBackupCheckArgs are the (empty) args of KindMissedBackupCheck.
type MissedBackupCheckArgs struct{}

// Kind implements river.JobArgs.
func (MissedBackupCheckArgs) Kind() string { return KindMissedBackupCheck }

// KindSpec declares how a kind is inserted and worked.
type KindSpec struct {
	// Queue the kind runs in; its timeout bounds every attempt.
	Queue Queue
	// MaxAttempts overrides the queue default when > 0. Kinds in the
	// restore queue must keep 1.
	MaxAttempts int
	// Unique allows one job per unique key (the args fields tagged
	// `river:"unique"`) in UniqueStates.
	Unique bool
	// Args is the zero value of the kind's args type (catalog check).
	Args river.JobArgs
}

// catalog is the static kind catalog.
var catalog = map[string]KindSpec{
	KindBackupRun:           {Queue: QueueBackup, Unique: true, Args: BackupRunArgs{}},
	KindScheduleSkipCleanup: {Queue: QueueMaintenance, Unique: true, Args: ScheduleSkipCleanupArgs{}},
	KindRetentionSweep:      {Queue: QueueRetention, Unique: true, Args: RetentionSweepArgs{}},
	KindTemporaryConnectionCleanup: {
		Queue: QueueMaintenance, Unique: true, Args: TemporaryConnectionCleanupArgs{},
	},
	KindRestore: {Queue: QueueRestore, MaxAttempts: MaxAttemptsRestore, Unique: true, Args: RestoreArgs{}},
	KindRestoreStuckSweep: {
		Queue: QueueMaintenance, Unique: true, Args: RestoreStuckSweepArgs{},
	},
	KindTestRestore: {Queue: QueueRestore, MaxAttempts: MaxAttemptsRestore, Unique: true, Args: TestRestoreArgs{}},
	KindTestRestoreSchedule: {
		Queue: QueueMaintenance, Unique: true, Args: TestRestoreScheduleArgs{},
	},
	KindNotifyFanout:  {Queue: QueueNotify, Unique: true, Args: NotifyFanoutArgs{}},
	KindNotifyDeliver: {Queue: QueueNotify, Unique: true, Args: NotifyDeliverArgs{}},
	KindNotifyRetentionDigest: {
		Queue: QueueMaintenance, Unique: true, Args: NotifyRetentionDigestArgs{},
	},
	KindNotifyCleanup: {Queue: QueueMaintenance, Unique: true, Args: NotifyCleanupArgs{}},
	KindSecurityCheck: {Queue: QueueMaintenance, Unique: true, Args: SecurityCheckArgs{}},
	KindMissedBackupCheck: {
		Queue: QueueMaintenance, Unique: true, Args: MissedBackupCheckArgs{},
	},
}

// Kinds returns the catalog's kinds in sorted order.
func Kinds() []string {
	out := make([]string, 0, len(catalog))
	for k := range catalog {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Spec returns the catalog entry of kind.
func Spec(kind string) (KindSpec, bool) {
	s, ok := catalog[kind]
	return s, ok
}

// maxAttempts resolves the attempts of a kind.
func (k KindSpec) maxAttempts(s Settings) int {
	if k.MaxAttempts > 0 {
		return k.MaxAttempts
	}
	return s.Queues[k.Queue].MaxAttempts
}

var kindName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ValidateCatalog checks every kind: a known queue, sane attempts (restore
// kinds exactly one), an args type whose Kind matches and that passes
// CheckArgs, and at least one unique field for unique kinds with fields.
func ValidateCatalog() error { return validateCatalog(catalog) }

func validateCatalog(c map[string]KindSpec) error {
	for kind, spec := range c {
		if !kindName.MatchString(kind) {
			return fmt.Errorf("kind %q: invalid name", kind)
		}
		if spec.Args == nil || spec.Args.Kind() != kind {
			return fmt.Errorf("kind %q: args prototype missing or of another kind", kind)
		}
		if !knownQueue(spec.Queue) {
			return fmt.Errorf("kind %q: unknown queue %q", kind, spec.Queue)
		}
		if spec.MaxAttempts < 0 || spec.MaxAttempts > 25 {
			return fmt.Errorf("kind %q: max attempts %d out of range", kind, spec.MaxAttempts)
		}
		if spec.Queue == QueueRestore && spec.MaxAttempts > MaxAttemptsRestore {
			return fmt.Errorf("kind %q: restores are never retried", kind)
		}
		if err := CheckArgs(spec.Args); err != nil {
			return fmt.Errorf("kind %q: %w", kind, err)
		}
		if spec.Unique {
			t := reflect.TypeOf(spec.Args)
			if t.NumField() > 0 && !hasUniqueField(t) {
				return fmt.Errorf("kind %q: unique kind without a river:\"unique\" field", kind)
			}
		}
	}
	return nil
}

func hasUniqueField(t reflect.Type) bool {
	for f := range t.Fields() {
		if f.Tag.Get("river") == "unique" {
			return true
		}
	}
	return false
}
