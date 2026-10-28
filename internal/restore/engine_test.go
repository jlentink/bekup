package restore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
)

// memSink collects log lines.
type memSink struct {
	mu    sync.Mutex
	lines []string
}

func (m *memSink) Line(level, msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lines = append(m.lines, level+": "+msg)
}

func (m *memSink) text() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.lines, "\n")
}

// pgReq is a PostgreSQL database restore request against f.
func pgReq(f *FakeRunner, mode Mode, db string, dump []byte, releases *atomic.Int32) EngineRequest {
	return EngineRequest{
		RestoreID: uuid.MustParse("0123456789ab4def8123456789abcdef"), Runner: f,
		Target: func(context.Context) (*runnerpb.Target, func(), error) {
			t := &runnerpb.Target{Engine: runnerpb.Engine_ENGINE_POSTGRES, Password: []byte("target-password")}
			return t, func() {
				if releases != nil {
					releases.Add(1)
				}
				runnerpb.ZeroSecrets(t)
			}, nil
		},
		TargetEngine: engine.KindPostgres, SourceEngine: engine.KindPostgres, SourceVersion: "16",
		TargetDB: db, Mode: mode, Kind: KindDatabase, Format: pipeline.FormatCustom,
		TotalBytes: uint64(len(dump)), NoOwner: true,
	}
}

func dumpOf(n int) []byte {
	b := bytes.Repeat([]byte("PGDMP-data-"), n/11+1)
	return b[:n]
}

