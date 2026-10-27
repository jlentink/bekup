package auth

import (
	"testing"
	"time"
)

func TestLockoutBackoff(t *testing.T) {
	p := LockoutPolicy{Threshold: 5, Base: time.Minute, Max: time.Hour}
	want := map[int]time.Duration{
		0: 0, 1: 0, 4: 0,
		5: time.Minute, 6: 2 * time.Minute, 7: 4 * time.Minute, 8: 8 * time.Minute,
		9: 16 * time.Minute, 10: 32 * time.Minute, 11: time.Hour, 12: time.Hour, 1000: time.Hour,
	}
	for n, w := range want {
		if got := p.LockFor(n); got != w {
			t.Errorf("LockFor(%d) = %s, want %s", n, got, w)
		}
	}
	// Defaults for a zero policy.
	if got := (LockoutPolicy{}).LockFor(DefaultLockoutThreshold); got != DefaultLockoutBase {
		t.Errorf("zero policy LockFor = %s", got)
	}
	// Max below Base is raised to Base.
	if got := (LockoutPolicy{Threshold: 3, Base: time.Hour, Max: time.Minute}).LockFor(10); got != time.Hour {
		t.Errorf("Max < Base: %s", got)
	}
}
