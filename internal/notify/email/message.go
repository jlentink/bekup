package email

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
	"unicode/utf8"
)

// Limits of a message.
const (
	// MaxRecipients bounds the recipients of one message.
	MaxRecipients = 50
	// MaxSubjectLen bounds the subject (characters).
	MaxSubjectLen = 200
	// MaxBodyBytes bounds each body part.
	MaxBodyBytes = 256 << 10
)

// ErrInvalidMessage is returned for a message that cannot be sent as is
// (the wrapped text says why; it never contains a body).
var ErrInvalidMessage = errors.New("email: invalid message")

// Message is one e-mail. Text is required; HTML is optional (a
// multipart/alternative message is sent when it is set). Build the bodies
// with Render; never put untrusted HTML into HTML yourself.
type Message struct {
	// To are bare addresses (no display names).
	To      []string
	Subject string
	Text    string
	HTML    string
}

// NewMessage returns a message with the rendered content.
func NewMessage(to []string, c Content) *Message {
	return &Message{To: to, Subject: c.Subject, Text: c.Text, HTML: c.HTML}
}

// Validate checks every header value and the sizes (header injection
// protection: CR, LF and other control characters are refused, never
// stripped).
func (m *Message) Validate() error {
	if len(m.To) == 0 {
		return fmt.Errorf("%w: no recipient", ErrInvalidMessage)
	}
	if len(m.To) > MaxRecipients {
		return fmt.Errorf("%w: more than %d recipients", ErrInvalidMessage, MaxRecipients)
	}
	for _, a := range m.To {
		if err := CheckAddress(a); err != nil {
			return fmt.Errorf("%w: recipient: %w", ErrInvalidMessage, err)
		}
	}
	if strings.TrimSpace(m.Subject) == "" {
		return fmt.Errorf("%w: empty subject", ErrInvalidMessage)
	}
	if hasControl(m.Subject) || utf8.RuneCountInString(m.Subject) > MaxSubjectLen {
		return fmt.Errorf("%w: the subject must be at most %d characters without control characters", ErrInvalidMessage, MaxSubjectLen)
	}
	if m.Text == "" {
		return fmt.Errorf("%w: empty text body", ErrInvalidMessage)
	}
	for _, b := range []string{m.Text, m.HTML} {
		if len(b) > MaxBodyBytes || !utf8.ValidString(b) || strings.ContainsRune(b, 0) {
			return fmt.Errorf("%w: a body is too large or not valid UTF-8", ErrInvalidMessage)
		}
	}
	return nil
}

// sender is the From of a message.
type sender struct {
	Address string
	Name    string
}

// build returns the RFC 5322 message (CRLF line endings, no dot
// stuffing: net/smtp's DATA writer does that).
func (m *Message) build(from sender, now time.Time) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if err := CheckAddress(from.Address); err != nil {
		return nil, fmt.Errorf("%w: sender: %w", ErrInvalidMessage, err)
	}
	if hasControl(from.Name) || len(from.Name) > maxFromNameLen {
		return nil, fmt.Errorf("%w: invalid sender name", ErrInvalidMessage)
	}
	id, err := messageID(from.Address)
	if err != nil {
		return nil, err
	}
	fromHdr := (&mail.Address{Name: from.Name, Address: from.Address}).String()
	to := make([]string, len(m.To))
	for i, a := range m.To {
		to[i] = (&mail.Address{Address: a}).String()
	}
	hdr := []struct{ k, v string }{
		{"From", fromHdr},
		{"To", strings.Join(to, ", ")},
		{"Subject", mime.QEncoding.Encode("utf-8", m.Subject)},
		{"Date", now.UTC().Format(time.RFC1123Z)},
		{"Message-ID", id},
		{"MIME-Version", "1.0"},
		// Automated mail: no auto-replies, no vacation responders.
		{"Auto-Submitted", "auto-generated"},
		{"X-Auto-Response-Suppress", "All"},
	}
	var buf bytes.Buffer
	for _, h := range hdr {
		if err := writeHeader(&buf, h.k, h.v); err != nil {
			return nil, err
		}
	}
	if m.HTML == "" {
		if err := writeHeader(&buf, "Content-Type", "text/plain; charset=utf-8"); err != nil {
			return nil, err
		}
		if err := writeHeader(&buf, "Content-Transfer-Encoding", "quoted-printable"); err != nil {
			return nil, err
		}
		buf.WriteString("\r\n")
		if err := writeQP(&buf, m.Text); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := writeHeader(&buf, "Content-Type", mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": mw.Boundary()})); err != nil {
		return nil, err
	}
	buf.WriteString("\r\n")
	for _, part := range []struct{ ctype, content string }{
		{"text/plain; charset=utf-8", m.Text},
		{"text/html; charset=utf-8", m.HTML},
	} {
		w, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.ctype},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, fmt.Errorf("email: build part: %w", err)
		}
		var pb bytes.Buffer
		if err := writeQP(&pb, part.content); err != nil {
			return nil, err
		}
		if _, err := w.Write(pb.Bytes()); err != nil {
			return nil, fmt.Errorf("email: build part: %w", err)
		}
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("email: build message: %w", err)
	}
	buf.Write(body.Bytes())
	return buf.Bytes(), nil
}

// writeHeader writes one header line after a final injection check of the
// encoded value.
func writeHeader(buf *bytes.Buffer, k, v string) error {
	if strings.ContainsAny(k+v, "\r\n") || hasControl(v) || len(k)+len(v) > 990 {
		return fmt.Errorf("%w: header %s cannot be encoded safely", ErrInvalidMessage, k)
	}
	buf.WriteString(k)
	buf.WriteString(": ")
	buf.WriteString(v)
	buf.WriteString("\r\n")
	return nil
}

// writeQP writes s quoted-printable with CRLF line breaks.
func writeQP(buf *bytes.Buffer, s string) error {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	w := quotedprintable.NewWriter(buf)
	if _, err := w.Write([]byte(s)); err != nil {
		return fmt.Errorf("email: encode body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: encode body: %w", err)
	}
	return nil
}

// messageID returns a random Message-ID in the sender's domain.
func messageID(from string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("email: message id: %w", err)
	}
	_, domain, _ := strings.Cut(from, "@")
	return "<" + hex.EncodeToString(b[:]) + "@" + domain + ">", nil
}
