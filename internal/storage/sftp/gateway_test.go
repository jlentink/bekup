package sftp

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	pkgsftp "github.com/pkg/sftp"

	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/storagetest"
)

// gatewayFS is an in-memory SFTP file system (pkg/sftp's request server
// over InMemHandler) that behaves like a cloud-backed SFTP gateway such as
// TransIP STACK: a stat of a file that is open for writing (FSTAT on the
// handle) reports size 0, and after the close the first staleLstats path
// stats still report size 0 (the upload is committed asynchronously).
// With truncate set, the close really keeps only half of the written
// bytes. With zeroReadFstat set, FSTAT on a read handle also reports size
// 0 and another modification time. onOpenRead runs before a file is
// opened for reading; readSize (if set) cuts or extends the read stream.
type gatewayFS struct {
	mem           pkgsftp.Handlers
	staleLstats   int
	truncate      bool
	zeroReadFstat bool
	onOpenRead    func(path string)
	readSize      func(size int64) int64

	mu      sync.Mutex
	open    map[string]int // open write handles per path
	reading map[string]int // open read handles per path
	stale   map[string]int // remaining stale path stats per closed path
	lstats  map[string]int // path stats per partial file
	fstatsZ int            // stats of open files answered with size 0
}

func newGatewayFS(staleLstats int, truncate bool) *gatewayFS {
	return &gatewayFS{
		mem: pkgsftp.InMemHandler(), staleLstats: staleLstats, truncate: truncate,
		open: map[string]int{}, reading: map[string]int{}, stale: map[string]int{}, lstats: map[string]int{},
	}
}

func (g *gatewayFS) handlers() pkgsftp.Handlers {
	return pkgsftp.Handlers{FileGet: g, FilePut: g, FileCmd: g, FileList: g}
}

// Fileread implements pkgsftp.FileReader.
func (g *gatewayFS) Fileread(r *pkgsftp.Request) (io.ReaderAt, error) {
	if g.onOpenRead != nil {
		g.onOpenRead(r.Filepath)
	}
	ra, err := g.mem.FileGet.Fileread(r)
	if err != nil {
		return nil, err
	}
	limit := int64(-1)
	if g.readSize != nil {
		fis := make([]os.FileInfo, 1)
		l, err := g.mem.FileList.Filelist(pkgsftp.NewRequest("Stat", r.Filepath))
		if err != nil {
			return nil, err
		}
		if n, err := l.ListAt(fis, 0); n != 1 {
			return nil, errors.Join(errors.New("gateway fake: no entry"), err)
		}
		limit = g.readSize(fis[0].Size())
	}
	g.mu.Lock()
	g.reading[r.Filepath]++
	g.mu.Unlock()
	return &gatewayReader{ReaderAt: ra, g: g, path: r.Filepath, limit: limit}, nil
}

type gatewayReader struct {
	io.ReaderAt
	g     *gatewayFS
	path  string
	limit int64 // < 0: the real file
	once  sync.Once
}

// ReadAt serves the file cut at limit, or padded with 'X' beyond its end
// up to limit.
func (r *gatewayReader) ReadAt(b []byte, off int64) (int, error) {
	if r.limit < 0 {
		return r.ReaderAt.ReadAt(b, off)
	}
	if off >= r.limit {
		return 0, io.EOF
	}
	b = b[:min(int64(len(b)), r.limit-off)]
	n, err := r.ReaderAt.ReadAt(b, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, err
	}
	for i := n; i < len(b); i++ {
		b[i] = 'X'
	}
	if off+int64(len(b)) >= r.limit {
		return len(b), io.EOF
	}
	return len(b), nil
}

func (r *gatewayReader) Close() error {
	r.once.Do(func() {
		r.g.mu.Lock()
		defer r.g.mu.Unlock()
		r.g.reading[r.path]--
		if r.g.reading[r.path] <= 0 {
			delete(r.g.reading, r.path)
		}
	})
	return nil
}

