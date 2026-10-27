package sftp

import "testing"

func TestValidateTarget(t *testing.T) {
	ok := []struct {
		host string
		port int
		user string
		base string
		dir  string
	}{
		{"sftp.example.com", 0, "bekup", "/upload", ""},
		{"127.0.0.1", 2222, "bekup", "/", "a/b"},
		{"[::1]", 22, "u", "/x", ""},
	}
	for _, c := range ok {
		if err := ValidateTarget(c.host, c.port, c.user, c.base, c.dir); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	bad := []struct {
		host string
		port int
		user string
		base string
		dir  string
	}{
		{"", 22, "u", "/x", ""},
		{"-bad", 22, "u", "/x", ""},
		// The shared host-name rule (internal/hostname): nothing the SSRF
		// guard would refuse.
		{"a_b.example", 22, "u", "/x", ""},
		{"a..b", 22, "u", "/x", ""},
		{"db.", 22, "u", "/x", ""},
		{"1.2.3", 22, "u", "/x", ""},
		{"[db]", 22, "u", "/x", ""},
		{"[10.0.0.1]", 22, "u", "/x", ""},
		{"fe80::1%eth0", 22, "u", "/x", ""},
		{"h", 22, "-oProxyCommand", "/x", ""},
		{"h", 70000, "u", "/x", ""},
		{"h", 22, "", "/x", ""},
		{"h", 22, "a b", "/x", ""},
		{"h", 22, "u", "relative", ""},
		{"h", 22, "u", "/x/../y", ""},
		{"h", 22, "u", "/x", "../y"},
	}
	for _, c := range bad {
		if err := ValidateTarget(c.host, c.port, c.user, c.base, c.dir); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
}
