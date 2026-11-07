package riskaccept

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// Duration is how long an acceptance lasts (the risk_duration enum).
type Duration string

// Durations.
const (
	DurationMonth   Duration = "month"
	DurationYear    Duration = "year"
	DurationForever Duration = "forever"
)

// Valid reports whether d is a known duration.
func (d Duration) Valid() bool {
	return d == DurationMonth || d == DurationYear || d == DurationForever
}

// EndReason is why an acceptance ended (the risk_end_reason enum).
type EndReason string

// End reasons.
const (
	EndRevoked            EndReason = "revoked"
	EndSuperseded         EndReason = "superseded"
	EndExpired            EndReason = "expired"
	EndConditionChanged   EndReason = "condition_changed"
	EndResolved           EndReason = "resolved"
	EndSubjectDeleted     EndReason = "subject_deleted"
	EndAcceptorLostRights EndReason = "acceptor_lost_rights"
)

// Status is an acceptance's state for readers.
type Status string

// Statuses.
const (
	StatusActive  Status = "active"
	StatusExpired Status = "expired"
	StatusRevoked Status = "revoked"
	StatusEnded   Status = "ended"
)

// Limits.
const (
	// MaxReasonRunes is risk_acceptances_reason_length.
	MaxReasonRunes = 500
	// MaxListRows bounds a listing (newest first).
	MaxListRows = 500
	// WarnBefore is when the "expires soon" alert goes out.
	WarnBefore = 7 * 24 * time.Hour
	// AuditTargetType is the audit target type of the entries.
	AuditTargetType = "risk_acceptance"
)

// Field names of the accept input (VALIDATION errors).
const (
	FieldKind        = "kind"
	FieldSubject     = "subject"
	FieldConditionID = "conditionId"
	FieldDuration    = "duration"
	FieldReason      = "reason"
)

// Errors (the GraphQL layer maps them).
var (
	// ErrConditionChanged: the condition differs from the one the caller
	// was shown (CONFLICT): review it again.
	ErrConditionChanged = errors.New("riskaccept: the condition changed")
	// ErrNotARisk: the subject carries no acceptable risk now
	// (VALIDATION).
	ErrNotARisk = errors.New("riskaccept: nothing to accept")
	// ErrNotFound: no such acceptance (NOT_FOUND, admins only learn it).
	ErrNotFound = errors.New("riskaccept: acceptance not found")
	// ErrEnded: the acceptance is no longer active (CONFLICT).
	ErrEnded = errors.New("riskaccept: acceptance already ended")
	// ErrSMTPUnavailable: the process has no SMTP settings service (a
	// worker): the SMTP risk is neither listed nor swept there.
	ErrSMTPUnavailable = errors.New("riskaccept: SMTP settings unavailable")
	// ErrForbidden: a non-admin asked for a kind without a job they own.
	ErrForbidden = errors.New("riskaccept: not allowed")
)

// Authorizer is the central policy (*authz.Policy).
type Authorizer interface {
	Authorize(ctx context.Context, op authz.Operation, res ...authz.Resource) error
	JobScope(ctx context.Context) (authz.Scope, uuid.UUID, error)
}

// Auditor records the entries in the change's transaction (*audit.Auditor
// with the notify.ObserveAudit observer, which raises the alerts).
type Auditor interface {
	RecordTx(ctx context.Context, q *sqlc.Queries, ev audit.Event) (audit.Entry, error)
}

// DB is the app pool.
type DB interface {
	db.TxBeginner
	sqlc.DBTX
}

// SMTPFacts are the stored SMTP settings that decide the SMTP risk.
type SMTPFacts struct {
	// Active: settings are stored and sending is enabled.
	Active   bool
	Mode     string
	Host     string
	Port     int
	Username string
}

// Options configure New.
type Options struct {
	DB         DB
	Auditor    Auditor
	Authorizer Authorizer
	// Posture returns this process's weakened instance conditions
	// (InsecureTransport, InsecureDBTLS, InsecureDBRoles, UnpinnedImage);
	// nil: none.
	Posture func() []Condition
	// SMTP reads the stored SMTP settings; nil: the SMTP risk is not
	// tracked.
	SMTP func(ctx context.Context) (SMTPFacts, error)
	// Now overrides the clock (tests).
	Now    func() time.Time
	Logger *slog.Logger
}

