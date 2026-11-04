package apiaccess

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/apikey"
	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// Token management limits (D-220).
const (
	// MinExpiry and MaxExpiry bound a new token's lifetime (1-365 days;
	// the database allows up to 366 days after creation).
	MinExpiry = 24 * time.Hour
	MaxExpiry = 365 * 24 * time.Hour
	// ExpirySkew tolerates a browser clock that differs from the server's
	// when it computes expiresAt from a preset (30 days from "now").
	ExpirySkew = time.Hour
	// MaxNameRunes is the api_tokens_name_length CHECK.
	MaxNameRunes = 100
	// MaxJobsPerToken bounds a token's scope.
	MaxJobsPerToken = 100
	// MaxListRows bounds a token listing (newest first).
	MaxListRows = 500
)

// Field names of the create input (VALIDATION errors).
const (
	FieldName      = "name"
	FieldJobIDs    = "jobIds"
	FieldExpiresAt = "expiresAt"
)

// Errors of the token service (the GraphQL layer maps them).
var (
	// ErrNotFound: no such token (only admins learn it; others get the
	// policy's FORBIDDEN).
	ErrNotFound = errors.New("apiaccess: API token not found")
	// ErrRevoked: the token is already revoked (revoke, address change).
	ErrRevoked = errors.New("apiaccess: API token already revoked")
	// ErrForbidden: a backup owner asked for the tokens of a job they do
	// not own.
	ErrForbidden = errors.New("apiaccess: not allowed")
)

// Authorizer is the central policy (*authz.Policy).
type Authorizer interface {
	Authorize(ctx context.Context, op authz.Operation, res ...authz.Resource) error
	JobScope(ctx context.Context) (authz.Scope, uuid.UUID, error)
}

// ServiceOptions configure NewService.
type ServiceOptions struct {
	DB DB
	// Auditor records api_token.created|revoked|addresses_changed in the
	// change's transaction (*audit.Auditor with the notify.ObserveAudit
	// observer, which raises the security alerts in the same
	// transaction).
	Auditor    Auditor
	Authorizer Authorizer
	// Now overrides the clock (tests).
	Now    func() time.Time
	Logger *slog.Logger
}

// Service manages API tokens for the GraphQL API: create (the plaintext
// is returned once and never stored or logged), list, revoke and change
// the client addresses. Every call authorizes its caller with the policy
// (api_token.*): admins for any jobs, backup owners (role user) for the
// jobs they own, maintainers never.
type Service struct {
	o ServiceOptions
}

// NewService validates o.
func NewService(o ServiceOptions) (*Service, error) {
	if o.DB == nil || o.Auditor == nil || o.Authorizer == nil {
		return nil, errors.New("apiaccess: DB, Auditor and Authorizer are required")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &Service{o: o}, nil
}

// CreateInput is a new token.
type CreateInput struct {
	Name   string
	JobIDs []uuid.UUID
	// ExpiresAt must lie 1-365 days ahead (ExpirySkew tolerated).
	ExpiresAt time.Time
	// AllowedCIDRs or AllowAnyAddress, exactly one (ParseAddresses).
	AllowedCIDRs    []string
	AllowAnyAddress bool
}

// TokenJob is a job of a token's scope.
type TokenJob struct {
	ID      uuid.UUID
	Name    string
	Deleted bool
}

// Creator is who created a token.
type Creator struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	Deleted     bool
}

// Sighting is where a token was last seen: its newest run request or its
// newest refused attempt from an address outside its allowlist.
type Sighting struct {
	At time.Time
	// IP is nil when the external API passed no address.
	IP *netip.Addr
	// Refused: the sighting was a refused attempt (a theft signal).
	Refused bool
}

