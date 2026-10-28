package scheduler

import (
	"fmt"
	"strings"
)

var unitSingular = map[Unit]string{
	UnitMinutes: "minute", UnitHours: "hour", UnitDays: "day", UnitWeeks: "week", UnitMonths: "month",
}

// Describe returns a short English summary of s for the UI, e.g.
// "Every 2 days at 03:00 (Europe/Amsterdam)". Invalid schedules yield
// "Invalid schedule".
func Describe(s Schedule) string {
	p, err := compile(s)
	if err != nil {
		return "Invalid schedule"
	}
	return p.Describe()
}

// Describe returns a short English summary of the plan.
func (p *Plan) Describe() string {
	c := p.canon
	switch p.kind {
	case KindManual:
		return "Manual only"
	case KindCron:
		return fmt.Sprintf("Cron: %s (%s)", c.Cron, c.Timezone)
	case KindInterval:
	}
	every := "Every " + unitSingular[p.unit]
	if p.every > 1 {
		every = fmt.Sprintf("Every %d %s", p.every, p.unit)
	}
	at := fmt.Sprintf("%02d:%02d", p.anchorWall.hh, p.anchorWall.mm)
	start := strings.Replace(c.Anchor, "T", " ", 1)
	var b strings.Builder
	b.WriteString(every)
	switch p.unit {
	case UnitMinutes, UnitHours:
		fmt.Fprintf(&b, ", starting %s", start)
	case UnitDays:
		fmt.Fprintf(&b, " at %s", at)
		if p.every > 1 {
			fmt.Fprintf(&b, ", starting %s", start[:10])
		}
	case UnitWeeks:
		day := strings.ToUpper(c.Weekday[:1]) + c.Weekday[1:]
		fmt.Fprintf(&b, " on %s at %s", day, at)
		if p.every > 1 {
			fmt.Fprintf(&b, ", starting %s", start[:10])
		}
	case UnitMonths:
		fmt.Fprintf(&b, " on day %d at %s", p.dom, at)
		if p.dom > 28 {
			b.WriteString(" (or the last day of shorter months)")
		}
		if p.every > 1 {
			fmt.Fprintf(&b, ", starting %s", start[:7])
		}
	}
	fmt.Fprintf(&b, " (%s)", c.Timezone)
	return b.String()
}
