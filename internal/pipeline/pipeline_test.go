package pipeline_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/storage"
)

// fakeDest is an in-memory destination with fault injection. A Put only
// stores its object after reading io.EOF (atomic finalize).
type fakeDest struct {
	name string
	// failAfter > 0 fails Put after reading that many bytes.
	failAfter int64
	// stallAfter > 0 stops reading after that many bytes until ctx ends.
	stallAfter int64
	// readDelay sleeps after every read.
	readDelay time.Duration

	mu       sync.Mutex
	objects  map[string][]byte
	canceled atomic.Bool
	started  atomic.Bool
}

func newFake(name string) *fakeDest { return &fakeDest{name: name, objects: map[string][]byte{}} }

func (f *fakeDest) Capabilities() storage.Capabilities {
	return storage.Capabilities{Type: "fake", AtomicRename: true, ExclusiveCreate: true}
}

var errBoom = errors.New("boom: backend failure")

func (f *fakeDest) Put(ctx context.Context, key storage.Key, r io.Reader, _ storage.PutOptions) (storage.ObjectInfo, error) {
	f.started.Store(true)
	var buf bytes.Buffer
	p := make([]byte, 64<<10)
	for {
		if f.stallAfter > 0 && int64(buf.Len()) >= f.stallAfter {
			<-ctx.Done()
			f.canceled.Store(true)
			return storage.ObjectInfo{}, ctx.Err()
		}
		n, err := r.Read(p)
		buf.Write(p[:n])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				f.canceled.Store(true)
			}
			return storage.ObjectInfo{}, err
		}
		if f.failAfter > 0 && int64(buf.Len()) >= f.failAfter {
			return storage.ObjectInfo{}, errBoom
		}
		if f.readDelay > 0 {
			select {
			case <-time.After(f.readDelay):
			case <-ctx.Done():
				f.canceled.Store(true)
				return storage.ObjectInfo{}, ctx.Err()
			}
		}
	}
	if err := ctx.Err(); err != nil {
		f.canceled.Store(true)
		return storage.ObjectInfo{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.objects[key.String()]; ok {
		return storage.ObjectInfo{}, storage.ErrAlreadyExists
	}
	b := buf.Bytes()
	f.objects[key.String()] = b
	sum := sha256.Sum256(b)
	return storage.ObjectInfo{Key: key, Size: int64(len(b)), SHA256: sum[:]}, nil
}

func (f *fakeDest) object(key storage.Key) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[key.String()]
	return b, ok
}

func (f *fakeDest) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.objects)
}

func (f *fakeDest) Get(_ context.Context, key storage.Key) (io.ReadCloser, storage.ObjectInfo, error) {
	b, ok := f.object(key)
	if !ok {
		return nil, storage.ObjectInfo{}, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), storage.ObjectInfo{Key: key, Size: int64(len(b))}, nil
}

func (f *fakeDest) Stat(_ context.Context, key storage.Key) (storage.ObjectInfo, error) {
	b, ok := f.object(key)
	if !ok {
		return storage.ObjectInfo{}, storage.ErrNotFound
	}
	return storage.ObjectInfo{Key: key, Size: int64(len(b))}, nil
}

func (f *fakeDest) List(context.Context, storage.Key, storage.ListOptions) (storage.ListPage, error) {
	return storage.ListPage{}, nil
}

func (f *fakeDest) Delete(_ context.Context, key storage.Key) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key.String())
	return nil
}

func (f *fakeDest) Test(context.Context, storage.Key) storage.TestResult { return storage.TestResult{} }
func (f *fakeDest) Close() error                                         { return nil }

// randomish returns n bytes that compress somewhat (like a dump).
func randomish(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	noise := rand.NewChaCha8([32]byte{byte(seed % 256)})
	words := []string{"INSERT ", "INTO ", "users ", "VALUES ", "(1, ", "'alice'", ");\n", "COPY ", "\\N\t"}
	var b bytes.Buffer
	for b.Len() < n {
		if r.IntN(4) == 0 {
			var x [16]byte
			_, _ = noise.Read(x[:]) //nolint:errcheck,gosec // ChaCha8.Read never fails
			b.Write(x[:])
		} else {
			b.WriteString(words[r.IntN(len(words))])
		}
	}
	return b.Bytes()[:n]
}

