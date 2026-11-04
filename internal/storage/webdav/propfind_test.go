package webdav

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/storage"
)

const msHead = `<?xml version="1.0" encoding="utf-8"?><d:multistatus xmlns:d="DAV:">`

func resp(href, props string) string {
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop>` + props +
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

func TestParseMultistatus(t *testing.T) {
	t.Parallel()
	body := msHead +
		resp("/dav/b/", `<d:resourcetype><d:collection/></d:resourcetype>`) +
		resp("https://h:1/dav/b/a%25b", `<d:resourcetype/><d:getcontentlength>12</d:getcontentlength>`+
			`<d:getlastmodified>Tue, 03 Nov 2026 10:00:00 GMT</d:getlastmodified><d:getetag>"e1"</d:getetag>`) +
		resp("/dav/b/sub/", `<d:resourcetype><d:collection/></d:resourcetype>`) +
		resp("/dav/other/x", `<d:getcontentlength>1</d:getcontentlength>`) + // not a member
		resp("/dav/b/../../etc", ``) + // refused href
		resp("/dav/b/deep/er", ``) + // not a direct member
		`<d:response><d:href>/dav/b/gone</d:href><d:status>HTTP/1.1 404 Not Found</d:status></d:response>` +
		`</d:multistatus>`
	entries, err := parseMultistatus(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	kids, self := children(entries, []string{"dav", "b"})
	if self == nil || !self.coll {
		t.Fatalf("self = %+v", self)
	}
	names := make([]string, 0, len(kids))
	for i := range kids {
		names = append(names, kids[i].name())
	}
	if !slices.Equal(names, []string{"a%b", "sub"}) {
		t.Fatalf("children %v", names)
	}
	f := kids[0]
	if f.coll || f.size != 12 || f.etag != `"e1"` || !f.mod.Equal(time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("file entry %+v", f)
	}
	if !kids[1].coll {
		t.Error("sub is not a collection")
	}
}

func TestParseMultistatusHostile(t *testing.T) {
	t.Parallel()
	deep := msHead + `<d:response><d:href>/a</d:href>` + strings.Repeat("<x>", 50) + strings.Repeat("</x>", 50) + `</d:response></d:multistatus>`
	outerDeep := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">` + strings.Repeat("<x>", 40) + strings.Repeat("</x>", 40) + `</d:multistatus>`
	cases := map[string]string{
		"dtd with entity":  `<?xml version="1.0"?><!DOCTYPE d [<!ENTITY a "aaaaaaaaaa">]>` + msHead + resp("/&a;", ``) + `</d:multistatus>`,
		"external entity":  `<?xml version="1.0"?><!DOCTYPE d [<!ENTITY x SYSTEM "file:///etc/passwd">]>` + msHead + resp("/&x;", ``) + `</d:multistatus>`,
		"undefined entity": msHead + resp("/&x;", ``) + `</d:multistatus>`,
		"not multistatus":  `<?xml version="1.0"?><html/>`,
		"other namespace":  `<?xml version="1.0"?><multistatus/>`,
		"truncated":        msHead + `<d:response><d:href>/a`,
		"empty":            ``,
		"latin1":           `<?xml version="1.0" encoding="ISO-8859-1"?>` + `<d:multistatus xmlns:d="DAV:"></d:multistatus>`,
		"outer nesting":    outerDeep,
		"too large":        msHead + strings.Repeat(resp("/a/b", `<d:getetag>`+strings.Repeat("x", 200)+`</d:getetag>`), (maxPropfindBody/250)+10) + `</d:multistatus>`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseMultistatus(strings.NewReader(body)); !errors.Is(err, errBadMultistatus) {
				t.Fatalf("got %v", err)
			}
		})
	}
	// Deep nesting inside one response is bounded by the body limit and
	// encoding/xml; it must not fail the listing as a whole.
	if _, err := parseMultistatus(strings.NewReader(deep)); err != nil {
		t.Fatalf("deep response: %v", err)
	}
}

