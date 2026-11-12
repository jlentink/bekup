package i18n

import (
	"errors"
	"fmt"
	"strings"
)

// Msg is a translatable server text (D-238): an English fmt format and its
// arguments. The format is the key of the central catalog (apiNL); In
// renders it in a language. Arguments that are themselves a Msg are
// translated too; every other argument (names, numbers, type-to-confirm
// phrases) is formatted as is and never translated.
//
// The format must be a constant: a source-walking test (apitexts_test.go)
// requires a Dutch entry for every format passed to the message
// constructors.
type Msg struct {
	Format string
	Args   []any
	// plain marks a constant text without arguments (Text): it is shown
	// as is, never passed through fmt.
	plain bool
}

// Text returns the constant text s as a message without arguments; s is
// not an fmt format (a '%' is shown as is).
func Text(s string) Msg { return Msg{Format: s, plain: true} }

// M returns the message format with args.
func M(format string, args ...any) Msg {
	if vetFormats {
		// Never runs: it makes go vet treat M (and every helper that
		// forwards to it) as a printf wrapper, so formats are checked
		// against their arguments at each call site.
		_ = fmt.Sprintf(format, args...)
	}
	return Msg{Format: format, Args: args}
}

// vetFormats is false; see M.
const vetFormats = false

// IsZero reports whether m holds no text.
func (m Msg) IsZero() bool { return m.Format == "" }

// String renders m in English.
func (m Msg) String() string { return m.In(EN) }

// In renders m in l. A format without an entry in l falls back to English.
func (m Msg) In(l Locale) string {
	if m.Format == "" {
		return ""
	}
	format := m.Format
	if l == NL {
		if t, ok := apiNL[m.Format]; ok {
			format = t
		}
	}
	if m.plain {
		return format
	}
	args := make([]any, len(m.Args))
	for i, a := range m.Args {
		if inner, ok := a.(Msg); ok {
			args[i] = inner.In(l)
		} else {
			args[i] = a
		}
	}
	return fmt.Sprintf(format, args...)
}

// Messager is an error whose text is a translatable Msg. Errors that
// another package shows to users implement it (or come from Errorf).
type Messager interface {
	error
	I18nMsg() Msg
}

// msgError is an error whose text is a Msg.
type msgError struct{ msg Msg }

func (e *msgError) Error() string { return e.msg.String() }

// I18nMsg implements Messager.
func (e *msgError) I18nMsg() Msg { return e.msg }

// Errorf returns an error whose text is the translatable message format
// with args. Use it for errors whose text another package shows to users,
// so the text keeps its format and arguments (MsgOf) instead of being
// passed on as err.Error(). It does not wrap (no %w).
func Errorf(format string, args ...any) error {
	if vetFormats {
		_ = fmt.Sprintf(format, args...) // see M
	}
	return &msgError{msg: M(format, args...)}
}

// ErrorOf returns an error whose text is m (like Errorf, for a message
// built elsewhere).
func ErrorOf(m Msg) error { return &msgError{msg: m} }

// MsgOf returns the message of the first Messager in err's chain.
func MsgOf(err error) (Msg, bool) {
	if e, ok := errors.AsType[Messager](err); ok {
		return e.I18nMsg(), true
	}
	return Msg{}, false
}

// Join returns one message listing msgs separated by sep (each part
// translated on its own). sep must not be a sentence of its own: it
// joins items, like "; ".
func Join(sep string, msgs ...Msg) Msg {
	if len(msgs) == 0 {
		return Msg{}
	}
	if len(msgs) == 1 {
		return msgs[0]
	}
	parts := make([]string, len(msgs))
	args := make([]any, len(msgs))
	for i, m := range msgs {
		parts[i] = "%s"
		args[i] = m
	}
	return Msg{Format: strings.Join(parts, strings.ReplaceAll(sep, "%", "%%")), Args: args}
}

// MsgsOf returns the messages of every Messager in err's tree (errors.Join
// and multi-%w errors included), in order. A Messager's own causes are not
// searched.
func MsgsOf(err error) []Msg {
	if err == nil {
		return nil
	}
	if m, ok := err.(Messager); ok { //nolint:errorlint // walks the tree itself, node by node
		return []Msg{m.I18nMsg()}
	}
	switch u := err.(type) { //nolint:errorlint // walks the tree itself, node by node
	case interface{ Unwrap() []error }:
		var out []Msg
		for _, e := range u.Unwrap() {
			out = append(out, MsgsOf(e)...)
		}
		return out
	case interface{ Unwrap() error }:
		return MsgsOf(u.Unwrap())
	}
	return nil
}
