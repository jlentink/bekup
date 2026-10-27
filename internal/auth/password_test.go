package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// weak are cheap parameters for tests (WeakForTests).
var weak = Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1}

func weakHasher(t testing.TB, p Argon2Params, maxConcurrent int) *Hasher {
	t.Helper()
	h, err := NewHasher(HasherOptions{Params: p, MaxConcurrent: maxConcurrent, WeakForTests: true})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestNewHasherRefusesWeakParams(t *testing.T) {
	for _, p := range []Argon2Params{
		{MemoryKiB: 32 * 1024, Iterations: 3, Parallelism: 1},
		{MemoryKiB: 64 * 1024, Iterations: 2, Parallelism: 1},
		{MemoryKiB: 64 * 1024, Iterations: 3, Parallelism: 0},
		{MemoryKiB: 8 * 1024 * 1024, Iterations: 3, Parallelism: 1},
		{MemoryKiB: 64 * 1024, Iterations: 65, Parallelism: 1},
	} {
		if _, err := NewHasher(HasherOptions{Params: p, MaxConcurrent: 1}); err == nil {
			t.Errorf("NewHasher(%+v) accepted", p)
		}
	}
	if _, err := NewHasher(HasherOptions{Params: weak, MaxConcurrent: 0, WeakForTests: true}); err == nil {
		t.Error("MaxConcurrent 0 accepted")
	}
}

func TestHashVerify(t *testing.T) {
	ctx := t.Context()
	h := weakHasher(t, weak, 2)
	enc, err := h.Hash(ctx, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("unexpected PHC string %q", enc)
	}
	ok, rehash, err := h.Verify(ctx, "correct horse battery staple", enc)
	if err != nil || !ok || rehash {
		t.Fatalf("Verify(right) = %v %v %v", ok, rehash, err)
	}
	ok, _, err = h.Verify(ctx, "correct horse battery stapler", enc)
	if err != nil || ok {
		t.Fatalf("Verify(wrong) = %v %v", ok, err)
	}
	enc2, err := h.Hash(ctx, "correct horse battery staple")
	if err != nil || enc2 == enc {
		t.Fatalf("hashes must be salted: %v", err)
	}
}

func TestVerifyRehashOnParamChange(t *testing.T) {
	ctx := t.Context()
	old := weakHasher(t, weak, 1)
	enc, err := old.Hash(ctx, "pw")
	if err != nil {
		t.Fatal(err)
	}
	stronger := weakHasher(t, Argon2Params{MemoryKiB: 128, Iterations: 2, Parallelism: 1}, 1)
	ok, rehash, err := stronger.Verify(ctx, "pw", enc)
	if err != nil || !ok || !rehash {
		t.Fatalf("Verify with new params = ok %v rehash %v err %v; want ok and rehash", ok, rehash, err)
	}
	// A wrong password never asks for a rehash.
	if ok, rehash, err := stronger.Verify(ctx, "nope", enc); ok || rehash || err != nil {
		t.Fatal("wrong password reported ok/rehash")
	}
}

func TestVerifyOverlongNeverMatches(t *testing.T) {
	ctx := t.Context()
	h := weakHasher(t, weak, 1)
	long := strings.Repeat("a", MaxPasswordBytes)
	stored, err := h.Hash(ctx, long)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, err := h.Verify(ctx, long+"a", stored); ok || err != nil {
		t.Fatalf("an over-long password must not match its truncated prefix: %v %v", ok, err)
	}
	if _, err := h.Hash(ctx, long+"a"); err == nil {
		t.Fatal("Hash accepted an over-long password")
	}
}

func TestParsePHC(t *testing.T) {
	good := "$argon2id$v=19$m=65536,t=3,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g"
	if _, err := parsePHC(good); err != nil {
		t.Fatalf("parsePHC(good): %v", err)
	}
	for _, bad := range []string{
		"",
		"$argon2i$v=19$m=65536,t=3,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g",
		"$argon2id$v=16$m=65536,t=3,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g",
		"$argon2id$v=19$t=3,m=65536,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g",
		"$argon2id$v=19$m=9999999999,t=3,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g",
		"$argon2id$v=19$m=65536,t=1000,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g",
		"$argon2id$v=19$m=65536,t=3,p=0$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g",
		"$argon2id$v=19$m=065536,t=3,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g",
		"$argon2id$v=19$m=65536,t=3,p=1$c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g",
		"$argon2id$v=19$m=65536,t=3,p=1$c2FsdHNhbHRzYWx0c2FsdA==$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g",
		"$argon2id$v=19$m=65536,t=3,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g$",
	} {
		if _, err := parsePHC(bad); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("parsePHC(%q) = %v, want ErrInvalidHash", bad, err)
		}
	}
	h := weakHasher(t, weak, 1)
	if ok, _, err := h.Verify(t.Context(), "pw", "plain"); ok || !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("Verify(invalid hash) = %v %v", ok, err)
	}
}

