package auth

import (
	"sync"
	"testing"
	"time"
)

func TestLimits(t *testing.T) {
	h := NewLimits(PasswordPolicy{MinLength: 1, MinScore: 0}, LockoutPolicy{})
	if p := h.Policy(); p.MinLength != MinPasswordLength || p.MinScore != MinPasswordScore {
		t.Fatalf("bounds not applied: %+v", p)
	}
	if l := h.Lockout(); l.Threshold != DefaultLockoutThreshold || l.Base != DefaultLockoutBase {
		t.Fatalf("defaults not applied: %+v", l)
	}
	tests := []struct {
		name string
		p    PasswordPolicy
		l    LockoutPolicy
		ok   bool
	}{
		{"valid", PasswordPolicy{MinLength: 20, MinScore: 4}, LockoutPolicy{Threshold: 3, Base: time.Second, Max: time.Minute}, true},
		{"no threshold", PasswordPolicy{}, LockoutPolicy{Threshold: 0, Base: time.Second, Max: time.Minute}, false},
		{"no base", PasswordPolicy{}, LockoutPolicy{Threshold: 3, Max: time.Minute}, false},
		{"max below base", PasswordPolicy{}, LockoutPolicy{Threshold: 3, Base: time.Hour, Max: time.Minute}, false},
		{"length too high", PasswordPolicy{MinLength: MaxPasswordLength + 1}, LockoutPolicy{Threshold: 3, Base: time.Second, Max: time.Minute}, false},
		{"score too high", PasswordPolicy{MinScore: 5}, LockoutPolicy{Threshold: 3, Base: time.Second, Max: time.Minute}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := h.Policy()
			commit, err := h.Prepare(tt.p, tt.l)
			if (err == nil) != tt.ok {
				t.Fatalf("Prepare err = %v, want ok=%v", err, tt.ok)
			}
			if h.Policy() != before {
				t.Fatal("Prepare applied before the commit")
			}
			if !tt.ok {
				return
			}
			commit()
			if h.Policy() != NewPasswordPolicy(tt.p.MinLength, tt.p.MinScore) || h.Lockout() != tt.l {
				t.Fatalf("commit: %+v %+v", h.Policy(), h.Lockout())
			}
		})
	}
}

// TestLimitsShared: one holder feeds every service (a reload swaps them
// together) and concurrent reads are race-free.
func TestLimitsShared(t *testing.T) {
	h := NewLimits(PasswordPolicy{}, LockoutPolicy{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 1000 {
			if err := h.Policy().Check("x"); err == nil {
				t.Error("a one-character password passed")
			}
			_ = h.Lockout().LockFor(7)
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 1000 {
			commit, err := h.Prepare(PasswordPolicy{MinLength: 12 + i%5, MinScore: 3}, LockoutPolicy{Threshold: 3 + i%3, Base: time.Second, Max: time.Hour})
			if err != nil {
				t.Error(err)
				return
			}
			commit()
		}
	}()
	wg.Wait()
}