// Service is the only reader and writer of risk_acceptances (D-228).
type Service struct {
	o Options
}

// New validates o.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Auditor == nil || o.Authorizer == nil {
		return nil, errors.New("riskaccept: DB, Auditor and Authorizer are required")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &Service{o: o}, nil
}

// Person is who accepted or ended an acceptance.
type Person struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	Deleted     bool
}

// Acceptance is an acceptance as the caller may see it.
type Acceptance struct {
	ID           uuid.UUID
	Kind         Kind
	ConnectionID uuid.UUID
	JobID        uuid.UUID
	// SubjectName is the connection's or job's name ("" for instance kinds).
	SubjectName    string
	SubjectDeleted bool
	Condition      string
	Duration       Duration
	Reason         string
	// CreatedBy is set for staff and for the acceptor themselves; the
	// e-mail address only for admins.
	CreatedBy       *Person
	CreatedByCaller bool
	CreatedAt       time.Time
	// ExpiresAt is nil for forever.
	ExpiresAt *time.Time
	EndedAt   *time.Time
	EndReason EndReason
	Status    Status
}

// Finding is an acceptable risk that exists now, with the acceptance that
// covers it (nil: open).
type Finding struct {
	Condition   Condition
	SubjectName string
	Acceptance  *Acceptance
	// CanAccept: the caller may accept (or replace the acceptance of)
	// this risk.
	CanAccept bool
}

// live is a live acceptance row and whether it covers anything now.
type live struct {
	row    sqlc.ListLiveRiskAcceptancesRow
	covers bool
}

func (s *Service) loadLive(ctx context.Context, q sqlc.DBTX) (map[key]live, error) {
	rows, err := sqlc.New(q).ListLiveRiskAcceptances(ctx)
	if err != nil {
		return nil, fmt.Errorf("riskaccept: list acceptances: %w", err)
	}
	out := make(map[key]live, len(rows))
	for i := range rows {
		r := &rows[i]
		k := key{kind: Kind(r.Kind)}
		if r.ConnectionID != nil {
			k.conn = *r.ConnectionID
		}
		if r.JobID != nil {
			k.job = *r.JobID
		}
		out[k] = live{row: *r, covers: r.Unexpired && r.AcceptorAllowed}
	}
	return out, nil
}

// covering returns the live acceptance that covers c now (nil: none).
func covering(lv map[key]live, c Condition) *sqlc.ListLiveRiskAcceptancesRow {
	l, ok := lv[c.key()]
	if !ok || !l.covers || len(l.row.ConditionHash) != 32 || [32]byte(l.row.ConditionHash) != c.Hash {
		return nil
	}
	return &l.row
}

// Covered returns, for each condition an active acceptance covers now, the
// acceptance id (keyed by Condition.ID). An acceptance covers a condition
// when it has not ended or expired, its acceptor still holds the right
// and the condition is exactly the accepted one. A lookup error covers
// nothing (fail closed: the risk stays flagged).
func (s *Service) Covered(ctx context.Context, conds []Condition) map[string]Coverage {
	out := map[string]Coverage{}
	if len(conds) == 0 {
		return out
	}
	lv, err := s.loadLive(ctx, s.o.DB)
	if err != nil {
		s.o.Logger.WarnContext(ctx, "risk acceptances unavailable; risks stay flagged", slog.String("error", err.Error()))
		return out
	}
	for _, c := range conds {
		if r := covering(lv, c); r != nil {
			out[c.ID()] = Coverage{AcceptanceID: r.ID, ExpiresAt: r.ExpiresAt}
		}
	}
	return out
}

// Coverage is the acceptance that covers a condition.
type Coverage struct {
	AcceptanceID uuid.UUID
	ExpiresAt    *time.Time
}

// AcceptedPosture returns the posture kinds (notify kind names) whose
// current condition in this process an active acceptance covers.
func (s *Service) AcceptedPosture(ctx context.Context) map[string]Coverage {
	out := map[string]Coverage{}
	if s.o.Posture == nil {
		return out
	}
	conds := s.o.Posture()
	cov := s.Covered(ctx, conds)
	for _, c := range conds {
		if v, ok := cov[c.ID()]; ok {
			out[string(c.Kind)] = v
		}
	}
	return out
}

