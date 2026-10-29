package jobs

import (
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/scheduler"
)

// DBMode selects the databases a job backs up.
type DBMode string

// Database modes (the job_db_mode enum).
const (
	// DBModeAll: every database on the server except Exclude (one file per
	// database, plus the PostgreSQL globals when IncludeGlobals).
	DBModeAll DBMode = "all"
	// DBModeSelected: exactly Databases.
	DBModeSelected DBMode = "selected"
)

// Valid reports whether m is a known mode.
func (m DBMode) Valid() bool { return m == DBModeAll || m == DBModeSelected }

// Verification is the completeness evidence level of a job (task 3.10).
type Verification string

// Verification levels (the job_verification enum).
const (
	// VerificationStandard: object counts plus row estimates.
	VerificationStandard Verification = "standard"
	// VerificationDeep: exact per-table row counts.
	VerificationDeep Verification = "deep"
)

// Valid reports whether v is a known level.
func (v Verification) Valid() bool { return v == VerificationStandard || v == VerificationDeep }

// Retention is how many backups a job keeps per destination (task 3.8).
// At least one of the two is set; nil means "no limit of this kind".
type Retention struct {
	KeepLast *int
	KeepDays *int
}

// ConnectionInfo is the non-secret identity of a job's connection, safe for
// every role that may view the job (owners included).
type ConnectionInfo struct {
	ID         uuid.UUID
	Name       string
	Engine     engine.Kind
	VersionKey string
	// Deleted: the connection was soft deleted after the job last changed
	// (possible only for deleted jobs; live jobs block the delete).
	Deleted bool
}

// DestinationRef is the non-secret identity of one of a job's
// destinations, in fan-out order.
type DestinationRef struct {
	ID       uuid.UUID
	Name     string
	Type     string
	Position int
	Deleted  bool
}

