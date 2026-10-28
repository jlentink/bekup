package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/jlentink/bekup/internal/config"
)

func testSettings() Settings {
	return SettingsFromConfig(config.DefaultBekup().Workers)
}

func TestSettingsFromConfig(t *testing.T) {
	w := config.DefaultBekup().Workers
	w.RestoreTimeout = config.Duration(20 * time.Hour)
	s := SettingsFromConfig(w)
	if err := s.validate(); err != nil {
		t.Fatal(err)
	}
	want := map[Queue]PerQueue{
		QueueBackup:      {MaxWorkers: w.BackupQueueWorkers, Timeout: w.BackupTimeout.D(), MaxAttempts: 2},
		QueueRestore:     {MaxWorkers: w.RestoreQueueWorkers, Timeout: 20 * time.Hour, MaxAttempts: 1},
		QueueRetention:   {MaxWorkers: w.RetentionQueueWorkers, Timeout: w.RetentionTimeout.D(), MaxAttempts: 3},
		QueueNotify:      {MaxWorkers: w.NotifyQueueWorkers, Timeout: w.NotifyTimeout.D(), MaxAttempts: 10},
		QueueMaintenance: {MaxWorkers: w.MaintenanceQueueWorkers, Timeout: MaintenanceTimeout, MaxAttempts: 3},
	}
	for q, ws := range want {
		if s.Queues[q] != ws {
			t.Errorf("%s: got %+v, want %+v", q, s.Queues[q], ws)
		}
	}
	if s.LongestTimeout() != 20*time.Hour || s.RescueStuckJobsAfter != 21*time.Hour {
		t.Fatalf("rescue %s for longest %s", s.RescueStuckJobsAfter, s.LongestTimeout())
	}
	if s.RescueStuckJobsAfter <= s.LongestTimeout() {
		t.Fatal("rescue must exceed the longest timeout")
	}
	// The longest timeout wins whichever queue it is.
	w.NotifyTimeout = config.Duration(30 * time.Hour)
	if s := SettingsFromConfig(w); s.RescueStuckJobsAfter != 31*time.Hour {
		t.Fatalf("rescue %s", s.RescueStuckJobsAfter)
	}
}

