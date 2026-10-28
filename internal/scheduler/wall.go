package scheduler

import "time"

// wall is a local (zone-less) date and time of day to the minute.
type wall struct {
	y      int
	m      time.Month
	d      int
	hh, mm int
}

func wallOf(t time.Time) wall {
	y, m, d := t.Date()
	return wall{y: y, m: m, d: d, hh: t.Hour(), mm: t.Minute()}
}

// utc encodes the wall clock as if it were UTC; used for arithmetic only.
func (w wall) utc() time.Time { return time.Date(w.y, w.m, w.d, w.hh, w.mm, 0, 0, time.UTC) }

// addDays moves the date by n days (normalized), keeping the time of day.
func (w wall) addDays(n int) wall {
	t := time.Date(w.y, w.m, w.d+n, 0, 0, 0, 0, time.UTC)
	return wall{y: t.Year(), m: t.Month(), d: t.Day(), hh: w.hh, mm: w.mm}
}

// dayNumber is the number of days since 1970-01-01 of the wall date.
func (w wall) dayNumber() int64 {
	return floorDiv(time.Date(w.y, w.m, w.d, 0, 0, 0, 0, time.UTC).Unix(), 86400)
}

func daysIn(y int, m time.Month) int {
	return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func offsetAt(unix int64, loc *time.Location) int64 {
	_, off := time.Unix(unix, 0).In(loc).Zone()
	return int64(off)
}

// resolve maps a local wall-clock time in loc to an instant, deterministically
// (time.Date leaves both cases unspecified):
//
//   - an ambiguous time (fall back) resolves to its first, earlier instant;
//   - a nonexistent time (spring forward) is shifted forward by the size of
//     the gap, i.e. it is read with the offset in force before the
//     transition (02:30 in a 02:00→03:00 gap becomes 03:30).
func resolve(w wall, loc *time.Location) time.Time {
	ws := w.utc().Unix()
	// Fast path: far (> 2 days) from any transition the wall time maps to
	// exactly one instant.
	if t, ok := resolveFast(ws, loc); ok {
		return t
	}
	// Offsets in force within ±48h of the wall time cover every candidate
	// (real offsets lie within -12h..+14h).
	offs := make([]int64, 0, 4)
	seen := func(o int64) bool {
		for _, x := range offs {
			if x == o {
				return true
			}
		}
		return false
	}
	for k := int64(-8); k <= 8; k++ {
		if o := offsetAt(ws+k*6*3600, loc); !seen(o) {
			offs = append(offs, o)
		}
	}
	best, found := int64(0), false
	minOff, maxOff := offs[0], offs[0]
	for _, o := range offs {
		minOff, maxOff = min(minOff, o), max(maxOff, o)
		t := ws - o
		if offsetAt(t, loc) == o && (!found || t < best) {
			best, found = t, true
		}
	}
	if found {
		return time.Unix(best, 0).In(loc)
	}
	// Gap: the transition lies in (ws-maxOff, ws-minOff]. The zone period
	// containing ws-minOff starts at the transition; read the wall time with
	// the offset in force just before it.
	start, _ := time.Unix(ws-minOff, 0).In(loc).ZoneBounds()
	if start.IsZero() {
		return time.Date(w.y, w.m, w.d, w.hh, w.mm, 0, 0, loc)
	}
	before := offsetAt(start.Unix()-1, loc)
	return time.Unix(ws-before, 0).In(loc)
}

func resolveFast(ws int64, loc *time.Location) (time.Time, bool) {
	o := offsetAt(ws, loc)
	t := time.Unix(ws-o, 0).In(loc)
	if _, off := t.Zone(); int64(off) != o {
		return time.Time{}, false
	}
	start, end := t.ZoneBounds()
	const margin = 48 * time.Hour
	if (start.IsZero() || t.Sub(start) > margin) && (end.IsZero() || end.Sub(t) > margin) {
		return t, true
	}
	return time.Time{}, false
}

// exists reports whether the wall time exists in loc.
func exists(w wall, loc *time.Location) bool {
	return wallOf(resolve(w, loc)) == w
}