// Job is a backup job as shown by the API (never secrets: the encryption
// status comes from internal/jobkeys).
type Job struct {
	ID          uuid.UUID
	Name        string
	Slug        string
	Description string
	Connection  ConnectionInfo
	DBMode      DBMode
	// Databases (DBModeSelected) and Exclude (DBModeAll).
	Databases      []string
	Exclude        []string
	IncludeGlobals bool
	Schedule       scheduler.Schedule
	// ScheduleDescription is a short English summary for the UI.
	ScheduleDescription string
	Enabled             bool
	NextRunAt           *time.Time
	LastScheduledAt     *time.Time
	Retention           Retention
	// Timeout is the job's own run timeout; 0 means workers.backup_timeout.
	Timeout time.Duration
	// ExpectedWindow is the missed-backup alert window (task 5.4); 0 means
	// derived from the schedule.
	ExpectedWindow time.Duration
	Verification   Verification
	Destinations   []DestinationRef
	// TestRestore are the scheduled test-restore settings (task 4.6).
	TestRestore TestRestoreSettings
	CreatedBy   *uuid.UUID
	UpdatedBy   *uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Owner is one backup owner of a job (D-011, D-067). Only owners with role
// user gain rights from the relationship.
type Owner struct {
	UserID      uuid.UUID
	Email       string
	DisplayName string
	Role        string
	Disabled    bool
	AddedAt     time.Time
}

// Encryption is the initial encryption of a new job (encryption is always
// on at creation, D-008; an admin may opt out afterwards).
type Encryption struct {
	// Mode is jobkeys.ModePassphrase (default) or jobkeys.ModeRecipient.
	Mode jobkeys.Mode
	// Passphrase (optional) is chosen by the creator; nil generates one.
	// Create consumes (destroys) it.
	Passphrase *crypto.Secret
	// Recipient (optional) is the creator's age recipient; empty generates
	// a keypair whose identity is returned once.
	Recipient string
	Label     string
}

// CreateInput is a new job. Nil optional fields take their defaults.
type CreateInput struct {
	Name string
	// Slug (optional) names the job's storage directory; derived from Name
	// when empty. It can never change afterwards.
	Slug         string
	Description  string
	ConnectionID uuid.UUID
	DBMode       DBMode
	Databases    []string
	Exclude      []string
	// IncludeGlobals defaults to true for PostgreSQL and must be false for
	// MySQL/MariaDB.
	IncludeGlobals *bool
	Schedule       scheduler.Schedule
	// Enabled defaults to true.
	Enabled   *bool
	Retention Retention
	// Timeout (optional) is at least a minute and at most
	// workers.backup_timeout.
	Timeout        *time.Duration
	ExpectedWindow *time.Duration
	// Verification defaults to VerificationStandard.
	Verification   Verification
	DestinationIDs []uuid.UUID
	Encryption     Encryption
}

// UpdateInput changes a job; nil fields stay unchanged. The slug cannot
// change. ClearTimeout / ClearExpectedWindow reset those to their
// defaults. Retention, when set, replaces both limits.
type UpdateInput struct {
	Name                *string
	Description         *string
	ConnectionID        *uuid.UUID
	DBMode              *DBMode
	Databases           *[]string
	Exclude             *[]string
	IncludeGlobals      *bool
	Schedule            *scheduler.Schedule
	Retention           *Retention
	Timeout             *time.Duration
	ClearTimeout        bool
	ExpectedWindow      *time.Duration
	ClearExpectedWindow bool
	Verification        *Verification
	DestinationIDs      *[]uuid.UUID
}

// Created is the result of Create: the job plus what its creator sees once
// (a generated passphrase or age identity). Destroy it after use.
type Created struct {
	Job        Job
	Encryption *jobkeys.InitResult
}

// Destroy zeroes the generated secrets.
func (c *Created) Destroy() {
	if c != nil {
		c.Encryption.Destroy()
	}
}

// Preview is the result of PreviewSchedule.
type Preview struct {
	Schedule    scheduler.Schedule
	Description string
	// Next are the next occurrences (empty for manual schedules).
	Next []time.Time
}

// RunRequest describes an accepted RunNow.
type RunRequest struct {
	JobID uuid.UUID
	// QueueJobID is the River job ID of the enqueued backup_run.
	QueueJobID int64
	// RunID is the queued run.
	RunID       uuid.UUID
	RequestedAt time.Time
}

// EarlyStart describes an accepted StartNextRunEarly.
type EarlyStart struct {
	RunRequest
	// ScheduledFor is the planned occurrence that runs now (the previous
	// next_run_at).
	ScheduledFor time.Time
	// NextRunAt is the job's new next_run_at.
	NextRunAt time.Time
}

// RunCancellation describes an accepted CancelRun.
type RunCancellation struct {
	RunID uuid.UUID
	JobID uuid.UUID
	// Status is the run status after the request: canceled when the run had
	// not started (its River job was canceled at once), otherwise still
	// queued or running until the worker stops it. For a finished run whose
	// River job waits for a retry it is the run's final status.
	Status      string
	RequestedAt time.Time
	// RetryCanceled: the run had finished but its River job was waiting for
	// a retry; that retry was canceled (no new attempt starts).
	RetryCanceled bool
}

// ListFilter narrows List; zero fields do not filter.
type ListFilter struct {
	Enabled      *bool
	ConnectionID *uuid.UUID
	// Search matches a substring of the name or slug (case-insensitive).
	Search string
	// OwnerID narrows the page to the jobs this user owns (backup owner
	// listed in job_owners). Staff only: List refuses it for an owned
	// scope (ErrOwnerFilterForbidden).
	OwnerID *uuid.UUID
}

// Cursor is the keyset position of a page (the last row of the previous
// page).
type Cursor struct {
	Name string
	ID   uuid.UUID
}

// Page is one page of List.
type Page struct {
	Jobs []Job
	// More reports that another page follows.
	More bool
}
