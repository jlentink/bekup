package restore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/spec"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// myLogs collects log lines.
type myLogs struct {
	mu    sync.Mutex
	lines []string
}

func (l *myLogs) Line(level, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, level+": "+msg)
}

func (l *myLogs) has(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// myFakeRunner records CreateDatabase and Restore calls.
type myFakeRunner struct {
	runnerpb.RunnerServiceClient
	mu        sync.Mutex
	creates   []string
	createErr error
	header    *runnerpb.RestoreHeader
	data      bytes.Buffer
	exitCode  int32
	// failAfter makes the runner end the stream with the result after
	// this many chunks (a client that exited early).
	failAfter int
	calls     []string
	canceled  bool
}

func (f *myFakeRunner) CreateDatabase(_ context.Context, in *runnerpb.CreateDatabaseRequest, _ ...grpc.CallOption) (*runnerpb.CreateDatabaseResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "create")
	f.creates = append(f.creates, in.GetDatabase())
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &runnerpb.CreateDatabaseResponse{}, nil
}

func (f *myFakeRunner) Restore(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[runnerpb.RestoreRequest, runnerpb.RestoreResponse], error) {
	f.mu.Lock()
	f.calls = append(f.calls, "restore")
	f.mu.Unlock()
	return &myFakeStream{ctx: ctx, f: f, out: make(chan *runnerpb.RestoreResponse, 4)}, nil
}

type myFakeStream struct {
	grpc.ClientStream
	ctx    context.Context
	f      *myFakeRunner
	out    chan *runnerpb.RestoreResponse
	chunks int
	ended  bool
}

func (s *myFakeStream) result() {
	if s.ended {
		return
	}
	s.ended = true
	s.out <- &runnerpb.RestoreResponse{Msg: &runnerpb.RestoreResponse_Log{Log: &runnerpb.LogLine{
		Stream: runnerpb.LogStream_LOG_STREAM_STDERR, Text: "ERROR at line 1: test",
	}}}
	s.out <- &runnerpb.RestoreResponse{Msg: &runnerpb.RestoreResponse_Result{Result: &runnerpb.RestoreResult{
		ExitCode: s.f.exitCode, Warnings: []string{"legacy_client"}, DroppedLogLines: 2, BytesReceived: uint64(s.f.data.Len()), //nolint:gosec // G115: a buffer length is never negative
	}}}
	close(s.out)
}

