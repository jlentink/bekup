package scheduler

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseValid(t *testing.T) {
	cases := []struct {
		in, canon string
	}{
		{`{"kind":"manual"}`, `{"kind":"manual"}`},
		{`{"kind":"interval","every":2,"unit":"days","anchor":"2026-01-01T03:00","timezone":"Europe/Amsterdam"}`,
			`{"kind":"interval","every":2,"unit":"days","anchor":"2026-01-01T03:00","timezone":"Europe/Amsterdam"}`},
		{`{"kind":"interval","every":1,"unit":"weeks","anchor":"2026-01-05T03:00","timezone":"UTC"}`,
			`{"kind":"interval","every":1,"unit":"weeks","anchor":"2026-01-05T03:00","weekday":"monday","timezone":"UTC"}`},
		{`{"kind":"interval","every":1,"unit":"months","anchor":"2026-01-31T02:00","timezone":"UTC"}`,
			`{"kind":"interval","every":1,"unit":"months","anchor":"2026-01-31T02:00","day_of_month":31,"timezone":"UTC"}`},
		{`{"kind":"interval","every":5,"unit":"minutes","anchor":"2026-01-01T00:00","timezone":"UTC"}`,
			`{"kind":"interval","every":5,"unit":"minutes","anchor":"2026-01-01T00:00","timezone":"UTC"}`},
		{`{"kind":"cron","cron":"  0   3 * *  1-5 ","timezone":"America/New_York"}`,
			`{"kind":"cron","cron":"0 3 * * 1-5","timezone":"America/New_York"}`},
		{`{"kind":"cron","cron":"@DAILY","timezone":"UTC"}`, `{"kind":"cron","cron":"@daily","timezone":"UTC"}`},
		{`{"kind":"cron","cron":"0 2 * JAN-MAR mon","timezone":"UTC"}`, `{"kind":"cron","cron":"0 2 * JAN-MAR mon","timezone":"UTC"}`},
	}
	for _, tc := range cases {
		s, err := Parse([]byte(tc.in), DefaultLimits())
		if err != nil {
			t.Errorf("Parse(%s): %v", tc.in, err)
			continue
		}
		out, err := s.Encode()
		if err != nil || string(out) != tc.canon {
			t.Errorf("Encode(%s) = %s, %v; want %s", tc.in, out, err, tc.canon)
		}
		again, err := Parse(out, DefaultLimits())
		if err != nil || again != s {
			t.Errorf("round trip %s: %+v, %v", tc.in, again, err)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	cases := map[string]string{
		"empty":                ``,
		"not json":             `nope`,
		"array":                `[]`,
		"trailing":             `{"kind":"manual"} {}`,
		"unknown field":        `{"kind":"manual","x":1}`,
		"unknown kind":         `{"kind":"weekly"}`,
		"manual with fields":   `{"kind":"manual","timezone":"UTC"}`,
		"too large":            `{"kind":"manual"` + strings.Repeat(" ", MaxJSONBytes) + `}`,
		"every zero":           `{"kind":"interval","every":0,"unit":"days","anchor":"2026-01-01T03:00","timezone":"UTC"}`,
		"every too big":        `{"kind":"interval","every":367,"unit":"days","anchor":"2026-01-01T03:00","timezone":"UTC"}`,
		"months too big":       `{"kind":"interval","every":25,"unit":"months","anchor":"2026-01-01T03:00","timezone":"UTC"}`,
		"bad unit":             `{"kind":"interval","every":1,"unit":"seconds","anchor":"2026-01-01T03:00","timezone":"UTC"}`,
		"below min interval":   `{"kind":"interval","every":4,"unit":"minutes","anchor":"2026-01-01T03:00","timezone":"UTC"}`,
		"anchor with seconds":  `{"kind":"interval","every":1,"unit":"days","anchor":"2026-01-01T03:00:00","timezone":"UTC"}`,
		"anchor with offset":   `{"kind":"interval","every":1,"unit":"days","anchor":"2026-01-01T03:00Z","timezone":"UTC"}`,
		"anchor invalid date":  `{"kind":"interval","every":1,"unit":"days","anchor":"2026-02-30T03:00","timezone":"UTC"}`,
		"anchor year":          `{"kind":"interval","every":1,"unit":"days","anchor":"1999-01-01T03:00","timezone":"UTC"}`,
		"missing timezone":     `{"kind":"interval","every":1,"unit":"days","anchor":"2026-01-01T03:00"}`,
		"local timezone":       `{"kind":"interval","every":1,"unit":"days","anchor":"2026-01-01T03:00","timezone":"Local"}`,
		"unknown timezone":     `{"kind":"interval","every":1,"unit":"days","anchor":"2026-01-01T03:00","timezone":"Mars/Base"}`,
		"path timezone":        `{"kind":"interval","every":1,"unit":"days","anchor":"2026-01-01T03:00","timezone":"../../etc/passwd"}`,
		"abs timezone":         `{"kind":"interval","every":1,"unit":"days","anchor":"2026-01-01T03:00","timezone":"/etc/localtime"}`,
		"weekday on days":      `{"kind":"interval","every":1,"unit":"days","anchor":"2026-01-01T03:00","weekday":"monday","timezone":"UTC"}`,
		"bad weekday":          `{"kind":"interval","every":1,"unit":"weeks","anchor":"2026-01-01T03:00","weekday":"Monday","timezone":"UTC"}`,
		"dom on weeks":         `{"kind":"interval","every":1,"unit":"weeks","anchor":"2026-01-01T03:00","day_of_month":3,"timezone":"UTC"}`,
		"dom 32":               `{"kind":"interval","every":1,"unit":"months","anchor":"2026-01-01T03:00","day_of_month":32,"timezone":"UTC"}`,
		"interval with cron":   `{"kind":"interval","every":1,"unit":"days","anchor":"2026-01-01T03:00","cron":"0 3 * * *","timezone":"UTC"}`,
		"cron with every":      `{"kind":"cron","every":1,"cron":"0 3 * * *","timezone":"UTC"}`,
		"cron seconds":         `{"kind":"cron","cron":"0 0 3 * * *","timezone":"UTC"}`,
		"cron 4 fields":        `{"kind":"cron","cron":"0 3 * *","timezone":"UTC"}`,
		"cron @every":          `{"kind":"cron","cron":"@every 1h","timezone":"UTC"}`,
		"cron @reboot":         `{"kind":"cron","cron":"@reboot","timezone":"UTC"}`,
		"cron TZ prefix":       `{"kind":"cron","cron":"TZ=UTC 0 3 * * *","timezone":"UTC"}`,
		"cron TZ no space":     `{"kind":"cron","cron":"CRON_TZ=UTC","timezone":"UTC"}`,
		"cron range":           `{"kind":"cron","cron":"0 24 * * *","timezone":"UTC"}`,
		"cron dow 7":           `{"kind":"cron","cron":"0 3 * * 7","timezone":"UTC"}`,
		"cron step 0":          `{"kind":"cron","cron":"*/0 3 * * *","timezone":"UTC"}`,
		"cron never":           `{"kind":"cron","cron":"0 0 30 2 *","timezone":"UTC"}`,
		"cron every minute":    `{"kind":"cron","cron":"* * * * *","timezone":"UTC"}`,
		"cron dense minutes":   `{"kind":"cron","cron":"0-4 3 * * *","timezone":"UTC"}`,
		"cron across midnight": `{"kind":"cron","cron":"1,58 0,23 * * *","timezone":"UTC"}`,
		"cron empty":           `{"kind":"cron","cron":"","timezone":"UTC"}`,
		"cron newline":         `{"kind":"cron","cron":"0 3 * * *\n","timezone":"UTC"}`,
		"cron too long":        `{"kind":"cron","cron":"` + strings.Repeat("1,", 70) + `1 3 * * *","timezone":"UTC"}`,
		"cron descriptor junk": `{"kind":"cron","cron":"@daily 3","timezone":"UTC"}`,
	}
	for name, in := range cases {
		if _, err := Parse([]byte(in), DefaultLimits()); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestLimits(t *testing.T) {
	five := iv(5, UnitMinutes, "2026-01-01T00:00", "UTC")
	if err := five.Validate(DefaultLimits()); err != nil {
		t.Fatalf("5m with default limits: %v", err)
	}
	if err := five.Validate(Limits{MinInterval: 10 * time.Minute}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("5m with 10m floor: %v", err)
	}
	// The absolute floor applies even with a zero policy.
	if err := iv(1, UnitMinutes, "2026-01-01T00:00", "UTC").Validate(Limits{}); err != nil {
		t.Fatalf("1m with zero policy: %v", err)
	}
	// Day intervals are never subject to the minimum.
	if err := iv(1, UnitDays, "2026-01-01T00:00", "UTC").Validate(Limits{MinInterval: 24 * time.Hour}); err != nil {
		t.Fatalf("1 day: %v", err)
	}
	if err := cronS("*/10 * * * *", "UTC").Validate(DefaultLimits()); err != nil {
		t.Fatalf("*/10: %v", err)
	}
	if err := cronS("*/10 * * * *", "UTC").Validate(Limits{MinInterval: 15 * time.Minute}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("*/10 with 15m floor: %v", err)
	}
	// Compile (computation) ignores the policy: tightened limits do not
	// break existing jobs.
	if _, err := Compile(cronS("* * * * *", "UTC")); err != nil {
		t.Fatalf("Compile ignores policy: %v", err)
	}
}

func TestMinGap(t *testing.T) {
	cases := map[string]time.Duration{
		"*/5 * * * *":     5 * time.Minute,
		"0 * * * *":       time.Hour,
		"0 3 * * *":       24 * time.Hour,
		"0,30 3 * * *":    30 * time.Minute,
		"59 23 * * *":     24 * time.Hour,
		"1,58 0,23 * * *": 3 * time.Minute,
		"@hourly":         time.Hour,
		"0,59 3 * * *":    59 * time.Minute,
	}
	for expr, want := range cases {
		spec, _, err := parseCron(expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if got := spec.minGap(); got != want {
			t.Errorf("minGap(%s) = %s, want %s", expr, got, want)
		}
	}
}

func TestDescribe(t *testing.T) {
	cases := []struct {
		s    Schedule
		want string
	}{
		{Schedule{Kind: KindManual}, "Manual only"},
		{iv(2, UnitDays, "2026-01-01T03:00", "Europe/Amsterdam"), "Every 2 days at 03:00, starting 2026-01-01 (Europe/Amsterdam)"},
		{iv(1, UnitDays, "2026-01-01T03:00", "Europe/Amsterdam"), "Every day at 03:00 (Europe/Amsterdam)"},
		{iv(6, UnitHours, "2026-01-01T00:15", "UTC"), "Every 6 hours, starting 2026-01-01 00:15 (UTC)"},
		{iv(1, UnitHours, "2026-01-01T00:15", "UTC"), "Every hour, starting 2026-01-01 00:15 (UTC)"},
		{iv(1, UnitWeeks, "2026-01-05T03:00", "UTC"), "Every week on Monday at 03:00 (UTC)"},
		{iv(1, UnitMonths, "2026-01-01T02:00", "Europe/Amsterdam"), "Every month on day 1 at 02:00 (Europe/Amsterdam)"},
		{iv(3, UnitMonths, "2026-01-31T02:00", "UTC"), "Every 3 months on day 31 at 02:00 (or the last day of shorter months), starting 2026-01 (UTC)"},
		{cronS("0 3 * * 1-5", "Europe/Amsterdam"), "Cron: 0 3 * * 1-5 (Europe/Amsterdam)"},
		{Schedule{Kind: "x"}, "Invalid schedule"},
	}
	for _, tc := range cases {
		if got := Describe(tc.s); got != tc.want {
			t.Errorf("Describe(%+v) = %q, want %q", tc.s, got, tc.want)
		}
	}
}
