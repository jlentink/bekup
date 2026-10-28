package restore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river/rivertype"

	"github.com/jlentink/bekup/internal/artifactverify"
	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// The restore API (task 4.5, D-167): what the GraphQL resolvers call. It
// authorizes every operation with the central policy (the artifact's job
// and the target are loaded from the database, never taken from input),
// verifies the artifact's signed manifest before it relies on the source
// engine, and records a restore, its River job, the restore.requested
// audit entry and the restore event in one transaction that also holds
// the target connection's share lock (connections.LockRestoreTargetTx).

// API defaults.
const (
	// DefaultSourceTTL caches a verified manifest's source facts (the
	// manifest is immutable; a tamper failure is cached as well, so a
	// probing client raises one alert per artifact and period).
	DefaultSourceTTL = 5 * time.Minute
	// DefaultDatabasesTTL caches a target's database list (runner Probe).
	DefaultDatabasesTTL = 30 * time.Second
	// maxCacheEntries bounds each cache.
	maxCacheEntries = 512
	// maxListedDatabases bounds a target's database list.
	maxListedDatabases = 2000
)

// TargetConnections is the connection service as the API uses it
// (*connections.Service).
type TargetConnections interface {
	List(ctx context.Context) ([]connections.Connection, error)
	ListTemporary(ctx context.Context) ([]connections.Connection, error)
	GetForRestore(ctx context.Context, id uuid.UUID) (connections.Connection, error)
	LockRestoreTargetTx(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (connections.Connection, error)
	// ProbeTarget is one runner Probe: the databases, warnings and the
	// role's privilege verdict (D-068).
	ProbeTarget(ctx context.Context, actor connections.Actor, id uuid.UUID) (connections.TargetProbe, error)
}

// OwnerTargets lists a backup owner's restore targets (jobs.Ownership).
// Listings only: restores are authorized through the policy.
type OwnerTargets interface {
	OwnedConnections(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	BackedUpDatabases(ctx context.Context, userID, connectionID uuid.UUID, names []string) ([]string, error)
}

// Canceler cancels River jobs in a transaction (*queue.Client).
type Canceler interface {
	CancelTx(ctx context.Context, tx pgx.Tx, jobID int64) (rivertype.JobState, error)
}

// APIOptions configure NewAPI.
type APIOptions struct {
	Service     *Service
	Policy      *authz.Policy
	Connections TargetConnections
	Owners      OwnerTargets
	// Queue inserts the restore's River job (resolved late: the queue
	// client is built after the API in serve).
	Queue func() Inserter
	// Canceler cancels a restore's River job (resolved late).
	Canceler     func() Canceler
	SourceTTL    time.Duration
	DatabasesTTL time.Duration
	// RestoreTimeout is workers.restore_timeout: a temporary target must
	// stay valid at least this long when a restore is requested (0: no
	// check).
	RestoreTimeout time.Duration
	Logger         *slog.Logger
}

// API is the restore API.
type API struct {
	o   APIOptions
	svc *Service

	mu      sync.Mutex
	sources map[uuid.UUID]sourceEntry
	dbs     map[uuid.UUID]dbEntry
}

type sourceEntry struct {
	src Source
	err error
	at  time.Time
}

type dbEntry struct {
	names, warnings []string
	privileges      connections.Privileges
	at              time.Time
}

// NewAPI validates o.
func NewAPI(o APIOptions) (*API, error) {
	if o.Service == nil || o.Policy == nil || o.Connections == nil || o.Owners == nil || o.Queue == nil || o.Canceler == nil {
		return nil, errors.New("restore: the API needs Service, Policy, Connections, Owners, Queue and Canceler")
	}
	if o.SourceTTL <= 0 {
		o.SourceTTL = DefaultSourceTTL
	}
	if o.DatabasesTTL <= 0 {
		o.DatabasesTTL = DefaultDatabasesTTL
	}
	if o.Logger == nil {
		o.Logger = o.Service.o.Logger
	}
	return &API{o: o, svc: o.Service, sources: map[uuid.UUID]sourceEntry{}, dbs: map[uuid.UUID]dbEntry{}}, nil
}

// errForbidden is the policy's generic denial (same code and text) for
// checks the API makes itself (e.g. a globals artifact for a non-admin).
var errForbidden = apierror.New(apierror.Forbidden, "not allowed")

// caller returns the principal or the policy's UNAUTHENTICATED.
func (a *API) caller(ctx context.Context) (auth.Principal, error) {
	pr, ok := auth.FromContext(ctx)
	if !ok {
		return auth.Principal{}, a.o.Policy.Authenticated(ctx)
	}
	return pr, nil
}

// --- source -------------------------------------------------------------------

// Source is what a verified artifact is: the facts the restore API relies
// on, taken from the signed manifest (engine) and the records.
type Source struct {
	ArtifactID uuid.UUID
	JobID      uuid.UUID
	RunID      uuid.UUID
	Kind       sqlc.ArtifactKind
	// Database is the item's database ("" for globals).
	Database string
	// Engine, VersionKey and ServerVersion come from the signed manifest.
	Engine        engine.Kind
	VersionKey    string
	ServerVersion string
	// Encryption is passphrase, recipients or none.
	Encryption string
	// JobConnectionID is the job's current connection (uuid.Nil when the
	// job was deleted): the "original" target.
	JobConnectionID uuid.UUID
}

// NeedsIdentity reports whether a restore needs the user's age identity.
func (s Source) NeedsIdentity() bool { return s.Encryption == encRecipients }

// loadArtifact loads an artifact row; ErrNotFound when it does not exist.
func (a *API) loadArtifact(ctx context.Context, id uuid.UUID) (sqlc.GetDownloadArtifactRow, error) {
	row, err := sqlc.New(a.svc.o.DB).GetDownloadArtifact(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, ErrNotFound
	}
	if err != nil {
		return row, fmt.Errorf("restore: load artifact: %w", err)
	}
	return row, nil
}

// authorizeArtifact loads the artifact and runs op's coarse check with its
// job (a missing artifact answers like another job's for backup owners:
// FORBIDDEN, and ErrNotFound for staff).
func (a *API) authorizeArtifact(ctx context.Context, op authz.Operation, id uuid.UUID) (sqlc.GetDownloadArtifactRow, error) {
	row, err := a.loadArtifact(ctx, id)
	if errors.Is(err, ErrNotFound) {
		if aerr := a.o.Policy.Authorize(ctx, op, authz.Job{}); aerr != nil {
			return row, aerr
		}
		return row, ErrNotFound
	}
	if err != nil {
		return row, err
	}
	if err := a.o.Policy.Authorize(ctx, op, authz.Job{ID: row.Artifact.JobID}); err != nil {
		return row, err
	}
	return row, nil
}

// source returns the verified source facts of an artifact row (cached).
// The artifact must be restorable (present, verified, signed) and its
// signed manifest must verify; a verification failure is a possible
// tampering: logged and alerted (once per SourceTTL and artifact).
func (a *API) source(ctx context.Context, row *sqlc.GetDownloadArtifactRow) (Source, error) {
	art := &row.Artifact
	if err := checkArtifact(row); err != nil {
		return Source{}, err
	}
	now := a.svc.o.Now()
	a.mu.Lock()
	if e, ok := a.sources[art.ID]; ok && now.Sub(e.at) < a.o.SourceTTL {
		a.mu.Unlock()
		return e.src, e.err
	}
	a.mu.Unlock()

	src, err := a.verifySource(ctx, row)
	if err != nil && !errors.Is(err, ErrVerification) {
		return Source{}, err // transient (destination, database): not cached
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sources) >= maxCacheEntries {
		evictOldest(a.sources, func(e sourceEntry) time.Time { return e.at })
	}
	a.sources[art.ID] = sourceEntry{src: src, err: err, at: now}
	return src, err
}

func (a *API) verifySource(ctx context.Context, row *sqlc.GetDownloadArtifactRow) (Source, error) {
	art := &row.Artifact
	dest, err := a.svc.o.Destinations.Open(ctx, art.DestinationID)
	if err != nil {
		return Source{}, fmt.Errorf("%w: %w", ErrDestination, err)
	}
	defer func() {
		if cerr := dest.Close(); cerr != nil {
			a.o.Logger.WarnContext(ctx, "closing the destination failed", slog.Any("error", cerr))
		}
	}()
	q := sqlc.New(a.svc.o.DB)
	m, err := artifactverify.VerifyManifest(ctx, q, dest, artifactverify.FromDownloadRow(row))
	switch {
	case errors.Is(err, artifactverify.ErrManifest):
		ve := manifestFailure(err)
		a.svc.o.Alert(ctx, Alert{Kind: AlertArtifactTampered, JobID: art.JobID, ArtifactID: art.ID,
			Details: map[string]any{"reason": ve.Code, "stage": "request"}})
		return Source{}, ve
	case errors.Is(err, artifactverify.ErrDestination):
		return Source{}, fmt.Errorf("%w: %w", ErrDestination, err)
	case err != nil:
		return Source{}, err
	}
	src := Source{
		ArtifactID: art.ID, JobID: art.JobID, RunID: art.RunID, Kind: art.Kind,
		Engine: engine.Kind(m.Engine.Kind), VersionKey: m.Engine.VersionKey, ServerVersion: m.Engine.ServerVersion,
		Encryption: encryptionOf(art),
	}
	if row.ItemDbName != nil {
		src.Database = *row.ItemDbName
	}
	if !src.Engine.Valid() {
		return Source{}, &VerificationError{Code: VerifyManifestInvalid, Err: errors.New("unknown engine")}
	}
	j, err := q.GetJob(ctx, art.JobID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return Source{}, fmt.Errorf("restore: load job: %w", err)
	default:
		src.JobConnectionID = j.ConnectionID
	}
	return src, nil
}

func evictOldest[K comparable, V any](m map[K]V, at func(V) time.Time) {
	var (
		oldK  K
		oldAt time.Time
		found bool
	)
	for k, v := range m {
		if t := at(v); !found || t.Before(oldAt) {
			oldK, oldAt, found = k, t, true
		}
	}
	if found {
		delete(m, oldK)
	}
}

// --- targets ------------------------------------------------------------------

// Target is a connection a caller may restore an artifact to.
type Target struct {
	Connection connections.Connection
	// Original: the connection the artifact's job backs up.
	Original bool
	// NewDatabaseAllowed: a new database may be created (database
	// artifacts).
	NewDatabaseAllowed bool
	// ExistingRestricted: only the databases the caller's own jobs back up
	// may be overwritten (backup owners).
	ExistingRestricted bool
}

// TargetList is the answer of Targets.
type TargetList struct {
	Source  Source
	Targets []Target
}

// Targets lists the connections the caller may restore artifactID to: the
// same engine as the backup (signed manifest); admins and maintainers any
// permanent connection (admins also temporary ones), backup owners only
// the connections their own jobs back up. Globals restores are admin only.
func (a *API) Targets(ctx context.Context, artifactID uuid.UUID) (TargetList, error) {
	pr, err := a.caller(ctx)
	if err != nil {
		return TargetList{}, err
	}
	row, err := a.authorizeArtifact(ctx, authz.OpRestoreTargetsList, artifactID)
	if err != nil {
		return TargetList{}, err
	}
	if row.Artifact.Kind == sqlc.ArtifactKindGlobals && pr.Role != auth.RoleAdmin {
		return TargetList{}, errForbidden
	}
	src, err := a.source(ctx, &row)
	if err != nil {
		return TargetList{}, err
	}
	cands, err := a.candidates(ctx, pr)
	if err != nil {
		return TargetList{}, err
	}
	out := TargetList{Source: src, Targets: []Target{}}
	for i := range cands {
		c := &cands[i]
		if c.Engine != src.Engine || (src.Kind == sqlc.ArtifactKindGlobals && c.Engine != engine.KindPostgres) {
			continue
		}
		out.Targets = append(out.Targets, Target{
			Connection: *c, Original: src.JobConnectionID != uuid.Nil && c.ID == src.JobConnectionID,
			NewDatabaseAllowed: src.Kind == sqlc.ArtifactKindDatabase, ExistingRestricted: pr.Role == auth.RoleUser,
		})
	}
	slices.SortStableFunc(out.Targets, func(x, y Target) int {
		switch {
		case x.Original != y.Original:
			if x.Original {
				return -1
			}
			return 1
		case x.Connection.Temporary != y.Connection.Temporary:
			if y.Connection.Temporary {
				return -1
			}
			return 1
		default:
			return strings.Compare(strings.ToLower(x.Connection.Name), strings.ToLower(y.Connection.Name))
		}
	})
	return out, nil
}

// candidates are the connections the caller may restore to at all.
func (a *API) candidates(ctx context.Context, pr auth.Principal) ([]connections.Connection, error) {
	all, err := a.o.Connections.List(ctx)
	if err != nil {
		return nil, err
	}
	switch pr.Role {
	case auth.RoleAdmin:
		tmp, err := a.o.Connections.ListTemporary(ctx)
		if err != nil {
			return nil, err
		}
		return append(all, tmp...), nil
	case auth.RoleMaintainer:
		return all, nil
	case auth.RoleUser:
		owned, err := a.o.Owners.OwnedConnections(ctx, pr.UserID)
		if err != nil {
			return nil, err
		}
		out := make([]connections.Connection, 0, len(owned))
		for i := range all {
			if slices.Contains(owned, all[i].ID) {
				out = append(out, all[i])
			}
		}
		return out, nil
	default:
		return nil, errForbidden
	}
}

// TargetDatabases is the answer of Databases.
type TargetDatabases struct {
	Target Target
	// Databases are the existing databases the caller may overwrite
	// (backup owners: only those their jobs back up), without bekup's
	// temporary restore databases.
	Databases []string
	// Warnings are the runner's warning codes.
	Warnings []string
	// Privileges is the verdict on the target role (D-068).
	Privileges connections.Privileges
	ListedAt   time.Time
}

// Databases lists the existing databases of one restore target through
// the runner (cached for DatabasesTTL). It is one runner call at most, so
// the GraphQL field is a single-root field (D-135, D-117).
func (a *API) Databases(ctx context.Context, artifactID, connectionID uuid.UUID) (TargetDatabases, error) {
	list, err := a.Targets(ctx, artifactID)
	if err != nil {
		return TargetDatabases{}, err
	}
	var t *Target
	for i := range list.Targets {
		if list.Targets[i].Connection.ID == connectionID {
			t = &list.Targets[i]
		}
	}
	if t == nil {
		return TargetDatabases{}, a.unknownTarget(ctx)
	}
	if list.Source.Kind != sqlc.ArtifactKindDatabase {
		return TargetDatabases{}, serviceutil.FieldErr("artifactId", "globals backups restore into the cluster, not into a database")
	}
	pr, err := a.caller(ctx)
	if err != nil {
		return TargetDatabases{}, err
	}
	e, err := a.probeTarget(ctx, pr, connectionID, false)
	if err != nil {
		return TargetDatabases{}, err
	}
	names := slices.DeleteFunc(e.names, isBekupTemporaryName)
	if pr.Role == auth.RoleUser {
		if names, err = a.o.Owners.BackedUpDatabases(ctx, pr.UserID, connectionID, names); err != nil {
			return TargetDatabases{}, err
		}
	}
	if names == nil {
		names = []string{}
	}
	return TargetDatabases{Target: *t, Databases: names, Warnings: e.warnings, Privileges: e.privileges, ListedAt: e.at}, nil
}

// probeTarget returns a connection's databases and privilege verdict from
// the cache or the runner (the per-user runner limiter applies to a fresh
// probe). fresh skips the cache: restore requests decide on a verdict read
// for the request (D-068).
func (a *API) probeTarget(ctx context.Context, pr auth.Principal, id uuid.UUID, fresh bool) (dbEntry, error) {
	now := a.svc.o.Now()
	a.mu.Lock()
	if e, ok := a.dbs[id]; ok && !fresh && now.Sub(e.at) < a.o.DatabasesTTL {
		a.mu.Unlock()
		return e.clone(), nil
	}
	a.mu.Unlock()
	actor := connections.Actor{UserID: pr.UserID, IP: auth.RequestMetaFrom(ctx).IP, AllowTemporary: pr.Role == auth.RoleAdmin}
	tp, err := a.o.Connections.ProbeTarget(ctx, actor, id)
	if err != nil {
		return dbEntry{}, err
	}
	names, warnings := tp.Databases, tp.Warnings
	if len(names) > maxListedDatabases {
		names = names[:maxListedDatabases]
		warnings = append(warnings, "database_list_truncated")
	}
	slices.Sort(names)
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.dbs) >= maxCacheEntries {
		evictOldest(a.dbs, func(e dbEntry) time.Time { return e.at })
	}
	e := dbEntry{names: names, warnings: warnings, privileges: tp.Privileges, at: now}
	a.dbs[id] = e.clone()
	return e, nil
}

// clone copies the slices of a cache entry.
func (e dbEntry) clone() dbEntry {
	e.names, e.warnings = slices.Clone(e.names), slices.Clone(e.warnings)
	e.privileges.Reasons = slices.Clone(e.privileges.Reasons)
	return e
}

// isBekupTemporaryName reports bekup's own temporary restore databases and
// the reserved test-restore scratch namespace (bekup_test_*, task 4.6).
func isBekupTemporaryName(name string) bool {
	return strings.Contains(name, "__bekup_restore_") || strings.Contains(name, "__bekup_old_") || engine.IsReservedScratchName(name)
}

// unknownTarget is the answer for a target the caller may not use or that
// does not exist: FORBIDDEN for backup owners (no probing), a field error
// for staff.
func (a *API) unknownTarget(ctx context.Context) error {
	pr, err := a.caller(ctx)
	if err != nil {
		return err
	}
	if pr.Role == auth.RoleUser {
		return errForbidden
	}
	return serviceutil.FieldErr("targetConnectionId", "not a restore target for this backup (unknown connection, another engine, or a temporary connection)")
}

// --- preview and request --------------------------------------------------------

// RequestInput is a database restore request (restoreArtifact,
// restorePreview). Identity is borrowed: the caller zeroes it.
type RequestInput struct {
	ArtifactID         uuid.UUID
	TargetConnectionID uuid.UUID
	Mode               Mode
	TargetDatabase     string
	// ConfirmTargetName must equal TargetDatabase for an overwrite
	// (restoreArtifact only).
	ConfirmTargetName string
	KeepOwners        bool
	Identity          []byte
	// AcknowledgePrivilegedTarget is the admin's acknowledgement that the
	// target role is privileged or its privileges are unknown (D-068;
	// restoreArtifact only).
	AcknowledgePrivilegedTarget bool
}

// Warning is one thing a user must know before a restore (stable code,
// fixed text).
type Warning struct {
	Code    string
	Message string
}

// Preview is what restorePreview answers: the warnings to show, the text
// to type for the confirmation, and whether an age identity is needed.
type Preview struct {
	Source   Source
	Target   connections.Connection
	Mode     Mode
	Database string
	Warnings []Warning
	// ConfirmText is what the user must type: the database name for an
	// overwrite, the globals phrase for a globals restore, "" otherwise.
	ConfirmText string
	// Privileges is the verdict on the target role (D-068). When Gated,
	// only admins may restore, with AcknowledgePrivilegedTarget.
	Privileges connections.Privileges
	// CrossSource: the target is not the connection the backup was taken
	// from (or the job was deleted).
	CrossSource bool
}

// plan is a checked and authorized request.
type plan struct {
	Preview
	keepOwners bool
}

// validateInput checks the syntax of a database restore request.
func validateInput(in *RequestInput) error {
	switch in.Mode {
	case ModeNewDB, ModeOverwrite:
	default:
		return serviceutil.FieldErr("mode", "unknown restore mode")
	}
	if in.ArtifactID == uuid.Nil {
		return serviceutil.FieldErr("artifactId", "required")
	}
	if in.TargetConnectionID == uuid.Nil {
		return serviceutil.FieldErr("targetConnectionId", "required")
	}
	if err := engine.ValidateDBName(in.TargetDatabase); err != nil {
		return serviceutil.FieldErr("targetDatabase", "1 to 63 characters: letters, digits, _ $ . - (not starting with - $ or .)")
	}
	if isBekupTemporaryName(in.TargetDatabase) {
		return serviceutil.FieldErr("targetDatabase", "names containing __bekup_restore_ or __bekup_old_ and names starting with bekup_test_ are reserved for bekup's temporary and test-restore databases")
	}
	return nil
}

// plan validates, authorizes (op with the artifact's job and the target)
// and checks a database restore request against the verified source.
func (a *API) plan(ctx context.Context, in *RequestInput, op authz.Operation) (*plan, error) {
	if err := validateInput(in); err != nil {
		return nil, err
	}
	row, err := a.authorizeArtifact(ctx, authz.OpRestoreTargetsList, in.ArtifactID)
	if err != nil {
		return nil, err
	}
	switch row.Artifact.Kind {
	case sqlc.ArtifactKindDatabase:
	case sqlc.ArtifactKindGlobals:
		return nil, serviceutil.FieldErr("artifactId", "this is a globals backup: use restoreGlobals (admins only)")
	default:
		return nil, serviceutil.FieldErr("artifactId", "manifest files cannot be restored")
	}
	target, err := a.o.Connections.GetForRestore(ctx, in.TargetConnectionID)
	if errors.Is(err, connections.ErrNotFound) {
		return nil, a.unknownTarget(ctx)
	}
	if err != nil {
		return nil, err
	}
	if err := a.checkTemporaryTTL(&target); err != nil {
		return nil, err
	}
	res := authz.RestoreTarget{
		ConnectionID: target.ID, Database: in.TargetDatabase, NewDatabase: in.Mode == ModeNewDB, Temporary: target.Temporary,
	}
	if err := a.o.Policy.Authorize(ctx, op, authz.Job{ID: row.Artifact.JobID}, res); err != nil {
		return nil, err
	}
	src, err := a.source(ctx, &row)
	if err != nil {
		return nil, err
	}
	if src.Engine != target.Engine {
		return nil, fmt.Errorf("%w: %w", ErrEngineMismatch, serviceutil.FieldErr("targetConnectionId",
			"the backup is a %s dump but the target runs %s (MySQL and MariaDB are different engines)", src.Engine, target.Engine))
	}
	if eng, err := engine.ForKind(target.Engine); err == nil && eng.IsSystemDatabase(in.TargetDatabase) {
		return nil, serviceutil.FieldErr("targetDatabase", "%s is a system database of %s and cannot be a restore target", in.TargetDatabase, target.Engine)
	}
	if in.KeepOwners && target.Engine != engine.KindPostgres {
		return nil, serviceutil.FieldErr("keepOwners", "only PostgreSQL restores can keep the object owners")
	}
	p := &plan{Preview: Preview{Source: src, Target: target, Mode: in.Mode, Database: in.TargetDatabase}, keepOwners: in.KeepOwners}
	if in.Mode == ModeOverwrite {
		p.ConfirmText = in.TargetDatabase
	}
	request := op == authz.OpRestoreArtifact
	if err := a.privileges(ctx, &p.Preview, request); err != nil {
		return nil, err
	}
	if request && p.Privileges.Gated() {
		// D-068: the target rules again with the privileged role (admins
		// only), then the acknowledgement.
		res.PrivilegedRole = true
		if err := a.o.Policy.Authorize(ctx, op, authz.Job{ID: row.Artifact.JobID}, res); err != nil {
			return nil, err
		}
		if err := acknowledged(in.AcknowledgePrivilegedTarget); err != nil {
			return nil, err
		}
	}
	p.Warnings = append(databaseWarnings(src, target, in), privilegeWarnings(p.Privileges)...)
	return p, nil
}

// checkTemporaryTTL refuses a temporary target that expires within
// workers.restore_timeout (D-173): a restore must be able to finish
// before its target's credentials are due for deletion. (A running
// restore holding its run slot keeps using an expired target, and the
// cleanup never deletes one while a slot is held.)
func (a *API) checkTemporaryTTL(target *connections.Connection) error {
	if !target.Temporary || a.o.RestoreTimeout <= 0 {
		return nil
	}
	if target.ExpiresAt == nil || target.ExpiresAt.Sub(a.svc.o.Now()) < a.o.RestoreTimeout {
		return serviceutil.FieldErr("targetConnectionId",
			"the temporary connection expires in less than the restore timeout (%s): extend it first", a.o.RestoreTimeout)
	}
	return nil
}

// Reasons of an unknown verdict the API sets itself (D-068).
const (
	// ReasonTargetNotReady: the target connection is not ready, so its
	// role could not be checked.
	ReasonTargetNotReady = "target_not_ready"
	// ReasonProbeFailed: the runner could not check the role (preview
	// only; a request returns the error).
	ReasonProbeFailed = "probe_failed"
)

// privileges reads the target role's privilege verdict into p (D-068): a
// request probes the target afresh, a preview may use the cached probe. A
// target that is not ready cannot be probed and is unknown; a preview
// whose probe fails shows unknown (security events and cancellations
// excepted), a request returns the error.
func (a *API) privileges(ctx context.Context, p *Preview, request bool) error {
	pr, err := a.caller(ctx)
	if err != nil {
		return err
	}
	p.CrossSource = p.Source.JobConnectionID == uuid.Nil || p.Target.ID != p.Source.JobConnectionID
	if !p.Target.Ready {
		p.Privileges = connections.Privileges{Status: connections.PrivilegeUnknown, Reasons: []string{ReasonTargetNotReady}}
		return nil
	}
	e, err := a.probeTarget(ctx, pr, p.Target.ID, request)
	var (
		pm *connections.PinMismatchError
		hk *connections.HostKeyMismatchError
	)
	switch {
	case err == nil:
		p.Privileges = e.privileges
	case request, ctx.Err() != nil, errors.As(err, &pm), errors.As(err, &hk):
		return err
	default:
		a.o.Logger.InfoContext(ctx, "restore preview: target probe failed", slog.String("target_connection_id", p.Target.ID.String()))
		p.Privileges = connections.Privileges{Status: connections.PrivilegeUnknown, Reasons: []string{ReasonProbeFailed}}
	}
	return nil
}

// acknowledged checks the admin's acknowledgement of a privileged or
// unknown target role (D-068; VALIDATION without it).
func acknowledged(ack bool) error {
	if !ack {
		return serviceutil.FieldErr("acknowledgePrivilegedTarget",
			"the target connection's role is privileged or its privileges are unknown: acknowledge that SQL in the backup runs with its rights")
	}
	return nil
}

// Preview checks a restore request (target rules, engine, names) without
// the fresh second factor and returns the warnings to show before the
// confirmation.
func (a *API) Preview(ctx context.Context, in RequestInput) (Preview, error) {
	p, err := a.plan(ctx, &in, authz.OpRestorePreview)
	if err != nil {
		return Preview{}, err
	}
	return p.Preview, nil
}

// Request queues a database restore (restoreArtifact): the request is
// checked and authorized (restore.artifact: the target rules and a fresh
// passkey/TOTP check, D-116), an overwrite must be confirmed by typing the
// database name, and the restore, its River job, the audit entry and the
// restore event are recorded in one transaction holding the target's
// share lock. A recipient-encrypted artifact needs the user's age
// identity, which only ever goes into the sealed handoff.
func (a *API) Request(ctx context.Context, in RequestInput) (sqlc.Restore, Preview, error) {
	if in.Mode == ModeOverwrite && in.ConfirmTargetName != in.TargetDatabase {
		return sqlc.Restore{}, Preview{}, serviceutil.FieldErr("confirmTargetName", "type the name of the database to overwrite exactly")
	}
	p, err := a.plan(ctx, &in, authz.OpRestoreArtifact)
	if err != nil {
		return sqlc.Restore{}, Preview{}, err
	}
	name := in.TargetDatabase
	priv := p.Privileges
	r, err := a.record(ctx, &p.Preview, CreateInput{
		ArtifactID: in.ArtifactID, TargetConnectionID: p.Target.ID, TargetDBName: &name, Mode: in.Mode,
		Identity: in.Identity, KeepOwners: p.keepOwners, Privileges: &priv, PrivilegedAck: priv.Gated() && in.AcknowledgePrivilegedTarget,
	})
	if err != nil {
		return sqlc.Restore{}, Preview{}, err
	}
	return r, p.Preview, nil
}

// GlobalsInput is a globals restore request (restoreGlobals).
type GlobalsInput struct {
	ArtifactID         uuid.UUID
	TargetConnectionID uuid.UUID
	// ConfirmPhrase must equal GlobalsConfirmPhrase(target name)
	// (restoreGlobals only).
	ConfirmPhrase string
	// AcknowledgePrivilegedTarget: see RequestInput (restoreGlobals only).
	AcknowledgePrivilegedTarget bool
}

// GlobalsConfirmPhrase is what an admin types to confirm a globals restore
// into the connection called name.
func GlobalsConfirmPhrase(name string) string { return "restore globals into " + name }

// planGlobals validates and authorizes a globals request (admins only:
// preview with the role, request with restore.globals incl. the fresh
// second factor).
func (a *API) planGlobals(ctx context.Context, in *GlobalsInput, request bool) (*Preview, error) {
	if in.ArtifactID == uuid.Nil || in.TargetConnectionID == uuid.Nil {
		return nil, serviceutil.FieldErr("artifactId", "artifactId and targetConnectionId are required")
	}
	if err := a.o.Policy.HasRole(ctx, auth.RoleAdmin); err != nil {
		return nil, err
	}
	row, err := a.loadArtifact(ctx, in.ArtifactID)
	if err != nil {
		return nil, err
	}
	if row.Artifact.Kind != sqlc.ArtifactKindGlobals {
		return nil, serviceutil.FieldErr("artifactId", "not a globals backup: use restoreArtifact")
	}
	target, err := a.o.Connections.GetForRestore(ctx, in.TargetConnectionID)
	if errors.Is(err, connections.ErrNotFound) {
		return nil, a.unknownTarget(ctx)
	}
	if err != nil {
		return nil, err
	}
	if request {
		if err := a.o.Policy.Authorize(ctx, authz.OpRestoreGlobals); err != nil {
			return nil, err
		}
	}
	if err := a.checkTemporaryTTL(&target); err != nil {
		return nil, err
	}
	src, err := a.source(ctx, &row)
	if err != nil {
		return nil, err
	}
	if src.Engine != engine.KindPostgres || target.Engine != engine.KindPostgres {
		return nil, fmt.Errorf("%w: %w", ErrEngineMismatch, serviceutil.FieldErr("targetConnectionId",
			"globals restores are PostgreSQL only (backup: %s, target: %s)", src.Engine, target.Engine))
	}
	p := &Preview{Source: src, Target: target, Mode: ModeOverwrite, ConfirmText: GlobalsConfirmPhrase(target.Name)}
	if err := a.privileges(ctx, p, request); err != nil {
		return nil, err
	}
	p.Warnings = append(globalsWarnings(src, target), privilegeWarnings(p.Privileges)...)
	return p, nil
}

// PreviewGlobals checks a globals restore request (admins) and returns the
// warnings and the confirmation phrase.
func (a *API) PreviewGlobals(ctx context.Context, in GlobalsInput) (Preview, error) {
	p, err := a.planGlobals(ctx, &in, false)
	if err != nil {
		return Preview{}, err
	}
	return *p, nil
}

// RequestGlobals queues a PostgreSQL globals restore (restoreGlobals:
// admins, fresh passkey/TOTP check, the exact confirmation phrase). It can
// change existing roles including their passwords (D-166).
func (a *API) RequestGlobals(ctx context.Context, in GlobalsInput) (sqlc.Restore, Preview, error) {
	p, err := a.planGlobals(ctx, &in, true)
	if err != nil {
		return sqlc.Restore{}, Preview{}, err
	}
	if in.ConfirmPhrase != p.ConfirmText {
		return sqlc.Restore{}, Preview{}, serviceutil.FieldErr("confirmPhrase", "type the confirmation phrase exactly: %s", p.ConfirmText)
	}
	if p.Privileges.Gated() {
		// restore.globals is admin only already (D-068: acknowledgement).
		if err := acknowledged(in.AcknowledgePrivilegedTarget); err != nil {
			return sqlc.Restore{}, Preview{}, err
		}
	}
	priv := p.Privileges
	r, err := a.record(ctx, p, CreateInput{
		ArtifactID: in.ArtifactID, TargetConnectionID: p.Target.ID, Mode: ModeOverwrite,
		Privileges: &priv, PrivilegedAck: priv.Gated() && in.AcknowledgePrivilegedTarget,
	})
	if err != nil {
		return sqlc.Restore{}, Preview{}, err
	}
	return r, *p, nil
}

// targetLockKey serializes requests for one target database (or, for
// globals, one connection) inside the insert transaction.
func targetLockKey(conn uuid.UUID, dbName *string) string {
	if dbName == nil {
		return "restore-target:" + conn.String() + ":globals"
	}
	return "restore-target:" + conn.String() + ":db:" + *dbName
}

// record inserts the restore, its River job, the audit entry and the
// event in one transaction under the target's share lock.
func (a *API) record(ctx context.Context, p *Preview, in CreateInput) (sqlc.Restore, error) {
	pr, err := a.caller(ctx)
	if err != nil {
		return sqlc.Restore{}, err
	}
	ins := a.o.Queue()
	if ins == nil {
		return sqlc.Restore{}, errors.New("restore: no queue configured")
	}
	in.RequestedBy = pr.UserID
	var r sqlc.Restore
	err = db.TxWith(ctx, a.svc.o.DB, func(tx pgx.Tx, q *sqlc.Queries) error {
		conn, err := a.o.Connections.LockRestoreTargetTx(ctx, q, in.TargetConnectionID)
		if errors.Is(err, connections.ErrNotFound) {
			return serviceutil.FieldErr("targetConnectionId", "the target connection was deleted or has expired")
		}
		if err != nil {
			return err
		}
		if conn.Engine != p.Target.Engine || conn.Temporary != p.Target.Temporary {
			return ErrTargetChanged
		}
		if err := q.LockRestoreTargetName(ctx, targetLockKey(conn.ID, in.TargetDBName)); err != nil {
			return fmt.Errorf("restore: lock the target name: %w", err)
		}
		busy, err := q.ActiveRestoreForTarget(ctx, sqlc.ActiveRestoreForTargetParams{TargetConnectionID: conn.ID, TargetDbName: in.TargetDBName})
		if err != nil {
			return fmt.Errorf("restore: active restores: %w", err)
		}
		if busy {
			return ErrTargetBusy
		}
		if r, err = a.svc.CreateTx(ctx, q, in); err != nil {
			return err
		}
		if err := a.svc.EnqueueTx(ctx, tx, q, ins, r.ID); err != nil {
			return err
		}
		if r, err = q.GetRestore(ctx, r.ID); err != nil {
			return fmt.Errorf("restore: reload restore: %w", err)
		}
		codes := make([]string, 0, len(p.Warnings))
		for _, w := range p.Warnings {
			codes = append(codes, w.Code)
		}
		d := audit.Details{
			"job_id": r.JobID.String(), "artifact_id": r.ArtifactID.String(), "run_id": p.Source.RunID.String(),
			"kind": string(p.Source.Kind), "source_engine": string(p.Source.Engine), "source_version": p.Source.VersionKey,
			"target_connection_id": conn.ID.String(), "target_connection": conn.Name, "temporary_target": conn.Temporary,
			"original_target": p.Source.JobConnectionID == conn.ID, "mode": string(r.Mode), "no_owner": r.NoOwner,
			"warnings": codes, "identity_supplied": len(in.Identity) > 0, "cross_source": p.CrossSource,
			"target_privileges": string(p.Privileges.Status), "target_privilege_reasons": slices.Clone(p.Privileges.Reasons),
			"privileged_target_acknowledged": p.Privileges.Gated(),
		}
		if p.Source.Database != "" {
			d["source_database"] = p.Source.Database
		}
		if r.TargetDbName != nil {
			d["target_database"] = *r.TargetDbName
		}
		if _, err := a.svc.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(pr.UserID), Action: audit.ActionRestoreRequested, TargetType: "restore",
			TargetID: r.ID.String(), IP: auth.RequestMetaFrom(ctx).IP, Details: d,
		}); err != nil {
			return err
		}
		return a.svc.publish(ctx, q, EventRestore, r.ID)
	})
	if err != nil {
		return sqlc.Restore{}, err
	}
	if p.Privileges.Gated() {
		// An acknowledged restore into a privileged (or unknown) target
		// role is a security event (D-068; delivered by task 5.5).
		a.svc.o.Alert(ctx, Alert{Kind: AlertPrivilegedTarget, RestoreID: r.ID, JobID: r.JobID, ArtifactID: r.ArtifactID,
			Details: map[string]any{
				"target_connection_id": r.TargetConnectionID.String(), "target_privileges": string(p.Privileges.Status),
				"reasons": slices.Clone(p.Privileges.Reasons), "cross_source": p.CrossSource, "kind": string(p.Source.Kind),
			}})
	}
	return r, nil
}