func TestSettingsValidate(t *testing.T) {
	for name, mut := range map[string]func(*Settings){
		"restore retried":  func(s *Settings) { q := s.Queues[QueueRestore]; q.MaxAttempts = 2; s.Queues[QueueRestore] = q },
		"rescue too short": func(s *Settings) { s.RescueStuckJobsAfter = s.LongestTimeout() },
		"missing queue":    func(s *Settings) { delete(s.Queues, QueueNotify) },
		"zero workers":     func(s *Settings) { q := s.Queues[QueueBackup]; q.MaxWorkers = 0; s.Queues[QueueBackup] = q },
		"zero timeout":     func(s *Settings) { q := s.Queues[QueueBackup]; q.Timeout = 0; s.Queues[QueueBackup] = q },
		"extra queue":      func(s *Settings) { s.Queues["other"] = s.Queues[QueueBackup] },
		"no shutdown":      func(s *Settings) { s.ShutdownTimeout = 0 },
	} {
		s := testSettings()
		mut(&s)
		if err := s.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestUniqueStates(t *testing.T) {
	got := UniqueStates()
	for _, st := range []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStateScheduled, rivertype.JobStateRunning, rivertype.JobStateRetryable, rivertype.JobStatePending} {
		if !slices.Contains(got, st) {
			t.Errorf("missing %s", st)
		}
	}
	for _, st := range []rivertype.JobState{rivertype.JobStateCompleted, rivertype.JobStateCancelled, rivertype.JobStateDiscarded} {
		if slices.Contains(got, st) {
			t.Errorf("%s must not block a new job", st)
		}
	}
}

func TestCatalog(t *testing.T) {
	if err := ValidateCatalog(); err != nil {
		t.Fatal(err)
	}
	spec, ok := Spec(KindBackupRun)
	if !ok || spec.Queue != QueueBackup || !spec.Unique || spec.maxAttempts(testSettings()) != MaxAttemptsBackup {
		t.Fatalf("backup_run spec %+v", spec)
	}
	for name, c := range map[string]map[string]KindSpec{
		"bad queue":       {KindBackupRun: {Queue: "nope", Args: BackupRunArgs{}}},
		"kind mismatch":   {"other": {Queue: QueueBackup, Args: BackupRunArgs{}}},
		"restore retried": {KindBackupRun: {Queue: QueueRestore, MaxAttempts: 2, Args: BackupRunArgs{}}},
		"secret args":     {"leaky": {Queue: QueueBackup, Args: leakyArgs{}}},
		"unique no field": {"nounique": {Queue: QueueBackup, Unique: true, Args: noUniqueArgs{}}},
		"nil args":        {"x": {Queue: QueueBackup}},
	} {
		if err := validateCatalog(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

type leakyArgs struct {
	JobID uuid.UUID `json:"job_id"`
	Pass  string    `json:"pass"`
}

func (leakyArgs) Kind() string { return "leaky" }

type noUniqueArgs struct {
	JobID uuid.UUID `json:"job_id"`
}

func (noUniqueArgs) Kind() string { return "nounique" }

type enumish string

type okArgs struct {
	JobID   uuid.UUID   `json:"job_id"`
	RunIDs  []uuid.UUID `json:"run_ids"`
	At      time.Time   `json:"at"`
	Retry   *int64      `json:"retry"`
	Mode    enumish     `json:"mode"`
	Flag    bool        `json:"flag"`
	Nested  nested      `json:"nested"`
	UserID  *uuid.UUID  `json:"user_id"`
	Attempt uint16      `json:"attempt"`
}

type nested struct {
	ArtifactID uuid.UUID `json:"artifact_id"`
}

func (okArgs) Kind() string { return "ok" }

type argsOf[T any] struct{ V T }

func (argsOf[T]) Kind() string { return "generic" }

// TestCheckArgsRegisteredKinds is the D-043 guard over the whole catalog:
// no registered kind may carry a field that could hold a secret.
func TestCheckArgsRegisteredKinds(t *testing.T) {
	for _, kind := range Kinds() {
		spec, _ := Spec(kind)
		if err := CheckArgs(spec.Args); err != nil {
			t.Errorf("%s: %v", kind, err)
		}
	}
}

func TestCheckArgs(t *testing.T) {
	if err := CheckArgs(okArgs{}); err != nil {
		t.Fatal(err)
	}
	type withName struct {
		ID uuid.UUID `json:"identity"`
	}
	type withGoName struct {
		APIKeyID uuid.UUID
	}
	type unexported struct {
		id uuid.UUID //nolint:unused // under test
	}
	bad := map[string]river.JobArgs{
		"plain string":   argsOf[string]{},
		"bytes":          argsOf[[]byte]{},
		"byte array":     argsOf[[32]byte]{},
		"map":            argsOf[map[string]int]{},
		"interface":      argsOf[any]{},
		"float":          argsOf[float64]{},
		"string slice":   argsOf[[]string]{},
		"string pointer": argsOf[*string]{},
		"json raw":       argsOf[json.RawMessage]{},
		"json name":      argsOf[withName]{},
		"go name":        argsOf[withGoName]{},
		"unexported":     argsOf[unexported]{},
		"nested string":  argsOf[struct{ Note string }]{},
	}
	for name, a := range bad {
		if err := CheckArgs(a); !errors.Is(err, ErrArgsNotIDsOnly) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	for _, n := range []string{"Password", "passphrase", "PrivateKey", "Token", "SECRET", "Identity", "Credentials", "DSN", "SessionCookie", "pwd"} {
		if !secretName.MatchString(n) {
			t.Errorf("%s not caught", n)
		}
	}
}

type testWorker[T river.JobArgs] struct {
	river.WorkerDefaults[T]
	work func(context.Context, *river.Job[T]) error
}

func (w *testWorker[T]) Work(ctx context.Context, j *river.Job[T]) error {
	if w.work == nil {
		return nil
	}
	return w.work(ctx, j)
}

func (w *testWorker[T]) Timeout(*river.Job[T]) time.Duration { return time.Nanosecond }

func TestRegister(t *testing.T) {
	ws, err := NewWorkers(testSettings())
	if err != nil {
		t.Fatal(err)
	}
	if err := Register(ws, &testWorker[okArgs]{}); err == nil {
		t.Fatal("kind outside the catalog registered")
	}
	if err := Register(ws, &testWorker[BackupRunArgs]{}); err != nil {
		t.Fatal(err)
	}
	if err := Register(ws, &testWorker[BackupRunArgs]{}); err == nil {
		t.Fatal("double registration accepted")
	}
	if d, ok := ws.Timeout(KindBackupRun); !ok || d != testSettings().Queues[QueueBackup].Timeout {
		t.Fatalf("timeout %s %v", d, ok)
	}
	// The bound worker overrides the worker's own Timeout.
	bw := boundWorker[BackupRunArgs]{Worker: &testWorker[BackupRunArgs]{}, timeout: time.Hour}
	if bw.Timeout(nil) != time.Hour {
		t.Fatal("timeout not bound")
	}
	if err := ws.AddPeriodic(time.Hour, ScheduleSkipCleanupArgs{}); err == nil {
		t.Fatal("periodic job without a worker accepted")
	}
	if err := Register(ws, &testWorker[ScheduleSkipCleanupArgs]{}); err != nil {
		t.Fatal(err)
	}
	if err := ws.AddPeriodic(time.Second, ScheduleSkipCleanupArgs{}); err == nil {
		t.Fatal("sub-minute periodic accepted")
	}
	if err := ws.AddPeriodic(time.Hour, ScheduleSkipCleanupArgs{}); err != nil {
		t.Fatal(err)
	}
	if got := ws.Kinds(); !slices.Equal(got, []string{KindBackupRun, KindScheduleSkipCleanup}) {
		t.Fatalf("kinds %v", got)
	}
}

func lazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), "postgres://nobody@127.0.0.1:1/none")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestNewRiverConfig(t *testing.T) {
	s := testSettings()
	ws, err := NewWorkers(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(lazyPool(t), Options{Settings: s, Logger: slog.New(slog.DiscardHandler), Workers: ws}); err == nil {
		t.Fatal("worker client without workers accepted")
	}
	if err := Register(ws, &testWorker[BackupRunArgs]{}); err != nil {
		t.Fatal(err)
	}
	c, err := New(lazyPool(t), Options{Settings: s, Logger: slog.New(slog.DiscardHandler), Workers: ws})
	if err != nil {
		t.Fatal(err)
	}
	cfg := c.RiverConfig()
	if cfg.RescueStuckJobsAfter != s.RescueStuckJobsAfter || cfg.RescueStuckJobsAfter <= s.LongestTimeout() {
		t.Errorf("rescue %s", cfg.RescueStuckJobsAfter)
	}
	if cfg.CompletedJobRetentionPeriod != CompletedJobRetention || cfg.DiscardedJobRetentionPeriod != DiscardedJobRetention || cfg.CancelledJobRetentionPeriod != CanceledJobRetention {
		t.Errorf("retention %+v", cfg)
	}
	if cfg.ReindexerSchedule == nil || !cfg.ReindexerSchedule.Next(time.Now()).After(time.Now().AddDate(100, 0, 0)) {
		t.Error("reindexer must be disabled")
	}
	if !cfg.SkipUnknownJobCheck {
		t.Error("serve must insert kinds worked elsewhere")
	}
	if !cfg.FetchOnlyKnownKinds {
		t.Error("workers must fetch only known kinds")
	}
	if len(cfg.Queues) != len(Queues()) {
		t.Fatalf("queues %v", cfg.Queues)
	}
	for q, qs := range s.Queues {
		if cfg.Queues[string(q)].MaxWorkers != qs.MaxWorkers {
			t.Errorf("%s workers %d", q, cfg.Queues[string(q)].MaxWorkers)
		}
	}
	if !c.Working() {
		t.Fatal("not a worker client")
	}
	if err := Register(ws, &testWorker[ScheduleSkipCleanupArgs]{}); !errors.Is(err, ErrSealed) {
		t.Fatalf("register after New: %v", err)
	}

	ins, err := New(lazyPool(t), Options{Settings: s, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	if ins.Working() || ins.RiverConfig().Queues != nil {
		t.Fatal("insert-only client has queues")
	}
	if _, err := New(nil, Options{Settings: s, Logger: slog.New(slog.DiscardHandler)}); err == nil {
		t.Fatal("nil pool accepted")
	}
}

func TestInsertOpts(t *testing.T) {
	s := testSettings()
	o := insertOpts(catalog[KindBackupRun], s, InsertOptions{})
	if o.Queue != string(QueueBackup) || o.MaxAttempts != MaxAttemptsBackup || !o.UniqueOpts.ByArgs || !slices.Equal(o.UniqueOpts.ByState, UniqueStates()) {
		t.Fatalf("%+v", o)
	}
	if o.UniqueOpts.ByPeriod != 0 || o.UniqueOpts.ByQueue {
		t.Fatal("uniqueness must be by args only")
	}
	restore := KindSpec{Queue: QueueRestore, Args: BackupRunArgs{}}
	if insertOpts(restore, s, InsertOptions{}).MaxAttempts != 1 {
		t.Fatal("restore kinds must not retry")
	}
	notify := KindSpec{Queue: QueueNotify, Args: BackupRunArgs{}}
	if insertOpts(notify, s, InsertOptions{}).MaxAttempts != 10 {
		t.Fatal("notify attempts")
	}
	at := time.Now().Add(time.Hour)
	if !insertOpts(catalog[KindBackupRun], s, InsertOptions{ScheduledAt: at}).ScheduledAt.Equal(at) {
		t.Fatal("scheduled at")
	}
}

func TestArgsFilter(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(argsFilter{slog.NewJSONHandler(&buf, nil)})
	l.With(slog.String("args", "x1")).Info("m", slog.String("encoded_args", "x2"), slog.Int64("job_id", 7),
		slog.Group("job", slog.String("metadata", "x3"), slog.String("kind", "backup_run")))
	l.WithGroup("args").Info("hidden", slog.String("v", "x4"))
	out := buf.String()
	for _, leak := range []string{"x1", "x2", "x3", "x4", "hidden"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %s: %s", leak, out)
		}
	}
	if !strings.Contains(out, `"job_id":7`) || !strings.Contains(out, "backup_run") {
		t.Fatalf("lost attrs: %s", out)
	}
}
