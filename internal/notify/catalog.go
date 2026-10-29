package notify

import (
	"slices"
	"sort"

	"github.com/jlentink/bekup/internal/authz"
)

// EventType is a notification event of the D-161 catalog.
type EventType string

// The D-161 event catalog. Add an event here and to catalog below; the
// preference matrix (task 5.3b) lists exactly these.
const (
	// Backup lifecycle (subject: a job and its run).
	BackupStarted               EventType = "backup.started"
	BackupSucceeded             EventType = "backup.succeeded"
	BackupSucceededWithWarnings EventType = "backup.succeeded_with_warnings"
	BackupPartial               EventType = "backup.partial"
	BackupFailed                EventType = "backup.failed"
	BackupCanceled              EventType = "backup.canceled"
	// BackupMissed is emitted by the dead-man check (task 5.4).
	BackupMissed EventType = "backup.missed"
	// BackupStartedEarly: a run pulled forward with startNextRunEarly
	// started (instead of backup.started).
	BackupStartedEarly EventType = "backup.started_early"

	// Restore lifecycle (subject: a job and its restore / test restore).
	RestoreStarted      EventType = "restore.started"
	RestoreCompleted    EventType = "restore.completed"
	RestoreFailed       EventType = "restore.failed"
	TestRestorePassed   EventType = "test_restore.passed"
	TestRestoreWarnings EventType = "test_restore.warnings"
	TestRestoreFailed   EventType = "test_restore.failed"

	// Retention & storage.
	// RetentionSummary is the daily digest of a job's retention removals.
	RetentionSummary      EventType = "retention.summary"
	ArtifactMissing       EventType = "artifact.missing"
	DestinationTestFailed EventType = "destination.test_failed"

	// Job & configuration changes.
	JobCreated               EventType = "job.created"
	JobUpdated               EventType = "job.updated"
	JobDisabled              EventType = "job.disabled"
	JobEnabled               EventType = "job.enabled"
	JobDeleted               EventType = "job.deleted"
	JobEncryptionChanged     EventType = "job.encryption_changed"
	ConnectionPinChanged     EventType = "connection.pin_changed"
	ConnectionTLSDowngraded  EventType = "connection.tls_downgraded"
	ConnectionHostKeyChanged EventType = "connection.host_key_changed"

	// Security (mandatory: always inbox + e-mail).
	// SecurityAccount concerns the recipient's own account.
	SecurityAccount EventType = "security.account"
	// SecuritySystem goes to every admin.
	SecuritySystem EventType = "security.system"
)

// Notice types: inbox rows that are not catalog events (no preferences).
const (
	// NoticeWebhookDisabled tells the owner (personal webhook) or the
	// admins (shared channel) that a webhook was auto-disabled.
	NoticeWebhookDisabled = "notice.webhook_disabled"
)

// Group is a D-161 event group (the rows of the preference matrix).
type Group string

// Groups.
const (
	GroupBackup    Group = "backup"
	GroupRestore   Group = "restore"
	GroupRetention Group = "retention"
	GroupConfig    Group = "config"
	GroupSecurity  Group = "security"
)

// Groups returns the groups in display order.
func Groups() []Group {
	return []Group{GroupBackup, GroupRestore, GroupRetention, GroupConfig, GroupSecurity}
}

// Audience is who may receive an event by default (before preferences).
// Delivery always re-checks it with the authz policy (Meta.Op) at
// delivery time.
type Audience string

// Audiences.
const (
	// AudienceJob: every user who may see the subject job (admins,
	// maintainers and the job's owners).
	AudienceJob Audience = "job"
	// AudienceStaff: admins and maintainers.
	AudienceStaff Audience = "staff"
	// AudienceAdmins: admins only.
	AudienceAdmins Audience = "admins"
	// AudienceOwnAccount: only the user the event is about.
	AudienceOwnAccount Audience = "own_account"
)

// Severity is the presentational severity of an event.
type Severity string

// Severities.
const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical"
)

// Valid reports whether s is a known severity.
func (s Severity) Valid() bool {
	switch s {
	case SeverityInfo, SeverityWarning, SeverityError, SeverityCritical:
		return true
	}
	return false
}

// Priority orders events; low-priority events (*.started) are collapsed
// per job within notifications.collapse_window.
type Priority string

// Priorities.
const (
	PriorityNormal Priority = "normal"
	PriorityLow    Priority = "low"
)

// SubjectKind is the identifier an event must carry.
type SubjectKind string

// Subject kinds.
const (
	SubjectJob         SubjectKind = "job"
	SubjectConnection  SubjectKind = "connection"
	SubjectDestination SubjectKind = "destination"
	SubjectUser        SubjectKind = "user"
	SubjectNone        SubjectKind = "none"
)

// Meta describes one catalog event.
type Meta struct {
	Type     EventType
	Group    Group
	Audience Audience
	// Problem events go to e-mail by default (D-161).
	Problem  bool
	Priority Priority
	// Mandatory events always reach the inbox and e-mail; preferences can
	// only add channels.
	Mandatory bool
	Severity  Severity
	// Subject is the identifier the event must carry.
	Subject SubjectKind
	// Op is the operation a recipient must be allowed at delivery time
	// (with the subject job as resource for job events).
	Op authz.Operation
	// Label is the short human name ("Backup failed").
	Label string
}

func jobEvent(t EventType, g Group, op authz.Operation, problem bool, sev Severity, label string) Meta {
	return Meta{
		Type: t, Group: g, Audience: AudienceJob, Problem: problem, Priority: PriorityNormal,
		Severity: sev, Subject: SubjectJob, Op: op, Label: label,
	}
}