// --- warnings -------------------------------------------------------------------

// Warning codes of restorePreview / restoreArtifact.
const (
	WarnOverwriteMySQL     = "overwrite_in_place"
	WarnOverwritePostgres  = "overwrite_swap"
	WarnNewDatabasePartial = "new_database_partial_on_failure"
	WarnClientVersion      = "client_version"
	WarnCrossSource        = "cross_source_target"
	WarnTemporaryTarget    = "temporary_target"
	WarnKeepOwners         = "keep_owners"
	WarnTargetNotReady     = "target_not_ready"
	WarnInsecureTransport  = "insecure_transport"
	WarnGlobalsRoles       = "globals_change_roles"
	WarnIdentityRequired   = "identity_required"
	WarnPrivilegedTarget   = "privileged_target"
	WarnRestoreHardening   = "restore_hardening"
)

// privilegeWarnings explains a privileged or unknown target role (D-068).
func privilegeWarnings(p connections.Privileges) []Warning {
	reasons := strings.Join(p.Reasons, ", ")
	switch p.Status {
	case connections.PrivilegeNotPrivileged:
		return nil
	case connections.PrivilegePrivileged:
		return []Warning{{WarnPrivilegedTarget, "The target connection's role is privileged (" + reasons + "). SQL in a dump runs " +
			"with that role's rights, so a malicious dump could take over the target server (run programs, read or write server " +
			"files, change accounts). Only admins can restore into it, with an explicit acknowledgement; use a dedicated " +
			"non-superuser restore role where possible."}}
	default:
		return []Warning{{WarnPrivilegedTarget, "bekup could not determine whether the target connection's role is privileged (" +
			reasons + "), so it is treated as privileged: SQL in a dump runs with that role's rights. Only admins can restore " +
			"into it, with an explicit acknowledgement."}}
	}
}

