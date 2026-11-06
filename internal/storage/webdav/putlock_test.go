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