// Filecmd implements pkgsftp.FileCmder. Like many cloud gateways the
// fake accepts and ignores permission changes (InMemHandler refuses them
// on directories).
func (g *gatewayFS) Filecmd(r *pkgsftp.Request) error {
	if r.Method == "Setstat" && !r.AttrFlags().Size {
		return nil
	}
	return g.mem.FileCmd.Filecmd(r)
}

func (g *gatewayFS) serve(ch io.ReadWriteCloser) error {
	return pkgsftp.NewRequestServer(ch, g.handlers()).Serve()
}

// Filewrite implements pkgsftp.FileWriter.
func (g *gatewayFS) Filewrite(r *pkgsftp.Request) (io.WriterAt, error) {
	w, err := g.mem.FilePut.Filewrite(r)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	g.open[r.Filepath]++
	g.mu.Unlock()
	return &gatewayWriter{WriterAt: w, g: g, path: r.Filepath}, nil
}

type gatewayWriter struct {
	io.WriterAt
	g    *gatewayFS
	path string

	mu   sync.Mutex
	end  int64
	once sync.Once
}

func (w *gatewayWriter) WriteAt(b []byte, off int64) (int, error) {
	n, err := w.WriterAt.WriteAt(b, off)
	w.mu.Lock()
	w.end = max(w.end, off+int64(n))
	w.mu.Unlock()
	return n, err
}

// Close is called by the request server when the handle is closed.
func (w *gatewayWriter) Close() error {
	w.once.Do(func() {
		if w.g.truncate {
			w.mu.Lock()
			end := w.end
			w.mu.Unlock()
			if t, ok := w.WriterAt.(interface{ Truncate(int64) error }); ok && end > 0 {
				discard(t.Truncate(end / 2))
			}
		}
		w.g.mu.Lock()
		defer w.g.mu.Unlock()
		w.g.open[w.path]--
		if w.g.open[w.path] <= 0 {
			delete(w.g.open, w.path)
		}
		if w.g.staleLstats > 0 {
			w.g.stale[w.path] = w.g.staleLstats
		}
	})
	return nil
}

// Filelist implements pkgsftp.FileLister (Stat on a path and FSTAT on a
// handle both arrive as "Stat").
func (g *gatewayFS) Filelist(r *pkgsftp.Request) (pkgsftp.ListerAt, error) {
	l, err := g.mem.FileList.Filelist(r)
	if err != nil || r.Method != "Stat" {
		return l, err
	}
	g.mu.Lock()
	open := g.open[r.Filepath] > 0
	reading := g.zeroReadFstat && g.reading[r.Filepath] > 0
	if open || reading {
		g.fstatsZ++
	}
	g.mu.Unlock()
	switch {
	case open:
		return zeroSize(l)
	case reading:
		return misreport(l)
	}
	return l, nil
}

// Lstat implements pkgsftp.LstatFileLister.
func (g *gatewayFS) Lstat(r *pkgsftp.Request) (pkgsftp.ListerAt, error) {
	ll, ok := g.mem.FileList.(pkgsftp.LstatFileLister)
	if !ok {
		return nil, errors.New("gateway fake: no Lstat")
	}
	l, err := ll.Lstat(r)
	if err != nil {
		return l, err
	}
	g.mu.Lock()
	if partialName.MatchString(path.Base(r.Filepath)) {
		g.lstats[r.Filepath]++
	}
	stale := g.stale[r.Filepath] > 0
	if stale {
		g.stale[r.Filepath]--
	}
	g.mu.Unlock()
	if !stale {
		return l, nil
	}
	return zeroSize(l)
}

func (g *gatewayFS) partialLstats() []int {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]int, 0, len(g.lstats))
	for _, n := range g.lstats {
		out = append(out, n)
	}
	return out
}

// zeroSize returns l's single entry with size 0.
func zeroSize(l pkgsftp.ListerAt) (pkgsftp.ListerAt, error) {
	fis := make([]os.FileInfo, 1)
	n, err := l.ListAt(fis, 0)
	if n != 1 {
		return nil, errors.Join(errors.New("gateway fake: no entry"), err)
	}
	return oneInfo{sizedInfo{FileInfo: fis[0]}}, nil
}