func databaseWarnings(src Source, target connections.Connection, in *RequestInput) []Warning {
	var ws []Warning
	switch {
	case in.Mode == ModeOverwrite && target.Engine != engine.KindPostgres:
		ws = append(ws, Warning{WarnOverwriteMySQL, MySQLOverwriteWarning})
	case in.Mode == ModeOverwrite:
		ws = append(ws, Warning{WarnOverwritePostgres, fmt.Sprintf("The backup is restored into a temporary database first and swapped in "+
			"by renaming once it restored completely: %s is replaced as a whole, its open sessions are terminated at the swap, and the "+
			"server needs room for both copies until the old database is dropped. If anything fails before the swap, %s stays unchanged.",
			in.TargetDatabase, in.TargetDatabase)})
	default:
		ws = append(ws, Warning{WarnNewDatabasePartial, fmt.Sprintf("The database %s is created for this restore. If the restore fails, "+
			"the partially restored database is left on the server; drop it before retrying.", in.TargetDatabase)})
	}
	if target.Engine == engine.KindPostgres {
		ws = append(ws, Warning{WarnRestoreHardening, fmt.Sprintf("bekup hardens the restored database before it becomes %s: "+
			"SECURITY DEFINER functions and procedures are switched to SECURITY INVOKER (otherwise they would run with the target "+
			"connection role's rights for every caller) and PUBLIC gets no CONNECT or TEMPORARY on it. Grant CONNECT to the roles that "+
			"need the database after the restore, and re-enable SECURITY DEFINER only for functions you reviewed.", in.TargetDatabase)})
	}
	ws = append(ws, commonWarnings(src, target)...)
	if in.KeepOwners {
		ws = append(ws, Warning{WarnKeepOwners, "Object owners are restored: objects are assigned to the roles of the same names on the " +
			"target server (which may not be the roles you expect). Without this option they belong to the target connection's role."})
	}
	return ws
}