// PostureCoverage is AcceptedPosture for one kind (startup audits).
func (s *Service) PostureCoverage(ctx context.Context, kind string) (Coverage, bool) {
	v, ok := s.AcceptedPosture(ctx)[kind]
	return v, ok
}

// caller returns the principal or the policy's answer for anonymous calls.
func (s *Service) caller(ctx context.Context, op authz.Operation) (auth.Principal, error) {
	pr, ok := auth.FromContext(ctx)
	if !ok {
		if err := s.o.Authorizer.Authorize(ctx, op); err != nil {
			return auth.Principal{}, err
		}
		return auth.Principal{}, errors.New("riskaccept: no principal")
	}
	return pr, nil
}

// authorizeKind authorizes op for a kind and its subject: job kinds with
// the job (owners may), every other kind admins only (the policy refuses
// owners without a job; refused here too, defense in depth).
func (s *Service) authorizeKind(ctx context.Context, pr auth.Principal, op authz.Operation, kind Kind, job uuid.UUID) error {
	if kind.OnJob() {
		return s.o.Authorizer.Authorize(ctx, op, authz.Job{ID: job})
	}
	if err := s.o.Authorizer.Authorize(ctx, op); err != nil {
		return err
	}
	if pr.Role != auth.RoleAdmin {
		return ErrForbidden
	}
	return nil
}

// AcceptInput is a new acceptance.
type AcceptInput struct {
	Kind         Kind
	ConnectionID uuid.UUID
	JobID        uuid.UUID
	// ConditionID is the Condition.ID the caller was shown.
	ConditionID string
	Duration    Duration
	Reason      string
}

// Accept accepts the current condition of a subject (risk.accept, fresh
// step-up). The condition is rebuilt from the database (or this process's
// configuration) under a share lock of the subject and must be the one the
// caller was shown. A live acceptance of the same kind and subject is
// superseded. Audited fail closed (risk.accepted; the observer raises the
// alert).
func (s *Service) Accept(ctx context.Context, in AcceptInput) (Acceptance, error) {
	pr, err := s.caller(ctx, authz.OpRiskAccept)
	if err != nil {
		return Acceptance{}, err
	}
	if !in.Kind.Valid() {
		return Acceptance{}, serviceutil.FieldErr(FieldKind, "unknown risk kind")
	}
	if err := checkSubject(in.Kind, in.ConnectionID, in.JobID); err != nil {
		return Acceptance{}, err
	}
	if err := s.authorizeKind(ctx, pr, authz.OpRiskAccept, in.Kind, in.JobID); err != nil {
		return Acceptance{}, err
	}
	if !in.Duration.Valid() {
		return Acceptance{}, serviceutil.FieldErr(FieldDuration, "choose one month, one year or forever")
	}
	reason, err := normalizeReason(in.Reason)
	if err != nil {
		return Acceptance{}, err
	}
	want, err := hex.DecodeString(in.ConditionID)
	if err != nil || len(want) != 32 {
		return Acceptance{}, serviceutil.FieldErr(FieldConditionID, "unknown condition")
	}
	var smtp *SMTPFacts
	if in.Kind == KindSMTPUnencrypted {
		if s.o.SMTP == nil {
			return Acceptance{}, ErrNotARisk
		}
		f, err := s.o.SMTP(ctx)
		if errors.Is(err, ErrSMTPUnavailable) {
			return Acceptance{}, ErrNotARisk
		}
		if err != nil {
			return Acceptance{}, fmt.Errorf("riskaccept: read SMTP settings: %w", err)
		}
		smtp = &f
	}
	var out Acceptance
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		cond, name, err := s.current(ctx, q, in.Kind, in.ConnectionID, in.JobID, smtp)
		if err != nil {
			return err
		}
		if [32]byte(want) != cond.Hash {
			return ErrConditionChanged
		}
		superseded := ""
		old, err := q.LockLiveRiskAcceptance(ctx, sqlc.LockLiveRiskAcceptanceParams{
			Kind: sqlc.RiskKind(in.Kind), ConnectionID: optID(in.ConnectionID), JobID: optID(in.JobID),
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("riskaccept: lock acceptance: %w", err)
		default:
			if _, err := q.EndRiskAcceptance(ctx, sqlc.EndRiskAcceptanceParams{
				ID: old.ID, EndedBy: &pr.UserID, EndReason: sqlc.RiskEndReason(EndSuperseded),
			}); err != nil {
				return fmt.Errorf("riskaccept: supersede acceptance: %w", err)
			}
			superseded = old.ID.String()
		}
		ins, err := q.InsertRiskAcceptance(ctx, sqlc.InsertRiskAcceptanceParams{
			Kind: sqlc.RiskKind(in.Kind), ConnectionID: optID(in.ConnectionID), JobID: optID(in.JobID),
			ConditionHash: cond.Hash[:], Condition: cond.Display, Duration: sqlc.RiskDuration(in.Duration),
			Reason: reason, CreatedBy: pr.UserID,
		})
		if serviceutil.IsUniqueViolation(err) {
			// A concurrent acceptance of the same risk won.
			return ErrConditionChanged
		}
		if err != nil {
			return fmt.Errorf("riskaccept: insert acceptance: %w", err)
		}
		d := details(ins.ID, in.Kind, in.ConnectionID, in.JobID, cond.Display, in.Duration, ins.ExpiresAt, pr.UserID)
		d["note"] = reason
		d["superseded_id"] = superseded
		if err := s.record(ctx, q, audit.UserActor(pr.UserID), audit.ActionRiskAccepted, ins.ID, d); err != nil {
			return err
		}
		out = Acceptance{
			ID: ins.ID, Kind: in.Kind, ConnectionID: in.ConnectionID, JobID: in.JobID, SubjectName: name,
			Condition: cond.Display, Duration: in.Duration, Reason: reason, CreatedByCaller: true,
			CreatedBy: &Person{ID: pr.UserID}, CreatedAt: ins.CreatedAt, ExpiresAt: ins.ExpiresAt, Status: StatusActive,
		}
		return nil
	})
	if err != nil {
		return Acceptance{}, err
	}
	s.o.Logger.InfoContext(ctx, "risk accepted", slog.String("risk_acceptance_id", out.ID.String()),
		slog.String("kind", string(out.Kind)), slog.String("duration", string(out.Duration)))
	return out, nil
}

