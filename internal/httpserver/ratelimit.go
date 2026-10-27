package httpserver

import (
	"container/list"
	"errors"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// RateLimitOptions configure a token-bucket limiter.
type RateLimitOptions struct {
	// PerSecond is the refill rate (sustained requests per second).
	PerSecond float64
	// Burst is the bucket capacity.
	Burst int
	// MaxClients bounds the number of tracked keys; the least recently
	// seen key is evicted beyond it.
	MaxClients int
	// Now replaces time.Now (tests).
	Now func() time.Time
}

// RateLimiter is an in-memory token bucket per key with LRU eviction, so
// memory stays bounded no matter how many keys send requests. An evicted
// key starts again with a full bucket, which is the accepted cost of the
// bound. It is safe for concurrent use. The HTTP layer keys it by client
// IP (IPKey); internal/auth uses it for the login limiter and the
// per-user limit.
type RateLimiter[K comparable] struct {
	mu         sync.Mutex
	rate       float64
	burst      float64
	maxClients int
	now        func() time.Time
	lru        *list.List // front = most recently seen; values are *bucket[K]
	buckets    map[K]*list.Element
}

type bucket[K comparable] struct {
	key    K
	tokens float64
	last   time.Time
}

// NewRateLimiter returns a limiter with o.
func NewRateLimiter[K comparable](o RateLimitOptions) (*RateLimiter[K], error) {
	if math.IsNaN(o.PerSecond) || math.IsInf(o.PerSecond, 0) || o.PerSecond <= 0 || o.Burst < 1 || o.MaxClients < 1 {
		return nil, errors.New("httpserver: rate limit needs PerSecond > 0, Burst >= 1 and MaxClients >= 1")
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &RateLimiter[K]{
		rate:       o.PerSecond,
		burst:      float64(o.Burst),
		maxClients: o.MaxClients,
		now:        now,
		lru:        list.New(),
		buckets:    make(map[K]*list.Element),
	}, nil
}

func newRateLimiter(o RateLimitOptions) (*RateLimiter[netip.Prefix], error) {
	return NewRateLimiter[netip.Prefix](o)
}

// IPKey groups addresses: IPv4 per address, IPv6 per /64 (one subscriber
// usually controls a whole /64). An unknown address maps to the zero
// prefix, so all unknown clients share one bucket.
func IPKey(a netip.Addr) netip.Prefix {
	switch {
	case !a.IsValid():
		return netip.Prefix{}
	case a.Is4():
		return netip.PrefixFrom(a, 32)
	default:
		p, err := a.Prefix(64)
		if err != nil {
			return netip.Prefix{}
		}
		return p
	}
}

// Allow takes one token for key; when none is left it returns false and
// the time until the next token.
func (l *RateLimiter[K]) Allow(key K) (bool, time.Duration) {
	return l.AllowN(key, 1)
}

// AllowN takes n tokens for key at once (n < 1 counts as 1); when fewer
// are left it takes none and returns false with the time until n tokens
// are available (n above the burst can never pass).
func (l *RateLimiter[K]) AllowN(key K, n int) (bool, time.Duration) {
	need := float64(max(n, 1))
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	var b *bucket[K]
	if el, ok := l.buckets[key]; ok {
		l.lru.MoveToFront(el)
		var ok bool
		if b, ok = el.Value.(*bucket[K]); !ok {
			panic("httpserver: rate limiter list holds a non-bucket")
		}
		if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
			b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
		}
		b.last = now
	} else {
		b = &bucket[K]{key: key, tokens: l.burst, last: now}
		l.buckets[key] = l.lru.PushFront(b)
		for l.lru.Len() > l.maxClients {
			oldest := l.lru.Back()
			l.lru.Remove(oldest)
			if ob, ok := oldest.Value.(*bucket[K]); ok {
				delete(l.buckets, ob.key)
			}
		}
	}
	if b.tokens >= need {
		b.tokens -= need
		return true, 0
	}
	wait := time.Duration((need - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// RetryAfterSeconds is the Retry-After value for wait (whole seconds, at
// least 1).
func RetryAfterSeconds(wait time.Duration) int64 {
	return max(int64(math.Ceil(wait.Seconds())), 1)
}

func writeTooMany(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.FormatInt(RetryAfterSeconds(wait), 10))
	writeError(w, http.StatusTooManyRequests)
}

func rateLimit(l *RateLimiter[netip.Prefix]) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ok, wait := l.Allow(IPKey(ClientIP(r.Context())))
			if !ok {
				writeTooMany(w, wait)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
