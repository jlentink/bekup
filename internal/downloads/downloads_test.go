package downloads

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTicketGenerationAndHashing(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		v, h := newTicket()
		if len(v) != ticketLen || !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(v) {
			t.Fatalf("ticket %q", v)
		}
		if seen[v] {
			t.Fatal("duplicate ticket")
		}
		seen[v] = true
		raw, err := base64.RawURLEncoding.DecodeString(v)
		if err != nil || len(raw) != ticketBytes {
			t.Fatalf("decode: %v", err)
		}
		want := sha256.Sum256(raw)
		if !bytes.Equal(h, want[:]) {
			t.Fatal("stored hash is not sha256 of the ticket bytes")
		}
		got, ok := hashTicket(v)
		if !ok || !bytes.Equal(got, h) {
			t.Fatal("hashTicket differs from newTicket")
		}
		if strings.Contains(string(h), v) {
			t.Fatal("hash contains the ticket")
		}
	}
}

func TestHashTicketRejects(t *testing.T) {
	v, _ := newTicket()
	for _, bad := range []string{
		"", "short", v + "A", v[:42], strings.Repeat("=", 43), v[:42] + "=", v[:42] + "+", v[:42] + "/",
		v[:42] + " ", "../../../../etc/passwd/aaaaaaaaaaaaaaaaaaaaaaaaaa", strings.ToUpper(v) + "x",
		strings.Repeat("\x00", 43), strings.Repeat("é", 21) + "a",
	} {
		if _, ok := hashTicket(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	// Non-canonical: 43 chars encode 258 bits; the last char's low 2 bits
	// must be zero ("B" = 000001 is never produced as the last char).
	if _, ok := hashTicket(v[:42] + "B"); ok {
		t.Error("accepted a non-canonical encoding")
	}
}

func TestSanitizeFileName(t *testing.T) {
	re := regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)
	cases := map[string]string{
		"nightly_app_20261027T030000Z.dump": "nightly_app_20261027T030000Z.dump",
		"../../etc/passwd":                  "etc_passwd",
		`a"b;c\d/e`:                         "a_b_c_d_e",
		"x\r\nContent-Type: text/html":      "x_Content-Type_text_html",
		".hidden":                           "hidden",
		"-rf":                               "rf",
		"...":                               fallbackName,
		"":                                  fallbackName,
		"___":                               fallbackName,
		"CON.sql":                           "_CON.sql",
		"nul":                               "_nul",
		"Ünïcödé.sql":                       "n_c_d_.sql",
		"a..b":                              "a.b",
		"name.":                             "name",
		"%2e%2e%2f":                         "2e_2e_2f",
		"tab\there":                         "tab_here",
		"emoji😀.dump":                       "emoji_.dump",
		"\u202egnp.exe":                     "gnp.exe",
	}
	for in, want := range cases {
		got := SanitizeFileName(in)
		if got != want {
			t.Errorf("SanitizeFileName(%q) = %q, want %q", in, got, want)
		}
		if !re.MatchString(got) || len(got) > maxFileName || strings.Contains(got, "..") {
			t.Errorf("unsafe result %q for %q", got, in)
		}
	}
	long := SanitizeFileName(strings.Repeat("a", 5000) + ".dump")
	if len(long) > maxFileName {
		t.Errorf("long name %d bytes", len(long))
	}
}

func FuzzSanitizeFileName(f *testing.F) {
	for _, s := range []string{"a", "../x", "CON", "\x00", "a..b.", "é"} {
		f.Add(s)
	}
	re := regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)
	f.Fuzz(func(t *testing.T, s string) {
		got := SanitizeFileName(s)
		if !re.MatchString(got) || len(got) > maxFileName || strings.Contains(got, "..") || strings.HasSuffix(got, ".") {
			t.Fatalf("unsafe %q from %q", got, s)
		}
		if cd := ContentDisposition(s); strings.ContainsAny(cd, "\r\n\x00") {
			t.Fatalf("header injection %q", cd)
		}
	})
}