// Token is a token as the caller may see it. It never holds the secret
// or its hash.
type Token struct {
	ID     uuid.UUID
	Name   string
	Prefix string
	// Jobs are the jobs of the scope the caller may see; HiddenJobs
	// counts the others (a backup owner sees only the jobs they own).
	Jobs       []TokenJob
	HiddenJobs int
	// CreatedBy is set for admins and for the creator; CreatedByCaller
	// reports whether the caller created the token.
	CreatedBy       *Creator
	CreatedByCaller bool
	CreatedAt       time.Time
	ExpiresAt       time.Time
	RevokedAt       *time.Time
	AllowedCIDRs    []netip.Prefix
	AllowAnyAddress bool
	// LastUsedAt is the newest run request.
	LastUsedAt *time.Time
	LastSeen   *Sighting
}

// Created is a new token with its plaintext, which exists only in this
// value: return it to the caller once, never log or store it.
type Created struct {
	Token     Token
	Plaintext string
}

// String never prints the plaintext.
func (c Created) String() string { return "Created{" + c.Token.ID.String() + "}" }

// GoString never prints the plaintext either (%#v).
func (c Created) GoString() string { return c.String() }

// tokenRow is the common shape of the token reads (sqlc gives each query
// its own identical row type).
type tokenRow struct {
	ID              uuid.UUID
	Name            string
	CreatedBy       uuid.UUID
	CreatedAt       time.Time
	ExpiresAt       time.Time
	RevokedAt       *time.Time
	RevokedBy       *uuid.UUID
	AllowedCidrs    []netip.Prefix
	AllowAnyAddress bool
	LastUsedAt      *time.Time
}

// tokenRowNoUse is the shape of the token writes (no last use).
type tokenRowNoUse struct {
	ID              uuid.UUID
	Name            string
	CreatedBy       uuid.UUID
	CreatedAt       time.Time
	ExpiresAt       time.Time
	RevokedAt       *time.Time
	RevokedBy       *uuid.UUID
	AllowedCidrs    []netip.Prefix
	AllowAnyAddress bool
}

func (r tokenRowNoUse) row() tokenRow {
	return tokenRow{ID: r.ID, Name: r.Name, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
		RevokedAt: r.RevokedAt, RevokedBy: r.RevokedBy, AllowedCidrs: r.AllowedCidrs, AllowAnyAddress: r.AllowAnyAddress}
}