func (s *myFakeStream) Send(m *runnerpb.RestoreRequest) error {
	if s.ended {
		return io.EOF
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	if h := m.GetHeader(); h != nil {
		s.f.header = h
		return nil
	}
	s.chunks++
	if len(m.GetChunk().GetData()) > runnerpb.MaxChunkBytes {
		panic("chunk too large")
	}
	s.f.data.Write(m.GetChunk().GetData())
	if s.f.failAfter > 0 && s.chunks >= s.f.failAfter {
		s.result()
	}
	return nil
}

func (s *myFakeStream) CloseSend() error {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	s.result()
	return nil
}

func (s *myFakeStream) Recv() (*runnerpb.RestoreResponse, error) {
	select {
	case m, ok := <-s.out:
		if !ok {
			return nil, io.EOF
		}
		return m, nil
	case <-s.ctx.Done():
		s.f.mu.Lock()
		s.f.canceled = true
		s.f.mu.Unlock()
		return nil, s.ctx.Err()
	}
}

func (s *myFakeStream) Header() (metadata.MD, error) { return nil, nil }

const myDump = "-- MySQL dump\nCREATE TABLE t (id int);\nINSERT INTO t VALUES (1);\n-- Dump completed on 2026-10-28  8:00:00\n"

func myRequest(f *myFakeRunner, kind engine.Kind, mode Mode, dump string) (EngineRequest, *int) {
	released := new(int)
	return EngineRequest{
		RestoreID: uuid.MustParse("0b9c6c84-0f3e-4c55-9f0e-1d2b3c4d5e6f"),
		Runner:    f,
		Target: func(context.Context) (*runnerpb.Target, func(), error) {
			t := &runnerpb.Target{Engine: myAPIEngine(kind), VersionKey: "8.4", Host: "db", Port: 3306, User: "u", Password: []byte("pw-value")}
			return t, func() { *released++; runnerpb.ZeroSecrets(t) }, nil
		},
		TargetEngine: kind, TargetDB: "shop", Mode: mode, Kind: KindDatabase,
		SourceEngine: kind, SourceVersion: "8.4", TotalBytes: uint64(len(dump)),
	}, released
}

func restorerOf(t *testing.T, k engine.Kind) EngineRestorer {
	t.Helper()
	r, ok := restorerFor(k)
	if !ok {
		t.Fatalf("no restorer for %s", k)
	}
	return r
}

func TestMySQLRegistered(t *testing.T) {
	got := RegisteredEngines()
	for _, k := range []string{"mysql", "mariadb"} {
		if !slices.Contains(got, k) {
			t.Fatalf("%s not registered: %v", k, got)
		}
	}
}

func TestMySQLRestoreNewDB(t *testing.T) {
	for _, k := range []engine.Kind{engine.KindMySQL, engine.KindMariaDB} {
		t.Run(string(k), func(t *testing.T) {
			f := &myFakeRunner{}
			req, released := myRequest(f, k, ModeNewDB, myDump)
			logs := &myLogs{}
			res, err := restorerOf(t, k).Restore(t.Context(), req, strings.NewReader(myDump), logs)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(f.calls, []string{"create", "restore"}) || !slices.Equal(f.creates, []string{"shop"}) {
				t.Fatalf("calls %v creates %v", f.calls, f.creates)
			}
			h := f.header
			if h.GetDatabase() != "shop" || h.GetKind() != runnerpb.DumpKind_DUMP_KIND_DATABASE || h.GetNoOwner() ||
				h.GetSourceVersionKey() != "8.4" || h.GetTotalBytes() != uint64(len(myDump)) || h.GetCtx().GetRunId() != req.RestoreID.String() {
				t.Fatalf("header %v", h)
			}
			if f.data.String() != myDump {
				t.Fatalf("data %q", f.data.String())
			}
			if *released != 2 || bytes.Contains(h.GetTarget().GetPassword(), []byte("pw-value")) {
				t.Fatalf("released %d times; password %q", *released, h.GetTarget().GetPassword())
			}
			if !slices.Equal(res.Warnings, []string{"legacy_client"}) {
				t.Fatalf("warnings %v", res.Warnings)
			}
			if !logs.has("info: ERROR at line 1") || !logs.has("created the new database shop") || !logs.has("dropped 2 tool log lines") {
				t.Fatalf("logs %v", logs.lines)
			}
		})
	}
}

func TestMySQLRestoreOverwrite(t *testing.T) {
	f := &myFakeRunner{}
	req, _ := myRequest(f, engine.KindMariaDB, ModeOverwrite, myDump)
	logs := &myLogs{}
	if _, err := restorerOf(t, engine.KindMariaDB).Restore(t.Context(), req, strings.NewReader(myDump), logs); err != nil {
		t.Fatal(err)
	}
	prefix, err := MySQLOverwritePrefix("shop")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.calls, []string{"restore"}) {
		t.Fatalf("calls %v", f.calls)
	}
	if f.data.String() != string(prefix)+myDump || f.header.GetTotalBytes() != uint64(len(prefix)+len(myDump)) {
		t.Fatalf("data %q total %d", f.data.String(), f.header.GetTotalBytes())
	}
	if !logs.has("warn: overwriting database shop in place") {
		t.Fatalf("logs %v", logs.lines)
	}
}

