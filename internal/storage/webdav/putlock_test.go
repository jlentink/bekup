package webdav

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/storage"
)

// racyMove emulates Apache mod_dav's MOVE: "Overwrite: F" is checked,
// then the file is renamed in a second step, so concurrent moves onto one
// key can all pass the check and replace each other.
func racyMove(next http.Handler, s *testServer) http.Handler {
	path := func(p string) (string, bool) {
		rel, ok := strings.CutPrefix(p, "/dav/")
		if !ok {
			return "", false
		}
		rel, err := url.PathUnescape(rel)
		if err != nil {
			return "", false
		}
		return filepath.Join(s.root, filepath.FromSlash(rel)), true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "MOVE" || r.Header.Get("Overwrite") != "F" {
			next.ServeHTTP(w, r)
			return
		}
		u, err := url.Parse(r.Header.Get("Destination"))
		src, okSrc := path(r.URL.Path)
		dst, okDst := "", false
		if err == nil {
			dst, okDst = path(u.Path)
		}
		if !okSrc || !okDst {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, err := os.Stat(dst); err == nil {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		time.Sleep(20 * time.Millisecond) // the window between check and rename
		if err := os.Rename(src, dst); err != nil {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
}

// TestConcurrentPutsRacyServer: same-key Puts within one process take
// turns, so a server whose MOVE checks and renames in two steps still
// stores the key once (D-222 amendment 2), also across destinations
// opened separately.
func TestConcurrentPutsRacyServer(t *testing.T) {
	s := startServer(t, racyMove)
	a, b := open(t, s.config(t, "racy")), open(t, s.config(t, "racy"))
	key := storage.MustParseKey("conc/same")
	var won, lost atomic.Int32
	var wg sync.WaitGroup
	for i := range byte(8) {
		d := a
		if i%2 == 1 {
			d = b
		}
		wg.Go(func() {
			_, err := d.Put(t.Context(), key, bytes.NewReader(bytes.Repeat([]byte{i}, 4<<10)), storage.PutOptions{})
			switch {
			case err == nil:
				won.Add(1)
			case errors.Is(err, storage.ErrAlreadyExists):
				lost.Add(1)
			default:
				t.Errorf("put: %v", err)
			}
		})
	}
	wg.Wait()
	if won.Load() != 1 || lost.Load() != 7 {
		t.Errorf("same-key puts: %d won, %d already existed", won.Load(), lost.Load())
	}
	if n := putLocks.size(); n != 0 {
		t.Errorf("%d put locks left", n)
	}
}

func TestKeyedLocks(t *testing.T) {
	var k = keyedLocks{m: map[string]*keyedLock{}}
	unlock, err := k.lock(t.Context(), "a")
	if err != nil {
		t.Fatal(err)
	}
	// Another name is independent.
	unlockB, err := k.lock(t.Context(), "b")
	if err != nil {
		t.Fatal(err)
	}
	unlockB()
	// The same name waits and gives up with its context.
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := k.lock(ctx, "a"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock = %v, want deadline exceeded", err)
	}
	got := make(chan struct{})
	go func() {
		u, err := k.lock(t.Context(), "a")
		if err != nil {
			t.Error(err)
			close(got)
			return
		}
		u()
		close(got)
	}()
	time.Sleep(10 * time.Millisecond)
	select {
	case <-got:
		t.Fatal("lock taken while held")
	default:
	}
	unlock()
	<-got
	if n := k.size(); n != 0 {
		t.Errorf("%d locks left, want 0", n)
	}
}

// lockingMkcol emulates ownCloud's folder locks: a MKCOL that finds the
// folder missing locks it while creating it, the loser of a concurrent
// creation keeps the lock a little longer, and a PUT into a locked folder
// answers 423 Locked.
func lockingMkcol(next http.Handler, s *testServer) http.Handler {
	var (
		mu     sync.Mutex
		locked = map[string]int{}
	)
	dir := func(p string) string {
		rel, _ := strings.CutPrefix(strings.TrimSuffix(p, "/"), "/dav/")
		return filepath.Join(s.root, filepath.FromSlash(rel))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "MKCOL":
			d := dir(r.URL.Path)
			if _, err := os.Stat(d); err == nil {
				next.ServeHTTP(w, r) // exists: 405, no lock
				return
			}
			mu.Lock()
			locked[d]++
			mu.Unlock()
			time.Sleep(10 * time.Millisecond) // the window between check and create
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			if rec.code == http.StatusMethodNotAllowed {
				time.Sleep(30 * time.Millisecond) // the loser still holds the lock
			}
			mu.Lock()
			locked[d]--
			mu.Unlock()
		case http.MethodPut:
			mu.Lock()
			n := locked[filepath.Dir(dir(r.URL.Path))]
			mu.Unlock()
			if n > 0 {
				w.WriteHeader(http.StatusLocked)
				return
			}
			next.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

// TestConcurrentPutsLockingMkcol: Puts into one new folder take turns at
// its MKCOL, so on a server that locks a folder while creating it (ownCloud,
// TransIP STACK) no PUT runs into that lock (423).
func TestConcurrentPutsLockingMkcol(t *testing.T) {
	s := startServer(t, lockingMkcol)
	d := open(t, s.config(t, "mkcol"))
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			k := storage.MustParseKey("conc/dir-" + string(rune('a'+i%3)) + "/obj-" + string(rune('a'+i)))
			if _, err := d.Put(t.Context(), k, bytes.NewReader(bytes.Repeat([]byte{byte(i)}, 4<<10)), storage.PutOptions{}); err != nil {
				t.Errorf("put %s: %v", k, err)
			}
		})
	}
	wg.Wait()
	if n := mkcolLocks.size(); n != 0 {
		t.Errorf("%d mkcol locks left", n)
	}
}