func TestTempID(t *testing.T) {
	id := uuid.MustParse("0123456789ab4def8123456789abcdef")
	if got := TempID(id); got != "0123456789ab" || engine.ValidateRestoreID(got) != nil {
		t.Fatalf("TempID = %q", got)
	}
	for range 100 {
		if err := engine.ValidateRestoreID(TempID(uuid.New())); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPGNewDB(t *testing.T) {
	f := NewFakeRunner("app")
	dump := dumpOf(600 << 10) // several chunks
	var rel atomic.Int32
	req := pgReq(f, ModeNewDB, "app_copy", dump, &rel)
	logs := &memSink{}
	if _, err := (pgRestorer{}).Restore(t.Context(), req, bytes.NewReader(dump), logs); err != nil {
		t.Fatal(err)
	}
	if got := f.CallList(); !slices.Equal(got, []string{"create:app_copy", "restore:app_copy", "neutralize:app_copy"}) {
		t.Fatalf("calls = %v", got)
	}
	if !bytes.Equal(f.Data("app_copy"), dump) {
		t.Fatal("restored data differs")
	}
	h := f.Headers[0]
	if !h.GetNoOwner() || h.GetSourceVersionKey() != "16" || h.GetTotalBytes() != uint64(len(dump)) || h.GetCtx().GetRunId() != req.RestoreID.String() {
		t.Fatalf("header = %v", h)
	}
	if rel.Load() != 3 {
		t.Fatalf("releases = %d, want one per RPC", rel.Load())
	}
	// Released targets were zeroed after the call (the fake kept a copy
	// taken during the call).
	for _, p := range f.Passwords {
		if string(p) != "target-password" {
			t.Fatalf("the RPC saw %q", p)
		}
	}
}

func TestPGOverwrite(t *testing.T) {
	old := []byte("old data")
	dump := dumpOf(300 << 10)
	setup := func() *FakeRunner {
		f := NewFakeRunner("app")
		f.Received["app"] = bytes.Clone(old)
		return f
	}
	const temp, oldName = "app__bekup_restore_0123456789ab", "app__bekup_old_0123456789ab"
	untouched := func(t *testing.T, f *FakeRunner) {
		t.Helper()
		if !f.Has("app") || !bytes.Equal(f.Data("app"), old) {
			t.Fatal("the target changed")
		}
		if f.Has(temp) {
			t.Fatal("the temporary database was not dropped")
		}
		for _, c := range f.CallList() {
			if strings.HasPrefix(c, "swap:") || c == "drop:"+oldName {
				t.Fatalf("unexpected call %s", c)
			}
		}
	}

	t.Run("swap", func(t *testing.T) {
		f := setup()
		f.Warnings = []string{"no_allowlisted_client_for_server"}
		res, err := (pgRestorer{}).Restore(t.Context(), pgReq(f, ModeOverwrite, "app", dump, nil), bytes.NewReader(dump), &memSink{})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"create:" + temp, "restore:" + temp, "neutralize:" + temp, "swap:app", "drop:" + oldName}
		if got := f.CallList(); !slices.Equal(got, want) {
			t.Fatalf("calls = %v, want %v", got, want)
		}
		if !bytes.Equal(f.Data("app"), dump) || f.Has(temp) || f.Has(oldName) {
			t.Fatal("the swap did not replace the data")
		}
		if !slices.Equal(res.Warnings, f.Warnings) {
			t.Fatalf("warnings = %v", res.Warnings)
		}
	})
	t.Run("tool failure leaves the target", func(t *testing.T) {
		f := setup()
		f.ExitCode = 1
		f.Logs = []string{`pg_restore: error: could not execute query`}
		logs := &memSink{}
		_, err := (pgRestorer{}).Restore(t.Context(), pgReq(f, ModeOverwrite, "app", dump, nil), bytes.NewReader(dump), logs)
		if !errors.Is(err, ErrRestoreFailed) || ErrorCode(err) != CodeRestoreFailed {
			t.Fatalf("err = %v", err)
		}
		untouched(t, f)
		if !strings.Contains(logs.text(), "could not execute query") {
			t.Fatalf("runner log missing:\n%s", logs.text())
		}
	})
	t.Run("swap failure leaves the target", func(t *testing.T) {
		f := setup()
		f.SwapErr = runnerpb.ReasonFailed.Status()
		_, err := (pgRestorer{}).Restore(t.Context(), pgReq(f, ModeOverwrite, "app", dump, nil), bytes.NewReader(dump), &memSink{})
		if ErrorCode(err) != "target_failed" {
			t.Fatalf("err = %v (%s)", err, ErrorCode(err))
		}
		if !f.Has("app") || !bytes.Equal(f.Data("app"), old) || f.Has(temp) {
			t.Fatal("target changed or temp kept")
		}
	})
	t.Run("final verification failure never completes the stream", func(t *testing.T) {
		f := setup()
		tampered := errors.New("dump digest mismatch")
		src := io.MultiReader(bytes.NewReader(dump), errReader{tampered})
		_, err := (pgRestorer{}).Restore(t.Context(), pgReq(f, ModeOverwrite, "app", dump, nil), src, &memSink{})
		if !errors.Is(err, tampered) {
			t.Fatalf("err = %v", err)
		}
		untouched(t, f)
		if n := len(f.Data(temp)); n != 0 {
			t.Fatalf("the runner completed %d bytes", n)
		}
	})
	t.Run("truncated dump", func(t *testing.T) {
		f := setup()
		_, err := (pgRestorer{}).Restore(t.Context(), pgReq(f, ModeOverwrite, "app", dump, nil), bytes.NewReader(dump[:len(dump)-10]), &memSink{})
		if !errors.Is(err, ErrRestoreIncomplete) {
			t.Fatalf("err = %v", err)
		}
		untouched(t, f)
	})
	t.Run("cancel drops the temporary database", func(t *testing.T) {
		f := setup()
		f.Block = true
		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			<-f.Started
			cancel()
		}()
		_, err := (pgRestorer{}).Restore(ctx, pgReq(f, ModeOverwrite, "app", dump, nil), bytes.NewReader(dump), &memSink{})
		if err == nil {
			t.Fatal("no error")
		}
		untouched(t, f)
		if !slices.Contains(f.CallList(), "drop:"+temp) {
			t.Fatalf("calls = %v", f.CallList())
		}
	})
	t.Run("old database kept is a warning", func(t *testing.T) {
		f := setup()
		res, err := (pgRestorer{}).Restore(t.Context(), pgReq(f, ModeOverwrite, "app", dump, nil), bytes.NewReader(dump), &memSink{})
		if err != nil || slices.Contains(res.Warnings, WarnOldDatabaseKept) {
			t.Fatalf("%v %v", res, err)
		}
		f2 := setup()
		f2.DropErr = status.Error(codes.Unavailable, "down")
		res, err = (pgRestorer{}).Restore(t.Context(), pgReq(f2, ModeOverwrite, "app", dump, nil), bytes.NewReader(dump), &memSink{})
		if err != nil || !slices.Contains(res.Warnings, WarnOldDatabaseKept) {
			t.Fatalf("%v %v", res, err)
		}
	})
}

