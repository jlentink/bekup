package scheduler

import (
	"errors"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// starBit mirrors robfig's marker for a '*' or '?' field (dom/dow OR rule).
const starBit = uint64(1) << 63

// cronScanDays bounds the day-by-day search for the next cron occurrence.
// 29 years cover every date/weekday combination (e.g. Feb 29 on a Monday)
// within 1901-2099; an expression without an occurrence in that window is
// rejected at validation.
const cronScanDays = 29*366 + 1

var allowedDescriptors = map[string]bool{
	"@yearly": true, "@annually": true, "@monthly": true, "@weekly": true,
	"@daily": true, "@midnight": true, "@hourly": true,
}

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// cronSpec holds the bit sets of a parsed expression. Only robfig's parser is
// used; occurrences are computed by next with this package's DST rules.
type cronSpec struct {
	minute, hour, dom, month, dow uint64
}

// parseCron validates expr and returns its bit sets and canonical text.
// Accepted: standard 5-field expressions (numbers, names, ranges, steps,
// lists, '*' and '?') and the descriptors in allowedDescriptors. Rejected:
// seconds fields, @every, @reboot, TZ=/CRON_TZ= prefixes (the zone is a
// separate field) and anything outside the printable-ASCII subset.
func parseCron(expr string) (spec *cronSpec, canon string, err error) {
	if expr == "" || len(expr) > MaxCronLength {
		return nil, "", invalid("cron expression must be 1 to %d characters", MaxCronLength)
	}
	for _, r := range expr {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == ' ', r == '\t', r == '*', r == ',', r == '-', r == '/', r == '?', r == '@':
		default:
			return nil, "", invalid("cron expression contains an unsupported character")
		}
	}
	fields := strings.Fields(expr)
	if len(fields) == 0 {
		return nil, "", invalid("cron expression is empty")
	}
	if strings.HasPrefix(fields[0], "@") {
		d := strings.ToLower(fields[0])
		if len(fields) != 1 || !allowedDescriptors[d] {
			return nil, "", invalid("cron descriptor must be one of @yearly, @annually, @monthly, @weekly, @daily, @midnight, @hourly")
		}
		canon = d
	} else {
		if len(fields) != 5 {
			return nil, "", invalid("cron expression must have exactly 5 fields (minute hour day-of-month month day-of-week)")
		}
		if strings.Contains(expr, "@") {
			return nil, "", invalid("cron expression contains an unsupported character")
		}
		canon = strings.Join(fields, " ")
	}
	sched, err := safeParse(canon)
	if err != nil {
		return nil, "", invalid("cron expression: %s", err.Error())
	}
	ss, ok := sched.(*cron.SpecSchedule)
	if !ok {
		return nil, "", invalid("unsupported cron expression")
	}
	if ss.Second&^starBit != 1 {
		return nil, "", invalid("unsupported cron expression")
	}
	spec = &cronSpec{minute: ss.Minute, hour: ss.Hour, dom: ss.Dom, month: ss.Month, dow: ss.Dow}
	// robfig accepts empty list items (e.g. ","), which yield empty sets.
	for _, f := range []uint64{spec.minute, spec.hour, spec.dom, spec.month, spec.dow} {
		if f&^starBit == 0 {
			return nil, "", invalid("cron expression has an empty field")
		}
	}
	// The expression must match some real date (rejects e.g. "0 0 30 2 *").
	ref := wall{y: 2000, m: time.January, d: 1}
	if _, ok := spec.nextDay(ref, cronScanDays); !ok {
		return nil, "", invalid("cron expression never matches a calendar date")
	}
	return spec, canon, nil
}

// safeParse shields against parser panics on hostile input.
func safeParse(expr string) (s cron.Schedule, err error) {
	defer func() {
		if r := recover(); r != nil {
			s, err = nil, errors.New("unparsable expression")
		}
	}()
	return cronParser.Parse(expr)
}

