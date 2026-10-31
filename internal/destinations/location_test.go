package destinations

import "testing"

// locationChange names the first location setting a change touches; any
// of them may point the destination at other files (D-209).
func TestLocationChange(t *testing.T) {
	s3 := func(mod func(*S3Config)) *S3Config {
		c := &S3Config{Endpoint: "https://minio.example:9000", Region: "eu-1", Bucket: "b", Prefix: "nightly", AccessKeyID: "k"}
		if mod != nil {
			mod(c)
		}
		return c
	}
	sftp := func(mod func(*SFTPConfig)) *SFTPConfig {
		c := &SFTPConfig{Host: "files.example", Port: 22, User: "bekup", BasePath: "/srv/backups", Dir: "nightly"}
		if mod != nil {
			mod(c)
		}
		return c
	}
	for _, tc := range []struct {
		name     string
		cur, nxt any
		want     string
	}{
		{"disk same", &DiskConfig{Path: "nightly"}, &DiskConfig{Path: "nightly/"}, ""},
		{"disk path", &DiskConfig{Path: "nightly"}, &DiskConfig{Path: "weekly"}, "disk.path"},
		{"s3 same", s3(nil), s3(func(c *S3Config) { c.Prefix = "/nightly/"; c.AccessKeyID = "other"; c.StorageClass = "GLACIER" }), ""},
		{"s3 endpoint", s3(nil), s3(func(c *S3Config) { c.Endpoint = "https://other.example" }), "s3.endpoint"},
		{"s3 endpoint spelling", s3(nil), s3(func(c *S3Config) { c.Endpoint = "HTTPS://MINIO.EXAMPLE:9000/" }), ""},
		{"s3 aws endpoints", s3(func(c *S3Config) { c.Endpoint = "" }), s3(func(c *S3Config) { c.Endpoint = "https://s3.eu-west-1.amazonaws.com" }), ""},
		{"s3 region", s3(nil), s3(func(c *S3Config) { c.Region = "eu-2" }), "s3.region"},
		{"s3 bucket", s3(nil), s3(func(c *S3Config) { c.Bucket = "c" }), "s3.bucket"},
		{"s3 prefix", s3(nil), s3(func(c *S3Config) { c.Prefix = "" }), "s3.prefix"},
		{"sftp same", sftp(nil), sftp(func(c *SFTPConfig) { c.Host = "FILES.example."; c.Dir = "/nightly/"; c.AuthMethod = "password" }), ""},
		{"sftp default port", sftp(nil), sftp(func(c *SFTPConfig) { c.Port = 0 }), ""},
		{"sftp host", sftp(nil), sftp(func(c *SFTPConfig) { c.Host = "nas.example" }), "sftp.host"},
		{"sftp port", sftp(nil), sftp(func(c *SFTPConfig) { c.Port = 2222 }), "sftp.port"},
		{"sftp user", sftp(nil), sftp(func(c *SFTPConfig) { c.User = "other" }), "sftp.user"},
		{"sftp base path", sftp(nil), sftp(func(c *SFTPConfig) { c.BasePath = "/srv/other" }), "sftp.basePath"},
		{"sftp base path cleaned", sftp(nil), sftp(func(c *SFTPConfig) { c.BasePath = "/srv/backups/" }), ""},
		{"sftp relative base path", sftp(nil), sftp(func(c *SFTPConfig) { c.BasePath = "srv/backups" }), "sftp.basePath"},
		{"sftp dir", sftp(nil), sftp(func(c *SFTPConfig) { c.Dir = "" }), "sftp.dir"},
	} {
		cur, st := &record{}, &state{}
		ok := false
		switch c := tc.cur.(type) {
		case *DiskConfig:
			cur.disk = c
			st.disk, ok = tc.nxt.(*DiskConfig)
		case *S3Config:
			cur.s3 = c
			st.s3, ok = tc.nxt.(*S3Config)
		case *SFTPConfig:
			cur.sftp = c
			st.sftp, ok = tc.nxt.(*SFTPConfig)
		}
		if !ok {
			t.Fatalf("%s: mismatched configs", tc.name)
		}
		if got := locationChange(cur, st); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Two destinations overlap when one location is, contains or lies inside
// the other (D-209).
func TestLocationOverlaps(t *testing.T) {
	loc := func(disk *DiskConfig, s3 *S3Config, sftp *SFTPConfig) location {
		t.Helper()
		l, ok := locationOf(disk, s3, sftp)
		if !ok {
			t.Fatal("no location")
		}
		return l
	}
	disk := func(p string) location { return loc(&DiskConfig{Path: p}, nil, nil) }
	s3 := func(endpoint, bucket, prefix string) location {
		return loc(nil, &S3Config{Endpoint: endpoint, Bucket: bucket, Prefix: prefix}, nil)
	}
	sftp := func(host string, port int, user, base, dir string) location {
		return loc(nil, nil, &SFTPConfig{Host: host, Port: port, User: user, BasePath: base, Dir: dir})
	}
	for _, tc := range []struct {
		name string
		a, b location
		want bool
	}{
		{"disk same", disk("nightly"), disk("nightly"), true},
		{"disk nested", disk("backups"), disk("backups/db"), true},
		{"disk sibling prefix", disk("back"), disk("backups"), false},
		{"disk other", disk("a"), disk("b"), false},
		{"s3 same", s3("", "b", "x"), s3("https://s3.eu-west-1.amazonaws.com", "b", "x/"), true},
		{"s3 bucket root contains all", s3("", "b", ""), s3("", "b", "x"), true},
		{"s3 other bucket", s3("", "b", "x"), s3("", "c", "x"), false},
		{"s3 other endpoint", s3("https://minio.a", "b", "x"), s3("https://minio.b", "b", "x"), false},
		{"s3 endpoint default port", s3("https://minio.a:443", "b", "x"), s3("minio.a", "b", "x"), true},
		{"s3 prefix sibling", s3("", "b", "x"), s3("", "b", "xy"), false},
		{"sftp same", sftp("h", 22, "u", "/srv", "a"), sftp("H.", 0, "other", "/srv/", "a"), true},
		{"sftp base path vs dir", sftp("h", 22, "u", "/srv/a", ""), sftp("h", 22, "u", "/srv", "a/b"), true},
		{"sftp other dir", sftp("h", 22, "u", "/srv", "a"), sftp("h", 22, "u", "/srv", "b"), false},
		{"sftp other port", sftp("h", 22, "u", "/srv", ""), sftp("h", 2222, "u", "/srv", ""), false},
		{"sftp relative other user", sftp("h", 22, "u", "backups", ""), sftp("h", 22, "v", "backups", ""), false},
		{"sftp relative same user", sftp("h", 22, "u", "backups", ""), sftp("h", 22, "u", "backups/x", ""), true},
		{"types never overlap", disk("x"), s3("", "x", ""), false},
	} {
		if got := tc.a.overlaps(tc.b); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
		if got := tc.b.overlaps(tc.a); got != tc.want {
			t.Errorf("%s (reversed): got %v, want %v", tc.name, got, tc.want)
		}
	}
}