// TestRunRestoreStreamEarlyEnd: a runner that ends the call before the
// whole dump arrived (Send returns io.EOF) is never a success, whatever
// the exit code of its result.
func TestRunRestoreStreamEarlyEnd(t *testing.T) {
	dump := dumpOf(600 << 10)
	for _, exit := range []int32{0, 1} {
		f := NewFakeRunner("app")
		f.EarlyEnd, f.ExitCode = true, exit
		req := pgReq(f, ModeNewDB, "app", dump, nil)
		res, err := RunRestoreStream(t.Context(), &req, StreamOptions{Database: "app", Kind: runnerpb.DumpKind_DUMP_KIND_DATABASE}, bytes.NewReader(dump), &memSink{})
		want := ErrRestoreIncomplete
		if exit != 0 {
			want = ErrRestoreFailed
		}
		if !errors.Is(err, want) {
			t.Fatalf("exit %d: res %v err %v", exit, res, err)
		}
	}
	// A stream that ends without any result is incomplete too.
	if _, err := checkResult(recvResult{}, 10, &memSink{}); !errors.Is(err, ErrRestoreIncomplete) {
		t.Fatalf("no result: %v", err)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestPGRefusals(t *testing.T) {
	dump := dumpOf(100)
	cases := map[string]func(r *EngineRequest){
		"target engine":   func(r *EngineRequest) { r.TargetEngine = engine.KindMySQL },
		"source engine":   func(r *EngineRequest) { r.SourceEngine = engine.KindMariaDB },
		"sql database":    func(r *EngineRequest) { r.Format = pipeline.FormatSQL },
		"no database":     func(r *EngineRequest) { r.TargetDB = "" },
		"temp name":       func(r *EngineRequest) { r.TargetDB = "app__bekup_restore_1" },
		"globals with db": func(r *EngineRequest) { r.Kind, r.Format = KindGlobals, pipeline.FormatSQL },
		"globals custom":  func(r *EngineRequest) { r.Kind, r.TargetDB = KindGlobals, "" },
		"unknown mode":    func(r *EngineRequest) { r.Mode = "merge" },
		"unknown kind":    func(r *EngineRequest) { r.Kind = "both" },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			f := NewFakeRunner("app")
			req := pgReq(f, ModeOverwrite, "app", dump, nil)
			mod(&req)
			_, err := (pgRestorer{}).Restore(t.Context(), req, bytes.NewReader(dump), &memSink{})
			if !errors.Is(err, ErrEngineMismatch) && !errors.Is(err, ErrUnsupported) {
				t.Fatalf("err = %v", err)
			}
			if len(f.CallList()) != 0 {
				t.Fatalf("calls = %v", f.CallList())
			}
		})
	}
	if ErrorCode(ErrEngineMismatch) != CodeEngineMismatch || ErrorCode(fmt.Errorf("%w: x", ErrUnsupported)) != CodeUnsupported {
		t.Fatal("codes")
	}
}

