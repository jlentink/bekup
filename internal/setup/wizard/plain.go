package wizard

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Plain is the plain-text interface (--plain, e.g. for screen readers):
// numbered choices, one line per answer, no escape sequences.
// "<" (or "back") goes back, end of input quits.
type Plain struct {
	in  *bufio.Reader
	out io.Writer
	// readPassword reads one line without echo (the prompt is printed).
	readPassword func() ([]byte, error)
}

// NewPlain returns the plain interface on in/out; readPassword reads a
// secret without echo (nil: secrets are read as plain lines, tests only).
func NewPlain(in io.Reader, out io.Writer, readPassword func() ([]byte, error)) *Plain {
	return &Plain{in: bufio.NewReader(in), out: out, readPassword: readPassword}
}

// maxLine bounds one answer.
const maxLine = 4096

func (p *Plain) line() (string, error) {
	s, err := p.in.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || s == "") {
		if errors.Is(err, io.EOF) {
			return "", ErrAborted
		}
		return "", err
	}
	if len(s) > maxLine {
		return "", errors.New("answer too long")
	}
	return strings.TrimSpace(s), nil
}

func (p *Plain) header(title, body string) {
	fmt.Fprintf(p.out, "\n== %s ==\n", title)
	if body != "" {
		fmt.Fprintln(p.out, body)
	}
}

func isBack(s string) bool { return s == "<" || strings.EqualFold(s, "back") }

// Note implements UI.
func (p *Plain) Note(title, body string) error {
	p.header(title, body)
	fmt.Fprint(p.out, "[Enter: continue, <: back] ")
	s, err := p.line()
	if err != nil {
		return err
	}
	if isBack(s) {
		return ErrBack
	}
	return nil
}

// Sensitive implements UI: the secret is printed once (as bytes); the
// plain interface sends no escape sequences, so it stays in the scrollback
// and the operator is told to clear it. The plain interface runs only with
// --plain; the wizard says so before it offers a generated passphrase.
func (p *Plain) Sensitive(title string, secret []byte, body string) error {
	p.header(title, "")
	if _, err := io.WriteString(p.out, "\n    "); err != nil {
		return err
	}
	if _, err := p.out.Write(secret); err != nil {
		return err
	}
	fmt.Fprintf(p.out, "\n\n%s\n", body)
	fmt.Fprint(p.out, "[Enter once it is stored; then clear the terminal (and its scrollback)] ")
	_, err := p.line()
	return err
}

// HidesSensitive reports false: the plain interface cannot keep a secret
// out of the terminal's scrollback.
func (p *Plain) HidesSensitive() bool { return false }

// Select implements UI.
func (p *Plain) Select(title, body string, opts []Option, def string) (string, error) {
	p.header(title, body)
	defIdx := 0
	for i, o := range opts {
		mark := ""
		if o.Value == def {
			mark, defIdx = " (default)", i+1
		}
		fmt.Fprintf(p.out, "  %d) %s%s\n", i+1, o.Label, mark)
	}
	for range 5 {
		fmt.Fprintf(p.out, "Choice [1-%d, <: back]: ", len(opts))
		s, err := p.line()
		if err != nil {
			return "", err
		}
		switch {
		case isBack(s):
			return "", ErrBack
		case s == "" && defIdx > 0:
			return opts[defIdx-1].Value, nil
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(opts) {
			return opts[n-1].Value, nil
		}
		for _, o := range opts {
			if s == o.Value {
				return o.Value, nil
			}
		}
		fmt.Fprintln(p.out, "  error: type the number of a choice")
	}
	return "", errors.New("too many invalid answers")
}

// Input implements UI.
func (p *Plain) Input(title, body, def string, validate func(string) error) (string, error) {
	p.header(title, body)
	for range 5 {
		if def != "" {
			fmt.Fprintf(p.out, "[%s] (<: back): ", def)
		} else {
			fmt.Fprint(p.out, "(<: back): ")
		}
		s, err := p.line()
		if err != nil {
			return "", err
		}
		if isBack(s) {
			return "", ErrBack
		}
		if s == "" {
			s = def
		}
		if validate != nil {
			if err := validate(s); err != nil {
				fmt.Fprintf(p.out, "  error: %v\n", err)
				continue
			}
		}
		return s, nil
	}
	return "", errors.New("too many invalid answers")
}

// Secret implements UI.
func (p *Plain) Secret(title, body string, validate func([]byte) error) ([]byte, error) {
	p.header(title, body)
	for range 5 {
		fmt.Fprint(p.out, "(no echo; empty: back): ")
		var b []byte
		if p.readPassword != nil {
			var err error
			if b, err = p.readPassword(); err != nil {
				return nil, err
			}
		} else {
			s, err := p.line()
			if err != nil {
				return nil, err
			}
			b = []byte(s)
		}
		if len(b) == 0 {
			return nil, ErrBack
		}
		if validate != nil {
			if err := validate(b); err != nil {
				clear(b)
				fmt.Fprintf(p.out, "  error: %v\n", err)
				continue
			}
		}
		return b, nil
	}
	return nil, errors.New("too many invalid answers")
}

// Confirm implements UI.
func (p *Plain) Confirm(title, body string, def bool) (bool, error) {
	p.header(title, body)
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	for range 5 {
		fmt.Fprintf(p.out, "[%s, <: back]: ", hint)
		s, err := p.line()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(s) {
		case "<", "back":
			return false, ErrBack
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(p.out, "  error: answer yes or no")
	}
	return false, errors.New("too many invalid answers")
}

// Out implements UI.
func (p *Plain) Out() io.Writer { return p.out }
