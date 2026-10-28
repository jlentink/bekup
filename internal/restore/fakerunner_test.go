package restore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// FakeRunner is a scripted runner API for the restore tests (exported for
// the restore_test package). It keeps a set of databases and records every
// call in order ("create:<db>", "restore:<db>", "swap:<db>",
// "drop:<derived name>").
type FakeRunner struct {
	runnerpb.RunnerServiceClient

	mu        sync.Mutex
	DBs       map[string]bool
	Calls     []string
	Headers   []*runnerpb.RestoreHeader
	Received  map[string][]byte
	Passwords [][]byte

	// ExitCode of the restore tool; RestoreErr fails the call instead.
	ExitCode   int32
	RestoreErr error
	// Block makes Restore wait for the context after the header (cancel
	// tests); Started is closed when the first chunk arrived.
	Block   bool
	Started chan struct{}
	// SwapErr / DropErr / CreateErr fail those calls.
	SwapErr, DropErr, CreateErr error
	// Logs are sent before the result.
	Logs []string
	// Warnings of the result.
	Warnings []string
	// OnRestore runs when a Restore header arrives (outside the lock).
	OnRestore func(h *runnerpb.RestoreHeader)
	// EarlyEnd ends the call after the first chunk with a result (the
	// tool exited before reading the whole dump); Send then returns
	// io.EOF.
	EarlyEnd bool
	// SwapAppliedErr applies the swap but fails the call (a lost answer).
	SwapAppliedErr error
	// NeutralizeErr fails NeutralizeRestoredDatabase; Secdef are the
	// SECURITY DEFINER functions it reports as switched.
	NeutralizeErr error
	Secdef        []string
	// ProbeErr fails Probe; Privileges is its verdict (nil:
	// NOT_PRIVILEGED).
	ProbeErr   error
	Privileges *runnerpb.TargetPrivileges
}

// NewFakeRunner returns a fake with the given existing databases.
func NewFakeRunner(dbs ...string) *FakeRunner {
	f := &FakeRunner{DBs: map[string]bool{}, Received: map[string][]byte{}, Started: make(chan struct{})}
	for _, d := range dbs {
		f.DBs[d] = true
	}
	return f
}

func (f *FakeRunner) record(call string, t *runnerpb.Target) {
	f.Calls = append(f.Calls, call)
	f.Passwords = append(f.Passwords, bytes.Clone(t.GetPassword()))
}

// CallList returns a copy of the calls.
func (f *FakeRunner) CallList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.Calls)
}

// Has reports whether a database exists.
func (f *FakeRunner) Has(db string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.DBs[db]
}

// Data returns what a database received.
func (f *FakeRunner) Data(db string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return bytes.Clone(f.Received[db])
}

func (f *FakeRunner) CreateDatabase(_ context.Context, in *runnerpb.CreateDatabaseRequest, _ ...grpc.CallOption) (*runnerpb.CreateDatabaseResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("create:"+in.GetDatabase(), in.GetTarget())
	if f.CreateErr != nil {
		return nil, f.CreateErr
	}
	if f.DBs[in.GetDatabase()] {
		return nil, runnerpb.ReasonDatabaseExists.Status()
	}
	f.DBs[in.GetDatabase()] = true
	return &runnerpb.CreateDatabaseResponse{}, nil
}

func (f *FakeRunner) RenameSwapDatabase(_ context.Context, in *runnerpb.RenameSwapDatabaseRequest, _ ...grpc.CallOption) (*runnerpb.RenameSwapDatabaseResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("swap:"+in.GetDatabase(), in.GetTarget())
	if f.SwapErr != nil {
		return nil, f.SwapErr
	}
	db := in.GetDatabase()
	temp := db + "__bekup_restore_" + in.GetTempId()
	old := db + "__bekup_old_" + in.GetTempId()
	if !f.DBs[db] || !f.DBs[temp] {
		return nil, runnerpb.ReasonDatabaseNotFound.Status()
	}
	f.DBs[old], f.Received[old] = true, f.Received[db]
	f.Received[db] = f.Received[temp]
	delete(f.DBs, temp)
	delete(f.Received, temp)
	if f.SwapAppliedErr != nil {
		return nil, f.SwapAppliedErr
	}
	return &runnerpb.RenameSwapDatabaseResponse{}, nil
}

func (f *FakeRunner) NeutralizeRestoredDatabase(_ context.Context, in *runnerpb.NeutralizeRestoredDatabaseRequest, _ ...grpc.CallOption) (*runnerpb.NeutralizeRestoredDatabaseResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var name string
	switch in.GetKind() {
	case runnerpb.NeutralizeKind_NEUTRALIZE_KIND_RESTORE_STAGING:
		name = in.GetDatabase() + "__bekup_restore_" + in.GetTempId()
	case runnerpb.NeutralizeKind_NEUTRALIZE_KIND_NEW_DATABASE:
		name = in.GetDatabase()
	case runnerpb.NeutralizeKind_NEUTRALIZE_KIND_SCRATCH:
		name = "bekup_test_" + in.GetTempId()
	default:
		return nil, runnerpb.ReasonFailed.Status()
	}
	f.record("neutralize:"+name, in.GetTarget())
	if f.NeutralizeErr != nil {
		return nil, f.NeutralizeErr
	}
	if !f.DBs[name] {
		return nil, runnerpb.ReasonDatabaseNotFound.Status()
	}
	return &runnerpb.NeutralizeRestoredDatabaseResponse{
		Database: name, Neutralized: uint32(len(f.Secdef)), Functions: slices.Clone(f.Secdef), //nolint:gosec // test data
	}, nil
}