var testKey = storage.MustParseKey("prefix/job-a/2026-10-27T030000Z/app.dump.zst.age")

func recipients(t *testing.T) (string, []byte) {
	t.Helper()
	kp, err := crypto.GenerateAgeKeypair()
	if err != nil {
		t.Fatal(err)
	}
	return kp.Recipient, kp.Identity.Bytes()
}

func baseSpec(enc pipeline.Encryption, dests ...*fakeDest) pipeline.Spec {
	s := pipeline.Spec{
		Encryption:   enc,
		ChunkSize:    pipeline.MinChunkSize * 4, // 16 KiB
		BufferChunks: 4,
		LagTimeout:   200 * time.Millisecond,
	}
	for _, d := range dests {
		s.Targets = append(s.Targets, pipeline.Target{Name: d.name, Dest: d, Key: testKey})
	}
	return s
}

func decode(t *testing.T, stored []byte, d pipeline.Decryption) []byte {
	t.Helper()
	r, err := pipeline.OpenArtifact(bytes.NewReader(stored), d)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	}()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func outcome(t *testing.T, res pipeline.Result, name string) pipeline.DestinationResult {
	t.Helper()
	for i := range res.Destinations {
		if res.Destinations[i].Name == name {
			return res.Destinations[i]
		}
	}
	t.Fatalf("no result for %s", name)
	return pipeline.DestinationResult{}
}

func TestRunAllOKRecipients(t *testing.T) {
	recip, ident := recipients(t)
	input := randomish(1, 3<<20)
	a, b, c := newFake("a"), newFake("b"), newFake("c")
	res, err := pipeline.Run(t.Context(), bytes.NewReader(input), baseSpec(pipeline.RecipientEncryption(1, recip), a, b, c))
	if err != nil {
		t.Fatal(err)
	}
	if res.Partial() || len(res.OK()) != 3 {
		t.Fatalf("want 3 ok, got %+v", res.Destinations)
	}
	rawSum := sha256.Sum256(input)
	if res.RawSize != int64(len(input)) || !bytes.Equal(res.RawSHA256, rawSum[:]) {
		t.Fatal("raw size/hash wrong")
	}
	for _, d := range []*fakeDest{a, b, c} {
		stored, ok := d.object(testKey)
		if !ok {
			t.Fatalf("%s: no object", d.name)
		}
		sum := sha256.Sum256(stored)
		if int64(len(stored)) != res.StoredSize || !bytes.Equal(sum[:], res.SHA256) {
			t.Fatalf("%s: stored size/hash wrong", d.name)
		}
		if bytes.Contains(stored, input[:64]) {
			t.Fatal("plaintext visible in the artifact")
		}
		if got := decode(t, stored, pipeline.Decryption{Identities: ident}); !bytes.Equal(got, input) {
			t.Fatalf("%s: round trip mismatch", d.name)
		}
		r := outcome(t, res, d.name)
		if r.Code != storage.CodeOK || r.Info.Size != res.StoredSize {
			t.Fatalf("%s: bad outcome %+v", d.name, r)
		}
	}
	if res.Encryption.Mode != pipeline.EncryptRecipients || res.Encryption.RecipientVersion != 1 || len(res.Encryption.RecipientFingerprints) != 1 ||
		res.Encryption.RecipientFingerprints[0] != pipeline.RecipientFingerprint(recip) {
		t.Fatalf("encryption description: %+v", res.Encryption)
	}
	// A wrong identity cannot decrypt.
	_, other := recipients(t)
	stored, _ := a.object(testKey)
	if _, err := pipeline.OpenArtifact(bytes.NewReader(stored), pipeline.Decryption{Identities: other}); err == nil {
		t.Fatal("decrypted with the wrong identity")
	}
}

