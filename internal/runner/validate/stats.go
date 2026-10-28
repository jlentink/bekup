package validate

import (
	"time"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// Statistics limits (task 3.10).
const (
	DefaultStatsPerTable = 5 * time.Minute
	MaxStatsPerTable     = time.Hour
	DefaultStatsBudget   = 30 * time.Minute
	MaxStatsBudget       = 6 * time.Hour
)

// Stats is a validated StatsOptions with the defaults applied.
type Stats struct {
	Deep     bool
	PerTable time.Duration
	Budget   time.Duration
}

// StatsOptions validates the stats options of a Dump or DumpGlobals
// request; nil means no statistics (absent or disabled). An enabled
// request needs a level; the limits are capped.
func StatsOptions(o *runnerpb.StatsOptions) (*Stats, error) {
	if o == nil || !o.GetEnabled() {
		return nil, nil
	}
	s := &Stats{PerTable: DefaultStatsPerTable, Budget: DefaultStatsBudget}
	switch o.GetLevel() {
	case runnerpb.StatsLevel_STATS_LEVEL_STANDARD:
	case runnerpb.StatsLevel_STATS_LEVEL_DEEP:
		s.Deep = true
	default:
		return nil, fieldError("stats.level", "unknown or unspecified level")
	}
	if ms := o.GetPerTableTimeoutMs(); ms > 0 {
		d := time.Duration(ms) * time.Millisecond
		if d > MaxStatsPerTable {
			return nil, fieldError("stats.per_table_timeout_ms", "at most 1 hour")
		}
		s.PerTable = d
	}
	if ms := o.GetBudgetMs(); ms > 0 {
		d := time.Duration(ms) * time.Millisecond
		if d > MaxStatsBudget {
			return nil, fieldError("stats.budget_ms", "at most 6 hours")
		}
		s.Budget = d
	}
	return s, nil
}