// current rebuilds the condition of a subject now (locking connection and
// job subjects FOR SHARE in q's transaction). ErrNotARisk when there is
// none.
func (s *Service) current(ctx context.Context, q *sqlc.Queries, kind Kind, conn, job uuid.UUID, smtp *SMTPFacts) (Condition, string, error) {
	switch {
	case kind.OnConnection():
		r, err := q.LockRiskConnection(ctx, conn)
		if errors.Is(err, pgx.ErrNoRows) {
			return Condition{}, "", ErrNotARisk
		}
		if err != nil {
			return Condition{}, "", fmt.Errorf("riskaccept: lock connection: %w", err)
		}
		c, ok := connectionCondition(kind, connRow{
			id: r.ID, mode: r.TlsMode, host: r.Host, port: int(r.Port), user: r.Username,
			ssh: r.SshEnabled, sshHost: r.SshHost, sshPort: r.SshPort, sshUser: r.SshUser,
			priv: r.LastTestPrivileges, reasons: r.LastTestPrivilegeReasons,
		})
		if !ok {
			return Condition{}, "", ErrNotARisk
		}
		return c, r.Name, nil
	case kind.OnJob():
		r, err := q.LockRiskJob(ctx, job)
		if errors.Is(err, pgx.ErrNoRows) {
			return Condition{}, "", ErrNotARisk
		}
		if err != nil {
			return Condition{}, "", fmt.Errorf("riskaccept: lock job: %w", err)
		}
		c, ok := EncryptionOptOut(r.ID, r.OptedOutAt)
		if !ok {
			return Condition{}, "", ErrNotARisk
		}
		return c, r.Name, nil
	case kind == KindSMTPUnencrypted:
		if smtp == nil || !smtp.Active {
			return Condition{}, "", ErrNotARisk
		}
		c, ok := SMTPUnencrypted(smtp.Mode, smtp.Host, smtp.Port, smtp.Username)
		if !ok {
			return Condition{}, "", ErrNotARisk
		}
		return c, "", nil
	default:
		if s.o.Posture != nil {
			for _, c := range s.o.Posture() {
				if c.Kind == kind {
					return c, "", nil
				}
			}
		}
		return Condition{}, "", ErrNotARisk
	}
}

