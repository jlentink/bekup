package sftp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"path"
	"strings"
	"sync"
	"time"

	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/jlentink/bekup/internal/sshpolicy"
	"github.com/jlentink/bekup/internal/storage"
)

// SFTP extensions bekup uses when the server advertises them.
const (
	extHardlink = "hardlink@openssh.com"
	extFsync    = "fsync@openssh.com"
)

// session is one SSH connection with its SFTP client.
type session struct {
	ssh  *ssh.Client
	sftp *pkgsftp.Client
	// root is the resolved destination directory (BasePath resolved by
	// the server, plus Config.Dir); "" is the server's "/".
	root     string
	hardlink bool
	fsync    bool
	done     chan struct{} // closed when the SSH connection ended
	wg       sync.WaitGroup
	once     sync.Once
}

func (s *session) alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// close ends the connection and waits for its goroutines.
func (s *session) close() error {
	var err error
	s.once.Do(func() {
		serr := s.sftp.Close()
		cerr := s.ssh.Close()
		if serr != nil && !errors.Is(serr, net.ErrClosed) {
			err = serr
		} else if cerr != nil && !errors.Is(cerr, net.ErrClosed) {
			err = cerr
		}
	})
	s.wg.Wait()
	return err
}

// path returns the server path of a key or relative directory.
func (s *session) path(rel string) string {
	if rel == "" {
		if s.root == "" {
			return "/"
		}
		return s.root
	}
	return s.root + "/" + rel
}

// dial opens a new session: guarded TCP dial, SSH handshake with the
// pinned host key, SFTP init and base path resolution, all bounded by the
// dial timeout and ctx.
func (d *Destination) dial(ctx context.Context) (*session, error) {
	hctx, cancel := context.WithTimeout(ctx, d.s.dialTO)
	defer cancel()

	conn, err := d.s.guard.DialContext(hctx, "tcp", d.s.addr)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		return nil, dialError("dial", err)
	}
	if deadline, ok := hctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			discard(conn.Close())
			return nil, dialError("dial", err)
		}
	}
	stop := context.AfterFunc(hctx, func() { discard(conn.Close()) })
	sess, err := d.handshake(hctx, conn)
	aborted := !stop()
	if err == nil && aborted {
		discard(sess.close())
		err = hctx.Err()
	}
	if err != nil {
		discard(conn.Close())
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		if aborted {
			return nil, storage.WithCode(storage.CodeTimeout, fmt.Errorf("sftp: connect timed out: %w", context.DeadlineExceeded))
		}
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		discard(sess.close())
		return nil, dialError("dial", err)
	}
	sess.wg.Add(2)
	go d.waitLoop(sess)
	go d.keepaliveLoop(sess)
	return sess, nil
}

func (d *Destination) handshake(ctx context.Context, conn net.Conn) (*session, error) {
	cfg, check := sshpolicy.ClientConfig(d.s.user, []ssh.AuthMethod{d.creds.method()}, d.s.pin, d.s.addr)
	cc, err := sshpolicy.Handshake(ctx, conn, d.s.addr, cfg)
	if err != nil {
		if v := check.Violation(err); v != nil {
			if errors.Is(v, sshpolicy.ErrHostKeyMismatch) {
				// A security event (possible impersonation), not an
				// ordinary permission problem.
				return nil, storage.WithCode(storage.CodeHostKeyMismatch, fmt.Errorf("sftp: %w", v))
			}
			return nil, storage.WithCode(storage.CodeUnsupported, fmt.Errorf("sftp: %w", v))
		}
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return nil, err
		}
		return nil, dialError("handshake", err)
	}
	sc, chans, reqs := cc.Conn, cc.Chans, cc.Reqs
	client := ssh.NewClient(sc, chans, reqs)
	sc2, err := pkgsftp.NewClient(client,
		pkgsftp.MaxConcurrentRequestsPerFile(d.s.maxRequests),
		pkgsftp.UseConcurrentWrites(true),
		pkgsftp.UseConcurrentReads(true),
		pkgsftp.UseFstat(true),
	)
	if err != nil {
		discard(client.Close())
		return nil, mapErr("sftp: start subsystem", err)
	}
	sess := &session{ssh: client, sftp: sc2, done: make(chan struct{})}
	_, sess.hardlink = sc2.HasExtension(extHardlink)
	sess.hardlink = sess.hardlink && !d.testNoHardlink
	if v, ok := sc2.HasExtension(extFsync); ok && v == "1" {
		sess.fsync = true
	}
	if err := d.resolveRoot(sess); err != nil {
		discard(sc2.Close())
		discard(client.Close())
		return nil, err
	}
	if !sess.hardlink {
		d.s.logger.WarnContext(ctx, "sftp_no_hardlink_extension", slog.String("addr", d.s.addr))
	}
	return sess, nil
}