func TestPGGlobals(t *testing.T) {
	f := NewFakeRunner()
	dump := []byte("CREATE ROLE reader;\n")
	req := pgReq(f, ModeNewDB, "", dump, nil)
	req.Kind, req.Format = KindGlobals, pipeline.FormatSQL
	if _, err := (pgRestorer{}).Restore(t.Context(), req, bytes.NewReader(dump), &memSink{}); err != nil {
		t.Fatal(err)
	}
	if got := f.CallList(); !slices.Equal(got, []string{"restore:(globals)"}) {
		t.Fatalf("calls = %v", got)
	}
	h := f.Headers[0]
	if h.GetKind() != runnerpb.DumpKind_DUMP_KIND_GLOBALS || h.GetDatabase() != "" || h.GetNoOwner() {
		t.Fatalf("header = %v", h)
	}
	f.ExitCode = 3
	logs := &memSink{}
	if _, err := (pgRestorer{}).Restore(t.Context(), req, bytes.NewReader(dump), logs); !errors.Is(err, ErrRestoreFailed) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(logs.text(), "first error") {
		t.Fatalf("logs:\n%s", logs.text())
	}
}

func TestIdempotentCreateRoles(t *testing.T) {
	in := "SET default_transaction_read_only = off;\n" +
		"CREATE ROLE bekup;\n" +
		"ALTER ROLE bekup WITH SUPERUSER INHERIT;\n" +
		"CREATE ROLE \"Odd \"\"Name\";\n" +
		"CREATE ROLE \"a$$b\";\n" +
		"CREATE ROLE x; DROP TABLE y;\n" +
		"  CREATE ROLE indented;\n" +
		"CREATE ROLE last;"
	out, n := IdempotentCreateRoles([]byte(in))
	want := "SET default_transaction_read_only = off;\n" +
		"DO $bekup_role$BEGIN CREATE ROLE bekup; EXCEPTION WHEN duplicate_object THEN NULL; END$bekup_role$;\n" +
		"ALTER ROLE bekup WITH SUPERUSER INHERIT;\n" +
		"DO $bekup_role$BEGIN CREATE ROLE \"Odd \"\"Name\"; EXCEPTION WHEN duplicate_object THEN NULL; END$bekup_role$;\n" +
		"CREATE ROLE \"a$$b\";\n" +
		"CREATE ROLE x; DROP TABLE y;\n" +
		"  CREATE ROLE indented;\n" +
		"DO $bekup_role$BEGIN CREATE ROLE last; EXCEPTION WHEN duplicate_object THEN NULL; END$bekup_role$;"
	if n != 3 || string(out) != want {
		t.Fatalf("n = %d\n%s", n, out)
	}
}

func FuzzIdempotentCreateRoles(f *testing.F) {
	f.Add([]byte("CREATE ROLE a;\nCREATE ROLE \"b\";\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		out, n := IdempotentCreateRoles(in)
		if n == 0 && !bytes.Equal(out, in) {
			t.Fatal("changed without a rewrite")
		}
		// Every DO block body is closed only by its own tag.
		if c := bytes.Count(out, []byte("$bekup_role$")); c != 2*n+bytes.Count(in, []byte("$bekup_role$")) {
			t.Fatalf("tag count %d for %d rewrites", c, n)
		}
	})
}

func TestRunnerErrorCodes(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{runnerpb.ReasonPinMismatch.Status(), "tls_pin_mismatch"},
		{fmt.Errorf("wrapped: %w", runnerpb.ReasonDatabaseExists.Status()), "target_database_exists"},
		{runnerpb.ReasonPermission.Status(), "target_permission_denied"},
		{status.Error(codes.Unavailable, "x"), CodeRunnerUnavailable},
		{status.Error(codes.ResourceExhausted, "x"), CodeRunnerBusy},
		{status.Error(codes.PermissionDenied, "x"), CodeTargetNotAllowed},
		{status.Error(codes.InvalidArgument, "x"), CodeRunnerRejected},
		{status.Error(codes.Internal, "x"), CodeRunnerFailed},
		{ErrTargetUnavailable, CodeTargetUnavailable},
		{fmt.Errorf("%w: x", ErrTargetNotReady), CodeTargetNotReady},
		{ErrNoRestorer, CodeNoRestorer},
		{ErrInterrupted, CodeInterrupted},
		{runstate.ErrSlotLost, CodeSlotLost},
		{ErrAuditFailed, CodeAuditFailed},
		{errStreamProtocol, CodeRestoreIncomplete},
	}
	for _, c := range cases {
		got := ErrorCode(c.err)
		if got != c.want {
			t.Errorf("ErrorCode(%v) = %q, want %q", c.err, got, c.want)
		}
		if !codeShape.MatchString(got) || ErrorMessage(got) == ErrorMessage(CodeInternal) {
			t.Errorf("code %q has no message", got)
		}
	}
}

