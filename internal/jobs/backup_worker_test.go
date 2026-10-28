package jobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/storage"
)

const testDBPassword = "Sup3r-Secret-DB-Passw0rd"

// --- fake run store ---------------------------------------------------------

type fakeItem struct {
	runstate.Item
	res      *runstate.ItemResult
	position int
}

type fakeStore struct {
	mu        sync.Mutex
	job       runstate.Job
	jobErr    error
	run       runstate.Run
	started   bool
	cancelReq atomic.Bool
	items     []*fakeItem
	artifacts []runstate.Artifact
	artIDs    []uuid.UUID
	manifests []runstate.Artifact
	// signedEntries: the entries MarkSigned received, per manifest.
	signedEntries [][]uuid.UUID
	logs          []runstate.LogLine
	result        *runstate.Result
	finishes      int
}

func (s *fakeStore) Begin(_ context.Context, p runstate.BeginParams) (runstate.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run.RunID == uuid.Nil {
		s.run = runstate.Run{Ref: runstate.Ref{RunID: uuid.New(), JobID: p.JobID}, Status: runstate.StatusQueued, Trigger: p.Trigger, Attempt: p.Attempt}
	}
	r := s.run
	r.CancelRequested = s.cancelReq.Load()
	return r, nil
}

func (s *fakeStore) Job(context.Context, uuid.UUID) (runstate.Job, error) {
	return s.job, s.jobErr
}

func (s *fakeStore) Start(_ context.Context, _ runstate.Ref, _ int, _, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = true
	return nil
}

func (s *fakeStore) CancelRequested(context.Context, uuid.UUID) (bool, error) {
	return s.cancelReq.Load(), nil
}

func (s *fakeStore) AddItem(_ context.Context, _ runstate.Ref, kind sqlc.RunItemKind, db string, pos int) (runstate.Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it := runstate.Item{ID: uuid.New(), Kind: kind, Database: db}
	s.items = append(s.items, &fakeItem{Item: it, position: pos})
	return it, nil
}

func (s *fakeStore) FinishItem(_ context.Context, _ runstate.Ref, id uuid.UUID, res runstate.ItemResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.items {
		if it.ID == id {
			if it.res != nil {
				return runstate.ErrNotActive
			}
			r := res
			it.res = &r
			return nil
		}
	}
	return errors.New("unknown item")
}

func (s *fakeStore) AddArtifact(_ context.Context, _ runstate.Ref, a runstate.Artifact) (uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := uuid.New()
	s.artifacts = append(s.artifacts, a)
	s.artIDs = append(s.artIDs, id)
	return id, nil
}

// MarkSigned signs exactly the listed entries, like the store (which fails
// unless each is a verified, unsigned object of the run on that
// destination).
func (s *fakeStore) MarkSigned(_ context.Context, _ runstate.Ref, m runstate.Artifact, entries []uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range entries {
		i := slices.Index(s.artIDs, id)
		if i < 0 || s.artifacts[i].DestinationID != m.DestinationID || !s.artifacts[i].Verified || s.artifacts[i].SigningKeyID != "" {
			return runstate.ErrSignedMismatch
		}
	}
	for _, id := range entries {
		s.artifacts[slices.Index(s.artIDs, id)].SigningKeyID = m.SigningKeyID
	}
	s.manifests = append(s.manifests, m)
	s.signedEntries = append(s.signedEntries, slices.Clone(entries))
	return nil
}

func (s *fakeStore) Finish(_ context.Context, _ runstate.Ref, res runstate.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishes++
	if s.result != nil {
		return runstate.ErrNotActive
	}
	r := res
	s.result = &r
	return nil
}

func (s *fakeStore) AppendLogs(_ context.Context, _ runstate.Ref, lines []runstate.LogLine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, lines...)
	return nil
}

func (s *fakeStore) item(db string, kind sqlc.RunItemKind) *fakeItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.items {
		if it.Database == db && it.Kind == kind {
			return it
		}
	}
	return nil
}

func (s *fakeStore) logText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	for _, l := range s.logs {
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// --- fake slots ----------------------------------------------------------------

type fakeSlots struct {
	mu       sync.Mutex
	full     bool
	held     map[uuid.UUID]bool
	acquired int
	released int
}

func (f *fakeSlots) TryAcquire(_ context.Context, holder, _ uuid.UUID, _ runstate.SlotKind, _ runstate.SlotLimits) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.full {
		return false, nil
	}
	if f.held == nil {
		f.held = map[uuid.UUID]bool{}
	}
	f.held[holder] = true
	f.acquired++
	return true, nil
}

func (f *fakeSlots) Heartbeat(_ context.Context, holder uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.held[holder] {
		return runstate.ErrSlotLost
	}
	return nil
}

func (f *fakeSlots) Release(_ context.Context, holder uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.held, holder)
	f.released++
	return nil
}

// --- fake targets -------------------------------------------------------------

type fakeTargets struct {
	mu        sync.Mutex
	released  int
	issued    []*runnerpb.Target
	failures  []error
	targetErr error
}

func (f *fakeTargets) Target(context.Context, uuid.UUID) (*runnerpb.Target, func(), error) {
	if f.targetErr != nil {
		return nil, nil, f.targetErr
	}
	t := &runnerpb.Target{Engine: runnerpb.Engine_ENGINE_POSTGRES, VersionKey: "17", Host: "db.example.test", Port: 5432,
		User: "backup", Password: []byte(testDBPassword), TlsMode: runnerpb.TlsMode_TLS_MODE_PINNED}
	f.mu.Lock()
	f.issued = append(f.issued, t)
	f.mu.Unlock()
	return t, func() {
		runnerpb.ZeroSecrets(t)
		f.mu.Lock()
		f.released++
		f.mu.Unlock()
	}, nil
}

