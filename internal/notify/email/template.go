package email

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"net/url"
	"strings"
	texttemplate "text/template"
	"unicode/utf8"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var (
	htmlNotice = htmltemplate.Must(htmltemplate.New("notice.html.tmpl").Option("missingkey=error").ParseFS(templateFS, "templates/notice.html.tmpl"))
	textNotice = texttemplate.Must(texttemplate.New("notice.txt.tmpl").Option("missingkey=error").ParseFS(templateFS, "templates/notice.txt.tmpl"))
)

// Limits of a notice.
const (
	maxParagraphs   = 20
	maxDetails      = 30
	maxNoticeField  = 2000
	maxActionURLLen = 2048
)

// Notice is the content of an e-mail: plain values only. Render escapes
// everything (html/template for the HTML part); the HTML part has no
// remote images, no scripts and no tracking. Values must not contain
// control characters (line breaks included): split text into Paragraphs.
type Notice struct {
	Subject    string
	Heading    string
	Paragraphs []string
	Details    []Detail
	// Action is an optional link (absolute http(s) URL without user info,
	// normally built from server.base_url).
	Action *Action
	Footer string
}

// Detail is one labeled value.
type Detail struct {
	Label string
	Value string
}

// Action is a link button.
type Action struct {
	Label string
	URL   string
}

// Content is a rendered e-mail.
type Content struct {
	Subject string
	Text    string
	HTML    string
}

// DefaultFooter is used when a Notice has no footer.
const DefaultFooter = "This message was sent automatically by bekup. It contains no images, no tracking and no secrets."

// Render validates n and renders its text and HTML parts.
func Render(n Notice) (Content, error) {
	if n.Footer == "" {
		n.Footer = DefaultFooter
	}
	if err := n.validate(); err != nil {
		return Content{}, err
	}
	var h, t bytes.Buffer
	if err := htmlNotice.Execute(&h, n); err != nil {
		return Content{}, fmt.Errorf("email: render html: %w", err)
	}
	if err := textNotice.Execute(&t, n); err != nil {
		return Content{}, fmt.Errorf("email: render text: %w", err)
	}
	return Content{Subject: n.Subject, Text: t.String(), HTML: h.String()}, nil
}

func (n *Notice) validate() error {
	if strings.TrimSpace(n.Subject) == "" || strings.TrimSpace(n.Heading) == "" {
		return fmt.Errorf("%w: subject and heading are required", ErrInvalidMessage)
	}
	if utf8.RuneCountInString(n.Subject) > MaxSubjectLen {
		return fmt.Errorf("%w: subject too long", ErrInvalidMessage)
	}
	if len(n.Paragraphs) > maxParagraphs || len(n.Details) > maxDetails {
		return fmt.Errorf("%w: too many paragraphs or details", ErrInvalidMessage)
	}
	fields := []string{n.Subject, n.Heading, n.Footer}
	fields = append(fields, n.Paragraphs...)
	for _, d := range n.Details {
		fields = append(fields, d.Label, d.Value)
	}
	if n.Action != nil {
		if err := checkActionURL(n.Action.URL); err != nil {
			return err
		}
		if strings.TrimSpace(n.Action.Label) == "" {
			return fmt.Errorf("%w: the action needs a label", ErrInvalidMessage)
		}
		fields = append(fields, n.Action.Label, n.Action.URL)
	}
	for _, f := range fields {
		if hasControl(f) || len(f) > maxNoticeField {
			return fmt.Errorf("%w: a field is too long or contains control characters", ErrInvalidMessage)
		}
	}
	return nil
}

// checkActionURL allows absolute http(s) URLs without user info only.
func checkActionURL(raw string) error {
	if len(raw) > maxActionURLLen {
		return fmt.Errorf("%w: link too long", ErrInvalidMessage)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Opaque != "" {
		return fmt.Errorf("%w: %w", ErrInvalidMessage, errors.New("the link must be an absolute http(s) URL"))
	}
	return nil
}