// Create authorizes the caller (api_token.create: admin, or the owner of
// every listed job; fresh step-up), validates the input, generates the
// token (internal/apikey) and stores its hash, scope and addresses with
// the api_token.created audit entry in one transaction.
func (s *Service) Create(ctx context.Context, in CreateInput) (Created, error) {
	pr, ok := auth.FromContext(ctx)
	if !ok {
		return Created{}, s.o.Authorizer.Authorize(ctx, authz.OpAPITokenCreate)
	}
	jobIDs, err := normalizeJobIDs(in.JobIDs)
	if err != nil {
		return Created{}, err
	}
	res := make([]authz.Resource, len(jobIDs))
	for i, id := range jobIDs {
		res[i] = authz.Job{ID: id}
	}
	if err := s.o.Authorizer.Authorize(ctx, authz.OpAPITokenCreate, res...); err != nil {
		return Created{}, err
	}
	name, err := normalizeName(in.Name)
	if err != nil {
		return Created{}, err
	}
	expires, err := s.checkExpiry(in.ExpiresAt)
	if err != nil {
		return Created{}, err
	}
	cidrs, err := ParseAddresses(in.AllowedCIDRs, in.AllowAnyAddress)
	if err != nil {
		return Created{}, err
	}
	g, err := apikey.Generate()
	if err != nil {
		return Created{}, fmt.Errorf("apiaccess: generate token: %w", err)
	}
	// Anything that might print the token while it is handled is scrubbed.
	defer logging.DefaultRegistry.Register([]byte(g.Plaintext))()
	var row tokenRow
	var jobs []TokenJob
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		locked, err := q.LockJobsForAPIToken(ctx, jobIDs)
		if err != nil {
			return fmt.Errorf("apiaccess: lock jobs: %w", err)
		}
		if len(locked) != len(jobIDs) {
			return serviceutil.FieldErr(FieldJobIDs, "a selected job does not exist or was deleted")
		}
		ins, err := q.InsertAPIToken(ctx, sqlc.InsertAPITokenParams{
			ID: g.ID, Name: name, SecretHash: g.Hash[:], CreatedBy: pr.UserID, ExpiresAt: expires,
			AllowedCidrs: cidrs, AllowAnyAddress: in.AllowAnyAddress,
		})
		if err != nil {
			return fmt.Errorf("apiaccess: insert token: %w", err)
		}
		n, err := q.InsertAPITokenJobs(ctx, sqlc.InsertAPITokenJobsParams{TokenID: g.ID, JobIds: jobIDs})
		if err != nil {
			return fmt.Errorf("apiaccess: insert token scope: %w", err)
		}
		if n != int64(len(jobIDs)) {
			return fmt.Errorf("apiaccess: token scope: %d of %d jobs stored", n, len(jobIDs))
		}
		row = tokenRowNoUse(ins).row()
		jobs = make([]TokenJob, len(locked))
		for i, j := range locked {
			jobs[i] = TokenJob{ID: j.ID, Name: j.Name}
		}
		details := tokenDetails(row, jobIDs)
		details["expires_at"] = row.ExpiresAt.UTC().Format(time.RFC3339)
		details["allowed_cidrs"] = AddressStrings(row.AllowedCidrs)
		details["allow_any_address"] = row.AllowAnyAddress
		return s.record(ctx, q, pr, audit.ActionAPITokenCreated, row.ID, details)
	})
	if err != nil {
		return Created{}, err
	}
	s.o.Logger.InfoContext(ctx, "API token created", slog.String("api_token_id", row.ID.String()),
		slog.Int("jobs", len(jobIDs)), slog.Bool("allow_any_address", row.AllowAnyAddress))
	views, err := s.views(ctx, sqlc.New(s.o.DB), pr, authz.ScopeAll, []tokenRow{row})
	if err != nil {
		return Created{}, err
	}
	t := views[0]
	if len(t.Jobs) == 0 {
		// The scope as locked, in case the read-back races a deletion.
		t.Jobs = jobs
	}
	return Created{Token: t, Plaintext: g.Plaintext}, nil
}

// List returns the tokens the caller may see, newest first (at most
// MaxListRows). Without jobID admins see every token and backup owners
// the tokens covering a job they own; with jobID the tokens covering that
// job (owners: only a job they own). Scope entries the caller may not see
// are only counted (Token.HiddenJobs).
func (s *Service) List(ctx context.Context, jobID *uuid.UUID) ([]Token, error) {
	pr, ok := auth.FromContext(ctx)
	if !ok {
		return nil, s.o.Authorizer.Authorize(ctx, authz.OpAPITokenList)
	}
	if err := s.o.Authorizer.Authorize(ctx, authz.OpAPITokenList); err != nil {
		return nil, err
	}
	scope, _, err := s.o.Authorizer.JobScope(ctx)
	if err != nil {
		return nil, err
	}
	q := sqlc.New(s.o.DB)
	var rows []tokenRow
	switch {
	case jobID != nil:
		if scope != authz.ScopeAll {
			owned, err := q.ListOwnedJobIDs(ctx, sqlc.ListOwnedJobIDsParams{UserID: pr.UserID, JobIds: []uuid.UUID{*jobID}})
			if err != nil {
				return nil, fmt.Errorf("apiaccess: job ownership: %w", err)
			}
			if len(owned) == 0 {
				return nil, ErrForbidden
			}
		}
		list, err := q.ListAPITokensForJob(ctx, sqlc.ListAPITokensForJobParams{JobID: *jobID, MaxRows: MaxListRows})
		if err != nil {
			return nil, fmt.Errorf("apiaccess: list tokens: %w", err)
		}
		for i := range list {
			rows = append(rows, tokenRow(list[i]))
		}
	case scope == authz.ScopeAll:
		list, err := q.ListAPITokens(ctx, MaxListRows)
		if err != nil {
			return nil, fmt.Errorf("apiaccess: list tokens: %w", err)
		}
		for i := range list {
			rows = append(rows, tokenRow(list[i]))
		}
	default:
		list, err := q.ListAPITokensForOwner(ctx, sqlc.ListAPITokensForOwnerParams{UserID: pr.UserID, MaxRows: MaxListRows})
		if err != nil {
			return nil, fmt.Errorf("apiaccess: list tokens: %w", err)
		}
		for i := range list {
			rows = append(rows, tokenRow(list[i]))
		}
	}
	return s.views(ctx, q, pr, scope, rows)
}