func TestRunPassphraseRoundTripAndWorkFactorBound(t *testing.T) {
	pass := []byte("correct horse battery staple and nine more words here")
	secret := crypto.NewSecret(append([]byte(nil), pass...))
	defer secret.Destroy()
	input := randomish(2, 200<<10)
	a := newFake("a")
	res, err := pipeline.Run(t.Context(), bytes.NewReader(input), baseSpec(pipeline.PassphraseEncryption(secret, 3), a))
	if err != nil {
		t.Fatal(err)
	}
	if res.Encryption.Mode != pipeline.EncryptPassphrase || res.Encryption.PassphraseVersion != 3 {
		t.Fatalf("encryption description: %+v", res.Encryption)
	}
	if secret.Len() == 0 {
		t.Fatal("Run destroyed the borrowed passphrase")
	}
	stored, _ := a.object(testKey)
	if got := decode(t, stored, pipeline.Decryption{Passphrase: pass}); !bytes.Equal(got, input) {
		t.Fatal("round trip mismatch")
	}
	// The artifact uses work factor 18; a reader bounded below refuses it
	// before running scrypt, and the bound can never exceed 20.
	if _, err := pipeline.OpenArtifact(bytes.NewReader(stored), pipeline.Decryption{Passphrase: pass, MaxWorkFactor: 17}); err == nil {
		t.Fatal("work factor above the bound accepted")
	}
	if _, err := pipeline.OpenArtifact(bytes.NewReader(stored), pipeline.Decryption{Passphrase: pass, MaxWorkFactor: crypto.MaxScryptWorkFactor + 1}); err == nil {
		t.Fatal("bound above the maximum accepted")
	}
	// Writing above the maximum is refused too.
	enc := pipeline.PassphraseEncryption(secret, 3)
	enc.WorkFactor = crypto.MaxScryptWorkFactor + 1
	if _, err := pipeline.Run(t.Context(), bytes.NewReader(input), baseSpec(enc, newFake("x"))); !errors.Is(err, pipeline.ErrEncryptionConfig) {
		t.Fatalf("want ErrEncryptionConfig, got %v", err)
	}
}

func TestRunOptOutPlainZstd(t *testing.T) {
	input := randomish(3, 100<<10)
	a := newFake("a")
	key := storage.MustParseKey("p/job/2026-10-27T030000Z/app.sql.zst")
	spec := baseSpec(pipeline.OptedOutEncryption())
	spec.Targets = []pipeline.Target{{Name: "a", Dest: a, Key: key}}
	res, err := pipeline.Run(t.Context(), bytes.NewReader(input), spec)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := a.object(key)
	if got := decode(t, stored, pipeline.Decryption{None: true}); !bytes.Equal(got, input) {
		t.Fatal("round trip mismatch")
	}
	if res.Encryption.Mode != pipeline.EncryptNone {
		t.Fatal("mode")
	}
}

func TestEncryptionFailsClosed(t *testing.T) {
	recip, _ := recipients(t)
	cases := map[string]pipeline.Encryption{
		"zero value":          {},
		"none without optout": {Mode: pipeline.EncryptNone},
		"empty passphrase":    pipeline.PassphraseEncryption(crypto.NewSecret(nil), 1),
		"version 0":           pipeline.PassphraseEncryption(crypto.NewSecret([]byte("x")), 0),
		"no recipients":       pipeline.RecipientEncryption(1),
		"bad recipient":       pipeline.RecipientEncryption(1, "age1notarecipient"),
		"mixed":               {Mode: pipeline.EncryptRecipients, Recipients: []string{recip}, RecipientVersion: 1, OptedOut: true},
		"recipient version 0": pipeline.RecipientEncryption(0, recip),
	}
	for name, enc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := enc.Validate(); !errors.Is(err, pipeline.ErrEncryptionConfig) {
				t.Fatalf("want ErrEncryptionConfig, got %v", err)
			}
			if strings.Contains(enc.String(), "x") && enc.Mode == pipeline.EncryptPassphrase {
				t.Fatal("String leaks the passphrase")
			}
		})
	}
}

func TestRunFailingDestinationDropped(t *testing.T) {
	recip, ident := recipients(t)
	input := randomish(4, 2<<20)
	a, bad, c := newFake("a"), newFake("bad"), newFake("c")
	bad.failAfter = 100 << 10
	res, err := pipeline.Run(t.Context(), bytes.NewReader(input), baseSpec(pipeline.RecipientEncryption(1, recip), a, bad, c))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Partial() || len(res.OK()) != 2 {
		t.Fatalf("want partial with 2 ok: %+v", res.Destinations)
	}
	r := outcome(t, res, "bad")
	if r.Outcome != pipeline.OutcomeDropped || r.Reason != pipeline.ReasonError || !errors.Is(r.Err, errBoom) {
		t.Fatalf("bad outcome: %+v", r)
	}
	if bad.count() != 0 {
		t.Fatal("failed destination holds an object")
	}
	for _, d := range []*fakeDest{a, c} {
		stored, _ := d.object(testKey)
		if !bytes.Equal(decode(t, stored, pipeline.Decryption{Identities: ident}), input) {
			t.Fatalf("%s: mismatch", d.name)
		}
	}
}

