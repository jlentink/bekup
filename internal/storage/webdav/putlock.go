package webdav

import (
	"context"
	"sync"
)

// putLocks serializes the Puts of one resource within this process
// (D-222 amendment 2). WebDAV has no exclusive create, and servers such
// as Apache mod_dav check "Overwrite: F" and rename in two steps, so
// concurrent MOVEs onto one key can all succeed and replace each other.
// Holding the resource's lock from the existence check to the MOVE makes
// every later Put of the key see the object and answer ErrAlreadyExists.
// The lock is per process and spans every Destination opened in it (keys
// are full resource URLs); across processes bekup never writes one key
// twice (artifact keys carry the run).
var putLocks = keyedLocks{m: map[string]*keyedLock{}}

// keyedLocks is a set of context-aware locks, each held in the map only
// while someone holds or waits for it.
type keyedLocks struct {
	mu sync.Mutex
	m  map[string]*keyedLock
}

type keyedLock struct {
	sem  chan struct{}
	refs int
}

// lock takes the lock of name or gives up with ctx; the returned func
// releases it.
func (k *keyedLocks) lock(ctx context.Context, name string) (func(), error) {
	k.mu.Lock()
	l := k.m[name]
	if l == nil {
		l = &keyedLock{sem: make(chan struct{}, 1)}
		k.m[name] = l
	}
	l.refs++
	k.mu.Unlock()
	unref := func() {
		k.mu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(k.m, name)
		}
		k.mu.Unlock()
	}
	release, err := acquire(ctx, l.sem)
	if err != nil {
		unref()
		return nil, err
	}
	return func() {
		release()
		unref()
	}, nil
}

// size reports the number of resources with a holder or waiter (tests).
func (k *keyedLocks) size() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.m)
}