// Revoke revokes a token (api_token.revoke: admin, or the owner of a job
// of its scope; fresh step-up) with the api_token.revoked audit entry.
// The external API refuses it from the next call on.
func (s *Service) Revoke(ctx context.Context, id uuid.UUID) (Token, error) {
	pr, scope, scopeJobs, err := s.authorizeManage(ctx, authz.OpAPITokenRevoke, id)
	if err != nil {
		return Token{}, err
	}
	var row tokenRow
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		cur, err := q.GetAPITokenForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("apiaccess: lock token: %w", err)
		}
		if cur.RevokedAt != nil {
			return ErrRevoked
		}
		rev, err := q.RevokeAPIToken(ctx, sqlc.RevokeAPITokenParams{ID: id, RevokedBy: &pr.UserID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRevoked
		}
		if err != nil {
			return fmt.Errorf("apiaccess: revoke token: %w", err)
		}
		row = tokenRowNoUse(rev).row()
		details := tokenDetails(row, scopeJobs)
		details["expires_at"] = row.ExpiresAt.UTC().Format(time.RFC3339)
		return s.record(ctx, q, pr, audit.ActionAPITokenRevoked, id, details)
	})
	if err != nil {
		return Token{}, err
	}
	s.o.Logger.InfoContext(ctx, "API token revoked", slog.String("api_token_id", id.String()))
	return s.one(ctx, pr, scope, id)
}

// UpdateAddresses replaces the client addresses of an unrevoked token
// (api_token.update: the same callers as Revoke; fresh step-up) with the
// api_token.addresses_changed audit entry (old and new values, whether
// the change widens access, the creator and the actor's name). An
// unchanged list (in any order) writes and audits nothing.
func (s *Service) UpdateAddresses(ctx context.Context, id uuid.UUID, cidrs []string, allowAny bool) (Token, error) {
	pr, scope, scopeJobs, err := s.authorizeManage(ctx, authz.OpAPITokenUpdate, id)
	if err != nil {
		return Token{}, err
	}
	nets, err := ParseAddresses(cidrs, allowAny)
	if err != nil {
		return Token{}, err
	}
	changed := false
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		cur, err := q.GetAPITokenForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("apiaccess: lock token: %w", err)
		}
		if cur.RevokedAt != nil {
			return ErrRevoked
		}
		if cur.AllowAnyAddress == allowAny && sameNetworks(cur.AllowedCidrs, nets) {
			return nil
		}
		upd, err := q.UpdateAPITokenAddresses(ctx, sqlc.UpdateAPITokenAddressesParams{
			ID: id, AllowedCidrs: nets, AllowAnyAddress: allowAny,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRevoked
		}
		if err != nil {
			return fmt.Errorf("apiaccess: update token addresses: %w", err)
		}
		changed = true
		row := tokenRowNoUse(upd).row()
		details := tokenDetails(row, scopeJobs)
		details["allowed_cidrs"] = AddressStrings(row.AllowedCidrs)
		details["allow_any_address"] = row.AllowAnyAddress
		details["previous_allowed_cidrs"] = AddressStrings(cur.AllowedCidrs)
		details["previous_allow_any_address"] = cur.AllowAnyAddress
		// A widening (M1 of the D-220 review) alerts the admins and the
		// token's creator, naming who changed it.
		details["widened"] = widens(cur.AllowedCidrs, cur.AllowAnyAddress, nets, allowAny)
		details["creator_id"] = cur.CreatedBy.String()
		details["changed_by_creator"] = cur.CreatedBy == pr.UserID
		name, err := actorName(ctx, q, pr.UserID)
		if err != nil {
			return err
		}
		details["actor_name"] = name
		return s.record(ctx, q, pr, audit.ActionAPITokenAddressesChanged, id, details)
	})
	if err != nil {
		return Token{}, err
	}
	if changed {
		s.o.Logger.InfoContext(ctx, "API token addresses changed", slog.String("api_token_id", id.String()),
			slog.Int("networks", len(nets)), slog.Bool("allow_any_address", allowAny))
	}
	return s.one(ctx, pr, scope, id)
}

