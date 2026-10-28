package scheduler

import (
	"errors"
	"testing"
	"time"
)

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		`{"kind":"manual"}`,
		`{"kind":"interval","every":2,"unit":"days","anchor":"2026-01-01T03:00","timezone":"Europe/Amsterdam"}`,
		`{"kind":"interval","every":1,"unit":"weeks","anchor":"2026-01-05T03:00","weekday":"friday","timezone":"UTC"}`,
		`{"kind":"interval","every":1,"unit":"months","anchor":"2026-01-31T02:00","day_of_month":31,"timezone":"UTC"}`,
		`{"kind":"interval","every":6,"unit":"hours","anchor":"2026-03-29T02:30","timezone":"Australia/Lord_Howe"}`,
		`{"kind":"cron","cron":"0 3 * * 1-5","timezone":"America/New_York"}`,
		`{"kind":"cron","cron":"@weekly","timezone":"UTC"}`,
		`{"kind":"cron","cron":"TZ=UTC","timezone":"UTC"}`,
		`{}`, `null`, `{"kind":"interval","every":-1}`,
	} {
		f.Add([]byte(seed))
	}
	after := time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := Parse(data, DefaultLimits())
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Parse error does not wrap ErrInvalid: %v", err)
			}
			return
		}
		enc, err := s.Encode()
		if err != nil {
			t.Fatalf("Encode of a parsed schedule: %v", err)
		}
		again, err := Parse(enc, DefaultLimits())
		if err != nil || again != s {
			t.Fatalf("round trip: %+v vs %+v (%v)", again, s, err)
		}
		_ = Describe(s)
		if s.Kind == KindManual {
			return
		}
		list, err := NextN(s, after, 3)
		if err != nil {
			t.Fatalf("NextN of a valid schedule: %v", err)
		}
		prev := after
		for _, n := range list {
			if !n.After(prev) {
				t.Fatalf("not increasing: %s after %s", n, prev)
			}
			prev = n
		}
	})
}

func FuzzCron(f *testing.F) {
	for _, seed := range []string{
		"0 3 * * 1-5", "*/15 * * * *", "@daily", "@every 5m", "@reboot", "0 0 31 2 *",
		"TZ=Europe/Amsterdam 0 3 * * *", "CRON_TZ=", "5-1/0 * * * *", "0 0 * * 7", "0 0 ? * MON",
		"1-59/2 0-23/2 1-31/3 1-12/4 0-6/5", "0 0 0 0 0", "",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, expr string) {
		spec, canon, err := parseCron(expr)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("parseCron error does not wrap ErrInvalid: %v", err)
			}
			return
		}
		if spec2, canon2, err := parseCron(canon); err != nil || canon2 != canon || *spec2 != *spec {
			t.Fatalf("canonical form %q does not re-parse identically (%v)", canon, err)
		}
		if spec.minGap() <= 0 {
			t.Fatalf("non-positive gap for %q", canon)
		}
		after := time.Date(2026, 10, 24, 23, 59, 30, 0, time.UTC)
		loc, err := loadZone("Europe/Amsterdam")
		if err != nil {
			t.Fatal(err)
		}
		n, err := spec.next(after, loc)
		if err != nil || !n.After(after) {
			t.Fatalf("next(%q) = %s, %v", canon, n, err)
		}
	})
}
