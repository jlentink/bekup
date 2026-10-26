package logging

import (
	"bytes"
	"slices"
	"sync"
)

// MinSecretLen is the minimum length of a registered secret. Shorter values
// would scrub ordinary words from logs and are ignored.
const MinSecretLen = 6

// Registry holds known secret values that must never appear in logs. It is
// safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	next    uint64
	secrets map[uint64][]byte
	ordered [][]byte // longest first, so overlapping secrets are fully removed
}

// DefaultRegistry is the process-wide registry used by New when no registry
// is given.
var DefaultRegistry = NewRegistry()

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{secrets: make(map[uint64][]byte)}
}

// Register adds a secret value. The registry keeps its own copy, so the
// caller may zero its slice. The returned function removes and zeroes the
// copy; call it when the secret is no longer in use.
func (r *Registry) Register(secret []byte) (unregister func()) {
	s := bytes.TrimSpace(secret)
	if len(s) < MinSecretLen {
		return func() {}
	}
	cp := bytes.Clone(s)
	r.mu.Lock()
	id := r.next
	r.next++
	r.secrets[id] = cp
	r.rebuildLocked()
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			delete(r.secrets, id)
			r.rebuildLocked()
			r.mu.Unlock()
			clear(cp)
		})
	}
}

// Len returns the number of registered secrets.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.secrets)
}

// Scrub replaces every registered secret in s with Redacted.
func (r *Registry) Scrub(s string) string {
	if r == nil || s == "" {
		return s
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	// Work on bytes so secrets are never copied into immutable strings.
	b := []byte(s)
	changed := false
	for _, secret := range r.ordered {
		if bytes.Contains(b, secret) {
			b = bytes.ReplaceAll(b, secret, redactedBytes)
			changed = true
		}
	}
	if !changed {
		return s
	}
	return string(b)
}

var redactedBytes = []byte(Redacted)

func (r *Registry) rebuildLocked() {
	r.ordered = r.ordered[:0]
	for _, secret := range r.secrets {
		r.ordered = append(r.ordered, secret)
	}
	slices.SortFunc(r.ordered, func(a, b []byte) int { return len(b) - len(a) })
}