// misreport returns l's single entry with size 0 and another
// modification time.
func misreport(l pkgsftp.ListerAt) (pkgsftp.ListerAt, error) {
	fis := make([]os.FileInfo, 1)
	n, err := l.ListAt(fis, 0)
	if n != 1 {
		return nil, errors.Join(errors.New("gateway fake: no entry"), err)
	}
	return oneInfo{sizedInfo{FileInfo: fis[0], mod: time.Unix(0, 0)}}, nil
}

type sizedInfo struct {
	os.FileInfo
	size int64
	mod  time.Time
}

func (s sizedInfo) Size() int64 { return s.size }

func (s sizedInfo) ModTime() time.Time {
	if s.mod.IsZero() {
		return s.FileInfo.ModTime()
	}
	return s.mod
}

type oneInfo struct{ fi os.FileInfo }

func (o oneInfo) ListAt(ls []os.FileInfo, off int64) (int, error) {
	if off > 0 {
		return 0, io.EOF
	}
	if len(ls) == 0 {
		return 0, nil
	}
	ls[0] = o.fi
	return 1, io.EOF
}

var testSizeDelays = []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}

func openGateway(t *testing.T, srv *testServer, dir string, noDelete, noHardlink bool) *Destination {
	t.Helper()
	cfg := srv.keyConfig(t, "/")
	cfg.Dir = dir
	cfg.NoDelete = noDelete
	d, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	d.sizeDelays = testSizeDelays
	d.testNoHardlink = noHardlink
	return d
}