func (f *fakeTargets) RecordRunFailure(_ context.Context, _ uuid.UUID, _ string, err error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, err)
	return nil
}

// --- fake destinations ----------------------------------------------------------

var errBackend = errors.New("backend failure at /secret/path")

type fakeDest struct {
	failAfter  int64
	stallAfter int64
	mu         sync.Mutex
	objects    map[string][]byte
	canceled   atomic.Int32
}

func newFakeDest() *fakeDest { return &fakeDest{objects: map[string][]byte{}} }

func (f *fakeDest) Capabilities() storage.Capabilities {
	return storage.Capabilities{Type: "fake", AtomicRename: true, ExclusiveCreate: true}
}

func (f *fakeDest) Put(ctx context.Context, key storage.Key, r io.Reader, opts storage.PutOptions) (storage.ObjectInfo, error) {
	var buf bytes.Buffer
	p := make([]byte, 32<<10)
	for {
		if f.stallAfter > 0 && int64(buf.Len()) >= f.stallAfter {
			<-ctx.Done()
			f.canceled.Add(1)
			return storage.ObjectInfo{}, ctx.Err()
		}
		n, err := r.Read(p)
		buf.Write(p[:n])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			f.canceled.Add(1)
			return storage.ObjectInfo{}, err
		}
		if f.failAfter > 0 && int64(buf.Len()) >= f.failAfter {
			return storage.ObjectInfo{}, errBackend
		}
	}
	if err := ctx.Err(); err != nil {
		f.canceled.Add(1)
		return storage.ObjectInfo{}, err
	}
	b := buf.Bytes()
	sum := sha256.Sum256(b)
	if len(opts.ExpectedSHA256) > 0 && !bytes.Equal(opts.ExpectedSHA256, sum[:]) {
		return storage.ObjectInfo{}, storage.ErrChecksumMismatch
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.objects[key.String()]; ok {
		return storage.ObjectInfo{}, storage.ErrAlreadyExists
	}
	f.objects[key.String()] = b
	return storage.ObjectInfo{Key: key, Size: int64(len(b)), SHA256: sum[:]}, nil
}

func (f *fakeDest) object(key string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[key]
	return b, ok
}

func (f *fakeDest) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.objects))
	for k := range f.objects {
		out = append(out, k)
	}
	return out
}

func (f *fakeDest) Get(_ context.Context, key storage.Key) (io.ReadCloser, storage.ObjectInfo, error) {
	b, ok := f.object(key.String())
	if !ok {
		return nil, storage.ObjectInfo{}, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), storage.ObjectInfo{Key: key, Size: int64(len(b))}, nil
}

func (f *fakeDest) Stat(_ context.Context, key storage.Key) (storage.ObjectInfo, error) {
	b, ok := f.object(key.String())
	if !ok {
		return storage.ObjectInfo{}, storage.ErrNotFound
	}
	return storage.ObjectInfo{Key: key, Size: int64(len(b))}, nil
}

func (f *fakeDest) List(context.Context, storage.Key, storage.ListOptions) (storage.ListPage, error) {
	return storage.ListPage{}, nil
}
func (f *fakeDest) Delete(context.Context, storage.Key) error            { return nil }
func (f *fakeDest) Test(context.Context, storage.Key) storage.TestResult { return storage.TestResult{} }
func (f *fakeDest) Close() error                                         { return nil }

type fakeOpener struct {
	dests   map[uuid.UUID]*fakeDest
	openErr map[uuid.UUID]error
	mu      sync.Mutex
	codes   []storage.ErrorCode
}

func (o *fakeOpener) Open(_ context.Context, id uuid.UUID) (storage.Destination, error) {
	if err := o.openErr[id]; err != nil {
		return nil, err
	}
	d, ok := o.dests[id]
	if !ok {
		return nil, errors.New("unknown destination")
	}
	return d, nil
}

func (o *fakeOpener) RecordRunFailure(_ context.Context, _ uuid.UUID, code storage.ErrorCode) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.codes = append(o.codes, code)
	return nil
}

// --- fake keys and signer -------------------------------------------------------

type fakeKeys struct{ recipient string }

func (k fakeKeys) Encryptor(uuid.UUID) pipeline.Encryptor { return k }

func (k fakeKeys) BackupEncryption(context.Context) (pipeline.Encryption, error) {
	return pipeline.RecipientEncryption(1, k.recipient), nil
}

type fakeSigner struct {
	mu  sync.Mutex
	pub map[string][]byte
}

func (s *fakeSigner) SigningKey(context.Context) (*crypto.SigningKey, error) {
	k, err := crypto.GenerateSigningKey()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pub == nil {
		s.pub = map[string][]byte{}
	}
	s.pub[k.ID] = k.Public
	return k, nil
}

// --- fake runner ----------------------------------------------------------------

type dumpScript struct {
	data      []byte
	logs      []string
	exit      int32
	warnings  []string
	rolesNoPw bool
	verified  bool
	// stats is sent before the first chunk (after it with statsLate).
	stats     *runnerpb.DumpStats
	statsLate bool
	// block: after the first chunk, wait until the call's context ends.
	block     bool
	callErr   error
	streamErr error
}

type fakeRunner struct {
	runnerpb.RunnerServiceClient

	mu        sync.Mutex
	probe     *runnerpb.ProbeResponse
	probeErr  error
	dumps     map[string]dumpScript // "" = globals
	verifyBad map[string]bool       // verify verdict false for archives starting with this
	verifies  [][]byte
	trailers  []uint64
	headers   []*runnerpb.VerifyDumpHeader
	requests  []*runnerpb.DumpRequest
	// globalsReqs are the DumpGlobals requests.
	globalsReqs []*runnerpb.DumpGlobalsRequest
	passwords   []string // passwords seen in requests (before release)
	blocked     chan struct{}
}

