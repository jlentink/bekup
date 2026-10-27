package config

import (
	"strings"
	"testing"
)

func TestValidateHealthSocket(t *testing.T) {
	long := "/tmp/bekup-runner/" + strings.Repeat("a", 90) + ".sock"
	for _, tc := range []struct {
		value, want string
	}{
		{"", ""},
		{"/tmp/bekup-runner/health.sock", ""},
		{"/var/lib/bekup/health/h.sock", ""},
		{"relative/health.sock", "absolute path"},
		{"/tmp/bekup-runner/../x/health.sock", "clean path"},
		{"/tmp/health.sock", "dedicated directory"},
		{"/run/h.sock", "dedicated directory"},
		{"/run/secrets/health/h.sock", "/run/secrets"},
		{long, "unix socket path limit"},
	} {
		c := DefaultRunner()
		c.Listen.Address = "10.0.0.1:7443"
		c.Listen.HealthSocket = tc.value
		errs := validateRunner(&c, false)
		var msgs []string
		for _, e := range errs {
			if strings.Contains(e.Error(), "listen.health_socket") {
				msgs = append(msgs, e.Error())
			}
		}
		got := strings.Join(msgs, "; ")
		if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%q: got %q, want %q", tc.value, got, tc.want)
		}
	}
}
