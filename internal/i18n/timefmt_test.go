package i18n

import (
	"testing"
	"time"
)

func TestFormatTime(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.March, 2, 14, 5, 0, 0, time.UTC) // a Monday
	for _, tc := range []struct {
		l      Locale
		layout string
		want   string
	}{
		{EN, "2 Jan 2006 15:04", "2 Mar 2026 14:05"},
		{NL, "2 Jan 2006 15:04", "2 mrt 2026 14:05"},
		{NL, "Monday 2 January 2006", "maandag 2 maart 2026"},
		{NL, "Mon 2 Jan, 15:04 MST", "ma 2 mrt, 14:05 UTC"},
		{NL, "2006-01-02 15:04:05 UTC", "2026-03-02 14:05:00 UTC"},
		{"", "Jan", "Mar"},
	} {
		if got := FormatTime(tc.l, at, tc.layout); got != tc.want {
			t.Errorf("FormatTime(%s, %q) = %q, want %q", tc.l, tc.layout, got, tc.want)
		}
	}
}

func TestLocalizeDecimal(t *testing.T) {
	t.Parallel()
	if got := LocalizeDecimal(NL, "1.40 GiB"); got != "1,40 GiB" {
		t.Errorf("NL = %q", got)
	}
	if got := LocalizeDecimal(EN, "1.40 GiB"); got != "1.40 GiB" {
		t.Errorf("EN = %q", got)
	}
}
