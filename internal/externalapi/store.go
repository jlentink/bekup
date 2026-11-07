package externalapi

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Credential is a presented token as the database checks it: the public
// id and the SHA-256 secret hash (apikey.Key.Hash). The plaintext secret
// never leaves the handler.
type Credential struct {
	TokenID    uuid.UUID
	SecretHash [32]byte
}

// Token is what an authenticated token may learn about itself.
type Token struct {
	Name string
	// ExpiresAt is nil when the token never expires (D-229; the database
	// guarantees never_expires = (expires_at IS NULL)).
	ExpiresAt *time.Time
}

// Job is a job in a token's scope.
type Job struct {
	ID   uuid.UUID
	Name string
}

// Refusal codes of api.request_run (migration 00037).
const (
	RefusalUnauthenticated     = "unauthenticated"
	RefusalInvalidRequest      = "invalid_request"
	RefusalNotFound            = "not_found"
	RefusalIdempotencyMismatch = "idempotency_mismatch"
	RefusalTooManyPending      = "too_many_pending"
	RefusalRateLimited         = "rate_limited"
	RefusalJobDisabled         = "job_disabled"
	RefusalRunActive           = "run_active"
)

// RunResult is the answer of api.request_run: a request (new, or the
// original one on an idempotent replay) or a refusal.
type RunResult struct {
	RequestID uuid.UUID
	CreatedAt time.Time
	// Refusal is empty when a request was filed or replayed.
	Refusal  string
	Replayed bool
	// RetryAt is set with RefusalRateLimited.
	RetryAt *time.Time
}

// RequestFacts are the raw facts of one request (api.request_status); the
// state is aggregated in Go (Aggregate).
type RequestFacts struct {
	RequestID uuid.UUID
	JobID     uuid.UUID
	// Outcome is pending, accepted or rejected.
	Outcome   string
	Reason    *string
	CreatedAt time.Time
	DecidedAt *time.Time
	// RiverState is the River job's state, nil once River deleted it.
	RiverState *string
	// The latest run of the River job by attempt (nil fields: none).
	RunID         *uuid.UUID
	RunStatus     *string
	RunAttempt    *int32
	RunQueuedAt   *time.Time
	RunStartedAt  *time.Time
	RunFinishedAt *time.Time
}

// Errors of a Store.
var (
	// ErrUnauthenticated: unknown id, wrong secret, revoked, expired,
	// creator no longer allowed or client address refused; all alike.
	ErrUnauthenticated = errors.New("externalapi: unauthenticated")
	// ErrNotFound: no such request of this token (or its job left the
	// scope).
	ErrNotFound = errors.New("externalapi: not found")
)

// Store is the external API's only access to the database: the api.*
// functions as bekup_external. Every call authenticates the token itself
// with the client address (an invalid address is passed as NULL, which no
// allowlist matches).
type Store interface {
	Authenticate(ctx context.Context, c Credential, ip netip.Addr) (Token, error)
	TokenJobs(ctx context.Context, c Credential, ip netip.Addr) ([]Job, error)
	RequestRun(ctx context.Context, c Credential, jobID uuid.UUID, idempotencyKey string, ip netip.Addr) (RunResult, error)
	RequestStatus(ctx context.Context, c Credential, requestID uuid.UUID, ip netip.Addr) (RequestFacts, error)
	// Ping checks that the database answers.
	Ping(ctx context.Context) error
}

// DBStore implements Store with the sqlc queries of external_api.sql on
// the bekup_external pool (outside explicit transactions: the api
// functions require READ COMMITTED, the pool's default).
type DBStore struct {
	DB interface {
		sqlc.DBTX
		Ping(ctx context.Context) error
	}
}

func addrArg(ip netip.Addr) *netip.Addr {
	if !ip.IsValid() {
		return nil
	}
	ip = ip.WithZone("").Unmap()
	return &ip
}

