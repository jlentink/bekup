package missed

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/notify"
)

func i32(v int32) *int32 { return &v }

func TestWindow(t *testing.T) {
	tests := []struct {
		name              string
		expected, timeout *int32
		def               time.Duration
		want              time.Duration
	}{
		{"explicit window wins", i32(1800), i32(600), 6 * time.Hour, 30 * time.Minute},
		{"derived from the default timeout", nil, nil, 6 * time.Hour, 13 * time.Hour},
		{"derived from the job timeout", nil, i32(3600), 6 * time.Hour, 3 * time.Hour},
		{"zero values are unset", i32(0), i32(0), time.Hour, 3 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Window(tt.expected, tt.timeout, tt.def); got != tt.want {
				t.Fatalf("Window = %s, want %s", got, tt.want)
			}
		})
	}
}

var t0 = time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)

func at(h float64) time.Time { return t0.Add(time.Duration(h * float64(time.Hour))) }
func atp(h float64) *time.Time {
	x := at(h)
	return &x
}

func run(h float64, status string, finished *time.Time) Occurrence {
	return Occurrence{At: at(h), Source: SourceRun, Status: status, Finished: finished}
}

func TestEvaluate(t *testing.T) {
	s1, s2 := uuid.New(), uuid.New()
	const w = 2 * time.Hour
	tests := []struct {
		name string
		occs []Occurrence
		succ []Success
		want []Outage
	}{
		{
			name: "own run succeeded in the window",
			occs: []Occurrence{run(0, "success", atp(0.5))},
			succ: []Success{{s1, at(0.5)}},
		},
		{
			name: "partial counts as not missed",
			occs: []Occurrence{run(0, "partial", atp(1))},
			succ: []Success{{s1, at(1)}},
		},
		{
			name: "failed run without a later success is missed",
			occs: []Occurrence{run(0, "failed", atp(0.2))},
			want: []Outage{{After: uuid.Nil, First: at(0), Last: at(0), Missed: 1, Reason: "run_failed", Ongoing: true}},
		},
		{
			name: "success after the deadline is late",
			occs: []Occurrence{run(0, "success", atp(3))},
			succ: []Success{{s1, at(3)}},
			want: []Outage{{After: uuid.Nil, First: at(0), Last: at(0), Missed: 1, Reason: "finished_late"}},
		},
		{
			name: "still-running skip covered by the long run",
			occs: []Occurrence{run(0, "success", atp(1.5)), {At: at(1), Source: SourceSkip, Status: "still_running"}},
			succ: []Success{{s1, at(1.5)}},
		},
		{
			name: "still-running skip whose run failed",
			occs: []Occurrence{run(0, "failed", atp(1.5)), {At: at(1), Source: SourceSkip, Status: "still_running"}},
			succ: []Success{{s1, at(-1)}},
			want: []Outage{{After: s1, First: at(0), Last: at(1), Missed: 2, Reason: "skipped_still_running", Ongoing: true}},
		},
		{
			name: "early run that finished before its occurrence",
			occs: []Occurrence{run(0, "success", atp(-5))},
			succ: []Success{{s1, at(-5)}},
		},
		{
			name: "a manual success in the window covers",
			occs: []Occurrence{{At: at(0), Source: SourcePending}},
			succ: []Success{{s1, at(1)}},
		},
		{
			name: "a success before the occurrence does not cover",
			occs: []Occurrence{{At: at(0), Source: SourcePending}},
			succ: []Success{{s1, at(-0.1)}},
			want: []Outage{{After: s1, First: at(0), Last: at(0), Missed: 1, Reason: "not_enqueued", Ongoing: true}},
		},
		{
			name: "two outages split by a success",
			occs: []Occurrence{
				run(0, "failed", atp(0.1)), run(1, "failed", atp(1.1)),
				run(4, "success", atp(4.1)),
				run(8, "failed", atp(8.1)), {At: at(9), Source: SourceSkip, Status: "unschedulable"},
			},
			succ: []Success{{s1, at(-2)}, {s2, at(4.1)}},
			want: []Outage{
				// 0 and 1 are missed (the success at 4.1 is after both deadlines).
				{After: s1, First: at(0), Last: at(1), Missed: 2, Reason: "run_failed"},
				{After: s2, First: at(8), Last: at(9), Missed: 2, Reason: "unschedulable", Ongoing: true},
			},
		},
		{
			name: "duplicate records of one occurrence count once (catch-up run + pending)",
			occs: []Occurrence{{At: at(0), Source: SourcePending}, run(0, "running", nil)},
			want: []Outage{{First: at(0), Last: at(0), Missed: 1, Reason: "run_running", Ongoing: true}},
		},
		{
			name: "a successful run beats a failed one at the same instant",
			occs: []Occurrence{run(0, "failed", atp(0.1)), run(0, "success", atp(0.2))},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(tt.occs, tt.succ, w)
			if len(got) != len(tt.want) {
				t.Fatalf("Evaluate = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				g, e := got[i], tt.want[i]
				if g.After != e.After || !g.First.Equal(e.First) || !g.Last.Equal(e.Last) || g.Missed != e.Missed ||
					g.Reason != e.Reason || g.Ongoing != e.Ongoing {
					t.Fatalf("outage %d = %+v, want %+v", i, g, e)
				}
			}
		})
	}
}

func TestReasonOf(t *testing.T) {
	for _, st := range []string{"queued", "running", "failed", "canceled", "skipped"} {
		if got := reasonOf(Occurrence{Source: SourceRun, Status: st}); got != "run_"+st {
			t.Fatalf("reasonOf(%s) = %s", st, got)
		}
	}
	if got := reasonOf(Occurrence{Source: SourceRun, Status: "weird"}); got != "unknown" {
		t.Fatalf("unknown status = %s", got)
	}
	if got := reasonOf(Occurrence{Source: SourceSkip, Status: "other"}); got != "skipped" {
		t.Fatalf("unknown skip = %s", got)
	}
}

func TestDedupKeyAndEvent(t *testing.T) {
	job, after := uuid.New(), uuid.New()
	k := DedupKey(job, after)
	if k != "backup.missed:"+job.String()+":after:"+after.String() || len(k)+4 > 200 {
		t.Fatalf("key %q", k)
	}
	if !strings.HasSuffix(DedupKey(job, uuid.Nil), ":after:never") {
		t.Fatal("no-success key")
	}
	ev := event(&watched{id: job, window: time.Hour}, Outage{First: t0, Last: t0, Missed: 3, Reason: "run_failed", Ongoing: true}, k+":r2", 2)
	if _, err := notify.ValidateDetail(ev.Detail); err != nil {
		t.Fatalf("detail: %v", err)
	}
	if ev.Type != notify.BackupMissed || ev.Subject.JobID != job || ev.DedupKey != k+":r2" {
		t.Fatalf("event %+v", ev)
	}
}

func TestManual(t *testing.T) {
	if !manual([]byte(`{"kind":"manual"}`)) {
		t.Fatal("manual schedule not recognized")
	}
	if manual([]byte(`{"kind":"cron","cron":"0 3 * * *","timezone":"UTC"}`)) {
		t.Fatal("cron schedule treated as manual")
	}
	if manual([]byte(`{"kind":"bogus"}`)) {
		t.Fatal("a broken schedule must stay watched")
	}
}
