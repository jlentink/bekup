package scheduler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // embedded zone database (D-028, D-044)
)

// Kind is the type of a schedule.
type Kind string

// Schedule kinds.
const (
	KindManual   Kind = "manual"
	KindInterval Kind = "interval"
	KindCron     Kind = "cron"
)

// Unit is the unit of an interval schedule.
type Unit string

// Interval units. Minutes and hours are absolute durations; days, weeks and
// months follow the wall clock of the schedule's zone.
const (
	UnitMinutes Unit = "minutes"
	UnitHours   Unit = "hours"
	UnitDays    Unit = "days"
	UnitWeeks   Unit = "weeks"
	UnitMonths  Unit = "months"
)

// AnchorLayout is the layout of Schedule.Anchor: a local date-time without
// seconds or offset, interpreted in Schedule.Timezone.
const AnchorLayout = "2006-01-02T15:04"

// Hard limits. They bound the arithmetic and the stored JSON; the configurable
// policy (Limits) is checked on top of them when a schedule is written.
const (
	// MaxJSONBytes is the largest accepted schedule JSON document.
	MaxJSONBytes = 1024
	// MaxCronLength is the longest accepted cron expression.
	MaxCronLength = 128
	// MaxTimezoneLength is the longest accepted IANA zone name.
	MaxTimezoneLength = 64
	// MaxPreview is the largest n accepted by NextN.
	MaxPreview = 50
	// MinAnchorYear and MaxAnchorYear bound the anchor date.
	MinAnchorYear = 2000
	MaxAnchorYear = 2199
	// AbsoluteMinInterval is the floor below which no schedule compiles,
	// whatever the configured policy.
	AbsoluteMinInterval = time.Minute
	// DefaultMinInterval is the default of scheduler.min_interval.
	DefaultMinInterval = 5 * time.Minute
)

// maxEvery is the largest N per unit.
var maxEvery = map[Unit]int{
	UnitMinutes: 1440, // one day
	UnitHours:   168,  // one week
	UnitDays:    366,
	UnitWeeks:   52,
	UnitMonths:  24,
}

// Errors returned by validation and computation. Validation errors wrap
// ErrInvalid so callers can map them to a VALIDATION API error.
var (
	ErrInvalid      = errors.New("invalid schedule")
	ErrManual       = errors.New("manual schedule has no occurrences")
	ErrNoOccurrence = errors.New("schedule has no further occurrence")
	ErrOutOfRange   = errors.New("time out of the supported range")
)

// Schedule is a validated-on-use, JSON-serializable schedule. The zero value
// is not valid; use Parse or Normalize to obtain a canonical schedule.
//
// JSON examples:
//
//	{"kind":"manual"}
//	{"kind":"interval","every":2,"unit":"days","anchor":"2026-01-01T03:00","timezone":"Europe/Amsterdam"}
//	{"kind":"interval","every":1,"unit":"weeks","anchor":"2026-01-05T03:00","weekday":"monday","timezone":"UTC"}
//	{"kind":"interval","every":1,"unit":"months","anchor":"2026-01-31T02:00","day_of_month":31,"timezone":"UTC"}
//	{"kind":"cron","cron":"0 3 * * 1-5","timezone":"Europe/Amsterdam"}
type Schedule struct {
	Kind Kind `json:"kind"`

	// Interval fields.
	Every int  `json:"every,omitempty"`
	Unit  Unit `json:"unit,omitempty"`
	// Anchor is the first occurrence (local, AnchorLayout). For minutes and
	// hours it fixes the phase of the series; for days, weeks and months it
	// also gives the time of day.
	Anchor string `json:"anchor,omitempty"`
	// Weekday (weeks only; lower-case English name) defaults to the anchor's
	// weekday. The series starts on the first such day on or after the anchor
	// date.
	Weekday string `json:"weekday,omitempty"`
	// DayOfMonth (months only, 1-31) defaults to the anchor's day. Shorter
	// months clamp it to their last day.
	DayOfMonth int `json:"day_of_month,omitempty"`

	// Cron is a 5-field expression or one of @yearly, @annually, @monthly,
	// @weekly, @daily, @midnight, @hourly.
	Cron string `json:"cron,omitempty"`

	// Timezone is an IANA zone name (interval and cron).
	Timezone string `json:"timezone,omitempty"`
}

