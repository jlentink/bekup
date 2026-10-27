package auth

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// maxRefusalEntries bounds the memory of a lockedRefusals: beyond it,
// entries of ended lock windows are pruned; if that is not enough the
// refusal is recorded anyway (never silently dropped).
const maxRefusalEntries = 10000

// lockedRefusals aggregates the audit entries of attempts refused while an
// account (password) or its second factors are locked (M1 review M2,
// D-117): only the first refusal per user and lock window is recorded; the
// further ones are counted in memory and the count is reported in the
// user's next failure, lockout or first-refusal entry
// ("refused_while_locked"). The count is lost on restart (the next refusal
// is then recorded again). It is safe for concurrent use.
type lockedRefusals struct {
	mu sync.Mutex
	m  map[uuid.UUID]*refusalWindow
}

type refusalWindow struct {
	until      time.Time
	suppressed int
}

func newLockedRefusals() *lockedRefusals {
	return &lockedRefusals{m: map[uuid.UUID]*refusalWindow{}}
}

// note registers a refusal of userID during the lock that ends at until.
// It reports whether the refusal must be recorded and, if so, how many
// refusals of the user's previous window were not recorded.
func (r *lockedRefusals) note(userID uuid.UUID, until, now time.Time) (record bool, previous int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w := r.m[userID]
	if w != nil && w.until.Equal(until) {
		w.suppressed++
		return false, 0
	}
	if w != nil {
		previous = w.suppressed
	}
	if w == nil && len(r.m) >= maxRefusalEntries {
		for id, e := range r.m {
			if !e.until.After(now) {
				delete(r.m, id)
			}
		}
		if len(r.m) >= maxRefusalEntries {
			return true, 0
		}
	}
	r.m[userID] = &refusalWindow{until: until}
	return true, previous
}

// take returns and forgets the number of unrecorded refusals of userID
// (the lock ended and a new attempt is being recorded).
func (r *lockedRefusals) take(userID uuid.UUID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	w := r.m[userID]
	if w == nil {
		return 0
	}
	delete(r.m, userID)
	return w.suppressed
}