func TestRunSlowDestinationDroppedAfterLag(t *testing.T) {
	recip, _ := recipients(t)
	input := randomish(5, 4<<20)
	fast, slow := newFake("fast"), newFake("slow")
	slow.stallAfter = 32 << 10
	spec := baseSpec(pipeline.RecipientEncryption(1, recip), fast, slow)
	spec.LagTimeout = 300 * time.Millisecond
	start := time.Now()
	res, err := pipeline.Run(t.Context(), bytes.NewReader(input), spec)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	r := outcome(t, res, "slow")
	if r.Outcome != pipeline.OutcomeDropped || r.Reason != pipeline.ReasonLag || r.Code != storage.CodeTimeout {
		t.Fatalf("slow outcome: %+v", r)
	}
	if !slow.canceled.Load() || slow.count() != 0 {
		t.Fatal("slow Put not canceled or object finalized")
	}
	if outcome(t, res, "fast").Outcome != pipeline.OutcomeOK {
		t.Fatal("fast destination not ok")
	}
	// The fast destination was held up by the lag threshold once, not
	// per chunk.
	if elapsed > 5*time.Second {
		t.Fatalf("run took %v; the slow destination held up the others", elapsed)
	}
}

func TestRunSlowButLiveDestinationAppliesBackpressure(t *testing.T) {
	recip, _ := recipients(t)
	input := randomish(6, 512<<10)
	fast, slow := newFake("fast"), newFake("slow")
	slow.readDelay = 2 * time.Millisecond
	spec := baseSpec(pipeline.RecipientEncryption(1, recip), fast, slow)
	spec.LagTimeout = 2 * time.Second
	res, err := pipeline.Run(t.Context(), bytes.NewReader(input), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.OK()) != 2 {
		t.Fatalf("slow-but-live destination dropped: %+v", res.Destinations)
	}
}

func TestRunAllDestinationsFail(t *testing.T) {
	recip, _ := recipients(t)
	a, b := newFake("a"), newFake("b")
	a.failAfter, b.failAfter = 20<<10, 40<<10
	res, err := pipeline.Run(t.Context(), bytes.NewReader(randomish(7, 2<<20)), baseSpec(pipeline.RecipientEncryption(1, recip), a, b))
	if !errors.Is(err, pipeline.ErrAllDestinationsFailed) {
		t.Fatalf("want ErrAllDestinationsFailed, got %v", err)
	}
	if len(res.OK()) != 0 || res.SHA256 != nil {
		t.Fatalf("result claims success: %+v", res)
	}
}

// endless is a source that never ends; it signals once n bytes were read.
type endless struct {
	n      atomic.Int64
	notify int64
	once   sync.Once
	ch     chan struct{}
	r      *rand.ChaCha8
}

func newEndless(notify int64) *endless {
	return &endless{notify: notify, ch: make(chan struct{}), r: rand.NewChaCha8([32]byte{1})}
}

func (e *endless) Read(p []byte) (int, error) {
	_, _ = e.r.Read(p) //nolint:errcheck,gosec // ChaCha8.Read never fails
	if e.n.Add(int64(len(p))) >= e.notify {
		e.once.Do(func() { close(e.ch) })
	}
	return len(p), nil
}