// Limits is the configurable policy checked when a schedule is created or
// changed (scheduler.min_interval). Existing schedules keep computing even if
// the policy is tightened later.
type Limits struct {
	// MinInterval is the shortest allowed interval for minute/hour schedules
	// and the shortest allowed gap between two cron occurrences.
	MinInterval time.Duration
}

// DefaultLimits returns the default policy.
func DefaultLimits() Limits { return Limits{MinInterval: DefaultMinInterval} }

// Parse decodes a schedule from its JSON form, normalizes it and checks it
// against lim. Unknown fields, trailing data and documents larger than
// MaxJSONBytes are rejected.
func Parse(data []byte, lim Limits) (Schedule, error) {
	if len(data) > MaxJSONBytes {
		return Schedule{}, fmt.Errorf("%w: document larger than %d bytes", ErrInvalid, MaxJSONBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s Schedule
	if err := dec.Decode(&s); err != nil {
		return Schedule{}, fmt.Errorf("%w: malformed JSON", ErrInvalid)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Schedule{}, fmt.Errorf("%w: trailing data after the schedule object", ErrInvalid)
	}
	n, err := s.Normalize()
	if err != nil {
		return Schedule{}, err
	}
	if err := n.CheckLimits(lim); err != nil {
		return Schedule{}, err
	}
	return n, nil
}

// Encode returns the canonical JSON form; a Schedule from Parse/Normalize
// round-trips through it unchanged.
func (s Schedule) Encode() ([]byte, error) {
	n, err := s.Normalize()
	if err != nil {
		return nil, err
	}
	return json.Marshal(n)
}

// Validate normalizes s and checks it against lim.
func (s Schedule) Validate(lim Limits) error {
	n, err := s.Normalize()
	if err != nil {
		return err
	}
	return n.CheckLimits(lim)
}

// Normalize checks the structure and the hard limits of s and returns its
// canonical form (defaults filled in, cron whitespace collapsed).
func (s Schedule) Normalize() (Schedule, error) {
	p, err := compile(s)
	if err != nil {
		return Schedule{}, err
	}
	return p.canon, nil
}

// CheckLimits checks the configurable policy. s must be normalized.
func (s Schedule) CheckLimits(lim Limits) error {
	minIv := max(lim.MinInterval, AbsoluteMinInterval)
	p, err := compile(s)
	if err != nil {
		return err
	}
	switch p.kind {
	case KindInterval:
		if p.unit == UnitMinutes || p.unit == UnitHours {
			if p.step < minIv {
				return fmt.Errorf("%w: interval %s is shorter than the minimum %s", ErrInvalid, p.step, minIv)
			}
		}
	case KindCron:
		if gap := p.cron.minGap(); gap < minIv {
			return fmt.Errorf("%w: cron occurrences can be %s apart, the minimum is %s", ErrInvalid, gap, minIv)
		}
	case KindManual:
	}
	return nil
}

// Plan is a compiled schedule ready for repeated computation.
type Plan struct {
	canon Schedule
	kind  Kind
	loc   *time.Location

	// interval
	unit       Unit
	every      int
	step       time.Duration // minutes/hours
	anchorWall wall          // local anchor
	anchorAt   time.Time     // resolved anchor instant
	baseDate   wall          // weeks: first matching weekday on/after the anchor date
	dom        int           // months

	cron *cronSpec
}

// Compile checks s (structure and hard limits, not the configurable policy)
// and returns a Plan.
func Compile(s Schedule) (*Plan, error) { return compile(s) }

// Schedule returns the canonical schedule of the plan.
func (p *Plan) Schedule() Schedule { return p.canon }

// Location returns the plan's zone (nil for manual schedules).
func (p *Plan) Location() *time.Location { return p.loc }

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func compile(s Schedule) (*Plan, error) {
	switch s.Kind {
	case KindManual:
		if s != (Schedule{Kind: KindManual}) {
			return nil, invalid("a manual schedule takes no other fields")
		}
		return &Plan{canon: s, kind: KindManual}, nil
	case KindInterval:
		return compileInterval(s)
	case KindCron:
		if s.Every != 0 || s.Unit != "" || s.Anchor != "" || s.Weekday != "" || s.DayOfMonth != 0 {
			return nil, invalid("a cron schedule takes only cron and timezone")
		}
		loc, err := loadZone(s.Timezone)
		if err != nil {
			return nil, err
		}
		spec, canonExpr, err := parseCron(s.Cron)
		if err != nil {
			return nil, err
		}
		return &Plan{
			canon: Schedule{Kind: KindCron, Cron: canonExpr, Timezone: s.Timezone},
			kind:  KindCron,
			loc:   loc,
			cron:  spec,
		}, nil
	default:
		return nil, invalid("kind must be manual, interval or cron")
	}
}

var weekdayNames = [...]string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

func parseWeekday(name string) (time.Weekday, bool) {
	for i, n := range weekdayNames {
		if n == name {
			return time.Weekday(i), true
		}
	}
	return 0, false
}

func compileInterval(s Schedule) (*Plan, error) {
	if s.Cron != "" {
		return nil, invalid("an interval schedule takes no cron expression")
	}
	limit, ok := maxEvery[s.Unit]
	if !ok {
		return nil, invalid("unit must be minutes, hours, days, weeks or months")
	}
	if s.Every < 1 || s.Every > limit {
		return nil, invalid("every must be between 1 and %d for %s", limit, s.Unit)
	}
	loc, err := loadZone(s.Timezone)
	if err != nil {
		return nil, err
	}
	if len(s.Anchor) != len(AnchorLayout) {
		return nil, invalid("anchor must have the form YYYY-MM-DDTHH:MM")
	}
	at, err := time.Parse(AnchorLayout, s.Anchor)
	if err != nil {
		return nil, invalid("anchor must have the form YYYY-MM-DDTHH:MM")
	}
	if at.Year() < MinAnchorYear || at.Year() > MaxAnchorYear {
		return nil, invalid("anchor year must be between %d and %d", MinAnchorYear, MaxAnchorYear)
	}
	aw := wallOf(at)
	p := &Plan{
		kind:       KindInterval,
		loc:        loc,
		unit:       s.Unit,
		every:      s.Every,
		anchorWall: aw,
		anchorAt:   resolve(aw, loc),
	}
	canon := Schedule{Kind: KindInterval, Every: s.Every, Unit: s.Unit, Anchor: at.Format(AnchorLayout), Timezone: s.Timezone}

	if s.Weekday != "" && s.Unit != UnitWeeks {
		return nil, invalid("weekday is only allowed for weekly schedules")
	}
	if s.DayOfMonth != 0 && s.Unit != UnitMonths {
		return nil, invalid("day_of_month is only allowed for monthly schedules")
	}
	switch s.Unit {
	case UnitDays:
	case UnitMinutes:
		p.step = time.Duration(s.Every) * time.Minute
	case UnitHours:
		p.step = time.Duration(s.Every) * time.Hour
	case UnitWeeks:
		wd := at.Weekday()
		if s.Weekday != "" {
			if wd, ok = parseWeekday(s.Weekday); !ok {
				return nil, invalid("weekday must be a lower-case English day name")
			}
		}
		canon.Weekday = weekdayNames[wd]
		shift := (int(wd) - int(at.Weekday()) + 7) % 7
		p.baseDate = aw.addDays(shift)
	case UnitMonths:
		dom := at.Day()
		if s.DayOfMonth != 0 {
			if s.DayOfMonth < 1 || s.DayOfMonth > 31 {
				return nil, invalid("day_of_month must be between 1 and 31")
			}
			dom = s.DayOfMonth
		}
		canon.DayOfMonth = dom
		p.dom = dom
	}
	p.canon = canon
	return p, nil
}

// zoneCache holds successfully loaded zones; it is bounded by the number of
// zones in the embedded database because only valid names are stored.
var zoneCache sync.Map // string -> *time.Location

func validZoneName(name string) bool {
	if name == "" || len(name) > MaxTimezoneLength || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '/', r == '_', r == '-', r == '+':
		default:
			return false
		}
	}
	return true
}

// loadZone loads an IANA zone. "Local" and the empty name are rejected: a
// schedule's zone must be explicit and host independent.
func loadZone(name string) (*time.Location, error) {
	if !validZoneName(name) || name == "Local" {
		return nil, invalid("timezone must be an IANA zone name such as Europe/Amsterdam or UTC")
	}
	if l, ok := zoneCache.Load(name); ok {
		if loc, ok := l.(*time.Location); ok {
			return loc, nil
		}
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, invalid("unknown timezone %q", name)
	}
	zoneCache.Store(name, loc)
	return loc, nil
}
