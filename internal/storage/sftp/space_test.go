package sftp

import (
	"errors"
	"testing"

	"github.com/jlentink/bekup/internal/storage"
)

// TestSpaceWithStatVFS: pkg/sftp's classic server offers
// statvfs@openssh.com and answers with the local file system's numbers.
func TestSpaceWithStatVFS(t *testing.T) {
	t.Parallel()
	srv := startServer(t)
	d := mustOpen(t, srv.keyConfig(t, t.TempDir()))
	sp, err := d.Space(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	if !sp.TotalKnown || !sp.FreeKnown || !sp.UsedKnown {
		t.Fatalf("space %+v: not every value known", sp)
	}
	if sp.Total <= 0 || sp.Free < 0 || sp.Free > sp.Total || sp.Used > sp.Total {
		t.Fatalf("implausible space %+v", sp)
	}
}

// TestSpaceWithoutStatVFS: a server without the extension reports nothing
// (ErrUnsupported, classified as unsupported), and no request is sent.
func TestSpaceWithoutStatVFS(t *testing.T) {
	t.Parallel()
	srv := startServer(t)
	d := mustOpen(t, srv.keyConfig(t, t.TempDir()))
	d.testNoStatVFS = true
	_, err := d.Space(ctxT(t))
	if !errors.Is(err, storage.ErrUnsupported) || storage.Classify(err) != storage.CodeUnsupported {
		t.Fatalf("err = %v (%s), want ErrUnsupported", err, storage.Classify(err))
	}
}

// TestSpaceRefusedAsUnsupported: a server that advertises the extension
// but answers OP_UNSUPPORTED (pkg/sftp's request server without a
// StatVFS handler, like many gateways) is unknown too.
func TestSpaceRefusedAsUnsupported(t *testing.T) {
	t.Parallel()
	g := newGatewayFS(0, false)
	srv := startServerWith(t, g.serve)
	d := mustOpen(t, srv.keyConfig(t, "/"))
	_, err := d.Space(ctxT(t))
	if !errors.Is(err, storage.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestSpaceClosed(t *testing.T) {
	t.Parallel()
	srv := startServer(t)
	d := mustOpen(t, srv.keyConfig(t, t.TempDir()))
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Space(ctxT(t)); !errors.Is(err, storage.ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}