func globalsWarnings(src Source, target connections.Connection) []Warning {
	ws := make([]Warning, 0, 8)
	ws = append(ws, Warning{WarnGlobalsRoles, "A globals restore runs the roles and tablespaces of the backup on the whole PostgreSQL server: " +
		"roles in the backup that already exist are changed, including their passwords, attributes and memberships; new roles are " +
		"created. This affects every database on the server and cannot be undone by bekup."})
	return append(ws, commonWarnings(src, target)...)
}

func commonWarnings(src Source, target connections.Connection) []Warning {
	ws := []Warning{{WarnClientVersion, fmt.Sprintf("The backup was taken from a %s %s server. The runner restores with the newer of "+
		"that client version and the target server's; restoring into an older server than the backup's may fail on features it lacks.",
		src.Engine, src.VersionKey)}}
	if src.JobConnectionID == uuid.Nil || target.ID != src.JobConnectionID {
		ws = append(ws, Warning{WarnCrossSource, "The target is not the connection this backup was taken from. SQL in a dump runs with " +
			"the target connection role's privileges: only restore dumps whose source you trust."})
	}
	if target.Temporary {
		ws = append(ws, Warning{WarnTemporaryTarget, "The target is a temporary restore connection; it and its credentials are deleted " +
			"after the restore finished or when it expires."})
	}
	if !target.Ready {
		ws = append(ws, Warning{WarnTargetNotReady, "The target connection is not ready (" + target.NotReadyReason + "); the restore will fail until it is."})
	}
	if target.InsecureTransport {
		ws = append(ws, Warning{WarnInsecureTransport, "The connection to the target server is not TLS-pinned; the restored data travels without a verified server identity."})
	}
	if src.NeedsIdentity() {
		ws = append(ws, Warning{WarnIdentityRequired, "This backup is encrypted to an age recipient: the restore needs its private key " +
			"(AGE-SECRET-KEY-1...). bekup keeps it only sealed until the restore starts."})
	}
	return ws
}