func (r *fakeRunner) Probe(_ context.Context, req *runnerpb.ProbeRequest, _ ...grpc.CallOption) (*runnerpb.ProbeResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.passwords = append(r.passwords, string(req.GetTarget().GetPassword()))
	if r.probeErr != nil {
		return nil, r.probeErr
	}
	return r.probe, nil
}

type msgStream[T any] struct {
	grpc.ClientStream
	ctx  context.Context
	next func() (*T, error)
}

func (s *msgStream[T]) Recv() (*T, error) { return s.next() }

// script turns a dumpScript into a Recv function producing T messages.
func scriptMsgs(ctx context.Context, sc dumpScript, blocked chan struct{}, wrap func(chunk []byte, log string, res *runnerpb.DumpResult, st *runnerpb.DumpStats) any) func() (any, error) {
	var msgs []any
	for _, l := range sc.logs {
		msgs = append(msgs, wrap(nil, l, nil, nil))
	}
	if sc.stats != nil && !sc.statsLate {
		msgs = append(msgs, wrap(nil, "", nil, sc.stats))
	}
	const chunk = 7000
	for off := 0; off < len(sc.data); off += chunk {
		end := min(off+chunk, len(sc.data))
		msgs = append(msgs, wrap(sc.data[off:end], "", nil, nil))
		if sc.stats != nil && sc.statsLate && off == 0 {
			msgs = append(msgs, wrap(nil, "", nil, sc.stats))
		}
	}
	res := &runnerpb.DumpResult{Bytes: uint64(len(sc.data)), ExitCode: sc.exit, Verified: sc.verified, Warnings: sc.warnings, RolesWithoutPasswords: sc.rolesNoPw}
	msgs = append(msgs, wrap(nil, "", res, nil))
	i, sentChunk := 0, false
	return func() (any, error) {
		if sc.block && sentChunk {
			if blocked != nil {
				select {
				case blocked <- struct{}{}:
				default:
				}
			}
			<-ctx.Done()
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		if sc.streamErr != nil && sentChunk {
			return nil, sc.streamErr
		}
		if i >= len(msgs) {
			return nil, io.EOF
		}
		m := msgs[i]
		i++
		if dm, ok := m.(dumpMsg); ok && dm.GetChunk() != nil {
			sentChunk = true
		}
		return m, nil
	}
}

func (r *fakeRunner) Dump(ctx context.Context, req *runnerpb.DumpRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[runnerpb.DumpResponse], error) {
	r.mu.Lock()
	c, _ := proto.Clone(req).(*runnerpb.DumpRequest) //nolint:errcheck // same type
	r.requests = append(r.requests, c)
	r.passwords = append(r.passwords, string(req.GetTarget().GetPassword()))
	sc := r.dumps[req.GetDatabase()]
	r.mu.Unlock()
	if sc.callErr != nil {
		return nil, sc.callErr
	}
	next := scriptMsgs(ctx, sc, r.blocked, func(chunk []byte, log string, res *runnerpb.DumpResult, st *runnerpb.DumpStats) any {
		switch {
		case st != nil:
			return &runnerpb.DumpResponse{Msg: &runnerpb.DumpResponse_Stats{Stats: st}}
		case chunk != nil:
			return &runnerpb.DumpResponse{Msg: &runnerpb.DumpResponse_Chunk{Chunk: &runnerpb.DataChunk{Data: chunk}}}
		case res != nil:
			return &runnerpb.DumpResponse{Msg: &runnerpb.DumpResponse_Result{Result: res}}
		default:
			return &runnerpb.DumpResponse{Msg: &runnerpb.DumpResponse_Log{Log: &runnerpb.LogLine{Stream: runnerpb.LogStream_LOG_STREAM_STDERR, Text: log}}}
		}
	})
	return &msgStream[runnerpb.DumpResponse]{ctx: ctx, next: func() (*runnerpb.DumpResponse, error) {
		m, err := next()
		if err != nil {
			return nil, err
		}
		r, _ := m.(*runnerpb.DumpResponse) //nolint:errcheck // test fake: always this type
		return r, nil
	}}, nil
}

func (r *fakeRunner) DumpGlobals(ctx context.Context, req *runnerpb.DumpGlobalsRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[runnerpb.DumpGlobalsResponse], error) {
	r.mu.Lock()
	gc, _ := proto.Clone(req).(*runnerpb.DumpGlobalsRequest) //nolint:errcheck // same type
	r.globalsReqs = append(r.globalsReqs, gc)
	r.passwords = append(r.passwords, string(req.GetTarget().GetPassword()))
	sc := r.dumps[""]
	r.mu.Unlock()
	if sc.callErr != nil {
		return nil, sc.callErr
	}
	next := scriptMsgs(ctx, sc, r.blocked, func(chunk []byte, log string, res *runnerpb.DumpResult, st *runnerpb.DumpStats) any {
		switch {
		case st != nil:
			return &runnerpb.DumpGlobalsResponse{Msg: &runnerpb.DumpGlobalsResponse_Stats{Stats: st}}
		case chunk != nil:
			return &runnerpb.DumpGlobalsResponse{Msg: &runnerpb.DumpGlobalsResponse_Chunk{Chunk: &runnerpb.DataChunk{Data: chunk}}}
		case res != nil:
			return &runnerpb.DumpGlobalsResponse{Msg: &runnerpb.DumpGlobalsResponse_Result{Result: res}}
		default:
			return &runnerpb.DumpGlobalsResponse{Msg: &runnerpb.DumpGlobalsResponse_Log{Log: &runnerpb.LogLine{Stream: runnerpb.LogStream_LOG_STREAM_RUNNER, Text: log}}}
		}
	})
	return &msgStream[runnerpb.DumpGlobalsResponse]{ctx: ctx, next: func() (*runnerpb.DumpGlobalsResponse, error) {
		m, err := next()
		if err != nil {
			return nil, err
		}
		r, _ := m.(*runnerpb.DumpGlobalsResponse) //nolint:errcheck // test fake: always this type
		return r, nil
	}}, nil
}

type verifyStream struct {
	grpc.ClientStream
	r       *fakeRunner
	ctx     context.Context
	header  *runnerpb.VerifyDumpHeader
	data    bytes.Buffer
	trailer *runnerpb.VerifyDumpTrailer
}

func (v *verifyStream) Send(m *runnerpb.VerifyDumpRequest) error {
	if v.ctx.Err() != nil {
		return io.EOF
	}
	switch {
	case m.GetHeader() != nil:
		v.header = m.GetHeader()
	case m.GetChunk() != nil:
		v.data.Write(m.GetChunk().GetData())
	case m.GetTrailer() != nil:
		v.trailer = m.GetTrailer()
	}
	return nil
}

func (v *verifyStream) CloseAndRecv() (*runnerpb.VerifyDumpResponse, error) {
	if err := v.ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	v.r.mu.Lock()
	defer v.r.mu.Unlock()
	v.r.verifies = append(v.r.verifies, bytes.Clone(v.data.Bytes()))
	v.r.headers = append(v.r.headers, v.header)
	if v.trailer == nil {
		return nil, status.Error(codes.InvalidArgument, "no trailer")
	}
	v.r.trailers = append(v.r.trailers, v.trailer.GetTotalBytes())
	ok := v.trailer.GetTotalBytes() == uint64(v.data.Len()) //nolint:gosec // G115: buffer length is non-negative
	for prefix := range v.r.verifyBad {
		if bytes.HasPrefix(v.data.Bytes(), []byte(prefix)) {
			ok = false
		}
	}
	resp := &runnerpb.VerifyDumpResponse{Verified: ok}
	if !ok {
		resp.ExitCode = 1
		resp.Logs = []*runnerpb.LogLine{{Stream: runnerpb.LogStream_LOG_STREAM_STDERR, Text: "pg_restore: error: could not read input file"}}
	}
	return resp, nil
}

func (r *fakeRunner) VerifyDump(ctx context.Context, _ ...grpc.CallOption) (grpc.ClientStreamingClient[runnerpb.VerifyDumpRequest, runnerpb.VerifyDumpResponse], error) {
	return &verifyStream{r: r, ctx: ctx}, nil
}

// --- harness ---------------------------------------------------------------------

type harness struct {
	t        *testing.T
	store    *fakeStore
	slots    *fakeSlots
	targets  *fakeTargets
	runner   *fakeRunner
	opener   *fakeOpener
	signer   *fakeSigner
	identity *age.X25519Identity
	destA    uuid.UUID
	destB    uuid.UUID
	reg      *logging.Registry
	logBuf   *syncBuffer
	opts     BackupWorkerOptions
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func pgArchive(name string, n int) []byte {
	var b bytes.Buffer
	b.WriteString("PGDMP-" + name + "\n")
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "row %d of %s\n", i, name)
	}
	return b.Bytes()
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{
		t: t, identity: id, destA: uuid.New(), destB: uuid.New(),
		slots: &fakeSlots{}, targets: &fakeTargets{}, signer: &fakeSigner{},
		reg: logging.NewRegistry(), logBuf: &syncBuffer{},
	}
	jobID := uuid.New()
	h.store = &fakeStore{job: runstate.Job{
		ID: jobID, Name: "Shop", Slug: "shop", Enabled: true, ConnectionID: uuid.New(),
		Engine: engine.KindPostgres, VersionKey: "17", AllDatabases: true, Exclude: []string{"skipme"},
		IncludeGlobals: true,
		Destinations:   []runstate.JobDestination{{ID: h.destA, Name: "a"}, {ID: h.destB, Name: "b"}},
	}}
	h.runner = &fakeRunner{
		probe: &runnerpb.ProbeResponse{ServerVersion: "17.2 (Debian 17.2-1)", Databases: []string{"shop", "app", "skipme"}, VersionKeyCompatible: true},
		dumps: map[string]dumpScript{
			"app":  {data: pgArchive("app", 40_000), logs: []string{"pg_dump: dumping contents of table public.users"}},
			"shop": {data: pgArchive("shop", 90_000)},
			"":     {data: []byte("CREATE ROLE app;\n--\n-- PostgreSQL database cluster dump complete\n--\n"), verified: true},
		},
		verifyBad: map[string]bool{},
	}
	h.opener = &fakeOpener{dests: map[uuid.UUID]*fakeDest{h.destA: newFakeDest(), h.destB: newFakeDest()}, openErr: map[uuid.UUID]error{}}
	logger, err := logging.New(h.logBuf, logging.Options{Format: "json", Level: "debug", Registry: h.reg})
	if err != nil {
		t.Fatal(err)
	}
	h.opts = BackupWorkerOptions{
		Store: h.store, Slots: h.slots, Targets: h.targets, Runner: h.runner, Destinations: h.opener,
		Keys: fakeKeys{recipient: id.Recipient().String()}, Signer: h.signer,
		Limits: runstate.SlotLimits{Global: 4, PerConnection: 1}, QueueTimeout: time.Minute,
		LogMaxLines: 1000, LogMaxLineBytes: 1024, LogFlushInterval: 5 * time.Millisecond,
		BekupVersion: "1.2.3", Registry: h.reg, Logger: logger,
		CancelPoll: 10 * time.Millisecond, SlotHeartbeat: 20 * time.Millisecond,
		Pipeline: PipelineTuning{ChunkSize: pipeline.MinChunkSize, LagTimeout: 2 * time.Second},
	}
	return h
}

