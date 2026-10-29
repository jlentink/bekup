package monitor

import (
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/notify"
)

func TestExpiryKind(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	d := 24 * time.Hour
	cases := []struct {
		left      time.Duration
		kind      string
		threshold int
		due       bool
	}{
		{90 * d, "", 0, false},
		{31 * d, "", 0, false},
		{30 * d, notify.KindCertExpiring, 30, true},
		{8 * d, notify.KindCertExpiring, 30, true},
		{7 * d, notify.KindCertExpiring, 7, true},
		{time.Hour, notify.KindCertExpiring, 7, true},
		{0, notify.KindCertExpired, 0, true},
		{-5 * d, notify.KindCertExpired, 0, true},
	}
	for _, c := range cases {
		kind, th, due := expiryKind(now.Add(c.left), now, notify.KindCertExpiring, notify.KindCertExpired)
		if kind != c.kind || th != c.threshold || due != c.due {
			t.Errorf("left %s: got %q %d %v, want %q %d %v", c.left, kind, th, due, c.kind, c.threshold, c.due)
		}
	}
}

// TestExpiryEventDedup: one alert per certificate, expiry and threshold;
// a renewed certificate (new expiry) alerts again.
func TestExpiryEventDedup(t *testing.T) {
	na := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	a := expiryEvent(notify.KindCertExpiring, 30, CertRunnerClient, na)
	b := expiryEvent(notify.KindCertExpiring, 30, CertRunnerClient, na)
	c := expiryEvent(notify.KindCertExpiring, 7, CertRunnerClient, na)
	r := expiryEvent(notify.KindCertExpiring, 30, CertRunnerClient, na.Add(90*24*time.Hour))
	if a.DedupKey != b.DedupKey || a.DedupKey == c.DedupKey || a.DedupKey == r.DedupKey {
		t.Fatalf("dedup keys %q %q %q %q", a.DedupKey, b.DedupKey, c.DedupKey, r.DedupKey)
	}
	if a.Severity != notify.SeverityWarning || c.Severity != notify.SeverityError {
		t.Fatalf("severities %s %s", a.Severity, c.Severity)
	}
	x := expiryEvent(notify.KindCertExpired, 0, CertRunnerClient, na)
	if x.Severity != notify.SeverityCritical {
		t.Fatalf("expired severity %s", x.Severity)
	}
	for _, ev := range []notify.Event{a, c, x} {
		if _, err := notify.ValidateDetail(ev.Detail); err != nil {
			t.Fatal(err)
		}
	}
}
