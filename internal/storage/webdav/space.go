package webdav

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jlentink/bekup/internal/storage"
)

var _ storage.SpaceReporter = (*Destination)(nil)

// Space implements storage.SpaceReporter with the RFC 4331 quota
// properties (quota-available-bytes, quota-used-bytes) of the
// destination's folder, asked with one PROPFIND Depth 0 (D-225). Before
// the first upload created the folder, its existing base is asked
// instead. Nextcloud and ownCloud report the account quota; a server
// without the properties (or with only negative values, Nextcloud's
// "unlimited" and "unknown") yields storage.ErrUnsupported. Total is
// used + available when both are known.
func (d *Destination) Space(ctx context.Context) (storage.Space, error) {
	const op = "read the quota"
	if d.closed.Load() {
		return storage.Space{}, storage.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return storage.Space{}, err
	}
	lay, err := d.layout(ctx)
	if err != nil {
		return storage.Space{}, err
	}
	segs := lay.files
	entries, err := d.propfindProps(ctx, op, segs, true, "0", quotaBody)
	if errors.Is(err, storage.ErrNotFound) && len(segs) > lay.floor {
		segs = segs[:lay.floor]
		entries, err = d.propfindProps(ctx, op, segs, true, "0", quotaBody)
	}
	if err != nil {
		return storage.Space{}, err
	}
	_, self := children(entries, segs)
	if self == nil && len(entries) == 1 {
		self = &entries[0] // named differently (escaping, case): still the one asked for
	}
	if self == nil {
		return storage.Space{}, storage.WithDetail(storage.WithCode(storage.CodeInternal, fmt.Errorf("webdav: %s: no entry for the folder", op)),
			op+": the server's answer did not describe the folder")
	}
	return quotaSpace(self)
}

// quotaSpace turns the quota properties of an entry into a Space.
func quotaSpace(e *entry) (storage.Space, error) {
	var sp storage.Space
	if e.quotaFree >= 0 {
		sp.Free, sp.FreeKnown = e.quotaFree, true
	}
	if e.quotaUsed >= 0 {
		sp.Used, sp.UsedKnown = e.quotaUsed, true
	}
	if sp.FreeKnown && sp.UsedKnown {
		sp.Total, sp.TotalKnown = sp.Free+sp.Used, true
		if sp.Total < 0 { // overflow
			sp.Total = math.MaxInt64
		}
	}
	if !sp.Known() {
		return storage.Space{}, fmt.Errorf("%w: the server reports no quota", storage.ErrUnsupported)
	}
	return sp, nil
}