func (h *harness) work(ctx context.Context) error {
	h.t.Helper()
	w, err := NewBackupWorker(h.opts)
	if err != nil {
		h.t.Fatal(err)
	}
	job := &river.Job[queue.BackupRunArgs]{
		JobRow: &rivertype.JobRow{ID: 42, Attempt: 1},
		Args:   queue.BackupRunArgs{JobID: h.store.job.ID, Trigger: queue.TriggerSchedule, ScheduledFor: time.Now()},
	}
	return w.Work(ctx, job)
}

func (h *harness) result() *runstate.Result {
	h.t.Helper()
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	if h.store.result == nil {
		h.t.Fatal("the run was not finished")
	}
	return h.store.result
}

// decrypt opens an artifact with the test identity.
func (h *harness) decrypt(b []byte) []byte {
	h.t.Helper()
	r, err := pipeline.OpenArtifact(bytes.NewReader(b), pipeline.Decryption{Identities: []byte(h.identity.String())})
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = r.Close() }() //nolint:gosec // G104: test cleanup
	out, err := io.ReadAll(r)
	if err != nil {
		h.t.Fatal(err)
	}
	return out
}

func (h *harness) assertNoSecrets() {
	h.t.Helper()
	for name, text := range map[string]string{"run log": h.store.logText(), "process log": h.logBuf.String(), "result": fmt.Sprintf("%+v", h.store.result)} {
		if strings.Contains(text, testDBPassword) {
			h.t.Fatalf("the database password leaked into the %s:\n%s", name, text)
		}
	}
	h.targets.mu.Lock()
	defer h.targets.mu.Unlock()
	for _, t := range h.targets.issued {
		if len(t.GetPassword()) != 0 && !bytes.Equal(t.GetPassword(), make([]byte, len(t.GetPassword()))) {
			h.t.Fatal("a runner request kept its password after the call")
		}
	}
}