func TestContentDisposition(t *testing.T) {
	got := ContentDisposition(`evil"; filename=x.html`)
	want := `attachment; filename="evil_filename_x.html"; filename*=UTF-8''evil_filename_x.html`
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestDownloadName(t *testing.T) {
	ts := time.Date(2026, 10, 27, 3, 0, 5, 0, time.UTC)
	if got := downloadName("nightly-pg", "app", ts, "dump.zst.age"); got != "nightly-pg_app_20261027T030005Z.dump.zst.age" {
		t.Errorf("got %q", got)
	}
	got := downloadName("x", "../we_ird db.name/"+strings.Repeat("z", 200), ts, "sql")
	if strings.Count(got, "_") != 2 || len(got) > maxFileName || !strings.HasSuffix(got, "_20261027T030005Z.sql") {
		t.Errorf("got %q", got)
	}
	if got := downloadName("", "", ts, "sql"); got != "x_x_20261027T030005Z.sql" {
		t.Errorf("got %q", got)
	}
}

func TestStoredFormat(t *testing.T) {
	cases := []struct {
		key       string
		enc       bool
		plain     string
		stored    string
		wantError bool
	}{
		{"job/2026-10-27T030000Z/app.dump.zst.age", true, "dump", "dump.zst.age", false},
		{"job/2026-10-27T030000Z/shop.sql.zst.age", true, "sql", "sql.zst.age", false},
		{"job/2026-10-27T030000Z/globals.globals.sql.zst.age", true, "sql", "sql.zst.age", false},
		{"job/2026-10-27T030000Z/app.dump.zst", false, "dump", "dump.zst", false},
		{"job/t/app.dump.zst", true, "", "", true},
		{"job/t/app.dump.zst.age", false, "", "", true},
		{"job/t/app.tar.zst.age", true, "", "", true},
		{"job/t/manifest.json", false, "", "", true},
	}
	for _, c := range cases {
		p, s, err := storedFormat(c.key, c.enc)
		if (err != nil) != c.wantError || p != c.plain || s != c.stored {
			t.Errorf("storedFormat(%q, %v) = %q %q %v", c.key, c.enc, p, s, err)
		}
	}
}

func TestHoldbackWriter(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), 20_000) // 320 KB
	w := httptest.NewRecorder()
	hw := newHoldbackWriter(w)
	for i := 0; i < len(data); i += 7000 {
		if _, err := hw.Write(data[i:min(i+7000, len(data))]); err != nil {
			t.Fatal(err)
		}
	}
	if !hw.started || w.Body.Len() != len(data)-holdBack {
		t.Fatalf("before finish: started=%v sent=%d", hw.started, w.Body.Len())
	}
	if err := hw.finish(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatal("content differs")
	}
	hw.wipe()

	// Small responses stay entirely held back until finish.
	w2 := httptest.NewRecorder()
	hw2 := newHoldbackWriter(w2)
	if _, err := hw2.Write([]byte("tiny")); err != nil {
		t.Fatal(err)
	}
	if hw2.started || w2.Body.Len() != 0 {
		t.Fatal("small body was sent before verification")
	}
}

// TestHoldbackWriterNeverReallocates: writes larger than the buffer (io.Copy
// uses 32 KiB, a decoder may hand over more) are taken in pieces; the one
// fixed buffer is never replaced, so wipe really zeroes every byte of
// plaintext the writer held, and the output is unchanged.
func TestHoldbackWriterNeverReallocates(t *testing.T) {
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(i*7 + i/251)
	}
	for _, size := range []int{1, 100, 4096, 70_000, 200_000, len(data)} {
		w := httptest.NewRecorder()
		hw := newHoldbackWriterSize(w, 1000, 3000)
		base := &hw.buf[:1][0]
		capBefore := cap(hw.buf)
		for i := 0; i < len(data); i += size {
			n, err := hw.Write(data[i:min(i+size, len(data))])
			if err != nil || n != min(size, len(data)-i) {
				t.Fatalf("write %d: n=%d err=%v", size, n, err)
			}
			if cap(hw.buf) != capBefore || &hw.buf[:1][0] != base || len(hw.buf) > 1000 {
				t.Fatalf("write size %d: the buffer was reallocated or exceeds the hold (len %d cap %d)", size, len(hw.buf), cap(hw.buf))
			}
		}
		if w.Body.Len() != len(data)-1000 {
			t.Fatalf("size %d: sent %d before finish", size, w.Body.Len())
		}
		if err := hw.finish(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(w.Body.Bytes(), data) {
			t.Fatalf("size %d: content differs", size)
		}
		full := hw.buf[:cap(hw.buf)]
		hw.wipe()
		for _, b := range full {
			if b != 0 {
				t.Fatalf("size %d: wipe left plaintext in the buffer", size)
			}
		}
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(3, 2)
	u1, u2, u3 := uuid.New(), uuid.New(), uuid.New()
	r1, ok1 := l.acquire(u1)
	r2, ok2 := l.acquire(u1)
	_, ok3 := l.acquire(u1)
	if !ok1 || !ok2 || ok3 {
		t.Fatal("per-user limit")
	}
	r4, ok4 := l.acquire(u2)
	_, ok5 := l.acquire(u3)
	if !ok4 || ok5 {
		t.Fatal("global limit")
	}
	r1()
	r1() // idempotent
	if _, ok := l.acquire(u3); !ok {
		t.Fatal("slot not released")
	}
	r2()
	r4()
	if len(l.users) != 1 || l.total != 1 {
		t.Fatalf("state %d %v", l.total, l.users)
	}
}

func TestWriteStatusClearsDownloadHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Content-Disposition", "attachment")
	w.Header().Set("Content-Length", "99")
	writeStatus(w, http.StatusGone)
	if w.Code != http.StatusGone || w.Header().Get("Content-Disposition") != "" || w.Header().Get("Cache-Control") != "no-store" ||
		w.Body.String() != "Gone\n" {
		t.Fatalf("%d %v %q", w.Code, w.Header(), w.Body.String())
	}
}

func TestTicketStringRedacted(t *testing.T) {
	tk := Ticket{ID: uuid.New(), URL: URLPrefix + "SECRETVALUE"}
	if strings.Contains(tk.String(), "SECRETVALUE") {
		t.Fatal("String leaks the URL")
	}
}