func FuzzParsePHC(f *testing.F) {
	f.Add("$argon2id$v=19$m=65536,t=3,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g")
	f.Add("$argon2id$v=19$m=1,t=1,p=1$$")
	f.Fuzz(func(t *testing.T, s string) {
		p, err := parsePHC(s)
		if err != nil {
			return
		}
		if p.params.MemoryKiB > maxParseMemoryKiB || p.params.Iterations > maxParseIterations || p.params.Parallelism < 1 ||
			len(p.salt) < minParseSaltLen || len(p.key) < minParseKeyLen {
			t.Fatalf("parsePHC accepted out-of-bounds %q", s)
		}
	})
}

// TestSemaphoreBound checks that at most MaxConcurrent hashes run at once
// and that callers beyond the bounded wait (or the waiter cap) get ErrBusy
// instead of queueing.
func TestSemaphoreBound(t *testing.T) {
	h, err := NewHasher(HasherOptions{Params: weak, MaxConcurrent: 2, Wait: 50 * time.Millisecond, MaxWaiters: 1, WeakForTests: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	// Take both slots directly.
	r1, err := h.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := h.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// One waiter is allowed and times out after Wait.
	start := time.Now()
	var wg sync.WaitGroup
	var waitErr error
	wg.Go(func() { _, waitErr = h.Hash(ctx, "x") })
	// Give it time to register as a waiter, then a second caller exceeds
	// MaxWaiters and fails at once.
	deadline := time.Now().Add(time.Second)
	for h.waiters.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, err := h.Hash(ctx, "y"); !errors.Is(err, ErrBusy) {
		t.Fatalf("caller beyond MaxWaiters: %v, want ErrBusy", err)
	}
	wg.Wait()
	if !errors.Is(waitErr, ErrBusy) {
		t.Fatalf("waiter: %v, want ErrBusy", waitErr)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatal("waiter did not wait")
	}
	if _, _, err := h.Verify(ctx, "x", h.dummy); !errors.Is(err, ErrBusy) {
		t.Fatalf("Verify with full semaphore: %v, want ErrBusy", err)
	}
	r1()
	if _, err := h.Hash(ctx, "x"); err != nil {
		t.Fatalf("after release: %v", err)
	}
	r2()

	// A canceled context stops the wait.
	if r1, err = h.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if r2, err = h.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := h.Hash(cctx, "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled wait: %v", err)
	}
	r1()
	r2()
}

// TestSemaphoreConcurrency runs many hashes and checks the observed
// concurrency never exceeds the bound.
func TestSemaphoreConcurrency(t *testing.T) {
	h, err := NewHasher(HasherOptions{Params: weak, MaxConcurrent: 3, Wait: 10 * time.Second, MaxWaiters: 100, WeakForTests: true})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	maxSeen := 0
	for range 30 {
		wg.Go(func() {
			if _, err := h.Hash(t.Context(), "pw"); err != nil {
				t.Error(err)
			}
			mu.Lock()
			if n := len(h.sem); n > maxSeen {
				maxSeen = n
			}
			mu.Unlock()
		})
	}
	wg.Wait()
	if maxSeen > 3 {
		t.Fatalf("observed %d concurrent hashes, bound is 3", maxSeen)
	}
}

func TestDummyVerifyCounts(t *testing.T) {
	h := weakHasher(t, weak, 1)
	if err := h.DummyVerify(t.Context(), "whatever"); err != nil {
		t.Fatal(err)
	}
	if err := h.DummyVerify(t.Context(), strings.Repeat("x", 2*MaxPasswordBytes)); err != nil {
		t.Fatal(err)
	}
	if got := h.DummyVerifications(); got != 2 {
		t.Fatalf("DummyVerifications = %d, want 2", got)
	}
}