func (f *FakeRunner) Probe(_ context.Context, in *runnerpb.ProbeRequest, _ ...grpc.CallOption) (*runnerpb.ProbeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("probe", in.GetTarget())
	if f.ProbeErr != nil {
		return nil, f.ProbeErr
	}
	dbs := make([]string, 0, len(f.DBs))
	for d := range f.DBs {
		dbs = append(dbs, d)
	}
	slices.Sort(dbs)
	priv := f.Privileges
	if priv == nil {
		priv = &runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_NOT_PRIVILEGED}
	}
	return &runnerpb.ProbeResponse{Databases: dbs, Privileges: priv}, nil
}

func (f *FakeRunner) DropTempDatabase(_ context.Context, in *runnerpb.DropTempDatabaseRequest, _ ...grpc.CallOption) (*runnerpb.DropTempDatabaseResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	marker := "__bekup_restore_"
	if in.GetRole() == runnerpb.TempDatabaseRole_TEMP_DATABASE_ROLE_OLD {
		marker = "__bekup_old_"
	}
	name := in.GetDatabase() + marker + in.GetTempId()
	f.record("drop:"+name, in.GetTarget())
	if f.DropErr != nil {
		return nil, f.DropErr
	}
	existed := f.DBs[name]
	delete(f.DBs, name)
	delete(f.Received, name)
	return &runnerpb.DropTempDatabaseResponse{Existed: existed}, nil
}

func (f *FakeRunner) Restore(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[runnerpb.RestoreRequest, runnerpb.RestoreResponse], error) {
	return &fakeStream{f: f, ctx: ctx, out: make(chan *runnerpb.RestoreResponse, 64)}, nil
}

// fakeStream is one Restore call.
type fakeStream struct {
	grpc.ClientStream
	f      *FakeRunner
	ctx    context.Context
	header *runnerpb.RestoreHeader
	got    bytes.Buffer
	out    chan *runnerpb.RestoreResponse
	once   sync.Once
	closed bool
	err    error
}

func (s *fakeStream) Context() context.Context     { return s.ctx }
func (s *fakeStream) Header() (metadata.MD, error) { return nil, nil }
func (s *fakeStream) Trailer() metadata.MD         { return nil }

func (s *fakeStream) Send(m *runnerpb.RestoreRequest) error {
	if err := s.ctx.Err(); err != nil {
		return io.EOF
	}
	if h := m.GetHeader(); h != nil {
		s.f.mu.Lock()
		s.header = h
		s.f.Headers = append(s.f.Headers, h)
		db := h.GetDatabase()
		if h.GetKind() == runnerpb.DumpKind_DUMP_KIND_GLOBALS {
			db = "(globals)"
		}
		s.f.record("restore:"+db, h.GetTarget())
		if hook := s.f.OnRestore; hook != nil {
			s.f.mu.Unlock()
			hook(h)
			s.f.mu.Lock()
		}
		if h.GetKind() == runnerpb.DumpKind_DUMP_KIND_DATABASE && !s.f.DBs[h.GetDatabase()] {
			s.f.mu.Unlock()
			s.finish(nil, runnerpb.ReasonDatabaseNotFound.Status())
			return nil
		}
		s.f.mu.Unlock()
		return nil
	}
	if s.closed {
		return io.EOF
	}
	s.got.Write(m.GetChunk().GetData())
	s.f.mu.Lock()
	block, early, exit := s.f.Block, s.f.EarlyEnd, s.f.ExitCode
	s.f.mu.Unlock()
	if early {
		s.closed = true
		s.finish([]*runnerpb.RestoreResponse{{Msg: &runnerpb.RestoreResponse_Result{Result: &runnerpb.RestoreResult{
			BytesReceived: uint64(s.got.Len()), //nolint:gosec // G115: a buffer length is never negative
			ExitCode:      exit,
		}}}}, nil)
		return io.EOF
	}
	if block {
		select {
		case <-s.f.Started:
		default:
			close(s.f.Started)
		}
		<-s.ctx.Done()
		return io.EOF
	}
	return nil
}

func (s *fakeStream) CloseSend() error {
	s.f.mu.Lock()
	db := s.header.GetDatabase()
	s.f.Received[db] = append(s.f.Received[db], s.got.Bytes()...)
	exit, rerr, logs, warns := s.f.ExitCode, s.f.RestoreErr, s.f.Logs, s.f.Warnings
	s.f.mu.Unlock()
	if rerr != nil {
		s.finish(nil, rerr)
		return nil
	}
	var msgs []*runnerpb.RestoreResponse
	for _, l := range logs {
		msgs = append(msgs, &runnerpb.RestoreResponse{Msg: &runnerpb.RestoreResponse_Log{Log: &runnerpb.LogLine{
			Stream: runnerpb.LogStream_LOG_STREAM_STDERR, Text: l,
		}}})
	}
	msgs = append(msgs, &runnerpb.RestoreResponse{Msg: &runnerpb.RestoreResponse_Result{Result: &runnerpb.RestoreResult{
		BytesReceived: uint64(s.got.Len()), //nolint:gosec // G115: a buffer length is never negative
		ExitCode:      exit, Warnings: warns,
	}}})
	s.finish(msgs, nil)
	return nil
}

func (s *fakeStream) finish(msgs []*runnerpb.RestoreResponse, err error) {
	s.once.Do(func() {
		for _, m := range msgs {
			s.out <- m
		}
		s.err = err
		close(s.out)
	})
}

func (s *fakeStream) Recv() (*runnerpb.RestoreResponse, error) {
	select {
	case m, ok := <-s.out:
		if !ok {
			if s.err != nil {
				return nil, s.err
			}
			return nil, io.EOF
		}
		return m, nil
	case <-s.ctx.Done():
		return nil, errors.Join(context.Canceled, s.ctx.Err())
	}
}