// --- tests ---------------------------------------------------------------------

func TestBackupRunSuccess(t *testing.T) {
	h := newHarness(t)
	if err := h.work(t.Context()); err != nil {
		t.Fatalf("Work: %v", err)
	}
	res := h.result()
	if res.Status != runstate.StatusSuccess || res.Failure != nil {
		t.Fatalf("status %s %+v warnings %+v\nlog:\n%s", res.Status, res.Failure, res.Warnings, h.store.logText())
	}
	// Items: app, shop (sorted, skipme excluded) + globals; all verified.
	for _, it := range []struct {
		db   string
		kind sqlc.RunItemKind
	}{{"app", sqlc.RunItemKindDatabase}, {"shop", sqlc.RunItemKindDatabase}, {"", sqlc.RunItemKindGlobals}} {
		fi := h.store.item(it.db, it.kind)
		if fi == nil || fi.res == nil || fi.res.Status != runstate.ItemSuccess || !fi.res.Verified || len(fi.res.SHA256) != 32 {
			t.Fatalf("item %q: %+v", it.db, fi)
		}
	}
	if h.store.item("skipme", sqlc.RunItemKindDatabase) != nil {
		t.Fatal("an excluded database was dumped")
	}
	// The raw PostgreSQL archives were teed into streamed verifies.
	if len(h.runner.verifies) != 2 || len(h.runner.trailers) != 2 {
		t.Fatalf("verifies %d trailers %v", len(h.runner.verifies), h.runner.trailers)
	}
	for i, v := range h.runner.verifies {
		if h.runner.trailers[i] != uint64(len(v)) || h.runner.headers[i].GetTotalBytes() != 0 || h.runner.headers[i].GetCtx().GetRunId() != h.store.run.RunID.String() {
			t.Fatalf("verify %d: %d bytes, trailer %d", i, len(v), h.runner.trailers[i])
		}
	}
	if len(h.store.artifacts) != 6 || len(h.store.manifests) != 2 {
		t.Fatalf("artifacts %d manifests %d", len(h.store.artifacts), len(h.store.manifests))
	}
	// Each destination holds the three artifacts and a manifest that
	// verifies against the run's records and decrypts to the dumps.
	for _, dest := range []uuid.UUID{h.destA, h.destB} {
		fd := h.opener.dests[dest]
		var mkey string
		var expect []pipeline.ExpectedEntry
		for _, a := range h.store.artifacts {
			if a.DestinationID != dest {
				continue
			}
			if !a.Verified || a.SigningKeyID == "" || a.EncryptionMode != "recipients" || a.RecipientVersion != 1 {
				t.Fatalf("artifact %+v", a)
			}
			b, ok := fd.object(a.Key)
			if !ok || int64(len(b)) != a.Size {
				t.Fatalf("object %s missing", a.Key)
			}
			expect = append(expect, pipeline.ExpectedEntry{Path: a.Key[strings.LastIndex(a.Key, "/")+1:], SHA256: a.SHA256, StoredSize: a.Size})
			plain := h.decrypt(b)
			var want []byte
			switch {
			case strings.Contains(a.Key, "/app."):
				want = h.runner.dumps["app"].data
			case strings.Contains(a.Key, "/shop."):
				want = h.runner.dumps["shop"].data
			default:
				want = h.runner.dumps[""].data
			}
			if !bytes.Equal(plain, want) {
				t.Fatalf("%s does not decrypt to its dump", a.Key)
			}
		}
		for _, m := range h.store.manifests {
			if m.DestinationID == dest {
				mkey = m.Key
			}
		}
		file, ok := fd.object(mkey)
		if !ok {
			t.Fatalf("manifest %q missing on %s", mkey, dest)
		}
		m, err := pipeline.Verify(file, pipeline.PublicKeys(h.signer.pub), pipeline.Binding{
			JobID: h.store.job.ID.String(), RunID: h.store.run.RunID.String(), JobSlug: "shop", Entries: expect,
		})
		if err != nil {
			t.Fatalf("manifest: %v", err)
		}
		if m.Engine.ServerVersion != "17.2 (Debian 17.2-1)" || m.BekupVersion != "1.2.3" || len(m.Entries) != 3 {
			t.Fatalf("manifest %+v", m)
		}
	}
	if h.slots.released != 1 || len(h.slots.held) != 0 {
		t.Fatal("the slot was not released")
	}
	if !strings.Contains(h.store.logText(), "pg_dump: dumping contents of table public.users") {
		t.Fatalf("runner log lines missing:\n%s", h.store.logText())
	}
	h.assertNoSecrets()
}

