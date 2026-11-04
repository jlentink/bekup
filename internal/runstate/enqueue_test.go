package runstate

import (
	"testing"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/queue"
)

// TestTriggerOf: every queue trigger maps to its run_trigger value, and
// every run_trigger value has a queue trigger (an enum value added by a
// migration without a trigger cannot be enqueued).
func TestTriggerOf(t *testing.T) {
	want := map[queue.Trigger]sqlc.RunTrigger{
		queue.TriggerSchedule: sqlc.RunTriggerSchedule,
		queue.TriggerCatchUp:  sqlc.RunTriggerCatchUp,
		queue.TriggerManual:   sqlc.RunTriggerManual,
		queue.TriggerEarly:    sqlc.RunTriggerEarly,
		queue.TriggerAPI:      sqlc.RunTriggerApi,
	}
	covered := map[sqlc.RunTrigger]bool{}
	for in, out := range want {
		got, err := TriggerOf(in)
		if err != nil || got != out {
			t.Errorf("TriggerOf(%q) = %q, %v; want %q", in, got, err, out)
		}
		if string(in) != string(out) {
			t.Errorf("trigger %q stored as %q", in, out)
		}
		covered[got] = true
	}
	for _, v := range sqlc.AllRunTriggerValues() {
		if !covered[v] {
			t.Errorf("run_trigger %q has no queue trigger", v)
		}
	}
	for _, bad := range []queue.Trigger{"", "API", "other"} {
		if _, err := TriggerOf(bad); err == nil {
			t.Errorf("TriggerOf(%q) accepted", bad)
		}
	}
}