func low(m Meta) Meta {
	m.Priority = PriorityLow
	return m
}

// catalog is the D-161 catalog.
var catalog = func() map[EventType]Meta {
	list := []Meta{
		low(jobEvent(BackupStarted, GroupBackup, authz.OpRunView, false, SeverityInfo, "Backup started")),
		jobEvent(BackupSucceeded, GroupBackup, authz.OpRunView, false, SeverityInfo, "Backup succeeded"),
		jobEvent(BackupSucceededWithWarnings, GroupBackup, authz.OpRunView, true, SeverityWarning, "Backup succeeded with warnings"),
		jobEvent(BackupPartial, GroupBackup, authz.OpRunView, true, SeverityError, "Backup partially failed"),
		jobEvent(BackupFailed, GroupBackup, authz.OpRunView, true, SeverityError, "Backup failed"),
		jobEvent(BackupCanceled, GroupBackup, authz.OpRunView, false, SeverityWarning, "Backup canceled"),
		jobEvent(BackupMissed, GroupBackup, authz.OpRunView, true, SeverityError, "Backup missed"),
		jobEvent(BackupStartedEarly, GroupBackup, authz.OpRunView, false, SeverityInfo, "Backup started early"),

		low(jobEvent(RestoreStarted, GroupRestore, authz.OpRestoreView, false, SeverityInfo, "Restore started")),
		jobEvent(RestoreCompleted, GroupRestore, authz.OpRestoreView, false, SeverityInfo, "Restore completed"),
		jobEvent(RestoreFailed, GroupRestore, authz.OpRestoreView, true, SeverityError, "Restore failed"),
		jobEvent(TestRestorePassed, GroupRestore, authz.OpTestRestoreView, false, SeverityInfo, "Test restore passed"),
		jobEvent(TestRestoreWarnings, GroupRestore, authz.OpTestRestoreView, true, SeverityWarning, "Test restore passed with warnings"),
		jobEvent(TestRestoreFailed, GroupRestore, authz.OpTestRestoreView, true, SeverityError, "Test restore failed"),

		jobEvent(RetentionSummary, GroupRetention, authz.OpArtifactView, false, SeverityInfo, "Retention summary"),
		jobEvent(ArtifactMissing, GroupRetention, authz.OpArtifactView, true, SeverityError, "Backup file missing"),
		{
			Type: DestinationTestFailed, Group: GroupRetention, Audience: AudienceStaff, Problem: true,
			Priority: PriorityNormal, Severity: SeverityWarning, Subject: SubjectDestination,
			Op: authz.OpDestinationView, Label: "Destination test failed",
		},

		jobEvent(JobCreated, GroupConfig, authz.OpJobView, false, SeverityInfo, "Job created"),
		jobEvent(JobUpdated, GroupConfig, authz.OpJobView, false, SeverityInfo, "Job updated"),
		jobEvent(JobDisabled, GroupConfig, authz.OpJobView, false, SeverityWarning, "Job disabled"),
		jobEvent(JobEnabled, GroupConfig, authz.OpJobView, false, SeverityInfo, "Job enabled"),
		jobEvent(JobDeleted, GroupConfig, authz.OpJobView, false, SeverityWarning, "Job deleted"),
		jobEvent(JobEncryptionChanged, GroupConfig, authz.OpJobView, true, SeverityWarning, "Job encryption changed"),
		staffConnection(ConnectionPinChanged, SeverityWarning, "Certificate pin changed"),
		staffConnection(ConnectionTLSDowngraded, SeverityError, "Connection TLS downgraded"),
		staffConnection(ConnectionHostKeyChanged, SeverityWarning, "SSH host key changed"),

		{
			Type: SecurityAccount, Group: GroupSecurity, Audience: AudienceOwnAccount, Problem: true,
			Priority: PriorityNormal, Mandatory: true, Severity: SeverityWarning, Subject: SubjectUser,
			Op: authz.OpAccountView, Label: "Account security",
		},
		{
			Type: SecuritySystem, Group: GroupSecurity, Audience: AudienceAdmins, Problem: true,
			Priority: PriorityNormal, Mandatory: true, Severity: SeverityCritical, Subject: SubjectNone,
			Op: authz.OpAlertList, Label: "Security alert",
		},
	}
	m := make(map[EventType]Meta, len(list))
	for _, e := range list {
		m[e.Type] = e
	}
	return m
}()

func staffConnection(t EventType, sev Severity, label string) Meta {
	return Meta{
		Type: t, Group: GroupConfig, Audience: AudienceStaff, Problem: true, Priority: PriorityNormal,
		Severity: sev, Subject: SubjectConnection, Op: authz.OpConnectionView, Label: label,
	}
}

// Lookup returns the catalog entry of t.
func Lookup(t EventType) (Meta, bool) {
	m, ok := catalog[t]
	return m, ok
}

// Catalog returns every event, grouped in Groups order and sorted by type
// within a group.
func Catalog() []Meta {
	out := make([]Meta, 0, len(catalog))
	for k := range catalog {
		out = append(out, catalog[k])
	}
	order := Groups()
	sort.Slice(out, func(i, j int) bool {
		gi, gj := slices.Index(order, out[i].Group), slices.Index(order, out[j].Group)
		if gi != gj {
			return gi < gj
		}
		return out[i].Type < out[j].Type
	})
	return out
}

// MandatoryTypes returns the mandatory event types (exempt from the flood
// rule and from preferences that would remove inbox or e-mail).
func MandatoryTypes() []string {
	var out []string
	all := Catalog()
	for i := range all {
		if all[i].Mandatory {
			out = append(out, string(all[i].Type))
		}
	}
	return out
}