func TestParseMultistatusEntryLimit(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString(msHead)
	r := `<d:response><d:href>/a</d:href></d:response>`
	for range maxEntries + 1 {
		b.WriteString(r)
	}
	b.WriteString(`</d:multistatus>`)
	if _, err := parseMultistatus(strings.NewReader(b.String())); !errors.Is(err, errBadMultistatus) {
		t.Fatalf("got %v", err)
	}
}

func FuzzParseMultistatus(f *testing.F) {
	f.Add(msHead + resp("/dav/b/x", `<d:getcontentlength>3</d:getcontentlength>`) + `</d:multistatus>`)
	f.Add(`<?xml version="1.0"?><!DOCTYPE a [<!ENTITY b "c">]><a>&b;</a>`)
	f.Add(msHead + `<d:response><d:href>%zz</d:href></d:response></d:multistatus>`)
	f.Fuzz(func(t *testing.T, body string) {
		entries, err := parseMultistatus(strings.NewReader(body))
		if err != nil {
			return
		}
		for i := range entries {
			for _, s := range entries[i].segs {
				if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\") {
					t.Fatalf("bad segment %q", s)
				}
			}
		}
	})
}

func TestPrincipalUser(t *testing.T) {
	t.Parallel()
	for href, want := range map[string]string{
		"/remote.php/dav/principals/users/alice/":             "alice",
		"https://x/remote.php/dav/principals/users/a%40b.de/": "a@b.de",
		"/remote.php/dav/principals/groups/admin/":            "",
		"/remote.php/dav/principals/users/a%3Ab/":             "", // ':' cannot be a Basic user
		"/principals/users/../":                               "",
		"":                                                    "",
	} {
		got, ok := principalUser(href)
		if got != want || ok != (want != "") {
			t.Errorf("principalUser(%q) = %q, %v", href, got, ok)
		}
	}
}

func TestParseURL(t *testing.T) {
	t.Parallel()
	ok := map[string][]string{
		"https://dav.example.com":                         nil,
		"https://dav.example.com/":                        nil,
		"https://dav.example.com:8443/a/My%20Backups/":    {"a", "My Backups"},
		"https://[2001:db8::1]:443/remote.php/webdav":     {"remote.php", "webdav"},
		"https://u123.your-storagebox.de/backups":         {"backups"},
		"https://user.stackstorage.com/remote.php/webdav": {"remote.php", "webdav"},
	}
	for raw, segs := range ok {
		u, err := ParseURL(raw, false)
		if err != nil || !slices.Equal(u.Segments(), segs) {
			t.Errorf("ParseURL(%q) = %v, %v", raw, u.Segments(), err)
		}
	}
	bad := []string{
		"", "http://dav.example.com", "ftp://x", "https://user:pw@x/", "https://x/?a=1", "https://x/#f",
		"https://x/a/../b", "https://x/a//b", "https://x/a%2Fb", "https://x/a b", "https://x/a\\b",
		"https://x:0/", "https://x:99999/", "https://-x-/", "https://x_y/", "https://x/a%00b", "https:x",
		"https://" + strings.Repeat("a", 2100),
	}
	for _, raw := range bad {
		if _, err := ParseURL(raw, false); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("ParseURL(%q) accepted: %v", raw, err)
		} else if raw != "" && strings.Contains(Message(err), raw) {
			t.Errorf("message repeats the input: %q", Message(err))
		}
	}
	if _, err := ParseURL("http://dav.example.com", true); err != nil {
		t.Errorf("dev mode http: %v", err)
	}
}

func FuzzParseURL(f *testing.F) {
	for _, s := range []string{"https://a.example/b/c", "https://[::1]:8443/x%20y", "https://a/../b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		u, err := ParseURL(raw, false)
		if err != nil {
			return
		}
		if u.Scheme() != "https" || u.Host() == "" {
			t.Fatalf("accepted %q as %+v", raw, u)
		}
		for _, s := range u.Segments() {
			if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\") {
				t.Fatalf("bad segment %q of %q", s, raw)
			}
		}
	})
}

