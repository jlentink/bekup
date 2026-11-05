package sftp

import (
	"context"
	"fmt"

	pkgsftp "github.com/pkg/sftp"

	"github.com/jlentink/bekup/internal/storage"
)

// extStatVFS is the OpenSSH extension that reports file system space.
const extStatVFS = "statvfs@openssh.com"

var _ storage.SpaceReporter = (*Destination)(nil)

// Space implements storage.SpaceReporter with the statvfs@openssh.com
// extension on the destination directory (D-225). A server that does not
// advertise the extension, or refuses the request as unsupported, yields
// storage.ErrUnsupported. The request is bounded by ctx: pkg/sftp sends it
// without a context, so a canceled ctx ends the session (the next
// operation reconnects).
func (d *Destination) Space(ctx context.Context) (storage.Space, error) {
	if d.closed.Load() {
		return storage.Space{}, storage.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return storage.Space{}, err
	}
	sess, err := d.session(ctx)
	if err != nil {
		return storage.Space{}, err
	}
	if !sess.statvfs {
		return storage.Space{}, fmt.Errorf("%w: the server does not offer %s", storage.ErrUnsupported, extStatVFS)
	}
	type result struct {
		st  *pkgsftp.StatVFS
		err error
	}
	ch := make(chan result, 1)
	go func() {
		st, err := sess.sftp.StatVFS(sess.path(""))
		ch <- result{st, err}
	}()
	var r result
	select {
	case r = <-ch:
	case <-ctx.Done():
		// Ends the pending request: the goroutine returns once the
		// connection is closed.
		d.abandon(sess)
		return storage.Space{}, ctx.Err()
	}
	if r.err != nil {
		return storage.Space{}, mapErr("sftp: statvfs", r.err)
	}
	bsize := r.st.Frsize // statvfs counts blocks in f_frsize units
	if bsize == 0 {
		bsize = r.st.Bsize
	}
	sp, err := storage.BlockSpace(r.st.Blocks, r.st.Bfree, r.st.Bavail, bsize)
	if err != nil {
		return storage.Space{}, storage.WithCode(storage.CodeInternal, fmt.Errorf("sftp: statvfs: %w", err))
	}
	return sp, nil
}

// abandon closes sess and forgets it if it is the current session.
func (d *Destination) abandon(sess *session) {
	d.mu.Lock()
	if d.cur == sess {
		d.cur = nil
	}
	d.mu.Unlock()
	discard(sess.close())
}
