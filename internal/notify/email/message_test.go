package email

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func validMessage() *Message {
	return &Message{To: []string{"ops@example.com"}, Subject: "Backup failed", Text: "Hello\nWorld", HTML: "<p>Hello</p>"}
}

func TestMessageHeaderInjectionRejected(t *testing.T) {
	cases := map[string]func(m *Message){
		"subject CRLF":       func(m *Message) { m.Subject = "Hi\r\nBcc: victim@example.com" },
		"subject LF":         func(m *Message) { m.Subject = "Hi\nBcc: victim@example.com" },
		"subject CR":         func(m *Message) { m.Subject = "Hi\rX: y" },
		"subject NUL":        func(m *Message) { m.Subject = "Hi\x00" },
		"subject U+2028":     func(m *Message) { m.Subject = "Hi\u2028there" },
		"subject C1":         func(m *Message) { m.Subject = "Hi\u0085there" },
		"subject invalid":    func(m *Message) { m.Subject = "Hi\xff" },
		"recipient LF":       func(m *Message) { m.To = []string{"a@example.com\nBcc: b@example.com"} },
		"recipient name":     func(m *Message) { m.To = []string{"Eve <a@example.com>"} },
		"recipient two":      func(m *Message) { m.To = []string{"a@example.com, b@example.com"} },
		"recipient empty":    func(m *Message) { m.To = nil },
		"recipient literal":  func(m *Message) { m.To = []string{"a@[127.0.0.1]"} },
		"recipient comment":  func(m *Message) { m.To = []string{"a(x)@example.com"} },
		"subject empty":      func(m *Message) { m.Subject = " " },
		"subject too long":   func(m *Message) { m.Subject = strings.Repeat("x", MaxSubjectLen+1) },
		"text empty":         func(m *Message) { m.Text = "" },
		"body NUL":           func(m *Message) { m.HTML = "a\x00b" },
		"too many":           func(m *Message) { m.To = make([]string, MaxRecipients+1) },
		"recipient too long": func(m *Message) { m.To = []string{strings.Repeat("a", 250) + "@example.com"} },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			m := validMessage()
			mut(m)
			if _, err := m.build(sender{Address: "bekup@example.com"}, time.Now()); !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("build: %v, want ErrInvalidMessage", err)
			}
		})
	}
	m := validMessage()
	for _, name := range []string{"Eve\r\nBcc: x@example.com", "Eve\nX: y"} {
		if _, err := m.build(sender{Address: "bekup@example.com", Name: name}, time.Now()); !errors.Is(err, ErrInvalidMessage) {
			t.Fatalf("sender name %q: %v", name, err)
		}
	}
	if _, err := m.build(sender{Address: "bekup@example.com\r\nBcc: x@example.com"}, time.Now()); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("sender address: %v", err)
	}
}

func TestMessageBuildMultipart(t *testing.T) {
	m := &Message{
		To:      []string{"ops@example.com", "dba@example.org"},
		Subject: "Sicherung fehlgeschlagen: café",
		Text:    "Line one\nLine two with a very long text that goes beyond seventy-six characters so it must be soft wrapped\n.leading dot",
		HTML:    "<p>Hello</p>",
	}
	raw, err := m.build(sender{Address: "bekup@example.com", Name: "bekup \"backups\""}, time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("\n")) && bytes.Count(raw, []byte("\n")) != bytes.Count(raw, []byte("\r\n")) {
		t.Fatal("bare LF in the message")
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	dec := new(mime.WordDecoder)
	subj, err := dec.DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subj != m.Subject {
		t.Fatalf("subject %q (%v)", subj, err)
	}
	from, err := msg.Header.AddressList("From")
	if err != nil || len(from) != 1 || from[0].Address != "bekup@example.com" || from[0].Name != "bekup \"backups\"" {
		t.Fatalf("from %+v (%v)", from, err)
	}
	to, err := msg.Header.AddressList("To")
	if err != nil || len(to) != 2 {
		t.Fatalf("to %+v (%v)", to, err)
	}
	for _, h := range []string{"Date", "Message-ID", "MIME-Version"} {
		if msg.Header.Get(h) == "" {
			t.Fatalf("missing %s", h)
		}
	}
	if msg.Header.Get("Auto-Submitted") != "auto-generated" {
		t.Fatal("Auto-Submitted missing")
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/alternative" {
		t.Fatalf("content type %q (%v)", mt, err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var parts []string
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(p) // multipart.Reader decodes quoted-printable
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, p.Header.Get("Content-Type")+"|"+string(b))
	}
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "text/plain; charset=utf-8|Line one\r\nLine two") ||
		!strings.Contains(parts[0], "\r\n.leading dot") || parts[1] != "text/html; charset=utf-8|<p>Hello</p>" {
		t.Fatalf("parts %q", parts)
	}
}

func TestMessageTextOnly(t *testing.T) {
	m := &Message{To: []string{"ops@example.com"}, Subject: "x", Text: "héllo"}
	raw, err := m.build(sender{Address: "bekup@example.com"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatal(msg.Header.Get("Content-Type"))
	}
	b, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil || string(b) != "héllo" {
		t.Fatalf("%q", b)
	}
}

func TestCheckAddress(t *testing.T) {
	for _, ok := range []string{"a@example.com", "first.last+tag@sub.example.org", "x@localhost"} {
		if err := CheckAddress(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a", "@example.com", "a@", "A <a@example.com>", "a@example.com\n", "a b@example.com", "\"a b\"@example.com", "a@[::1]"} {
		if err := CheckAddress(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func FuzzMessageHeaders(f *testing.F) {
	f.Add("subject", "a@example.com", "Name")
	f.Add("a\r\nb", "x@y\nz", "n\rm")
	f.Fuzz(func(t *testing.T, subject, to, name string) {
		m := &Message{To: []string{to}, Subject: subject, Text: "body"}
		raw, err := m.build(sender{Address: "bekup@example.com", Name: name}, time.Now())
		if err != nil {
			return
		}
		head, _, _ := bytes.Cut(raw, []byte("\r\n\r\n"))
		for _, line := range bytes.Split(head, []byte("\r\n")) {
			if bytes.ContainsAny(line, "\r\n") {
				t.Fatalf("raw line break in header: %q", line)
			}
			k, _, ok := bytes.Cut(line, []byte(": "))
			if !ok {
				t.Fatalf("header line without name: %q", line)
			}
			switch string(k) {
			case "From", "To", "Subject", "Date", "Message-ID", "MIME-Version", "Auto-Submitted",
				"X-Auto-Response-Suppress", "Content-Type", "Content-Transfer-Encoding":
			default:
				t.Fatalf("unexpected header %q", k)
			}
		}
	})
}