// sameNetworks reports whether a and b hold the same networks in any
// order (both canonical, as stored and as ParseAddresses returns them).
func sameNetworks(a, b []netip.Prefix) bool {
	if len(a) != len(b) {
		return false
	}
	return slices.Equal(sortedNetworks(a), sortedNetworks(b))
}

func sortedNetworks(ps []netip.Prefix) []netip.Prefix {
	out := make([]netip.Prefix, len(ps))
	for i, p := range ps {
		out[i] = p.Masked()
	}
	slices.SortFunc(out, func(x, y netip.Prefix) int {
		if c := x.Addr().Compare(y.Addr()); c != 0 {
			return c
		}
		return x.Bits() - y.Bits()
	})
	return out
}

// widens reports whether changing a token's addresses from (old, oldAny)
// to (nets, anyAddr) lets it be used from an address it could not be
// used from before: "any address" newly chosen, or a network of the new
// list not covered by a network of the old one.
func widens(old []netip.Prefix, oldAny bool, nets []netip.Prefix, anyAddr bool) bool {
	if oldAny {
		return false
	}
	if anyAddr {
		return true
	}
	for _, n := range nets {
		if !slices.ContainsFunc(old, func(o netip.Prefix) bool { return covers(o, n) }) {
			return true
		}
	}
	return false
}

// covers reports whether network o contains all of network n.
func covers(o, n netip.Prefix) bool {
	return o.Addr().Is4() == n.Addr().Is4() && o.Bits() <= n.Bits() && o.Contains(n.Addr())
}