func TestRunCancelMidStreamDisk(t *testing.T) {
	recip, _ := recipients(t)
	root := t.TempDir()
	d1, err := storage.OpenDisk(storage.DiskConfig{Root: root, Dir: "one"})
	if err != nil {
		t.Fatal(err)
	}
	d2, err := storage.OpenDisk(storage.DiskConfig{Root: root, Dir: "two"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := errors.Join(d1.Close(), d2.Close()); err != nil {
			t.Error(err)
		}
	})
	src := newEndless(3 << 20)
	ctx, cancel := context.WithCancel(t.Context())
	go func() { <-src.ch; cancel() }()
	spec := baseSpec(pipeline.RecipientEncryption(1, recip))
	spec.Targets = []pipeline.Target{{Name: "one", Dest: d1, Key: testKey}, {Name: "two", Dest: d2, Key: testKey}}
	spec.LagTimeout = time.Minute // a slow disk must not be dropped before the cancel
	res, err := pipeline.Run(ctx, src, spec)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v (%+v)", err, res.Destinations)
	}
	for _, d := range []storage.Destination{d1, d2} {
		if _, err := d.Stat(context.Background(), testKey); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("object finalized after cancel: %v", err)
		}
	}
	for _, r := range res.Destinations {
		if r.Outcome != pipeline.OutcomeDropped || r.Reason != pipeline.ReasonAborted {
			t.Fatalf("outcome after cancel: %+v", r)
		}
	}
}

func TestRunCancelMidStreamFakes(t *testing.T) {
	recip, _ := recipients(t)
	a, b := newFake("a"), newFake("b")
	b.readDelay = time.Millisecond
	src := newEndless(1 << 20)
	ctx, cancel := context.WithCancel(t.Context())
	go func() { <-src.ch; cancel() }()
	_, err := pipeline.Run(ctx, src, baseSpec(pipeline.RecipientEncryption(1, recip), a, b))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if a.count() != 0 || b.count() != 0 {
		t.Fatal("object finalized after cancel")
	}
}

type errReader struct{ n int }

func (e *errReader) Read(p []byte) (int, error) {
	if e.n <= 0 {
		return 0, errors.New("dump client died")
	}
	m := min(len(p), e.n)
	e.n -= m
	clear(p[:m])
	return m, nil
}

func TestRunSourceErrorFinalizesNothing(t *testing.T) {
	recip, _ := recipients(t)
	a := newFake("a")
	_, err := pipeline.Run(t.Context(), &errReader{n: 1 << 20}, baseSpec(pipeline.RecipientEncryption(1, recip), a))
	if !errors.Is(err, pipeline.ErrSourceFailed) {
		t.Fatalf("want ErrSourceFailed, got %v", err)
	}
	if a.count() != 0 {
		t.Fatal("object finalized after a source error")
	}
}

func TestRunMemoryBoundedWithStalledDestination(t *testing.T) {
	recip, _ := recipients(t)
	stalled := newFake("stalled")
	stalled.stallAfter = 1 // reads one chunk, then never again
	src := newEndless(1 << 62)
	spec := baseSpec(pipeline.RecipientEncryption(1, recip), stalled)
	spec.LagTimeout = time.Hour
	ctx, cancel := context.WithTimeout(t.Context(), 700*time.Millisecond)
	defer cancel()
	res, err := pipeline.Run(ctx, src, spec)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline, got %v", err)
	}
	bound := int64(spec.BufferChunks+2) * int64(spec.ChunkSize)
	if res.PeakBufferedBytes == 0 || res.PeakBufferedBytes > bound {
		t.Fatalf("peak buffered %d, bound %d", res.PeakBufferedBytes, bound)
	}
	// Backpressure: the source was not drained beyond the buffers (plus
	// what the compressor and age hold).
	if read := src.n.Load(); read > 64<<20 {
		t.Fatalf("source read %d bytes while the only destination stalled", read)
	}
}

func TestRunMemoryBoundedWithStalledAndFastDestinations(t *testing.T) {
	recip, _ := recipients(t)
	fast, stalled := newFake("fast"), newFake("stalled")
	stalled.stallAfter = 1
	spec := baseSpec(pipeline.RecipientEncryption(1, recip), fast, stalled)
	spec.LagTimeout = 300 * time.Millisecond
	res, err := pipeline.Run(t.Context(), bytes.NewReader(randomish(8, 8<<20)), spec)
	if err != nil {
		t.Fatal(err)
	}
	// One extra chunk may stay with the dropped destination until its
	// backend returns.
	bound := int64(spec.BufferChunks+3) * int64(spec.ChunkSize)
	if res.PeakBufferedBytes > bound {
		t.Fatalf("peak buffered %d, bound %d", res.PeakBufferedBytes, bound)
	}
	if !res.Partial() {
		t.Fatal("want partial")
	}
}