// Authenticate implements Store.
func (s DBStore) Authenticate(ctx context.Context, c Credential, ip netip.Addr) (Token, error) {
	row, err := sqlc.New(s.DB).ExternalAuthenticate(ctx, sqlc.ExternalAuthenticateParams{
		TokenID: c.TokenID, SecretHash: c.SecretHash[:], ClientIp: addrArg(ip),
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Token{}, ErrUnauthenticated
	case err != nil:
		return Token{}, err
	case row.TokenID == nil || row.Name == nil:
		return Token{}, ErrUnauthenticated
	}
	t := Token{Name: *row.Name}
	if row.ExpiresAt != nil {
		at := row.ExpiresAt.UTC()
		t.ExpiresAt = &at
	}
	return t, nil
}

// TokenJobs implements Store.
func (s DBStore) TokenJobs(ctx context.Context, c Credential, ip netip.Addr) ([]Job, error) {
	rows, err := sqlc.New(s.DB).ExternalTokenJobs(ctx, sqlc.ExternalTokenJobsParams{
		TokenID: c.TokenID, SecretHash: c.SecretHash[:], ClientIp: addrArg(ip),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(rows))
	for _, r := range rows {
		if r.JobID == nil || r.JobName == nil {
			continue
		}
		out = append(out, Job{ID: *r.JobID, Name: *r.JobName})
	}
	return out, nil
}

// RequestRun implements Store.
func (s DBStore) RequestRun(ctx context.Context, c Credential, jobID uuid.UUID, idempotencyKey string, ip netip.Addr) (RunResult, error) {
	var key *string
	if idempotencyKey != "" {
		key = &idempotencyKey
	}
	row, err := sqlc.New(s.DB).ExternalRequestRun(ctx, sqlc.ExternalRequestRunParams{
		TokenID: c.TokenID, SecretHash: c.SecretHash[:], JobID: jobID, IdempotencyKey: key, Ip: addrArg(ip),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return RunResult{}, errors.New("externalapi: api.request_run returned no row")
	}
	if err != nil {
		return RunResult{}, err
	}
	res := RunResult{RetryAt: row.RetryAt}
	if row.Refusal != nil {
		res.Refusal = *row.Refusal
		return res, nil
	}
	if row.RequestID == nil || row.CreatedAt == nil {
		return RunResult{}, errors.New("externalapi: api.request_run returned neither a request nor a refusal")
	}
	res.RequestID, res.CreatedAt = *row.RequestID, *row.CreatedAt
	res.Replayed = row.Replayed != nil && *row.Replayed
	return res, nil
}

// RequestStatus implements Store.
func (s DBStore) RequestStatus(ctx context.Context, c Credential, requestID uuid.UUID, ip netip.Addr) (RequestFacts, error) {
	row, err := sqlc.New(s.DB).ExternalRequestStatus(ctx, sqlc.ExternalRequestStatusParams{
		TokenID: c.TokenID, SecretHash: c.SecretHash[:], RequestID: requestID, ClientIp: addrArg(ip),
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return RequestFacts{}, ErrNotFound
	case err != nil:
		return RequestFacts{}, err
	case row.RequestID == nil || row.JobID == nil || row.Outcome == nil || row.CreatedAt == nil:
		return RequestFacts{}, ErrNotFound
	}
	return RequestFacts{
		RequestID: *row.RequestID, JobID: *row.JobID, Outcome: *row.Outcome, Reason: row.Reason,
		CreatedAt: *row.CreatedAt, DecidedAt: row.DecidedAt, RiverState: row.RiverState,
		RunID: row.RunID, RunStatus: row.RunStatus, RunAttempt: row.RunAttempt,
		RunQueuedAt: row.RunQueuedAt, RunStartedAt: row.RunStartedAt, RunFinishedAt: row.RunFinishedAt,
	}, nil
}

// Ping implements Store.
func (s DBStore) Ping(ctx context.Context) error { return s.DB.Ping(ctx) }