// actorName is the display name of the user who changes a token (named
// in the widening alert); never the e-mail address.
func actorName(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (string, error) {
	rows, err := q.ListAPITokenCreators(ctx, []uuid.UUID{id})
	if err != nil {
		return "", fmt.Errorf("apiaccess: load actor: %w", err)
	}
	if len(rows) == 1 {
		return rows[0].DisplayName, nil
	}
	return "", nil
}

// authorizeManage loads a token's scope and authorizes op (revoke,
// update) with a job of it: admins by role, backup owners with a job of
// the scope they own. An unknown token is NOT_FOUND only for callers who
// could manage tokens by role; others get the policy's FORBIDDEN, as for
// a token outside their jobs.
func (s *Service) authorizeManage(ctx context.Context, op authz.Operation, id uuid.UUID) (auth.Principal, authz.Scope, []uuid.UUID, error) {
	pr, ok := auth.FromContext(ctx)
	if !ok {
		return auth.Principal{}, 0, nil, s.o.Authorizer.Authorize(ctx, op)
	}
	q := sqlc.New(s.o.DB)
	if _, err := q.GetAPIToken(ctx, id); errors.Is(err, pgx.ErrNoRows) {
		if aerr := s.o.Authorizer.Authorize(ctx, op, authz.Job{ID: uuid.Nil}); aerr != nil {
			return auth.Principal{}, 0, nil, aerr
		}
		return auth.Principal{}, 0, nil, ErrNotFound
	} else if err != nil {
		return auth.Principal{}, 0, nil, fmt.Errorf("apiaccess: load token: %w", err)
	}
	scopeRows, err := q.ListAPITokenJobs(ctx, []uuid.UUID{id})
	if err != nil {
		return auth.Principal{}, 0, nil, fmt.Errorf("apiaccess: load token scope: %w", err)
	}
	jobIDs := make([]uuid.UUID, len(scopeRows))
	for i, r := range scopeRows {
		jobIDs[i] = r.JobID
	}
	// The job the caller acts through: for an admin any job of the scope
	// (the role decides), for a backup owner one they own.
	via := uuid.Nil
	if pr.Role == auth.RoleAdmin {
		if len(jobIDs) > 0 {
			via = jobIDs[0]
		}
	} else if len(jobIDs) > 0 {
		owned, err := q.ListOwnedJobIDs(ctx, sqlc.ListOwnedJobIDsParams{UserID: pr.UserID, JobIds: jobIDs})
		if err != nil {
			return auth.Principal{}, 0, nil, fmt.Errorf("apiaccess: job ownership: %w", err)
		}
		if len(owned) > 0 {
			via = owned[0]
		}
	}
	if err := s.o.Authorizer.Authorize(ctx, op, authz.Job{ID: via}); err != nil {
		return auth.Principal{}, 0, nil, err
	}
	scope, _, err := s.o.Authorizer.JobScope(ctx)
	if err != nil {
		return auth.Principal{}, 0, nil, err
	}
	return pr, scope, jobIDs, nil
}

// one reads a single token as the caller sees it.
func (s *Service) one(ctx context.Context, pr auth.Principal, scope authz.Scope, id uuid.UUID) (Token, error) {
	q := sqlc.New(s.o.DB)
	r, err := q.GetAPIToken(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Token{}, ErrNotFound
	}
	if err != nil {
		return Token{}, fmt.Errorf("apiaccess: load token: %w", err)
	}
	views, err := s.views(ctx, q, pr, scope, []tokenRow{tokenRow(r)})
	if err != nil {
		return Token{}, err
	}
	return views[0], nil
}

// views turns token rows into what the caller may see: the visible part
// of each scope (backup owners: the jobs they own), the creator (admins,
// or the caller's own tokens) and the last sighting.
func (s *Service) views(ctx context.Context, q *sqlc.Queries, pr auth.Principal, scope authz.Scope, rows []tokenRow) ([]Token, error) {
	out := make([]Token, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, len(rows))
	creatorIDs := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
		if scope == authz.ScopeAll || rows[i].CreatedBy == pr.UserID {
			creatorIDs = append(creatorIDs, rows[i].CreatedBy)
		}
	}
	scopeRows, err := q.ListAPITokenJobs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("apiaccess: load token scopes: %w", err)
	}
	var visible map[uuid.UUID]bool
	if scope != authz.ScopeAll {
		jobIDs := make([]uuid.UUID, 0, len(scopeRows))
		for _, j := range scopeRows {
			jobIDs = append(jobIDs, j.JobID)
		}
		owned, err := q.ListOwnedJobIDs(ctx, sqlc.ListOwnedJobIDsParams{UserID: pr.UserID, JobIds: jobIDs})
		if err != nil {
			return nil, fmt.Errorf("apiaccess: job ownership: %w", err)
		}
		visible = make(map[uuid.UUID]bool, len(owned))
		for _, id := range owned {
			visible[id] = true
		}
	}
	jobsOf := make(map[uuid.UUID][]TokenJob, len(rows))
	hidden := make(map[uuid.UUID]int, len(rows))
	for _, j := range scopeRows {
		if visible != nil && !visible[j.JobID] {
			hidden[j.TokenID]++
			continue
		}
		jobsOf[j.TokenID] = append(jobsOf[j.TokenID], TokenJob{ID: j.JobID, Name: j.JobName, Deleted: j.JobDeleted})
	}
	creators := map[uuid.UUID]Creator{}
	if len(creatorIDs) > 0 {
		cs, err := q.ListAPITokenCreators(ctx, creatorIDs)
		if err != nil {
			return nil, fmt.Errorf("apiaccess: load token creators: %w", err)
		}
		for _, c := range cs {
			creators[c.ID] = Creator{ID: c.ID, Email: c.Email, DisplayName: c.DisplayName, Deleted: c.Deleted}
		}
	}
	seen, err := q.ListAPITokenLastSeen(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("apiaccess: load token sightings: %w", err)
	}
	sightings := make(map[uuid.UUID]*Sighting, len(seen))
	for _, r := range seen {
		sg := &Sighting{At: r.SeenAt, Refused: r.Denied}
		if r.ClientIp != nil {
			a := r.ClientIp.Unmap()
			sg.IP = &a
		}
		sightings[r.TokenID] = sg
	}
	for i := range rows {
		r := &rows[i]
		t := Token{
			ID: r.ID, Name: r.Name, Prefix: apikey.DisplayPrefix(r.ID),
			Jobs: jobsOf[r.ID], HiddenJobs: hidden[r.ID], CreatedByCaller: r.CreatedBy == pr.UserID,
			CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, RevokedAt: r.RevokedAt,
			AllowedCIDRs: r.AllowedCidrs, AllowAnyAddress: r.AllowAnyAddress, LastUsedAt: r.LastUsedAt,
			LastSeen: sightings[r.ID],
		}
		if t.Jobs == nil {
			t.Jobs = []TokenJob{}
		}
		if c, ok := creators[r.CreatedBy]; ok && (scope == authz.ScopeAll || t.CreatedByCaller) {
			t.CreatedBy = &c
		}
		out[i] = t
	}
	return out, nil
}