// connectionCondition builds a connection kind's condition from its row.
// connRow is a connection as the risk reads see it.
type connRow struct {
	id               uuid.UUID
	mode, host, user string
	port             int
	ssh              bool
	sshHost, sshUser *string
	sshPort          *int32
	priv             *string
	reasons          []string
}

func connectionCondition(kind Kind, r connRow) (Condition, bool) {
	sshPort := 0
	if r.sshPort != nil {
		sshPort = int(*r.sshPort)
	}
	route := Route(r.ssh, deStr(r.sshUser), deStr(r.sshHost), sshPort)
	if kind == KindConnectionTLS {
		return ConnectionTLS(r.id, r.mode, r.host, r.port, route)
	}
	if r.priv == nil {
		return Condition{}, false
	}
	return PrivilegedRole(r.id, *r.priv, r.reasons, r.host, r.port, r.user, route)
}

func deStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Revoke ends an active acceptance (risk.revoke, fresh step-up): the risk
// is flagged again. Owners may revoke the acceptances of a job they own,
// whoever made them.
func (s *Service) Revoke(ctx context.Context, id uuid.UUID) (Acceptance, error) {
	pr, err := s.caller(ctx, authz.OpRiskRevoke)
	if err != nil {
		return Acceptance{}, err
	}
	q := sqlc.New(s.o.DB)
	r, err := q.GetRiskAcceptance(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := s.o.Authorizer.Authorize(ctx, authz.OpRiskRevoke); err != nil {
			return Acceptance{}, err
		}
		if pr.Role != auth.RoleAdmin {
			return Acceptance{}, ErrForbidden
		}
		return Acceptance{}, ErrNotFound
	}
	if err != nil {
		return Acceptance{}, fmt.Errorf("riskaccept: read acceptance: %w", err)
	}
	if err := s.authorizeKind(ctx, pr, authz.OpRiskRevoke, Kind(r.Kind), deref(r.JobID)); err != nil {
		return Acceptance{}, err
	}
	if !r.Active {
		return Acceptance{}, ErrEnded
	}
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		e, err := q.EndRiskAcceptance(ctx, sqlc.EndRiskAcceptanceParams{ID: id, EndedBy: &pr.UserID, EndReason: sqlc.RiskEndReason(EndRevoked)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrEnded
		}
		if err != nil {
			return fmt.Errorf("riskaccept: revoke acceptance: %w", err)
		}
		return s.record(ctx, q, audit.UserActor(pr.UserID), audit.ActionRiskAcceptanceRevoked, id, endDetails(e))
	})
	if err != nil {
		return Acceptance{}, err
	}
	views, err := s.views(ctx, sqlc.New(s.o.DB), pr, []acceptRow{rowOfGet(r)})
	if err != nil {
		return Acceptance{}, err
	}
	v := views[0]
	now := s.o.Now()
	v.EndedAt, v.EndReason, v.Status = &now, EndRevoked, StatusRevoked
	return v, nil
}

// ListFilter selects acceptances.
type ListFilter string

// List filters.
const (
	ListActive ListFilter = "active"
	ListEnded  ListFilter = "ended"
	ListAll    ListFilter = "all"
)

// List returns the acceptances the caller may see, newest first (at most
// first, ≤ MaxListRows): staff every one, backup owners those on jobs they
// own (risk.list).
func (s *Service) List(ctx context.Context, f ListFilter, first int) ([]Acceptance, error) {
	pr, err := s.caller(ctx, authz.OpRiskList)
	if err != nil {
		return nil, err
	}
	if err := s.o.Authorizer.Authorize(ctx, authz.OpRiskList); err != nil {
		return nil, err
	}
	scope, user, err := s.o.Authorizer.JobScope(ctx)
	if err != nil {
		return nil, err
	}
	if first <= 0 || first > MaxListRows {
		first = MaxListRows
	}
	p := sqlc.ListRiskAcceptancesParams{LiveOnly: f == ListActive, EndedOnly: f == ListEnded, MaxRows: int32(first)}
	if scope != authz.ScopeAll {
		p.OwnerID = &user
	}
	q := sqlc.New(s.o.DB)
	rows, err := q.ListRiskAcceptances(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("riskaccept: list acceptances: %w", err)
	}
	ar := make([]acceptRow, 0, len(rows))
	for i := range rows {
		// Instance and SMTP acceptances are admin-only (like the facts).
		if Kind(rows[i].Kind).Instance() && pr.Role != auth.RoleAdmin {
			continue
		}
		ar = append(ar, acceptRow(rows[i]))
	}
	return s.views(ctx, q, pr, ar)
}

