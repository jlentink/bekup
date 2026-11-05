package storage

import (
	"errors"
	"math"
	"runtime"
	"testing"
)

func TestBlockSpace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		got     func() (Space, error)
		want    Space
		wantErr bool
	}{
		{"linux types", func() (Space, error) { return BlockSpace(uint64(100), uint64(40), uint64(30), int64(4096)) },
			Space{Total: 409600, Free: 122880, Used: 245760, TotalKnown: true, FreeKnown: true, UsedKnown: true}, false},
		{"darwin types", func() (Space, error) { return BlockSpace(uint64(10), uint64(5), uint64(5), uint32(512)) },
			Space{Total: 5120, Free: 2560, Used: 2560, TotalKnown: true, FreeKnown: true, UsedKnown: true}, false},
		{"freebsd types", func() (Space, error) { return BlockSpace(uint64(10), uint64(5), int64(4), uint64(1024)) },
			Space{Total: 10240, Free: 4096, Used: 5120, TotalKnown: true, FreeKnown: true, UsedKnown: true}, false},
		{"empty file system", func() (Space, error) { return BlockSpace(uint64(0), uint64(0), uint64(0), int64(4096)) },
			Space{TotalKnown: true, FreeKnown: true, UsedKnown: true}, false},
		{"capped at MaxInt64", func() (Space, error) {
			return BlockSpace(uint64(math.MaxUint64), uint64(math.MaxUint64), uint64(math.MaxUint64), int64(4096))
		}, Space{Total: math.MaxInt64, Free: math.MaxInt64, Used: 0, TotalKnown: true, FreeKnown: true, UsedKnown: true}, false},
		{"zero block size", func() (Space, error) { return BlockSpace(uint64(10), uint64(5), uint64(5), int64(0)) }, Space{}, true},
		{"negative available", func() (Space, error) { return BlockSpace(uint64(10), uint64(5), int64(-1), uint64(512)) }, Space{}, true},
		{"more free than total", func() (Space, error) { return BlockSpace(uint64(10), uint64(11), uint64(5), int64(512)) }, Space{}, true},
		{"more available than total", func() (Space, error) { return BlockSpace(uint64(10), uint64(5), uint64(11), int64(512)) }, Space{}, true},
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
			if err != nil || got != tt.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestDiskSpace(t *testing.T) {
	t.Parallel()
	d, err := OpenDisk(DiskConfig{Root: t.TempDir(), Dir: "dest"})
	if err != nil {
		t.Fatal(err)
	}
	var r SpaceReporter = d
	sp, err := r.Space(t.Context())
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("err = %v, want ErrUnsupported", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if !sp.TotalKnown || !sp.FreeKnown || !sp.UsedKnown || !sp.Known() {
		t.Fatalf("space %+v: not every value known", sp)
	}
	if sp.Total <= 0 || sp.Free < 0 || sp.Free > sp.Total || sp.Used < 0 || sp.Used > sp.Total {
		t.Fatalf("implausible space %+v", sp)
	}
	free, err := d.FreeSpace()
	if err != nil {
		t.Fatal(err)
	}
	// Both read the same file system; allow for other processes writing
	// in between.
	if diff := int64(free) - sp.Free; diff > 1<<30 || diff < -(1<<30) { //nolint:gosec // G115: test values, far below MaxInt64
		t.Errorf("FreeSpace %d and Space.Free %d disagree", free, sp.Free)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Space(t.Context()); !errors.Is(err, ErrClosed) {
		t.Errorf("after Close: %v, want ErrClosed", err)
	}
}