func TestSpecValidation(t *testing.T) {
	recip, _ := recipients(t)
	enc := pipeline.RecipientEncryption(1, recip)
	a := newFake("a")
	cases := map[string]func(*pipeline.Spec){
		"no targets":   func(s *pipeline.Spec) { s.Targets = nil },
		"dup names":    func(s *pipeline.Spec) { s.Targets = append(s.Targets, s.Targets[0]) },
		"nil dest":     func(s *pipeline.Spec) { s.Targets[0].Dest = nil },
		"zero key":     func(s *pipeline.Spec) { s.Targets[0].Key = storage.Key{} },
		"expected sum": func(s *pipeline.Spec) { s.Targets[0].Options.ExpectedSHA256 = make([]byte, 32) },
		"level":        func(s *pipeline.Spec) { s.CompressionLevel = 23 },
		"concurrency":  func(s *pipeline.Spec) { s.CompressionConcurrency = pipeline.MaxCompressionConcurrency + 1 },
		"chunk small":  func(s *pipeline.Spec) { s.ChunkSize = 1 },
		"buffer large": func(s *pipeline.Spec) { s.BufferChunks = pipeline.MaxBufferChunks + 1 },
		"negative lag": func(s *pipeline.Spec) { s.LagTimeout = -1 },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			s := baseSpec(enc, a)
			mut(&s)
			if _, err := pipeline.Run(t.Context(), strings.NewReader("x"), s); !errors.Is(err, pipeline.ErrInvalidSpec) {
				t.Fatalf("want ErrInvalidSpec, got %v", err)
			}
		})
	}
	if a.started.Load() {
		t.Fatal("Put started for an invalid spec")
	}
}

func TestEmptySource(t *testing.T) {
	recip, ident := recipients(t)
	a := newFake("a")
	res, err := pipeline.Run(t.Context(), strings.NewReader(""), baseSpec(pipeline.RecipientEncryption(1, recip), a))
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := a.object(testKey)
	if len(decode(t, stored, pipeline.Decryption{Identities: ident})) != 0 || res.RawSize != 0 {
		t.Fatal("empty round trip")
	}
}

func TestArtifactFileName(t *testing.T) {
	cases := []struct {
		kind   pipeline.EntryKind
		db     string
		format pipeline.DumpFormat
		mode   pipeline.EncryptionMode
		want   string
	}{
		{pipeline.KindDatabase, "app", pipeline.FormatCustom, pipeline.EncryptPassphrase, "app.dump.zst.age"},
		{pipeline.KindDatabase, "my.db", pipeline.FormatSQL, pipeline.EncryptNone, "my%2Edb.sql.zst"},
		{pipeline.KindDatabase, "globals", pipeline.FormatSQL, pipeline.EncryptRecipients, "globals.sql.zst.age"},
		{pipeline.KindGlobals, "", pipeline.FormatSQL, pipeline.EncryptRecipients, "globals.globals.sql.zst.age"},
	}
	for _, c := range cases {
		got, err := pipeline.ArtifactFileName(c.kind, c.db, c.format, c.mode)
		if err != nil || got != c.want {
			t.Fatalf("%v %q: got %q, %v; want %q", c.kind, c.db, got, err, c.want)
		}
	}
	if _, err := pipeline.ArtifactFileName(pipeline.KindGlobals, "", pipeline.FormatCustom, pipeline.EncryptNone); err == nil {
		t.Fatal("custom-format globals accepted")
	}
	if _, err := pipeline.ArtifactFileName(pipeline.KindDatabase, "x", pipeline.FormatSQL, ""); err == nil {
		t.Fatal("empty mode accepted")
	}
}

func TestOpenArtifactNeedsExactlyOneKey(t *testing.T) {
	if _, err := pipeline.OpenArtifact(strings.NewReader(""), pipeline.Decryption{}); !errors.Is(err, pipeline.ErrArtifact) {
		t.Fatal("no key accepted")
	}
	if _, err := pipeline.OpenArtifact(strings.NewReader(""), pipeline.Decryption{None: true, Passphrase: []byte("x")}); !errors.Is(err, pipeline.ErrArtifact) {
		t.Fatal("two keys accepted")
	}
}
