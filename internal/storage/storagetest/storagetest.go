// Package storagetest is the conformance suite every storage backend must
// pass (task 2.8; S3 and SFTP run it in their integration tests).
package storagetest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/storage"
)

// Options are passed to Config.New.
type Options struct {
	// NoDelete requests a destination in no-delete mode.
	NoDelete bool
}

// Config configures the suite.
type Config struct {
	// New returns a fresh, empty destination. The suite closes it.
	New func(t *testing.T, o Options) storage.Destination
	// LargeSize is the size of the streamed round-trip object
	// (default 50 MiB).
	LargeSize int64
}

// Run runs the conformance suite.
func Run(t *testing.T, cfg Config) {
	t.Helper()
	if cfg.LargeSize <= 0 {
		cfg.LargeSize = 50 << 20
	}
	open := func(t *testing.T, o Options) storage.Destination {
		t.Helper()
		d := cfg.New(t, o)
		t.Cleanup(func() {
			if err := d.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
		})
		return d
	}
	tests := []struct {
		name string
		fn   func(t *testing.T, open func(*testing.T, Options) storage.Destination, cfg Config)
	}{
		{"LargeRoundTrip", testLargeRoundTrip},
		{"ExpectedSHA256", testExpectedSHA256},
		{"AtomicVisibility", testAtomicVisibility},
		{"SourceError", testSourceError},
		{"NoOverwrite", testNoOverwrite},
		{"ListPagination", testListPagination},
		{"Delete", testDelete},
		{"NoDeleteMode", testNoDeleteMode},
		{"InvalidKeys", testInvalidKeys},
		{"ConcurrentPuts", testConcurrentPuts},
		{"CancelMidPut", testCancelMidPut},
		{"Probe", testProbe},
		{"GetMissing", testGetMissing},
		{"LockOptionsFailClosed", testLockOptions},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { tc.fn(t, open, cfg) })
	}
}

// Reader returns a deterministic pseudo-random stream of n bytes.
func Reader(seed uint64, n int64) io.Reader {
	return io.LimitReader(&pcgReader{r: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))}, n) //nolint:gosec // G404: test data, not security relevant
}

type pcgReader struct{ r *rand.Rand }