// record writes an audit entry of a token change (fail closed).
func (s *Service) record(ctx context.Context, q *sqlc.Queries, pr auth.Principal, action audit.Action, id uuid.UUID, d audit.Details) error {
	if _, err := s.o.Auditor.RecordTx(ctx, q, audit.Event{
		Actor: audit.UserActor(pr.UserID), Action: action, TargetType: AuditTargetType, TargetID: id.String(),
		IP: auth.RequestMetaFrom(ctx).IP, Details: d,
	}); err != nil {
		return fmt.Errorf("apiaccess: audit %s: %w", action, err)
	}
	return nil
}

// tokenDetails are the audit details every token entry carries (never
// the token or its hash).
func tokenDetails(r tokenRow, jobIDs []uuid.UUID) audit.Details {
	ids := make([]string, len(jobIDs))
	for i, id := range jobIDs {
		ids[i] = id.String()
	}
	return audit.Details{"api_token_id": r.ID.String(), "api_token_name": r.Name, "job_ids": ids}
}

// normalizeJobIDs checks a new token's scope: 1-MaxJobsPerToken jobs,
// duplicates dropped, sorted.
func normalizeJobIDs(ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, serviceutil.FieldErr(FieldJobIDs, "choose at least one job")
	}
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	out = slices.Compact(out)
	if len(out) > MaxJobsPerToken {
		return nil, serviceutil.FieldErr(FieldJobIDs, "at most %d jobs per token", MaxJobsPerToken)
	}
	if slices.Contains(out, uuid.Nil) {
		return nil, serviceutil.FieldErr(FieldJobIDs, "a selected job does not exist or was deleted")
	}
	return out, nil
}

// normalizeName trims a token name and checks the api_tokens_name_length
// CHECK and that it has no control characters.
func normalizeName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) {
		return "", serviceutil.FieldErr(FieldName, "must be valid UTF-8")
	}
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return "", serviceutil.FieldErr(FieldName, "must not be empty")
	}
	if n > MaxNameRunes {
		return "", serviceutil.FieldErr(FieldName, "must be at most %d characters", MaxNameRunes)
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return "", serviceutil.FieldErr(FieldName, "must not contain control characters")
		}
	}
	return s, nil
}

// checkExpiry requires an expiry 1-365 days ahead (ExpirySkew tolerated
// on both ends) and returns it in the database's precision.
func (s *Service) checkExpiry(at time.Time) (time.Time, error) {
	now := s.o.Now()
	if at.IsZero() || at.Before(now.Add(MinExpiry-ExpirySkew)) || at.After(now.Add(MaxExpiry+ExpirySkew)) {
		return time.Time{}, serviceutil.FieldErr(FieldExpiresAt, "the token must expire between 1 and 365 days from now")
	}
	return at.UTC().Truncate(time.Microsecond), nil
}