func TestBackupRunDestinationDropIsPartial(t *testing.T) {
	h := newHarness(t)
	h.opener.dests[h.destB].failAfter = 1
	if err := h.work(t.Context()); err != nil {
		t.Fatal(err)
	}
	res := h.result()
	if res.Status != runstate.StatusPartial {
		t.Fatalf("status %s %+v", res.Status, res.Failure)
	}
	if len(h.opener.dests[h.destB].keys()) != 0 {
		t.Fatalf("the failing destination holds objects: %v", h.opener.dests[h.destB].keys())
	}
	if len(h.store.manifests) != 1 || h.store.manifests[0].DestinationID != h.destA {
		t.Fatalf("manifests %+v", h.store.manifests)
	}
	found := false
	for _, w := range res.Warnings {
		if w.Code == CodeDestinationDropped && w.DestinationID == h.destB.String() {
			found = true
			if strings.Contains(w.Message, "/secret/path") {
				t.Fatal("a raw backend error reached a warning")
			}
		}
	}
	if !found {
		t.Fatalf("no drop warning: %+v", res.Warnings)
	}
	if it := h.store.item("app", sqlc.RunItemKindDatabase); it.res.Status != runstate.ItemPartial {
		t.Fatalf("item status %s", it.res.Status)
	}
	h.assertSignedExactlyEntries()
}

func TestBackupRunVerifyFailure(t *testing.T) {
	h := newHarness(t)
	h.runner.verifyBad["PGDMP-shop"] = true
	if err := h.work(t.Context()); err != nil {
		t.Fatal(err)
	}
	res := h.result()
	if res.Status != runstate.StatusPartial {
		t.Fatalf("status %s", res.Status)
	}
	it := h.store.item("shop", sqlc.RunItemKindDatabase)
	if it.res.Status != runstate.ItemFailed || it.res.Failure.Code != CodeVerifyFailed || it.res.Verified {
		t.Fatalf("item %+v", it.res)
	}
	unverified := 0
	for _, a := range h.store.artifacts {
		if strings.Contains(a.Key, "/shop.") {
			if a.Verified || a.SigningKeyID != "" {
				t.Fatalf("unverified artifact flagged as verified/signed: %+v", a)
			}
			unverified++
		}
	}
	if unverified != 2 {
		t.Fatalf("unverified artifacts %d", unverified)
	}
	// The manifest lists only the verified entries.
	file, _ := h.opener.dests[h.destA].object(h.store.manifests[0].Key)
	m, err := pipeline.VerifySignature(file, pipeline.PublicKeys(h.signer.pub))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Entries {
		if strings.HasPrefix(e.Path, "shop.") {
			t.Fatal("an unverified dump is in the manifest")
		}
	}
	if !strings.Contains(h.store.logText(), "pg_restore: error") {
		t.Fatal("the verify output is not in the run log")
	}
	h.assertSignedExactlyEntries()
}

// assertSignedExactlyEntries checks that each MarkSigned call named exactly
// the artifacts the manifest on that destination lists (by object key).
func (h *harness) assertSignedExactlyEntries() {
	h.t.Helper()
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	for i := range h.store.manifests {
		man := &h.store.manifests[i]
		file, _ := h.opener.dests[man.DestinationID].object(man.Key)
		m, err := pipeline.VerifySignature(file, pipeline.PublicKeys(h.signer.pub))
		if err != nil {
			h.t.Fatal(err)
		}
		listed := make([]string, 0, len(m.Entries))
		signed := make([]string, 0, len(m.Entries))
		for j := range m.Entries {
			listed = append(listed, m.Entries[j].Path)
		}
		for _, id := range h.store.signedEntries[i] {
			a := h.store.artifacts[slices.Index(h.store.artIDs, id)]
			signed = append(signed, a.Key[strings.LastIndex(a.Key, "/")+1:])
		}
		slices.Sort(listed)
		slices.Sort(signed)
		if !slices.Equal(listed, signed) {
			h.t.Fatalf("manifest on %s lists %v, signed %v", man.DestinationID, listed, signed)
		}
		for j := range h.store.artifacts {
			a := &h.store.artifacts[j]
			if a.DestinationID == man.DestinationID && a.SigningKeyID != "" && !slices.Contains(h.store.signedEntries[i], h.store.artIDs[j]) {
				h.t.Fatalf("artifact %s signed but not in the manifest", a.Key)
			}
		}
	}
}

