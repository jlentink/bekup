package storage

import (
	"errors"
	"math"
	"testing"
)

func TestStatfsFree(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		got     func() (uint64, error)
		want    uint64
		wantErr bool
	}{
		{"linux amd64 types", func() (uint64, error) { return statfsFree(uint64(10), int64(4096)) }, 40960, false},
		{"darwin types", func() (uint64, error) { return statfsFree(uint64(3), uint32(512)) }, 1536, false},
		{"freebsd types", func() (uint64, error) { return statfsFree(int64(7), uint64(4096)) }, 28672, false},
		{"linux arm types", func() (uint64, error) { return statfsFree(uint64(2), int32(1024)) }, 2048, false},
		{"zero blocks", func() (uint64, error) { return statfsFree(uint64(0), int64(4096)) }, 0, false},
		{"zero block size", func() (uint64, error) { return statfsFree(uint64(10), int64(0)) }, 0, true},
		{"negative block size", func() (uint64, error) { return statfsFree(uint64(10), int64(-4096)) }, 0, true},
		{"negative int32 block size", func() (uint64, error) { return statfsFree(uint64(10), int32(math.MinInt32)) }, 0, true},
		{"negative blocks", func() (uint64, error) { return statfsFree(int64(-1), uint64(4096)) }, 0, true},
		{"max without overflow", func() (uint64, error) { return statfsFree(uint64(math.MaxUint64), uint32(1)) }, math.MaxUint64, false},
		{"overflow capped", func() (uint64, error) { return statfsFree(uint64(math.MaxUint64), int64(4096)) }, math.MaxUint64, false},
		{"overflow capped (int64 blocks)", func() (uint64, error) { return statfsFree(int64(math.MaxInt64), int64(math.MaxInt64)) }, math.MaxUint64, false},
		{"largest exact product", func() (uint64, error) { return statfsFree(uint64(1)<<52-1, int64(4096)) }, math.MaxUint64 - 4095, false},
		{"first overflowing product", func() (uint64, error) { return statfsFree(uint64(1)<<52, int64(4096)) }, math.MaxUint64, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.got()
			if tt.wantErr {
				if !errors.Is(err, errStatfsValues) {
					t.Fatalf("err = %v, want errStatfsValues", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}
