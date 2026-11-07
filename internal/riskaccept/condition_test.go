package riskaccept

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestConditionBuilders(t *testing.T) {
	conn, job := uuid.New(), uuid.New()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	tls, ok := ConnectionTLS(conn, "disabled", "DB.example", 5432, RouteDirect)
	if !ok || tls.Kind != KindConnectionTLS || tls.ConnectionID != conn || tls.Display != "TLS disabled (DB.example:5432)" {
		t.Fatalf("tls %+v", tls)
	}
	if _, ok := ConnectionTLS(conn, "pinned", "db", 5432, RouteDirect); ok {
		t.Fatal("pinned is not a risk")
	}
	priv, ok := PrivilegedRole(conn, "privileged", []string{"superuser", "createrole", "superuser"}, "db", 5432, "backup", RouteDirect)
	if !ok || priv.Display != "Privileged role backup: createrole, superuser" {
		t.Fatalf("priv %+v", priv)
	}
	if _, ok := PrivilegedRole(conn, "not_privileged", nil, "db", 5432, "backup", RouteDirect); ok {
		t.Fatal("not privileged is not a risk")
	}
	if u, ok := PrivilegedRole(conn, "unknown", nil, "db", 5432, "backup", RouteDirect); !ok || !strings.Contains(u.Display, "unknown") {
		t.Fatalf("unknown %+v", u)
	}
	if _, ok := SMTPUnencrypted("starttls", "mail", 587, ""); ok {
		t.Fatal("starttls is not a risk")
	}
	if _, ok := SMTPUnencrypted("auto", "mail", 587, ""); !ok {
		t.Fatal("auto is a risk")
	}
	if _, ok := EncryptionOptOut(job, nil); ok {
		t.Fatal("an encrypted job is not a risk")
	}
	opt, ok := EncryptionOptOut(job, &at)
	if !ok || opt.JobID != job || opt.ConnectionID != uuid.Nil {
		t.Fatalf("opt-out %+v", opt)
	}
	if len(opt.ID()) != 64 {
		t.Fatalf("id %q", opt.ID())
	}
	for _, c := range []Condition{InsecureTransport("http://x", true), InsecureDBTLS("require", true, "db"), InsecureDBRoles(), UnpinnedImage()} {
		if !c.Kind.Instance() || c.ConnectionID != uuid.Nil || c.JobID != uuid.Nil || c.Display == "" {
			t.Fatalf("instance %+v", c)
		}
	}
}

// TestConditionChanges: every fact that makes it a different risk changes
// the hash; the same facts (in another order, another host case) do not.
func TestConditionChanges(t *testing.T) {
	conn := uuid.New()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	base, _ := ConnectionTLS(conn, "unverified", "db", 5432, RouteDirect)
	same, _ := ConnectionTLS(conn, "unverified", "DB", 5432, RouteDirect)
	if base.Hash != same.Hash {
		t.Fatal("host case changed the hash")
	}
	p1, _ := PrivilegedRole(conn, "privileged", []string{"a", "b"}, "db", 5432, "u", RouteDirect)
	p2, _ := PrivilegedRole(conn, "privileged", []string{"b", "a"}, "db", 5432, "u", RouteDirect)
	if p1.Hash != p2.Hash {
		t.Fatal("reason order changed the hash")
	}
	o1, _ := EncryptionOptOut(conn, &at)
	later := at.Add(time.Microsecond)
	o2, _ := EncryptionOptOut(conn, &later)
	differ := map[string][2]Condition{
		"tls mode":   {base, must(ConnectionTLS(conn, "disabled", "db", 5432, RouteDirect))},
		"host":       {base, must(ConnectionTLS(conn, "unverified", "db2", 5432, RouteDirect))},
		"port":       {base, must(ConnectionTLS(conn, "unverified", "db", 5433, RouteDirect))},
		"connection": {base, must(ConnectionTLS(uuid.New(), "unverified", "db", 5432, RouteDirect))},
		"reasons":    {p1, must(PrivilegedRole(conn, "privileged", []string{"a"}, "db", 5432, "u", RouteDirect))},
		"status":     {p1, must(PrivilegedRole(conn, "unknown", []string{"a", "b"}, "db", 5432, "u", RouteDirect))},
		"role":       {p1, must(PrivilegedRole(conn, "privileged", []string{"a", "b"}, "db", 5432, "v", RouteDirect))},
		"opt-out":    {o1, o2},
		"db tls":     {InsecureDBTLS("require", false, "db"), InsecureDBTLS("disable", false, "db")},
		"db tls ca":  {InsecureDBTLS("require", false, "db"), InsecureDBTLS("require", true, "db")},
		"base url":   {InsecureTransport("http://a", false), InsecureTransport("http://b", false)},
		"smtp mode":  {must(SMTPUnencrypted("auto", "m", 25, "")), must(SMTPUnencrypted("none", "m", 25, ""))},
		"kind":       {base, must(PrivilegedRole(conn, "unknown", nil, "db", 5432, "", RouteDirect))},
		"ssh route":  {base, must(ConnectionTLS(conn, "unverified", "db", 5432, Route(true, "u", "bastion", 22)))},
		"bastion":    {must(ConnectionTLS(conn, "unverified", "db", 5432, Route(true, "u", "b1", 22))), must(ConnectionTLS(conn, "unverified", "db", 5432, Route(true, "u", "b2", 22)))},
		"smtp user":  {must(SMTPUnencrypted("auto", "m", 25, "a")), must(SMTPUnencrypted("auto", "m", 25, "b"))},
		"db host":    {InsecureDBTLS("require", false, "a"), InsecureDBTLS("require", false, "b")},
	}
	for name, p := range differ {
		if p[0].Hash == p[1].Hash {
			t.Errorf("%s: same hash", name)
		}
	}
}

func must(c Condition, ok bool) Condition {
	if !ok {
		panic("no risk")
	}
	return c
}

// FuzzConditionEncoding: moving text between fields never yields the same
// hash (the encoding is length-prefixed).
func FuzzConditionEncoding(f *testing.F) {
	f.Add("a", "b", "ab", "")
	f.Add("x\x00", "y", "x", "\x00y")
	f.Fuzz(func(t *testing.T, a, b, c, d string) {
		if a == c && b == d {
			return
		}
		h1 := build(KindSMTPUnencrypted, uuid.Nil, uuid.Nil, "", map[string]string{"k": a, "l": b})
		h2 := build(KindSMTPUnencrypted, uuid.Nil, uuid.Nil, "", map[string]string{"k": c, "l": d})
		if h1.Hash == h2.Hash {
			t.Fatalf("collision: %q %q vs %q %q", a, b, c, d)
		}
	})
}

func TestClip(t *testing.T) {
	long := strings.Repeat("é", maxDisplay+10)
	if got := clip(long); len([]rune(got)) != maxDisplay || !strings.HasSuffix(got, "…") {
		t.Fatalf("clip %d", len([]rune(got)))
	}
	if got := clip("a\nb\x7f"); got != "a b " {
		t.Fatalf("controls %q", got)
	}
}
