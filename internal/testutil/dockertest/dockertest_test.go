package dockertest

import (
	"testing"
)

// recorder captures Skipf/Fatalf without ending the calling goroutine.
type recorder struct {
	testing.TB
	skipped, failed bool
	msg             string
}

func (r *recorder) Helper() {}
func (r *recorder) Skipf(format string, _ ...any) {
	r.skipped, r.msg = true, format
}
func (r *recorder) Fatalf(format string, _ ...any) {
	r.failed, r.msg = true, format
}

func TestSkipf(t *testing.T) {
	tests := []struct {
		ci       string
		wantFail bool
	}{
		{"", false},
		{"false", false},
		{"0", false},
		{"garbage", false},
		{"true", true},
		{"1", true},
		{"TRUE", true},
	}
	for _, tc := range tests {
		t.Run("CI="+tc.ci, func(t *testing.T) {
			t.Setenv("CI", tc.ci)
			r := &recorder{TB: t}
			Skipf(r, "docker not available: %v", "boom")
			if r.failed != tc.wantFail || r.skipped == tc.wantFail {
				t.Fatalf("failed=%v skipped=%v, want failed=%v", r.failed, r.skipped, tc.wantFail)
			}
		})
	}
}