// --- cancel ---------------------------------------------------------------------

// Cancellation is an accepted cancelRestore.
type Cancellation struct {
	RestoreID   uuid.UUID
	JobID       uuid.UUID
	Status      Status
	RequestedAt time.Time
}

// Cancel cancels a queued or running restore (restore.cancel: admins and
// maintainers). A restore whose River job is not running is finished as
// canceled at once (its identity handoff deleted); a running one is
// stopped by its worker: the runner kills the restore container, and an
// overwritten PostgreSQL database is left unchanged unless the swap
// already happened.
func (a *API) Cancel(ctx context.Context, id uuid.UUID) (Cancellation, error) {
	pr, err := a.caller(ctx)
	if err != nil {
		return Cancellation{}, err
	}
	if err := a.o.Policy.Authorize(ctx, authz.OpRestoreCancel); err != nil {
		return Cancellation{}, err
	}
	c := a.o.Canceler()
	if c == nil {
		return Cancellation{}, errors.New("restore: no queue configured")
	}
	now := a.svc.o.Now().UTC()
	var out Cancellation
	err = db.TxWith(ctx, a.svc.o.DB, func(tx pgx.Tx, q *sqlc.Queries) error {
		r, err := q.LockRestore(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("restore: lock restore: %w", err)
		}
		if isTerminal(r.Status) {
			return ErrFinished
		}
		state := rivertype.JobState("")
		if r.RiverJobID != nil {
			state, err = c.CancelTx(ctx, tx, *r.RiverJobID)
			if err != nil && !errors.Is(err, queue.ErrJobNotFound) {
				return err
			}
		}
		out = Cancellation{RestoreID: r.ID, JobID: r.JobID, Status: r.Status, RequestedAt: now}
		if state != rivertype.JobStateRunning {
			// Nobody works on it: finish it here.
			if _, err := a.svc.finishTx(ctx, q, r.ID, StatusCanceled, context.Canceled, Outcome{}); err != nil {
				return err
			}
			if _, err := q.DeleteRestoreIdentityHandoff(ctx, r.ID); err != nil {
				return fmt.Errorf("restore: delete identity handoff: %w", err)
			}
			out.Status = StatusCanceled
		}
		_, err = a.svc.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(pr.UserID), Action: audit.ActionRestoreCancelRequested, TargetType: "restore",
			TargetID: r.ID.String(), IP: auth.RequestMetaFrom(ctx).IP,
			Details: audit.Details{"job_id": r.JobID.String(), "status": string(r.Status), "river_state": string(state)},
		})
		return err
	})
	if err != nil {
		return Cancellation{}, err
	}
	return out, nil
}

func isTerminal(s Status) bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusCanceled
}

// outcomeMessages are the texts of the warning codes a finished restore
// records (restores.warnings).
var outcomeMessages = map[string]string{
	WarnOldDatabaseKept:    "The replaced database could not be dropped after the swap; it is kept under a __bekup_old_ name. Drop it when no longer needed.",
	WarnTempDatabaseKept:   "The temporary restore database could not be dropped; it is kept under a __bekup_restore_ name. Drop it manually.",
	WarnPartialNewDatabase: "The restore failed after the new database was created; the partially restored database was left on the server.",
	WarnSecdefNeutralized: "SECURITY DEFINER functions or procedures of the backup were switched to SECURITY INVOKER (they would have run " +
		"with the restore role's rights for every caller); the restore log lists them. An admin may re-enable SECURITY DEFINER " +
		"deliberately after reviewing them.",
}

// OutcomeWarningMessage is the fixed text of a recorded warning code.
func OutcomeWarningMessage(code string) string {
	if m, ok := outcomeMessages[code]; ok {
		return m
	}
	return "Warning: " + code + "."
}
