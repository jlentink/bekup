// Package authztest provides in-memory fakes of the authz hooks and
// principal helpers for tests.
package authztest

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
)

// Ownership is an in-memory authz.Ownership. The zero value owns nothing.
type Ownership struct {
	mu sync.Mutex
	// jobs maps a job to its owners.
	jobs map[uuid.UUID]map[uuid.UUID]bool
	// jobDBs maps a job to the (connection, database) pairs it backs up.
	jobDBs map[uuid.UUID][]dbRef
	// Err, when set, is returned by every lookup.
	Err error
}

type dbRef struct {
	conn uuid.UUID
	db   string
}

// AddOwner makes userID an owner of jobID.
func (o *Ownership) AddOwner(jobID, userID uuid.UUID) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.jobs == nil {
		o.jobs = map[uuid.UUID]map[uuid.UUID]bool{}
	}
	if o.jobs[jobID] == nil {
		o.jobs[jobID] = map[uuid.UUID]bool{}
	}
	o.jobs[jobID][userID] = true
}

// AddDatabase records that jobID backs up database on connID.
func (o *Ownership) AddDatabase(jobID, connID uuid.UUID, database string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.jobDBs == nil {
		o.jobDBs = map[uuid.UUID][]dbRef{}
	}
	o.jobDBs[jobID] = append(o.jobDBs[jobID], dbRef{conn: connID, db: database})
}

// IsJobOwner implements authz.Ownership.
func (o *Ownership) IsJobOwner(_ context.Context, userID, jobID uuid.UUID) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.Err != nil {
		return false, o.Err
	}
	return o.jobs[jobID][userID], nil
}

// BacksUpDatabase implements authz.Ownership.
func (o *Ownership) BacksUpDatabase(_ context.Context, userID, connID uuid.UUID, database string) (bool, error) {
	return o.match(userID, func(r dbRef) bool { return r.conn == connID && r.db == database })
}

// UsesConnection implements authz.Ownership.
func (o *Ownership) UsesConnection(_ context.Context, userID, connID uuid.UUID) (bool, error) {
	return o.match(userID, func(r dbRef) bool { return r.conn == connID })
}

func (o *Ownership) match(userID uuid.UUID, f func(dbRef) bool) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.Err != nil {
		return false, o.Err
	}
	for job, owners := range o.jobs {
		if !owners[userID] {
			continue
		}
		for _, r := range o.jobDBs[job] {
			if f(r) {
				return true, nil
			}
		}
	}
	return false, nil
}

// SecondFactors is an in-memory authz.SecondFactors: users in Enrolled
// have a factor.
type SecondFactors struct {
	Enrolled map[uuid.UUID]bool
	Err      error
}

// HasSecondFactor implements authz.SecondFactors.
func (s SecondFactors) HasSecondFactor(_ context.Context, userID uuid.UUID) (bool, error) {
	if s.Err != nil {
		return false, s.Err
	}
	return s.Enrolled[userID], nil
}

// Principal returns a valid principal with role and a fresh step-up and
// second-factor check (AAL2).
func Principal(role auth.Role) auth.Principal {
	now := time.Now()
	return auth.Principal{
		UserID: uuid.New(), SessionID: uuid.New(), Role: role,
		AAL: 2, StepUpAt: now, AAL2At: now,
	}
}

// Stale returns p with step-up and AAL2 older than any allowed window.
func Stale(p auth.Principal) auth.Principal {
	old := time.Now().Add(-time.Hour)
	p.StepUpAt, p.AAL2At = old, old
	return p
}

// Context returns ctx carrying p.
func Context(ctx context.Context, p auth.Principal) context.Context {
	return auth.WithPrincipal(ctx, p)
}
