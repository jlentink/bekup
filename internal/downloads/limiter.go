package downloads

import (
	"sync"

	"github.com/google/uuid"
)

// limiter bounds concurrent downloads per process and per user. It never
// queues: a caller over a limit is refused at once (and the ticket stays
// valid, so the client may retry within its lifetime).
type limiter struct {
	mu      sync.Mutex
	total   int
	max     int
	perUser int
	users   map[uuid.UUID]int
}

func newLimiter(maxTotal, maxPerUser int) *limiter {
	return &limiter{max: maxTotal, perUser: maxPerUser, users: make(map[uuid.UUID]int)}
}

// acquire takes a slot for user; release must be called exactly once.
func (l *limiter) acquire(user uuid.UUID) (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= l.max || l.users[user] >= l.perUser {
		return nil, false
	}
	l.total++
	l.users[user]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.total--
			l.users[user]--
			if l.users[user] <= 0 {
				delete(l.users, user)
			}
		})
	}, true
}
