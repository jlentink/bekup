package wizard

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// Answer is one scripted answer: Title must be part of the screen's title
// ("" matches any), Value is the choice, the text, the secret or "yes" /
// "no"; Back goes back instead.
type Answer struct {
	Title string
	Value string
	Back  bool
}

// Script is a UI that replays answers (tests and demos). It records every
// screen it showed in Screens ("kind: title") and their texts in Text; a
// value that the screen's validation refuses is an error.
type Script struct {
	Answers []Answer
	Screens []string
	Text    strings.Builder
	Output  bytes.Buffer
	next    int
}

func (s *Script) take(kind, title, body string) (Answer, error) {
	s.Screens = append(s.Screens, kind+": "+title)
	fmt.Fprintf(&s.Text, "== %s ==\n%s\n", title, body)
	if s.next >= len(s.Answers) {
		return Answer{}, fmt.Errorf("%w (script ended at %s %q)", ErrAborted, kind, title)
	}
	a := s.Answers[s.next]
	s.next++
	if a.Title != "" && !strings.Contains(title, a.Title) {
		return Answer{}, fmt.Errorf("script answer %d expects a screen %q, got %s %q", s.next, a.Title, kind, title)
	}
	if a.Back {
		return a, ErrBack
	}
	return a, nil
}

// Done reports whether every answer was used.
func (s *Script) Done() bool { return s.next == len(s.Answers) }

// Note implements UI.
func (s *Script) Note(title, body string) error {
	_, err := s.take("note", title, body)
	return err
}

// Sensitive implements UI (the script records the secret in Text: tests
// only).
func (s *Script) Sensitive(title string, secret []byte, body string) error {
	_, err := s.take("sensitive", title, string(secret)+"\n"+body)
	return err
}

// Select implements UI.
func (s *Script) Select(title, body string, opts []Option, def string) (string, error) {
	labels := make([]string, 0, len(opts))
	for _, o := range opts {
		labels = append(labels, o.Value+": "+o.Label)
	}
	a, err := s.take("select", title, body+"\n"+strings.Join(labels, "\n"))
	if err != nil {
		return "", err
	}
	if a.Value == "" {
		return def, nil
	}
	for _, o := range opts {
		if o.Value == a.Value {
			return a.Value, nil
		}
	}
	return "", fmt.Errorf("script: %q is not a choice of %q", a.Value, title)
}

// Input implements UI.
func (s *Script) Input(title, body, def string, validate func(string) error) (string, error) {
	a, err := s.take("input", title, body)
	if err != nil {
		return "", err
	}
	v := a.Value
	if v == "" {
		v = def
	}
	if validate != nil {
		if err := validate(v); err != nil {
			return "", fmt.Errorf("script: %q refused by %q: %w", v, title, err)
		}
	}
	return v, nil
}

// Secret implements UI.
func (s *Script) Secret(title, body string, validate func([]byte) error) ([]byte, error) {
	a, err := s.take("secret", title, body)
	if err != nil {
		return nil, err
	}
	b := []byte(a.Value)
	if validate != nil {
		if err := validate(b); err != nil {
			return nil, fmt.Errorf("script: secret refused by %q: %w", title, err)
		}
	}
	return b, nil
}

// Confirm implements UI.
func (s *Script) Confirm(title, body string, def bool) (bool, error) {
	a, err := s.take("confirm", title, body)
	if err != nil {
		return false, err
	}
	switch a.Value {
	case "":
		return def, nil
	case "yes":
		return true, nil
	case "no":
		return false, nil
	}
	return false, fmt.Errorf("script: confirm %q needs yes or no, got %q", title, a.Value)
}

// Out implements UI.
func (s *Script) Out() io.Writer { return &s.Output }
