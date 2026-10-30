package storage

import (
	"errors"
	"math"
	"math/bits"
)

// statfsInt covers the integer types statfs uses for block counts and
// sizes across the supported platforms.
type statfsInt interface {
	~int32 | ~int64 | ~uint32 | ~uint64
}

// errStatfsValues reports values a sane file system never returns.
var errStatfsValues = errors.New("storage: statfs: invalid block size or count")

// statfsFree returns bavail × bsize in bytes. A non-positive block size or
// a negative block count is refused; a product beyond uint64 is capped at
// math.MaxUint64 (the free space check then passes, the write itself still
// fails on ENOSPC).
func statfsFree[A, B statfsInt](bavail A, bsize B) (uint64, error) {
	if bsize <= 0 || bavail < 0 {
		return 0, errStatfsValues
	}
	// Both values are non-negative here, so the conversions are exact.
	hi, lo := bits.Mul64(uint64(bavail), uint64(bsize))
	if hi != 0 {
		return math.MaxUint64, nil
	}
	return lo, nil
}