func TestBackupRunGlobalsFallbackWarning(t *testing.T) {
	h := newHarness(t)
	g := h.runner.dumps[""]
	g.rolesNoPw, g.warnings = true, []string{"roles_without_passwords"}
	h.runner.dumps[""] = g
	if err := h.work(t.Context()); err != nil {
		t.Fatal(err)
	}
	res := h.result()
	if res.Status != runstate.StatusSuccess {
		t.Fatalf("status %s %+v", res.Status, res.Failure)
	}
	found := false
	for _, w := range res.Warnings {
		if w.Code == CodeRolesNoPasswords {
			found = true
		}
	}
	if !found {
		t.Fatalf("no fallback warning: %+v", res.Warnings)
	}
	file, _ := h.opener.dests[h.destA].object(h.store.manifests[0].Key)
	m, err := pipeline.VerifySignature(file, pipeline.PublicKeys(h.signer.pub))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(m.Warnings, "\n"), "roles_without_passwords") {
		t.Fatalf("manifest warnings %v", m.Warnings)
	}
}

func TestBackupRunCancelMidDump(t *testing.T) {
	h := newHarness(t)
	h.runner.blocked = make(chan struct{}, 1)
	sc := h.runner.dumps["app"]
	sc.block = true
	h.runner.dumps["app"] = sc
	go func() {
		<-h.runner.blocked
		h.store.cancelReq.Store(true)
	}()
	err := h.work(t.Context())
	var ce *river.JobCancelError
	if !errors.As(err, &ce) {
		t.Fatalf("Work = %v, want a JobCancel error", err)
	}
	res := h.result()
	if res.Status != runstate.StatusCanceled || res.Failure.Code != CodeCanceled {
		t.Fatalf("status %s %+v", res.Status, res.Failure)
	}
	for _, d := range h.opener.dests {
		if keys := d.keys(); len(keys) != 0 {
			t.Fatalf("a canceled run left objects: %v", keys)
		}
	}
	if it := h.store.item("app", sqlc.RunItemKindDatabase); it.res.Status != runstate.ItemCanceled {
		t.Fatalf("item %+v", it.res)
	}
	if h.store.item("shop", sqlc.RunItemKindDatabase) != nil {
		t.Fatal("a database was dumped after the cancel")
	}
}

func TestBackupRunTimeout(t *testing.T) {
	h := newHarness(t)
	h.store.job.Timeout = 300 * time.Millisecond
	sc := h.runner.dumps["shop"]
	sc.block = true
	h.runner.dumps["shop"] = sc
	if err := h.work(t.Context()); err != nil {
		t.Fatalf("Work = %v (a timeout is not retried)", err)
	}
	res := h.result()
	// app (sorted first) completed before the timeout: partial, with the
	// manifest of the complete item.
	if res.Status != runstate.StatusPartial || res.Failure.Code != CodeTimeout {
		t.Fatalf("status %s %+v", res.Status, res.Failure)
	}
	if len(h.store.manifests) != 2 {
		t.Fatalf("manifests %d", len(h.store.manifests))
	}
	if it := h.store.item("shop", sqlc.RunItemKindDatabase); it.res.Failure.Code != CodeTimeout {
		t.Fatalf("item %+v", it.res)
	}
	for _, d := range h.opener.dests {
		for _, k := range d.keys() {
			if strings.Contains(k, "/shop.") {
				t.Fatalf("a timed-out dump left an object: %s", k)
			}
		}
	}
}

func TestBackupRunSkipsDeletedOrDisabledJob(t *testing.T) {
	for name, mut := range map[string]func(*runstate.Job){
		"deleted":  func(j *runstate.Job) { j.Deleted = true },
		"disabled": func(j *runstate.Job) { j.Enabled = false },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			mut(&h.store.job)
			if err := h.work(t.Context()); err != nil {
				t.Fatal(err)
			}
			res := h.result()
			if res.Status != runstate.StatusSkipped || h.slots.acquired != 0 || h.store.started {
				t.Fatalf("status %s acquired %d started %v", res.Status, h.slots.acquired, h.store.started)
			}
		})
	}
	h := newHarness(t)
	h.store.jobErr = runstate.ErrJobNotFound
	if err := h.work(t.Context()); err != nil {
		t.Fatal(err)
	}
	if res := h.result(); res.Status != runstate.StatusSkipped || res.Failure.Code != CodeJobMissing {
		t.Fatalf("missing job: %+v", res)
	}
}

func TestBackupRunRedactsSecrets(t *testing.T) {
	h := newHarness(t)
	sc := h.runner.dumps["app"]
	sc.logs = append(sc.logs,
		"pg_dump: connecting with password "+testDBPassword,
		"pg_dump: error: connection to postgres://backup:"+testDBPassword+"@db.example.test/app failed",
		"pg_dump: host=db password=hunter2hunter2 dbname=app")
	sc.exit = 1
	h.runner.dumps["app"] = sc
	if err := h.work(t.Context()); err != nil {
		t.Fatal(err)
	}
	logs := h.store.logText()
	if strings.Contains(logs, "hunter2hunter2") || !strings.Contains(logs, logging.Redacted) {
		t.Fatalf("run log not redacted:\n%s", logs)
	}
	it := h.store.item("app", sqlc.RunItemKindDatabase)
	if it.res.Failure.Code != CodeDumpFailed || !strings.Contains(it.res.Failure.Message, "exited with code 1") {
		t.Fatalf("item %+v", it.res.Failure)
	}
	if h.reg.Len() != 0 {
		t.Fatalf("%d secrets stay registered after the run", h.reg.Len())
	}
	h.assertNoSecrets()
}