func TestMySQLOverwritePrefix(t *testing.T) {
	p, err := MySQLOverwritePrefix("my-db$1.x")
	if err != nil {
		t.Fatal(err)
	}
	s := string(p)
	// The CREATE is prepared before the DROP; the DROP is the only
	// destructive statement and names the target only.
	iPrep, iDrop, iExec, iUse := strings.Index(s, "PREPARE bekup_create"), strings.Index(s, "DROP DATABASE `my-db$1.x`;"),
		strings.Index(s, "EXECUTE bekup_create"), strings.Index(s, "USE `my-db$1.x`;")
	if iPrep < 0 || iDrop < iPrep || iExec < iDrop || iUse < iExec {
		t.Fatalf("order: %s", s)
	}
	if strings.Count(s, "DROP") != 1 || strings.Contains(s, "IF EXISTS") ||
		!strings.Contains(s, "FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = DATABASE()") {
		t.Fatalf("prefix: %s", s)
	}
	for _, bad := range []string{"", "a`b", "a'b", "a\\b", "a b", "-x", "a;DROP"} {
		if _, err := MySQLOverwritePrefix(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestMySQLRestoreRefused(t *testing.T) {
	cases := []struct {
		name string
		k    engine.Kind
		mod  func(*EngineRequest)
		want error
	}{
		{"globals", engine.KindMySQL, func(r *EngineRequest) { r.Kind = KindGlobals; r.TargetDB = "" }, engine.ErrUnsupported},
		{"mysql dump into mariadb", engine.KindMariaDB, func(r *EngineRequest) { r.SourceEngine = engine.KindMySQL }, engine.ErrWrongEngine},
		{"mariadb dump into mysql", engine.KindMySQL, func(r *EngineRequest) { r.SourceEngine = engine.KindMariaDB }, engine.ErrWrongEngine},
		{"target engine", engine.KindMySQL, func(r *EngineRequest) { r.TargetEngine = engine.KindMariaDB }, engine.ErrWrongEngine},
		{"postgres", engine.KindMySQL, func(r *EngineRequest) { r.SourceEngine, r.TargetEngine = engine.KindPostgres, engine.KindPostgres }, engine.ErrWrongEngine},
		{"system database", engine.KindMySQL, func(r *EngineRequest) { r.TargetDB = "mysql" }, engine.ErrSystemDatabase},
		{"invalid name", engine.KindMySQL, func(r *EngineRequest) { r.TargetDB = "a`b" }, engine.ErrInvalidName},
		{"mode", engine.KindMySQL, func(r *EngineRequest) { r.Mode = "swap" }, engine.ErrUnsupported},
		{"size", engine.KindMySQL, func(r *EngineRequest) { r.TotalBytes = 0 }, ErrUnsupported},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &myFakeRunner{}
			req, _ := myRequest(f, c.k, ModeOverwrite, myDump)
			c.mod(&req)
			_, err := restorerOf(t, c.k).Restore(t.Context(), req, strings.NewReader(myDump), &myLogs{})
			if !errors.Is(err, c.want) {
				t.Fatalf("err %v, want %v", err, c.want)
			}
			if f.header != nil || len(f.creates) != 0 {
				t.Fatal("the runner was called")
			}
		})
	}
}

// myErrReader returns the dump and then err instead of io.EOF.
type myErrReader struct {
	r   io.Reader
	err error
}

func (e *myErrReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if errors.Is(err, io.EOF) {
		return n, e.err
	}
	return n, err
}

func TestMySQLRestoreStreamFailures(t *testing.T) {
	big := strings.Repeat("INSERT INTO t VALUES (1);\n", 100000) + "-- Dump completed\n" // > 2 chunks
	cases := []struct {
		name  string
		dump  string
		total int
		src   func(string) io.Reader
		exit  int32
		want  error
		log   string
	}{
		{"no trailer", "CREATE TABLE t (id int);\n", 0, nil, 0, ErrRestoreIncomplete, "does not end with the"},
		{"shorter", myDump, len(myDump) + 1, nil, 0, ErrRestoreIncomplete, "left behind"},
		{"longer", myDump, len(myDump) - 1, nil, 0, ErrRestoreIncomplete, "left behind"},
		{"final verification", big, 0, func(s string) io.Reader {
			return &myErrReader{strings.NewReader(s), &VerificationError{Code: VerifyDumpDigest}}
		}, 0, ErrVerification, "left behind"},
		{"client failed", myDump, 0, nil, 1, ErrRestoreFailed, "exited with code 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &myFakeRunner{exitCode: c.exit}
			req, _ := myRequest(f, engine.KindMySQL, ModeNewDB, c.dump)
			if c.total > 0 {
				req.TotalBytes = uint64(c.total)
			}
			var src io.Reader = strings.NewReader(c.dump)
			if c.src != nil {
				src = c.src(c.dump)
			}
			logs := &myLogs{}
			res, err := restorerOf(t, engine.KindMySQL).Restore(t.Context(), req, src, logs)
			if !errors.Is(err, c.want) {
				t.Fatalf("err %v, want %v", err, c.want)
			}
			if !logs.has(c.log) || !logs.has("left behind partially restored") || !slices.Contains(res.Warnings, WarnPartialNewDatabase) {
				t.Fatalf("logs %v warnings %v", logs.lines, res.Warnings)
			}
			if c.exit == 0 && strings.HasSuffix(f.data.String(), c.dump[len(c.dump)-10:]) {
				t.Fatal("the last chunk was sent")
			}
		})
	}
}