func TestConfigValidation(t *testing.T) {
	t.Parallel()
	g := testGuard(t)
	base := func() Config {
		return Config{URL: "https://dav.example.com/b", Username: "u", Password: crypto.NewSecret([]byte("p")), Guard: g}
	}
	cases := map[string]func(*Config){
		"no password":       func(c *Config) { c.Password = nil },
		"user with colon":   func(c *Config) { c.Username = "a:b" },
		"no user":           func(c *Config) { c.Username = "" },
		"bad flavor":        func(c *Config) { c.Flavor = "s3" },
		"nextcloud dav url": func(c *Config) { c.Flavor = FlavorNextcloud; c.URL = "https://c.example/remote.php/webdav" },
		"bad path":          func(c *Config) { c.Path = "../x" },
		"dot path":          func(c *Config) { c.Path = ".hidden" },
		"bad bundle":        func(c *Config) { c.CABundlePEM = []byte("nope") },
		"timeout":           func(c *Config) { c.RequestTimeout = time.Second },
		"chunk":             func(c *Config) { c.ChunkSize = 1024 },
		"no guard":          func(c *Config) { c.Guard = nil },
		"pin without cert":  func(c *Config) { c.Pin = &Pin{} },
		"pin with two blocks": func(c *Config) {
			c.Pin = &Pin{CertificatePEM: []byte("-----BEGIN CERTIFICATE-----\n-----END CERTIFICATE-----\n")}
		},
	}
	for name, mut := range cases {
		c := base()
		mut(&c)
		if _, err := Open(c); !errors.Is(err, storage.ErrInvalidArgument) && !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("%s: %v", name, err)
		}
	}
	d, err := Open(base())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestListChildrenSpelling: a folder listing whose hrefs spell the folder
// differently (letter case, NFD instead of NFC) still lists its members;
// a listing that names neither the folder nor a member is an error, never
// an empty folder (finding 4 of the D-220 review).
func TestListChildrenSpelling(t *testing.T) {
	t.Parallel()
	nfc, nfd := "M\u00fcller", "Mu\u0308ller"
	parse := func(body string) []entry {
		t.Helper()
		entries, err := parseMultistatus(strings.NewReader(msHead + body + `</d:multistatus>`))
		if err != nil {
			t.Fatal(err)
		}
		return entries
	}
	coll := `<d:resourcetype><d:collection/></d:resourcetype>`
	file := `<d:resourcetype/><d:getcontentlength>1</d:getcontentlength>`
	for name, c := range map[string]struct {
		body string
		segs []string
		kids []string
		self bool
		err  bool
	}{
		"exact":        {resp("/dav/B/", coll) + resp("/dav/B/x", file), []string{"dav", "B"}, []string{"x"}, true, false},
		"letter case":  {resp("/DAV/b/", coll) + resp("/DAV/b/x", file) + resp("/DAV/b/Y/", coll), []string{"dav", "B"}, []string{"x", "Y"}, true, false},
		"nfd":          {resp("/dav/"+nfd+"/", coll) + resp("/dav/"+nfd+"/x", file), []string{"dav", nfc}, []string{"x"}, true, false},
		"nfd escaped":  {resp("/dav/Mu%CC%88ller/", coll) + resp("/dav/Mu%CC%88ller/x", file), []string{"dav", nfc}, []string{"x"}, true, false},
		"members only": {resp("/dav/b/x", file), []string{"dav", "B"}, []string{"x"}, false, false},
		"single entry": {resp("/elsewhere/", coll), []string{"dav", "b"}, nil, true, false},
		"empty answer": {"", []string{"dav", "b"}, nil, false, false},
		"mismatch":     {resp("/elsewhere/", coll) + resp("/elsewhere/x", file), []string{"dav", "b"}, nil, false, true},
		"other folder": {resp("/dav/c/", coll) + resp("/dav/c/x", file), []string{"dav", "b"}, nil, false, true},
	} {
		kids, self, err := listChildren(parse(c.body), c.segs)
		if (err != nil) != c.err || (err != nil && !errors.Is(err, errListingMismatch)) {
			t.Errorf("%s: error %v", name, err)
			continue
		}
		names := make([]string, 0, len(kids))
		for i := range kids {
			names = append(names, kids[i].name())
		}
		if !slices.Equal(names, c.kids) || (self != nil) != c.self {
			t.Errorf("%s: children %v, self %v", name, names, self != nil)
		}
	}
}