// resolveRoot resolves the base path and creates Config.Dir below it.
func (d *Destination) resolveRoot(sess *session) error {
	rp, err := sess.sftp.RealPath(d.s.basePath)
	if err != nil {
		return mapErr("sftp: resolve base path", err)
	}
	if rp == "" || rp[0] != '/' || path.Clean(rp) != rp {
		return storage.WithCode(storage.CodeInternal, errors.New("sftp: the server returned a non-canonical base path"))
	}
	fi, err := sess.sftp.Stat(rp)
	if err != nil {
		return mapErr("sftp: stat base path", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%w: the base path is not a directory", storage.ErrNotFound)
	}
	if rp == "/" {
		rp = ""
	}
	sess.root = rp
	if !d.s.dir.IsZero() {
		if err := mkdirs(sess, d.s.dir.String()); err != nil {
			return err
		}
		sess.root = sess.path(d.s.dir.String())
	}
	return nil
}

// waitLoop marks the session dead when the SSH connection ends.
func (d *Destination) waitLoop(sess *session) {
	defer sess.wg.Done()
	discard(sess.ssh.Wait())
	close(sess.done)
}

// keepaliveLoop sends keepalive@openssh.com requests (one in flight) and
// closes the connection after sshpolicy.DefaultKeepaliveMisses intervals
// without a reply.
func (d *Destination) keepaliveLoop(sess *session) {
	defer sess.wg.Done()
	if sshpolicy.Keepalive(sess.done, sess.ssh, d.s.keepalive, &sess.wg) {
		d.s.logger.Warn("sftp_keepalive_timeout", slog.String("addr", d.s.addr))
		discard(sess.ssh.Close())
	}
}

// errNotDir reports a path component that exists but is not a real
// directory (for example a symbolic link planted on the server).
var errNotDir = fmt.Errorf("%w: a path component is not a directory", storage.ErrPermission)

// mkdirs creates the relative directory rel below the session root
// (mkdir -p, 0700), refusing any component that exists but is not a real
// directory (for example a symbolic link).
func mkdirs(sess *session, rel string) error {
	cur := ""
	for seg := range strings.SplitSeq(rel, "/") {
		if cur == "" {
			cur = seg
		} else {
			cur += "/" + seg
		}
		p := sess.path(cur)
		fi, err := sess.sftp.Lstat(p)
		if err == nil {
			if !fi.IsDir() {
				return errNotDir
			}
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return mapErr("sftp: stat directory", err)
		}
		if err := sess.sftp.Mkdir(p); err != nil {
			// A concurrent writer may have created it.
			fi, lerr := sess.sftp.Lstat(p)
			if lerr != nil || !fi.IsDir() {
				return mapErr("sftp: create directory", err)
			}
			continue
		}
		if err := sess.sftp.Chmod(p, 0o700); err != nil {
			return mapErr("sftp: chmod directory", err)
		}
	}
	return nil
}

// checkDirs verifies that every component of the relative directory rel is
// a real directory (Lstat), never a symbolic link.
func checkDirs(sess *session, rel string) error {
	if rel == "" {
		return nil
	}
	cur := ""
	for seg := range strings.SplitSeq(rel, "/") {
		if cur == "" {
			cur = seg
		} else {
			cur += "/" + seg
		}
		fi, err := sess.sftp.Lstat(sess.path(cur))
		if err != nil {
			return mapErr("sftp: stat directory", err)
		}
		if !fi.IsDir() {
			return errNotDir
		}
	}
	return nil
}

func discard(error) {}