// A client that exits early (a refused command) ends the stream while the
// dump is still being sent: the result is reported, not a send error.
func TestMySQLRestoreClientExitsEarly(t *testing.T) {
	big := strings.Repeat("INSERT INTO t VALUES (1);\n", 200000) + "-- Dump completed\n"
	f := &myFakeRunner{exitCode: 1, failAfter: 1}
	req, _ := myRequest(f, engine.KindMySQL, ModeOverwrite, big)
	logs := &myLogs{}
	_, err := restorerOf(t, engine.KindMySQL).Restore(t.Context(), req, strings.NewReader(big), logs)
	if !errors.Is(err, ErrRestoreIncomplete) && !errors.Is(err, ErrRestoreFailed) {
		t.Fatalf("err %v", err)
	}
	if !logs.has("may be empty or partially restored") {
		t.Fatalf("logs %v", logs.lines)
	}
}

// The runner's spec builder runs the hardened client for every allowlisted
// MySQL/MariaDB image: the safety flags, the database only as
// --database=, the bekup option file first, no --force.
func TestMySQLRestoreHardenedArgv(t *testing.T) {
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	want := map[engine.Kind][]string{
		engine.KindMySQL:   {"--system-command=OFF", "--commands=OFF", "--local-infile=0"},
		engine.KindMariaDB: {"--sandbox", "--local-infile=0"},
	}
	for k, flags := range want {
		e, err := engine.ForKind(k)
		if err != nil {
			t.Fatal(err)
		}
		entries := cat.EntriesFor(k)
		if len(entries) == 0 {
			t.Fatalf("no %s entries", k)
		}
		for _, ent := range entries {
			cmd, err := e.Restore(ent, engine.Target{Host: "db.example", Port: 3306, User: "u"}, "shop", engine.RestoreOptions{})
			if err != nil {
				t.Fatal(err)
			}
			sp, err := spec.Build(spec.Params{
				RunID: "0b9c6c84-0f3e-4c55-9f0e-1d2b3c4d5e6f", Entry: ent, Command: cmd, Network: "bekup-run-x",
				Limits: spec.Limits{MemoryMiB: 256, CPUs: 1, PidsLimit: 64},
			})
			if err != nil {
				t.Fatal(err)
			}
			argv := sp.Config.Cmd
			for _, fl := range append(slices.Clone(flags), "--default-character-set=utf8mb4") {
				if !slices.Contains(argv, fl) {
					t.Errorf("%s %s: %s missing in %v", k, ent.Key(), fl, argv)
				}
			}
			if argv[1] != "--defaults-file="+string(engine.FileMyCnf) || argv[len(argv)-1] != "--database=shop" {
				t.Errorf("%s %s: argv %v", k, ent.Key(), argv)
			}
			for _, a := range argv {
				if a == "shop" || a == "--force" || a == "-f" || strings.HasPrefix(a, "--init-command") || strings.HasPrefix(a, "--local-infile=1") {
					t.Errorf("%s %s: unexpected %q in %v", k, ent.Key(), a, argv)
				}
			}
			if !sp.HostConfig.ReadonlyRootfs || !slices.Equal(sp.HostConfig.CapDrop, []string{"ALL"}) || !cmd.Stdin {
				t.Errorf("%s %s: not hardened", k, ent.Key())
			}
		}
	}
}

func myAPIEngine(k engine.Kind) runnerpb.Engine {
	if k == engine.KindMariaDB {
		return runnerpb.Engine_ENGINE_MARIADB
	}
	return runnerpb.Engine_ENGINE_MYSQL
}