// TestGatewayFakeReportsZeroForOpenHandles checks the fake itself: FSTAT
// on an open write handle says 0 bytes, the path stat after the close
// (once the stale stats are used up) the real size.
func TestGatewayFakeReportsZeroForOpenHandles(t *testing.T) {
	g := newGatewayFS(1, false)
	srv := startServerWith(t, g.serve)
	d := mustOpen(t, srv.keyConfig(t, "/"))
	sess, err := d.session(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	f, err := sess.sftp.OpenFile("/probe", os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(bytes.Repeat([]byte("x"), 1000)); err != nil {
		t.Fatal(err)
	}
	st, err := f.Stat()
	if err != nil || st.Size() != 0 {
		t.Fatalf("fstat of the open handle = %v, %v; want size 0", st, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if fi, err := sess.sftp.Lstat("/probe"); err != nil || fi.Size() != 0 {
		t.Fatalf("first stat after close = %v, %v; want stale 0", fi, err)
	}
	if fi, err := sess.sftp.Lstat("/probe"); err != nil || fi.Size() != 1000 {
		t.Fatalf("stat after commit = %v, %v; want 1000", fi, err)
	}
}

// TestGatewayPut: Put succeeds against a gateway whose open handles report
// size 0 and whose path stat lags behind the close, with the hard-link
// and the rename finalize.
func TestGatewayPut(t *testing.T) {
	for _, noHardlink := range []bool{false, true} {
		name := "hardlink"
		if noHardlink {
			name = "rename"
		}
		t.Run(name, func(t *testing.T) {
			g := newGatewayFS(2, false)
			srv := startServerWith(t, g.serve)
			d := openGateway(t, srv, "dest", false, noHardlink)
			t.Cleanup(func() { discard(d.Close()) })
			key := storage.MustParseKey("job/2026-11-03T030000Z/db.dump.zst.age")
			data := bytes.Repeat([]byte("backup bytes "), 50_000)
			info, err := d.Put(ctxT(t), key, bytes.NewReader(data), storage.PutOptions{})
			if err != nil {
				t.Fatalf("put: %v", err)
			}
			if info.Size != int64(len(data)) || info.ModTime.IsZero() {
				t.Fatalf("info = %+v", info)
			}
			if got := g.partialLstats(); len(got) != 1 || got[0] != 3 {
				t.Fatalf("path stats of the partial = %v, want [3] (two stale, then the size)", got)
			}
			rc, _, err := d.Get(ctxT(t), key)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			back, err := io.ReadAll(rc)
			discard(rc.Close())
			if err != nil || !bytes.Equal(back, data) {
				t.Fatalf("read back %d bytes, %v", len(back), err)
			}
		})
	}
}

func TestGatewayConformance(t *testing.T) {
	g := newGatewayFS(1, false)
	g.zeroReadFstat = true
	srv := startServerWith(t, g.serve)
	n := 0
	storagetest.Run(t, storagetest.Config{
		LargeSize: 1 << 20, // InMemHandler sleeps 1 µs per written byte
		New: func(t *testing.T, o storagetest.Options) storage.Destination {
			n++
			dir := "dest-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + string(rune('a'+n%26))
			return openGateway(t, srv, dir, o.NoDelete, false)
		},
	})
}

// TestGatewayTruncatedPutFails: a server that really stores fewer bytes
// still fails the Put with ErrChecksumMismatch after the retries, leaves
// no object, and the run log's drop message carries the sizes.
func TestGatewayTruncatedPutFails(t *testing.T) {
	g := newGatewayFS(0, true)
	srv := startServerWith(t, g.serve)
	d := openGateway(t, srv, "dest", false, false)
	t.Cleanup(func() { discard(d.Close()) })
	key := storage.MustParseKey("job/2026-11-03T030000Z/db.dump")
	data := bytes.Repeat([]byte("y"), 3<<20)
	_, err := d.Put(ctxT(t), key, bytes.NewReader(data), storage.PutOptions{})
	if !errors.Is(err, storage.ErrChecksumMismatch) {
		t.Fatalf("put = %v, want checksum mismatch", err)
	}
	want := "the data did not match the expected checksum: the server reported 1.5 MiB of 3.0 MiB after closing"
	if got := storage.Describe(storage.Classify(err), err); got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
	if got := g.partialLstats(); len(got) != 1 || got[0] != 1+len(testSizeDelays) {
		t.Fatalf("path stats of the partial = %v, want [%d]", got, 1+len(testSizeDelays))
	}
	if _, err := d.Stat(ctxT(t), key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("stat after failed put = %v", err)
	}
	if n, err := d.CleanupPartials(ctxT(t), -time.Hour); err != nil || n != 0 {
		t.Fatalf("partials left = %d, %v", n, err)
	}

	// Through the pipeline: the target is dropped with the detail.
	res, err := pipeline.Run(ctxT(t), bytes.NewReader(data), pipeline.Spec{
		Encryption: pipeline.Encryption{Mode: pipeline.EncryptNone, OptedOut: true},
		Targets:    []pipeline.Target{{Name: "stack", Dest: d, Key: storage.MustParseKey("job/2026-11-03T040000Z/db.dump.zst")}},
		LagTimeout: 30 * time.Second,
	})
	if !errors.Is(err, pipeline.ErrAllDestinationsFailed) || len(res.Destinations) != 1 {
		t.Fatalf("pipeline = %+v, %v", res.Destinations, err)
	}
	r := res.Destinations[0]
	if r.Outcome != pipeline.OutcomeDropped || r.Code != storage.CodeChecksumMismatch ||
		!strings.HasPrefix(r.Message(), "the data did not match the expected checksum: the server reported ") ||
		!strings.HasSuffix(r.Message(), " after closing") {
		t.Fatalf("result = %+v, message %q", r, r.Message())
	}
}

// TestGatewayGetMisreportedReadHandle: FSTAT on read handles says 0 bytes
// and another time; Get accepts the unchanged directory entry and returns
// the right bytes and size.
func TestGatewayGetMisreportedReadHandle(t *testing.T) {
	g := newGatewayFS(0, false)
	g.zeroReadFstat = true
	srv := startServerWith(t, g.serve)
	d := openGateway(t, srv, "dest", false, false)
	t.Cleanup(func() { discard(d.Close()) })
	key := storage.MustParseKey("job/2026-11-03T030000Z/db.dump")
	data := bytes.Repeat([]byte("restore me "), 100_000)
	if _, err := d.Put(ctxT(t), key, bytes.NewReader(data), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	before := g.fstatsZ
	g.mu.Unlock()
	rc, info, err := d.Get(ctxT(t), key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	back, err := io.ReadAll(rc)
	discard(rc.Close())
	if err != nil || !bytes.Equal(back, data) || info.Size != int64(len(data)) || info.ModTime.Equal(time.Unix(0, 0)) {
		t.Fatalf("read %d bytes, info %+v, %v", len(back), info, err)
	}
	g.mu.Lock()
	misreported := g.fstatsZ - before
	g.mu.Unlock()
	if misreported == 0 {
		t.Fatal("the read handle's FSTAT was not misreported")
	}
}

// TestGatewayGetEntryChangedRefused: the file is replaced between the
// Lstat and the open; Get refuses it whether or not FSTAT is reliable.
func TestGatewayGetEntryChangedRefused(t *testing.T) {
	for _, zero := range []bool{false, true} {
		t.Run(map[bool]string{false: "fstat", true: "misreported-fstat"}[zero], func(t *testing.T) {
			g := newGatewayFS(0, false)
			g.zeroReadFstat = zero
			srv := startServerWith(t, g.serve)
			d := openGateway(t, srv, "dest", false, false)
			t.Cleanup(func() { discard(d.Close()) })
			key := storage.MustParseKey("job/2026-11-03T030000Z/db.dump")
			other := storage.MustParseKey("job/2026-11-03T030000Z/other.dump")
			if _, err := d.Put(ctxT(t), key, bytes.NewReader(bytes.Repeat([]byte("a"), 1000)), storage.PutOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Put(ctxT(t), other, bytes.NewReader(bytes.Repeat([]byte("b"), 2000)), storage.PutOptions{}); err != nil {
				t.Fatal(err)
			}
			var once sync.Once
			g.onOpenRead = func(p string) {
				if !strings.HasSuffix(p, "/"+key.String()) {
					return
				}
				once.Do(func() {
					discard(g.mem.FileCmd.Filecmd(pkgsftp.NewRequest("Remove", p)))
					mv := pkgsftp.NewRequest("Rename", path.Join(path.Dir(p), "other.dump"))
					mv.Target = p
					discard(g.mem.FileCmd.Filecmd(mv))
				})
			}
			rc, _, err := d.Get(ctxT(t), key)
			if err == nil {
				discard(rc.Close())
			}
			if !errors.Is(err, storage.ErrNotFound) || !strings.Contains(storage.Detail(err), "did not match its directory entry") {
				t.Fatalf("get of a swapped file = %v", err)
			}
		})
	}
}

// TestGatewayGetLengthEnforced: a stream shorter or longer than the
// directory entry fails the read with ErrChecksumMismatch.
func TestGatewayGetLengthEnforced(t *testing.T) {
	cases := map[string]struct {
		size func(int64) int64
		want string
	}{
		"short": {func(n int64) int64 { return n / 2 }, "read 512.0 KiB of 1.0 MiB"},
		"long":  {func(n int64) int64 { return n + 100 }, "the server returned more than the expected 1.0 MiB"},
	}
	for name, tc := range cases {
		for _, zero := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "", true: "-misreported-fstat"}[zero], func(t *testing.T) {
				g := newGatewayFS(0, false)
				g.zeroReadFstat = zero
				srv := startServerWith(t, g.serve)
				d := openGateway(t, srv, "dest", false, false)
				t.Cleanup(func() { discard(d.Close()) })
				key := storage.MustParseKey("job/2026-11-03T030000Z/db.dump")
				data := bytes.Repeat([]byte("z"), 1<<20)
				if _, err := d.Put(ctxT(t), key, bytes.NewReader(data), storage.PutOptions{}); err != nil {
					t.Fatal(err)
				}
				g.readSize = tc.size
				rc, _, err := d.Get(ctxT(t), key)
				if err != nil {
					t.Fatalf("get: %v", err)
				}
				back, err := io.ReadAll(rc)
				discard(rc.Close())
				if !errors.Is(err, storage.ErrChecksumMismatch) || storage.Detail(err) != tc.want {
					t.Fatalf("read = %v (detail %q), want %q", err, storage.Detail(err), tc.want)
				}
				if int64(len(back)) > int64(len(data)) {
					t.Fatalf("read %d bytes beyond the expected %d", len(back), len(data))
				}
			})
		}
	}
}