func (p *pcgReader) Read(b []byte) (int, error) {
	for i := 0; i < len(b); i += 8 {
		v := p.r.Uint64()
		for j := 0; j < 8 && i+j < len(b); j++ {
			b[i+j] = byte(v >> (8 * j)) //nolint:gosec // G115: truncation intended
		}
	}
	return len(b), nil
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func put(t *testing.T, d storage.Destination, key string, data []byte) storage.ObjectInfo {
	t.Helper()
	info, err := d.Put(ctxT(t), storage.MustParseKey(key), bytes.NewReader(data), storage.PutOptions{})
	if err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
	return info
}

func readAll(t *testing.T, d storage.Destination, key storage.Key) []byte {
	t.Helper()
	rc, _, err := d.Get(ctxT(t), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer func() {
		if err := rc.Close(); err != nil {
			t.Errorf("close reader: %v", err)
		}
	}()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return b
}

func assertMissing(t *testing.T, d storage.Destination, key storage.Key) {
	t.Helper()
	if _, err := d.Stat(ctxT(t), key); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("stat %s: got %v, want ErrNotFound", key, err)
	}
	page, err := d.List(ctxT(t), storage.Key{}, storage.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for i := range page.Objects {
		if page.Objects[i].Key == key {
			t.Errorf("list shows %s", key)
		}
	}
}

func listKeys(t *testing.T, d storage.Destination, prefix storage.Key) []string {
	t.Helper()
	var keys []string
	for o, err := range storage.All(ctxT(t), d, prefix) {
		if err != nil {
			t.Fatalf("list %q: %v", prefix, err)
		}
		keys = append(keys, o.Key.String())
	}
	return keys
}

func testLargeRoundTrip(t *testing.T, open func(*testing.T, Options) storage.Destination, cfg Config) {
	d := open(t, Options{})
	key := storage.MustParseKey("job/2026-10-26T030000Z/db.dump.zst.age")
	h := sha256.New()
	if _, err := io.Copy(h, Reader(1, cfg.LargeSize)); err != nil {
		t.Fatal(err)
	}
	want := h.Sum(nil)
	info, err := d.Put(ctxT(t), key, Reader(1, cfg.LargeSize), storage.PutOptions{ExpectedSHA256: want, SizeHint: cfg.LargeSize})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if info.Size != cfg.LargeSize || !bytes.Equal(info.SHA256, want) || info.Key != key {
		t.Fatalf("put info = %+v", info)
	}
	st, err := d.Stat(ctxT(t), key)
	if err != nil || st.Size != cfg.LargeSize {
		t.Fatalf("stat = %+v, %v", st, err)
	}
	rc, gi, err := d.Get(ctxT(t), key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	gh := sha256.New()
	n, err := io.Copy(gh, rc)
	if cerr := rc.Close(); cerr != nil {
		t.Errorf("close: %v", cerr)
	}
	if err != nil || n != cfg.LargeSize || gi.Size != cfg.LargeSize || !bytes.Equal(gh.Sum(nil), want) {
		t.Fatalf("get: n=%d size=%d err=%v hash match=%v", n, gi.Size, err, bytes.Equal(gh.Sum(nil), want))
	}
}

func testExpectedSHA256(t *testing.T, open func(*testing.T, Options) storage.Destination, _ Config) {
	d := open(t, Options{})
	data := []byte("hello checksum")
	sum := sha256.Sum256(data)
	ok := storage.MustParseKey("c/ok")
	if _, err := d.Put(ctxT(t), ok, bytes.NewReader(data), storage.PutOptions{ExpectedSHA256: sum[:]}); err != nil {
		t.Fatalf("matching checksum: %v", err)
	}
	bad := storage.MustParseKey("c/bad")
	wrong := sha256.Sum256([]byte("other"))
	_, err := d.Put(ctxT(t), bad, bytes.NewReader(data), storage.PutOptions{ExpectedSHA256: wrong[:]})
	if !errors.Is(err, storage.ErrChecksumMismatch) {
		t.Fatalf("mismatch: got %v", err)
	}
	assertMissing(t, d, bad)
	_, err = d.Put(ctxT(t), bad, bytes.NewReader(data), storage.PutOptions{ExpectedSHA256: []byte{1, 2}})
	if !errors.Is(err, storage.ErrInvalidArgument) {
		t.Fatalf("short checksum: got %v", err)
	}
}

func testAtomicVisibility(t *testing.T, open func(*testing.T, Options) storage.Destination, _ Config) {
	d := open(t, Options{})
	key := storage.MustParseKey("atomic/a/obj")
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := d.Put(ctxT(t), key, pr, storage.PutOptions{})
		done <- err
	}()
	chunk := bytes.Repeat([]byte("x"), 64<<10)
	for range 4 {
		if _, err := pw.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	// The writer has consumed data but the object is not finished.
	assertMissing(t, d, key)
	if got := listKeys(t, d, storage.Key{}); len(got) != 0 {
		t.Errorf("list during put = %v", got)
	}
	if _, err := pw.Write(chunk); err != nil {
		t.Fatal(err)
	}
	if err := pw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("put: %v", err)
	}
	if got := readAll(t, d, key); len(got) != 5*len(chunk) {
		t.Fatalf("size = %d", len(got))
	}
}

type failingReader struct {
	n   int
	err error
}

func (f *failingReader) Read(b []byte) (int, error) {
	if f.n <= 0 {
		return 0, f.err
	}
	m := min(len(b), f.n)
	for i := range m {
		b[i] = 'z'
	}
	f.n -= m
	return m, nil
}

func testSourceError(t *testing.T, open func(*testing.T, Options) storage.Destination, _ Config) {
	d := open(t, Options{})
	key := storage.MustParseKey("src/err")
	boom := errors.New("producer failed")
	_, err := d.Put(ctxT(t), key, &failingReader{n: 3 << 20, err: boom}, storage.PutOptions{})
	if !errors.Is(err, boom) || !errors.Is(err, storage.ErrSource) {
		t.Fatalf("got %v, want the source error", err)
	}
	assertMissing(t, d, key)
	if got := listKeys(t, d, storage.Key{}); len(got) != 0 {
		t.Errorf("leftover objects %v", got)
	}
}

func testNoOverwrite(t *testing.T, open func(*testing.T, Options) storage.Destination, _ Config) {
	d := open(t, Options{})
	put(t, d, "ow/obj", []byte("first"))
	_, err := d.Put(ctxT(t), storage.MustParseKey("ow/obj"), bytes.NewReader([]byte("second")), storage.PutOptions{})
	if !errors.Is(err, storage.ErrAlreadyExists) {
		t.Fatalf("got %v, want ErrAlreadyExists", err)
	}
	if got := readAll(t, d, storage.MustParseKey("ow/obj")); string(got) != "first" {
		t.Fatalf("content = %q", got)
	}
}

func testListPagination(t *testing.T, open func(*testing.T, Options) storage.Destination, _ Config) {
	d := open(t, Options{})
	want := make([]string, 0, 25)
	for i := range 25 {
		var k string
		switch i % 3 {
		case 0:
			k = fmt.Sprintf("p/job-%d/2026-10-26T0300%02dZ/db.dump", i%4, i)
		case 1:
			k = fmt.Sprintf("p/job-%d.x/f%02d", i%2, i)
		default:
			k = fmt.Sprintf("p/a%%2E-%02d", i)
		}
		put(t, d, k, []byte(k))
		want = append(want, k)
	}
	put(t, d, "pz/outside", []byte("x")) // shares the "p" byte prefix only
	put(t, d, "p-sibling", []byte("x"))
	slices.Sort(want)

	prefix := storage.MustParseKey("p")
	var got []string
	var after storage.Key
	pages := 0
	for {
		page, err := d.List(ctxT(t), prefix, storage.ListOptions{PageSize: 7, StartAfter: after})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		pages++
		if len(page.Objects) > 7 {
			t.Fatalf("page of %d objects", len(page.Objects))
		}
		for i := range page.Objects {
			o := &page.Objects[i]
			got = append(got, o.Key.String())
			if o.Size != int64(len(o.Key.String())) {
				t.Errorf("%s size %d", o.Key, o.Size)
			}
		}
		if !page.More {
			break
		}
		after = page.Next
		if pages > 10 {
			t.Fatal("pagination does not terminate")
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("list order:\n got %v\nwant %v", got, want)
	}
	if pages != 4 {
		t.Errorf("pages = %d, want 4", pages)
	}
	all := listKeys(t, d, storage.Key{})
	if !slices.IsSorted(all) || len(all) != 27 {
		t.Errorf("root list = %v", all)
	}
	if got := listKeys(t, d, storage.MustParseKey("nope")); len(got) != 0 {
		t.Errorf("missing prefix lists %v", got)
	}
	if got := listKeys(t, d, storage.MustParseKey("p-sibling")); len(got) != 0 {
		t.Errorf("object as prefix lists %v", got)
	}
	page, err := d.List(ctxT(t), prefix, storage.ListOptions{StartAfter: storage.MustParseKey(want[10])})
	if err != nil || len(page.Objects) != 14 || page.Objects[0].Key.String() != want[11] || page.More {
		t.Errorf("start-after page: %d objects, more=%v, err=%v", len(page.Objects), page.More, err)
	}
}

func testDelete(t *testing.T, open func(*testing.T, Options) storage.Destination, _ Config) {
	d := open(t, Options{})
	put(t, d, "del/a/obj", []byte("x"))
	put(t, d, "del/b/obj", []byte("y"))
	key := storage.MustParseKey("del/a/obj")
	if err := d.Delete(ctxT(t), key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	assertMissing(t, d, key)
	if err := d.Delete(ctxT(t), key); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	if got := listKeys(t, d, storage.Key{}); !slices.Equal(got, []string{"del/b/obj"}) {
		t.Errorf("after delete: %v", got)
	}
	// The key can be written again after a delete.
	put(t, d, "del/a/obj", []byte("again"))
}

func testNoDeleteMode(t *testing.T, open func(*testing.T, Options) storage.Destination, _ Config) {
	d := open(t, Options{NoDelete: true})
	if !d.Capabilities().NoDeleteMode {
		t.Fatal("capabilities do not report no-delete mode")
	}
	put(t, d, "nd/obj", []byte("x"))
	err := d.Delete(ctxT(t), storage.MustParseKey("nd/obj"))
	if !errors.Is(err, storage.ErrDeleteNotPermitted) {
		t.Fatalf("got %v, want ErrDeleteNotPermitted", err)
	}
	if got := readAll(t, d, storage.MustParseKey("nd/obj")); string(got) != "x" {
		t.Fatalf("content = %q", got)
	}
}

func testInvalidKeys(t *testing.T, open func(*testing.T, Options) storage.Destination, _ Config) {
	d := open(t, Options{})
	ctx := ctxT(t)
	var zero storage.Key
	if _, err := d.Put(ctx, zero, bytes.NewReader(nil), storage.PutOptions{}); !errors.Is(err, storage.ErrInvalidKey) {
		t.Errorf("put zero key: %v", err)
	}
	if _, _, err := d.Get(ctx, zero); !errors.Is(err, storage.ErrInvalidKey) {
		t.Errorf("get zero key: %v", err)
	}
	if _, err := d.Stat(ctx, zero); !errors.Is(err, storage.ErrInvalidKey) {
		t.Errorf("stat zero key: %v", err)
	}
	if err := d.Delete(ctx, zero); !errors.Is(err, storage.ErrInvalidKey) {
		t.Errorf("delete zero key: %v", err)
	}
	for _, s := range []string{"../x", "/abs", "a/../b", "a//b", ".hidden", "a\\b", "a\x00b"} {
		if _, err := storage.ParseKey(s); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("ParseKey(%q) = %v", s, err)
		}
	}
}

func testConcurrentPuts(t *testing.T, open func(*testing.T, Options) storage.Destination, _ Config) {
	d := open(t, Options{})
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := range 16 {
		wg.Go(func() {
			k := storage.MustParseKey(fmt.Sprintf("conc/dir-%d/obj-%02d", i%3, i))
			if _, err := d.Put(ctxT(t), k, Reader(uint64(i), 100<<10), storage.PutOptions{}); err != nil { //nolint:gosec // G115: small loop index
				errs <- err
			}
		})
	}
	var won, lost atomic.Int32
	for range 8 {
		wg.Go(func() {
			_, err := d.Put(ctxT(t), storage.MustParseKey("conc/same"), Reader(99, 10<<10), storage.PutOptions{})
			switch {
			case err == nil:
				won.Add(1)
			case errors.Is(err, storage.ErrAlreadyExists):
				lost.Add(1)
			default:
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent put: %v", err)
	}
	if won.Load() != 1 || lost.Load() != 7 {
		t.Errorf("same-key puts: %d won, %d already existed", won.Load(), lost.Load())
	}
	if got := listKeys(t, d, storage.MustParseKey("conc")); len(got) != 17 {
		t.Errorf("listed %d objects, want 17", len(got))
	}
}

func testCancelMidPut(t *testing.T, open func(*testing.T, Options) storage.Destination, _ Config) {
	d := open(t, Options{})
	key := storage.MustParseKey("cancel/obj")
	ctx, cancel := context.WithCancel(ctxT(t))
	defer cancel()
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := d.Put(ctx, key, pr, storage.PutOptions{})
		done <- err
	}()
	if _, err := pw.Write(bytes.Repeat([]byte("c"), 32<<10)); err != nil {
		t.Fatal(err)
	}
	cancel()
	// Keep feeding until Put gives up; a backend may notice the
	// cancellation only on its next read.
	go func() {
		chunk := bytes.Repeat([]byte("c"), 32<<10)
		for {
			if _, err := pw.Write(chunk); err != nil {
				return
			}
		}
	}()
	err := <-done
	pr.CloseWithError(errors.New("test done")) //nolint:gosec // G104: always returns nil
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	assertMissing(t, d, key)
}

func testProbe(t *testing.T, open func(*testing.T, Options) storage.Destination, _ Config) {
	d := open(t, Options{})
	prefix := storage.MustParseKey("probe-prefix")
	res := d.Test(ctxT(t), prefix)
	if !res.OK || res.ProbeRemains || len(res.Steps) != 3 {
		t.Fatalf("test result = %+v", res)
	}
	if !res.ProbeKey.HasPrefix(prefix) {
		t.Errorf("probe key %s outside prefix", res.ProbeKey)
	}
	if got := listKeys(t, d, storage.Key{}); len(got) != 0 {
		t.Errorf("probe left %v", got)
	}

	nd := open(t, Options{NoDelete: true})
	res = nd.Test(ctxT(t), storage.Key{})
	if !res.OK || !res.ProbeRemains || len(res.Steps) != 2 || res.Note == "" {
		t.Fatalf("no-delete test result = %+v", res)
	}
	if _, err := nd.Stat(ctxT(t), res.ProbeKey); err != nil {
		t.Errorf("probe object missing in no-delete mode: %v", err)
	}
}

func testGetMissing(t *testing.T, open func(*testing.T, Options) storage.Destination, _ Config) {
	d := open(t, Options{})
	if _, _, err := d.Get(ctxT(t), storage.MustParseKey("missing/obj")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if storage.Classify(storage.ErrNotFound) != storage.CodeNotFound {
		t.Fatal("classify")
	}
}

func testLockOptions(t *testing.T, open func(*testing.T, Options) storage.Destination, _ Config) {
	d := open(t, Options{})
	if d.Capabilities().SupportsObjectLock {
		t.Skip("backend supports object lock; covered by its own tests")
	}
	key := storage.MustParseKey("lock/obj")
	_, err := d.Put(ctxT(t), key, bytes.NewReader([]byte("x")), storage.PutOptions{
		RetainUntil: time.Now().Add(time.Hour), LockMode: storage.LockCompliance,
	})
	if !errors.Is(err, storage.ErrUnsupported) {
		t.Fatalf("got %v, want ErrUnsupported", err)
	}
	_, err = d.Put(ctxT(t), key, bytes.NewReader([]byte("x")), storage.PutOptions{LegalHold: true})
	if !errors.Is(err, storage.ErrUnsupported) {
		t.Fatalf("legal hold: got %v, want ErrUnsupported", err)
	}
	assertMissing(t, d, key)
}