func TestPayload(t *testing.T) {
	id := uuid.New()
	for _, k := range []string{EventRestore, EventLog} {
		kind, got, err := ParsePayload(Payload(k, id))
		if err != nil || kind != k || got != id {
			t.Fatalf("%s: %v %v %v", k, kind, got, err)
		}
	}
	for _, bad := range []string{"", "run:" + id.String(), "restore:" + id.String() + "x", "restore:nope"} {
		if _, _, err := ParsePayload(bad); !errors.Is(err, ErrBadPayload) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestRegisterEngine(t *testing.T) {
	if !slices.Contains(RegisteredEngines(), "postgres") {
		t.Fatal("postgres restorer not registered")
	}
	mustPanic := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: no panic", name)
			}
		}()
		f()
	}
	mustPanic("duplicate", func() { RegisterEngine("postgres", pgRestorer{}) })
	mustPanic("unknown", func() { RegisterEngine("oracle", pgRestorer{}) })
	mustPanic("nil", func() { RegisterEngine("mysql", nil) })
}

// TestLogSinkCapsAndRedaction: the worker's sink redacts registered
// credentials and credentials in URLs, sanitizes, truncates and caps.
func TestLogSinkCapsAndRedaction(t *testing.T) {
	reg := logging.NewRegistry()
	unreg := reg.Register([]byte("s3cr3t-target-pw"))
	defer unreg()
	var mu sync.Mutex
	var stored []runstate.LogLine
	w := runstate.StartLogWriter(t.Context(), runstate.LogWriterOptions{
		Append: func(_ context.Context, lines []runstate.LogLine) error {
			mu.Lock()
			defer mu.Unlock()
			stored = append(stored, lines...)
			return nil
		},
		Scrub:         func(s string) string { return reg.Scrub(logging.RedactCredentials(s)) },
		MaxLines:      5,
		MaxLineBytes:  200,
		FlushInterval: 10 * time.Millisecond,
	})
	sink := &logSink{w: w}
	sink.Line(LogError, "password s3cr3t-target-pw rejected")
	sink.runnerLine(&runnerpb.LogLine{Stream: runnerpb.LogStream_LOG_STREAM_STDERR, Text: "connect postgres://bekup:hunter2@db/app\x00failed"})
	sink.Line(LogInfo, strings.Repeat("x", 1000))
	for range 10 {
		sink.Line(LogWarn, "noise")
	}
	n, dropped := w.Close()
	mu.Lock()
	defer mu.Unlock()
	if n != 6 || dropped != 8 || len(stored) != 6 {
		t.Fatalf("stored %d (%d rows), dropped %d", n, len(stored), dropped)
	}
	var b strings.Builder
	for _, l := range stored {
		b.WriteString(l.Text + "\n")
		if len(l.Text) > 200 {
			t.Fatalf("line not truncated: %d bytes", len(l.Text))
		}
	}
	all := b.String()
	if strings.Contains(all, "s3cr3t-target-pw") || strings.Contains(all, "hunter2") || strings.Contains(all, "\x00") {
		t.Fatalf("not redacted:\n%s", all)
	}
	if stored[0].Level != runstate.LevelError || stored[1].Stream != runstate.StreamStderr || stored[0].Stream != runstate.StreamWorker {
		t.Fatalf("levels/streams: %+v", stored[:2])
	}
	if !strings.Contains(stored[5].Text, "were not stored") {
		t.Fatalf("no drop notice: %q", stored[5].Text)
	}
}