// Findings returns every acceptable risk that exists now and the caller
// may see (risk.list): staff all kinds, backup owners the encryption
// opt-out of jobs they own. Each carries the acceptance covering it.
func (s *Service) Findings(ctx context.Context) ([]Finding, error) {
	pr, err := s.caller(ctx, authz.OpRiskList)
	if err != nil {
		return nil, err
	}
	if err := s.o.Authorizer.Authorize(ctx, authz.OpRiskList); err != nil {
		return nil, err
	}
	scope, user, err := s.o.Authorizer.JobScope(ctx)
	if err != nil {
		return nil, err
	}
	staff := scope == authz.ScopeAll
	q := sqlc.New(s.o.DB)
	type cand struct {
		c    Condition
		name string
	}
	var cands []cand
	admin := pr.Role == auth.RoleAdmin
	if staff {
		// Instance and SMTP facts are admin-only elsewhere (config.status_view,
		// smtp.view): maintainers see connection and job risks only.
		if admin && s.o.Posture != nil {
			for _, c := range s.o.Posture() {
				cands = append(cands, cand{c: c})
			}
		}
		if admin && s.o.SMTP != nil {
			f, err := s.o.SMTP(ctx)
			if err != nil && !errors.Is(err, ErrSMTPUnavailable) {
				return nil, fmt.Errorf("riskaccept: read SMTP settings: %w", err)
			}
			if err == nil && f.Active {
				if c, ok := SMTPUnencrypted(f.Mode, f.Host, f.Port, f.Username); ok {
					cands = append(cands, cand{c: c})
				}
			}
		}
		conns, err := q.ListRiskConnections(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("riskaccept: list connections: %w", err)
		}
		for i := range conns {
			r := &conns[i]
			for _, k := range []Kind{KindConnectionTLS, KindPrivilegedRole} {
				if c, ok := connectionCondition(k, listedConn(r)); ok {
					cands = append(cands, cand{c: c, name: r.Name})
				}
			}
		}
	}
	jobs, err := q.ListRiskJobs(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("riskaccept: list jobs: %w", err)
	}
	owned := map[uuid.UUID]bool{}
	if !staff && len(jobs) > 0 {
		ids := make([]uuid.UUID, len(jobs))
		for i, j := range jobs {
			ids[i] = j.ID
		}
		mine, err := q.ListOwnedJobIDs(ctx, sqlc.ListOwnedJobIDsParams{UserID: user, JobIds: ids})
		if err != nil {
			return nil, fmt.Errorf("riskaccept: owned jobs: %w", err)
		}
		for _, id := range mine {
			owned[id] = true
		}
	}
	for _, j := range jobs {
		if !staff && !owned[j.ID] {
			continue
		}
		if c, ok := EncryptionOptOut(j.ID, j.OptedOutAt); ok {
			cands = append(cands, cand{c: c, name: j.Name})
		}
	}
	lv, err := s.loadLive(ctx, s.o.DB)
	if err != nil {
		return nil, err
	}
	var covered []acceptRow
	for _, cd := range cands {
		if r := covering(lv, cd.c); r != nil {
			covered = append(covered, rowOfLive(*r))
		}
	}
	views, err := s.views(ctx, q, pr, covered)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]*Acceptance, len(views))
	for i := range views {
		byID[views[i].ID] = &views[i]
	}
	out := make([]Finding, 0, len(cands))
	for _, cd := range cands {
		f := Finding{Condition: cd.c, SubjectName: cd.name}
		if r := covering(lv, cd.c); r != nil {
			f.Acceptance = byID[r.ID]
		}
		switch {
		case pr.Role == auth.RoleAdmin:
			f.CanAccept = true
		case pr.Role == auth.RoleUser && cd.c.Kind.OnJob():
			f.CanAccept = owned[cd.c.JobID]
		}
		out = append(out, f)
	}
	return out, nil
}

