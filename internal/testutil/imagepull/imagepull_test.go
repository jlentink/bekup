package imagepull

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// errRate is the daemon's answer from CI, as the moby client returns it.
var errRate = errors.New("error response from daemon: toomanyrequests: retry-after: 607.907µs, allowed: 44000/minute")

func TestIsRateLimited(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errRate, true},
		{errors.New("toomanyrequests: you have reached your pull rate limit"), true},
		{errors.New("unexpected status: 429 Too Many Requests"), true},
		{errors.New("manifest unknown"), false},
		{errors.New("unauthorized: authentication required"), false},
	} {
		if got := IsRateLimited(tt.err); got != tt.want {
			t.Errorf("IsRateLimited(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestDelay(t *testing.T) {
	p := Policy{Attempts: 7, Base: 5 * time.Second, Max: time.Minute}
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, time.Minute, time.Minute}
	for i, w := range want {
		if got := p.delay(i + 1); got != w {
			t.Errorf("delay(%d) = %v, want %v", i+1, got, w)
		}
	}
}

func TestRetry(t *testing.T) {
	p := Policy{Attempts: 4, Base: time.Second, Max: 4 * time.Second}
	tests := []struct {
		name      string
		errs      []error
		wantPulls int
		wantErr   string
		wantSleep int
	}{
		{"first try", []error{nil}, 1, "", 0},
		{"rate limited then ok", []error{errRate, errRate, nil}, 3, "", 2},
		{"other error is final", []error{errRate, errors.New("manifest unknown")}, 2, "manifest unknown", 1},
		{"attempts used up", []error{errRate, errRate, errRate, errRate}, 4, "still rate limited after 4 attempts", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pulls := 0
			var slept []time.Duration
			err := Retry(t.Context(), p, func(context.Context) error {
				e := tt.errs[pulls]
				pulls++
				return e
			}, func(_ context.Context, d time.Duration) error {
				slept = append(slept, d)
				return nil
			})
			if pulls != tt.wantPulls || len(slept) != tt.wantSleep {
				t.Fatalf("pulls %d sleeps %d, want %d %d", pulls, len(slept), tt.wantPulls, tt.wantSleep)
			}
			if tt.wantErr == "" && err != nil || tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			for i, d := range slept {
				if base := p.delay(i + 1); d < base || d > base+base/2 {
					t.Fatalf("pause %d = %v, want %v..%v", i, d, base, base+base/2)
				}
			}
		})
	}
}

func TestRetryContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := Retry(ctx, Policy{Attempts: 3, Base: time.Hour, Max: time.Hour}, func(context.Context) error { return errRate }, nil)
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "toomanyrequests") {
		t.Fatalf("err = %v", err)
	}
}