func (c *cronSpec) dayMatches(w wall) bool {
	if c.month&(1<<uint(w.m)) == 0 {
		return false
	}
	domMatch := c.dom&(1<<uint(w.d)) != 0
	wd := time.Date(w.y, w.m, w.d, 0, 0, 0, 0, time.UTC).Weekday()
	dowMatch := c.dow&(1<<uint(wd)) != 0
	if c.dom&starBit != 0 || c.dow&starBit != 0 {
		return domMatch && dowMatch
	}
	return domMatch || dowMatch
}

// nextDay returns the first matching date on or after from, scanning at most
// limit days.
func (c *cronSpec) nextDay(from wall, limit int) (wall, bool) {
	d := wall{y: from.y, m: from.m, d: from.d}
	for range limit {
		if c.dayMatches(d) {
			return d, true
		}
		d = d.addDays(1)
	}
	return wall{}, false
}

// times returns the matching minutes of day in ascending order.
func (c *cronSpec) times() []int {
	var out []int
	for h := range 24 {
		if c.hour&(1<<uint(h)) == 0 {
			continue
		}
		for m := range 60 {
			if c.minute&(1<<uint(m)) != 0 {
				out = append(out, h*60+m)
			}
		}
	}
	return out
}

// minGap is the smallest wall-clock gap between two occurrences, assuming
// consecutive matching days (conservative). It ignores DST shifts.
func (c *cronSpec) minGap() time.Duration {
	ts := c.times()
	gap := 1440 + ts[0] - ts[len(ts)-1] // across midnight
	for i := 1; i < len(ts); i++ {
		gap = min(gap, ts[i]-ts[i-1])
	}
	return time.Duration(gap) * time.Minute
}

// next returns the earliest occurrence strictly after `after`. Every matching
// wall time maps to an instant through resolve (DST rules); because shifted
// gap times are not monotonic in wall time, matches are scanned until no later
// wall time can resolve to an earlier instant: resolve(w) >= w - maxOffset,
// with maxOffset the largest offset in force around the best candidate.
func (c *cronSpec) next(after time.Time, loc *time.Location) (time.Time, error) {
	// Start a day early so that wall times shifted across midnight are seen.
	start := wallOf(after.In(loc)).addDays(-1)
	ts := c.times()
	var best time.Time
	var bound int64 // best.Unix() + maxOffset near best
	// resolve(w) <= w - minOffset: earlier wall times cannot be after `after`.
	skipUpTo := after.Unix() + minOffsetNear(after.Unix(), loc)
	day := start
	remaining := cronScanDays + 2
	for remaining > 0 {
		d, ok := c.nextDay(day, remaining)
		if !ok {
			break
		}
		remaining -= int(d.dayNumber()-day.dayNumber()) + 1
		for _, t := range ts {
			w := wall{y: d.y, m: d.m, d: d.d, hh: t / 60, mm: t % 60}
			if !best.IsZero() && w.utc().Unix() > bound {
				return best, nil
			}
			if w.utc().Unix() <= skipUpTo {
				continue
			}
			at := resolve(w, loc)
			if at.After(after) && (best.IsZero() || at.Before(best)) {
				best = at
				bound = best.Unix() + maxOffsetNear(best.Unix(), loc)
			}
		}
		day = d.addDays(1)
	}
	if !best.IsZero() {
		return best, nil
	}
	return time.Time{}, ErrNoOccurrence
}

// minOffsetNear is the smallest UTC offset in force within ±48h of unix.
func minOffsetNear(unix int64, loc *time.Location) int64 {
	m := offsetAt(unix, loc)
	for k := int64(-8); k <= 8; k++ {
		m = min(m, offsetAt(unix+k*6*3600, loc))
	}
	return m
}

// maxOffsetNear is the largest UTC offset in force within ±48h of unix.
func maxOffsetNear(unix int64, loc *time.Location) int64 {
	m := offsetAt(unix, loc)
	for k := int64(-8); k <= 8; k++ {
		m = max(m, offsetAt(unix+k*6*3600, loc))
	}
	return m
}