// checkSubject requires exactly the subject the kind needs.
func checkSubject(k Kind, conn, job uuid.UUID) error {
	switch {
	case k.OnConnection() && (conn == uuid.Nil || job != uuid.Nil),
		k.OnJob() && (job == uuid.Nil || conn != uuid.Nil),
		k.Instance() && (conn != uuid.Nil || job != uuid.Nil):
		return serviceutil.FieldErr(FieldSubject, "the risk kind does not match the subject")
	}
	return nil
}

// normalizeReason trims the reason and checks its length (1-500
// characters; line breaks allowed, other control characters not).
func normalizeReason(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) {
		return "", serviceutil.FieldErr(FieldReason, "must be valid UTF-8")
	}
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return "", serviceutil.FieldErr(FieldReason, "say why the risk is accepted")
	}
	if n > MaxReasonRunes {
		return "", serviceutil.FieldErr(FieldReason, "must be at most %d characters", MaxReasonRunes)
	}
	for _, r := range s {
		if (unicode.IsControl(r) && r != '\n') || r == '\u2028' || r == '\u2029' {
			return "", serviceutil.FieldErr(FieldReason, "must not contain control characters")
		}
	}
	return s, nil
}

func optID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func deref(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

// details are the details every entry carries.
func details(id uuid.UUID, kind Kind, conn, job uuid.UUID, cond string, d Duration, expires *time.Time, creator uuid.UUID) audit.Details {
	out := audit.Details{
		"risk_acceptance_id": id.String(), "kind": string(kind), "condition": cond,
		"duration": string(d), "expires_at": "", "creator_id": creator.String(),
	}
	if expires != nil {
		out["expires_at"] = expires.UTC().Format(time.RFC3339)
	}
	if conn != uuid.Nil {
		out["connection_id"] = conn.String()
	}
	if job != uuid.Nil {
		out["job_id"] = job.String()
	}
	return out
}

func endDetails(e sqlc.EndRiskAcceptanceRow) audit.Details {
	return details(e.ID, Kind(e.Kind), deref(e.ConnectionID), deref(e.JobID), e.Condition, Duration(e.Duration), e.ExpiresAt, e.CreatedBy)
}

// record writes an audit entry (fail closed).
func (s *Service) record(ctx context.Context, q *sqlc.Queries, actor audit.Actor, action audit.Action, id uuid.UUID, d audit.Details) error {
	ev := audit.Event{Actor: actor, Action: action, TargetType: AuditTargetType, TargetID: id.String(), Details: d}
	if actor.Kind == audit.ActorUser {
		ev.IP = auth.RequestMetaFrom(ctx).IP
	}
	if _, err := s.o.Auditor.RecordTx(ctx, q, ev); err != nil {
		return fmt.Errorf("riskaccept: audit %s: %w", action, err)
	}
	return nil
}

// Counts are the figures of the admins' summary line (D-228).
type Counts struct {
	// Accepted is the number of active acceptances, Forever those
	// without an end date.
	Accepted int64
	Forever  int64
	// TokensNeverExpiring counts the valid API tokens without expiry
	// (D-229).
	TokensNeverExpiring int64
}

// CountsOf reads the summary figures (no authorization: the caller has
// checked monitor.alert_list for the recipient).
func CountsOf(ctx context.Context, q *sqlc.Queries) (Counts, error) {
	r, err := q.CountRiskSummary(ctx)
	if err != nil {
		return Counts{}, fmt.Errorf("riskaccept: counts: %w", err)
	}
	return Counts{Accepted: r.Accepted, Forever: r.AcceptedForever, TokensNeverExpiring: r.TokensNeverExpiring}, nil
}

// listedConn converts a ListRiskConnections row.
func listedConn(r *sqlc.ListRiskConnectionsRow) connRow {
	return connRow{
		id: r.ID, mode: r.TlsMode, host: r.Host, port: int(r.Port), user: r.Username,
		ssh: r.SshEnabled, sshHost: r.SshHost, sshPort: r.SshPort, sshUser: r.SshUser,
		priv: r.LastTestPrivileges, reasons: r.LastTestPrivilegeReasons,
	}
}
