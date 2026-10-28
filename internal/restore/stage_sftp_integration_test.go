//go:build integration

package restore_test

// Staging from an SFTP destination (task 4.1 coverage): the artifact and
// its signed manifest live on a real OpenSSH server (atmoz/sftp, the
// docker-compose.dev.yml image, through internal/storage/sftp/sftptest);
// the staged dump is byte-identical, and an object swapped on the server
// is refused by the verification.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/sftp"
	"github.com/jlentink/bekup/internal/storage/sftp/sftptest"
	"github.com/jlentink/bekup/internal/testutil/dockertest"
)

// sftpDest is an SFTP destination of the test server.
type sftpDest struct {
	srv  *sftptest.Server
	dir  string
	open func() (storage.Destination, error)
}

// startSFTPDest starts an atmoz/sftp server and returns an opener for the
// destination directory dir below its base path.
func startSFTPDest(t *testing.T, dir string) *sftpDest {
	t.Helper()
	srv, err := sftptest.Start(context.Background())
	if err != nil {
		dockertest.Skipf(t, "start atmoz/sftp: %v", err)
	}
	t.Cleanup(func() {
		if err := srv.Terminate(context.Background()); err != nil {
			t.Logf("terminate atmoz/sftp: %v", err)
		}
	})
	guard, err := netguard.New(netguard.Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.1/32"}})
	if err != nil {
		t.Fatal(err)
	}
	return &sftpDest{srv: srv, dir: dir, open: func() (storage.Destination, error) {
		return sftp.Open(sftp.Config{
			Host: srv.Host, Port: srv.Port, User: sftptest.User,
			Auth:       sftp.Auth{PrivateKey: crypto.NewSecret(bytes.Clone(srv.ClientKeyPEM))},
			HostKeyPin: sftp.HostKeyPin{AuthorizedKey: srv.HostKeyAuthorized()},
			BasePath:   sftptest.BasePath, Dir: dir, Guard: guard,
		})
	}}
}

// add registers a destination (safe while workers open others).
func (o *opener) add(id uuid.UUID, open func() (storage.Destination, error)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.open[id] = open
}

// corrupt changes one byte of the stored object k on the server (as root in
// the container: the SFTP backend itself never overwrites).
func (s *sftpDest) corrupt(t *testing.T, k storage.Key, offset int) {
	t.Helper()
	path := sftptest.FSUpload + "/" + s.dir + "/" + k.String()
	// A different byte: "A", or "B" where the byte is an "A".
	script := fmt.Sprintf(`set -e; b=$(dd if=%[1]q bs=1 skip=%[2]d count=1 2>/dev/null); c=A; [ "$b" = A ] && c=B; `+
		`printf %%s "$c" | dd of=%[1]q bs=1 seek=%[2]d conv=notrunc 2>/dev/null`, path, offset)
	code, r, err := s.srv.Container.Exec(t.Context(), []string{"sh", "-c", script})
	if err != nil {
		t.Fatal(err)
	}
	out, rerr := io.ReadAll(r)
	if code != 0 || rerr != nil {
		t.Fatalf("corrupt %s: exit %d (%v): %s", path, code, rerr, out)
	}
}

func TestStageFromSFTP(t *testing.T) {
	s := startSFTPDest(t, "restores")
	e := newEnv(t)
	sftpID := e.destRow("sftp")
	e.op.add(sftpID, s.open)
	dest, err := s.open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := dest.Close(); err != nil {
			t.Error(err)
		}
	}()
	j := e.job(jobkeys.ModePassphrase, sftpID)
	dump := randomDump(3 << 20)
	sd := e.seed(j, sftpID, dest, dump, time.Now())
	id := e.mustCreate(j, sd.artifact, "")
	st, err := e.svc.Stage(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAllOpen(t, st); !bytes.Equal(got, dump) {
		t.Fatal("dump differs")
	}
	st.Cleanup()
	if len(e.scratchEntries()) != 0 {
		t.Fatal("not cleaned up")
	}

	// A byte changed on the server is refused.
	s.corrupt(t, sd.key, len(sd.stored)/2)
	id2 := e.mustCreate(j, sd.artifact, "")
	if _, err := e.svc.Stage(t.Context(), id2); !errors.Is(err, restore.ErrVerification) {
		t.Fatalf("tampered SFTP object: %v", err)
	}
	checkFailed(t, e, id2, sd.artifact, restore.CodeVerification)
}