func TestBackupRunLogCaps(t *testing.T) {
	h := newHarness(t)
	h.opts.LogMaxLines = 10
	h.opts.LogMaxLineBytes = 200
	sc := h.runner.dumps["app"]
	sc.logs = nil
	sc.logs = append(sc.logs, strings.Repeat("x", 5000))
	for i := range 50 {
		sc.logs = append(sc.logs, fmt.Sprintf("line %d", i))
	}
	h.runner.dumps["app"] = sc
	if err := h.work(t.Context()); err != nil {
		t.Fatal(err)
	}
	res := h.result()
	h.store.mu.Lock()
	n := len(h.store.logs)
	var long string
	for _, l := range h.store.logs {
		if strings.HasPrefix(l.Text, "xxx") {
			long = l.Text
		}
	}
	h.store.mu.Unlock()
	if n != 11 || res.LogLines != 11 || res.LogLinesDropped == 0 {
		t.Fatalf("stored %d lines (result %d, dropped %d)", n, res.LogLines, res.LogLinesDropped)
	}
	if len(long) > 200 || !strings.HasSuffix(long, "[truncated]") {
		t.Fatalf("long line %d bytes: %q", len(long), long)
	}
	if !strings.Contains(h.store.logText(), "log lines were not stored") {
		t.Fatal("no truncation notice")
	}
}

func TestBackupRunWaitsForSlot(t *testing.T) {
	h := newHarness(t)
	h.slots.full = true
	err := h.work(t.Context())
	var se *river.JobSnoozeError
	if !errors.As(err, &se) {
		t.Fatalf("Work = %v, want a snooze", err)
	}
	if h.store.started || h.store.result != nil {
		t.Fatal("a run without a slot started or finished")
	}
}

func TestBackupRunMissingSelectedDatabase(t *testing.T) {
	h := newHarness(t)
	h.store.job.AllDatabases, h.store.job.Exclude = false, nil
	h.store.job.Databases = []string{"shop", "gone"}
	h.store.job.IncludeGlobals = false
	if err := h.work(t.Context()); err != nil {
		t.Fatal(err)
	}
	res := h.result()
	if res.Status != runstate.StatusPartial {
		t.Fatalf("status %s", res.Status)
	}
	it := h.store.item("gone", sqlc.RunItemKindDatabase)
	if it == nil || it.res.Status != runstate.ItemFailed || it.res.Failure.Code != CodeDatabaseMissing {
		t.Fatalf("missing item %+v", it)
	}
	if h.store.item("app", sqlc.RunItemKindDatabase) != nil {
		t.Fatal("an unselected database was dumped")
	}
}

func TestBackupRunRunnerUnavailableIsRetried(t *testing.T) {
	h := newHarness(t)
	h.runner.probeErr = status.Error(codes.Unavailable, "connection refused")
	err := h.work(t.Context())
	if err == nil || strings.Contains(err.Error(), testDBPassword) {
		t.Fatalf("Work = %v, want a retry error", err)
	}
	if res := h.result(); res.Status != runstate.StatusFailed || res.Failure.Code != CodeRunnerUnavailable {
		t.Fatalf("result %+v", res)
	}
}

func TestBackupRunPinMismatchIsASecurityEvent(t *testing.T) {
	h := newHarness(t)
	h.runner.probeErr = runnerpb.PinMismatchStatus("leaf_spki")
	if err := h.work(t.Context()); err != nil {
		t.Fatalf("Work = %v", err)
	}
	res := h.result()
	if res.Status != runstate.StatusFailed || res.Failure.Code != "tls_pin_mismatch" {
		t.Fatalf("result %+v", res.Failure)
	}
	if len(h.targets.failures) != 1 {
		t.Fatalf("security events %d", len(h.targets.failures))
	}
}

func TestBackupRunDestinationUnavailable(t *testing.T) {
	h := newHarness(t)
	h.opener.openErr[h.destB] = storage.WithCode(storage.CodeHostKeyMismatch, errors.New("ssh: host key mismatch"))
	if err := h.work(t.Context()); err != nil {
		t.Fatal(err)
	}
	res := h.result()
	if res.Status != runstate.StatusPartial {
		t.Fatalf("status %s", res.Status)
	}
	if len(h.opener.codes) != 1 || h.opener.codes[0] != storage.CodeHostKeyMismatch {
		t.Fatalf("destination security events %v", h.opener.codes)
	}
}

func TestSelectDatabases(t *testing.T) {
	all := runstate.Job{AllDatabases: true, Exclude: []string{"b"}}
	dbs, missing := selectDatabases(all, []string{"c", "b", "a", "a"})
	if strings.Join(dbs, ",") != "a,c" || len(missing) != 0 {
		t.Fatalf("all: %v %v", dbs, missing)
	}
	sel := runstate.Job{Databases: []string{"z", "x", "q"}}
	dbs, missing = selectDatabases(sel, []string{"x", "z"})
	if strings.Join(dbs, ",") != "z,x" || strings.Join(missing, ",") != "q" {
		t.Fatalf("selected: %v %v", dbs, missing)
	}
}

func TestRunnerFailureNeverEchoesServerText(t *testing.T) {
	err := status.Error(codes.Internal, "FATAL: password authentication failed for user backup: "+testDBPassword)
	f := runnerFailure(context.Background(), err)
	if strings.Contains(f.Message, testDBPassword) || f.Code != CodeRunnerFailed {
		t.Fatalf("%+v", f)
	}
	f = runnerFailure(context.Background(), runnerpb.ReasonAuthFailed.Status())
	if f.Code != "target_auth_failed" || !strings.Contains(f.Message, "rejected") {
		t.Fatalf("%+v", f)
	}
}
