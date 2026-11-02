package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/setup/wizard"
)

// Step kinds: one per wizard.UI screen.
const (
	KindNote      = "note"
	KindSensitive = "sensitive"
	KindSelect    = "select"
	KindInput     = "input"
	KindSecret    = "secret"
	KindConfirm   = "confirm"
)

// maxOut bounds the report text the wizard and setup write (Out).
const maxOut = 1 << 20

// Step is the screen the browser shows. It never carries a secret: the
// passphrase of a sensitive step is fetched once through Reveal.
type Step struct {
	ID      uint64   `json:"id"`
	Kind    string   `json:"kind"`
	Title   string   `json:"title"`
	Body    string   `json:"body"`
	Options []Option `json:"options,omitempty"`
	// Default is the preselected value (select, input) or "yes"/"no"
	// (confirm).
	Default string `json:"default,omitempty"`
	// Error is why the previous answer to this screen was refused.
	Error string `json:"error,omitempty"`
}

// Option is one choice of a select step.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Answer is the browser's answer to the current step.
type Answer struct {
	Step  uint64 `json:"step"`
	Back  bool   `json:"back,omitempty"`
	Value string `json:"value,omitempty"`
	// secret is set only by AnswerSecret (never decoded from JSON).
	secret []byte
}

// Errors of the answer API.
var (
	// ErrStale: the answer names a step that is not the current one
	// (answered twice, or from an old tab).
	ErrStale = errors.New("this step was answered already: reload")
	// ErrNoStep: nothing waits for an answer.
	ErrNoStep = errors.New("no question is waiting")
	// ErrWrongKind: the answer does not fit the step (a secret for a text
	// field or the other way round).
	ErrWrongKind = errors.New("the answer does not fit this step")
	// ErrRevealed: the secret of a sensitive step was fetched already.
	ErrRevealed = errors.New("the passphrase was shown already")
)

// pending is the screen waiting for its answer.
type pending struct {
	step    Step
	answers chan Answer
	// sensitive is the caller's secret of a sensitive step (not a copy;
	// valid only while the step waits); revealed marks it fetched.
	sensitive []byte
	revealed  bool
}

// UI is a wizard.UI driven by HTTP: every screen becomes the current
// Step until the browser answers it. One screen waits at a time (the
// wizard is sequential). Canceling ctx aborts the waiting screen.
type UI struct {
	ctx    context.Context
	mu     sync.Mutex
	cur    *pending
	nextID uint64
	// changed is closed and replaced whenever the current step changes
	// (long polling).
	changed chan struct{}
	out     boundedBuffer
}

// NewUI returns a UI whose screens are aborted when ctx ends.
func NewUI(ctx context.Context) *UI {
	return &UI{ctx: ctx, changed: make(chan struct{})}
}

var _ wizard.UI = (*UI)(nil)

// Current returns the waiting step (ok false when none waits) and a
// channel closed at the next change.
func (u *UI) Current() (Step, bool, <-chan struct{}) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cur == nil {
		return Step{}, false, u.changed
	}
	return u.cur.step, true, u.changed
}

// Report returns the text written to Out so far.
func (u *UI) Report() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.out.String()
}

// Answer delivers a non-secret answer to the current step.
func (u *UI) Answer(a Answer) error {
	a.secret = nil
	return u.deliver(a, false)
}

// AnswerSecret delivers secret as the answer to the current secret step;
// the UI owns secret from now on (it is zeroed on every path).
func (u *UI) AnswerSecret(step uint64, secret []byte) error {
	return u.deliver(Answer{Step: step, secret: secret}, true)
}

func (u *UI) deliver(a Answer, secret bool) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	fail := func(err error) error {
		crypto.Zero(a.secret)
		return err
	}
	switch {
	case u.cur == nil:
		return fail(ErrNoStep)
	case u.cur.step.ID != a.Step:
		return fail(ErrStale)
	case !a.Back && secret != (u.cur.step.Kind == KindSecret):
		return fail(ErrWrongKind)
	}
	select {
	case u.cur.answers <- a:
	default:
		return fail(ErrStale) // answered already, not yet taken
	}
	return nil
}

// Reveal writes the secret of the current sensitive step to w, once.
func (u *UI) Reveal(step uint64, w io.Writer) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	switch {
	case u.cur == nil:
		return ErrNoStep
	case u.cur.step.ID != step:
		return ErrStale
	case u.cur.step.Kind != KindSensitive:
		return ErrWrongKind
	case u.cur.revealed:
		return ErrRevealed
	}
	u.cur.revealed = true
	// Written as bytes: never a Go string or an fmt buffer.
	_, err := w.Write(u.cur.sensitive)
	return err
}

// ask publishes step and waits for its answer. An answer refused by check
// (a non-empty message) is published again with the message.
func (u *UI) ask(step Step, sensitive []byte, check func(Answer) string) (Answer, error) {
	p := &pending{step: step, answers: make(chan Answer, 1), sensitive: sensitive}
	u.publish(p)
	defer u.publish(nil)
	for {
		select {
		case <-u.ctx.Done():
			return Answer{}, wizard.ErrAborted
		case a := <-p.answers:
			if a.Back {
				crypto.Zero(a.secret)
				return a, wizard.ErrBack
			}
			if check == nil {
				return a, nil
			}
			msg := check(a)
			if msg == "" {
				return a, nil
			}
			crypto.Zero(a.secret)
			next := p.step
			next.Error = msg
			p = &pending{step: next, answers: make(chan Answer, 1), sensitive: sensitive}
			u.publish(p)
		}
	}
}

// wake ends the waits on Current's channel without changing the step (the
// flow's phase changed).
func (u *UI) wake() {
	u.mu.Lock()
	defer u.mu.Unlock()
	close(u.changed)
	u.changed = make(chan struct{})
}

// publish makes p the current screen (nil: none) under a new step id.
func (u *UI) publish(p *pending) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if p != nil {
		u.nextID++
		p.step.ID = u.nextID
	}
	u.cur = p
	close(u.changed)
	u.changed = make(chan struct{})
}

// Note implements wizard.UI.
func (u *UI) Note(title, body string) error {
	_, err := u.ask(Step{Kind: KindNote, Title: title, Body: body}, nil, nil)
	return err
}

// Sensitive implements wizard.UI: the browser fetches secret once through
// Reveal, then acknowledges the step.
func (u *UI) Sensitive(title string, secret []byte, body string) error {
	_, err := u.ask(Step{Kind: KindSensitive, Title: title, Body: body}, secret, nil)
	return err
}

// Select implements wizard.UI.
func (u *UI) Select(title, body string, opts []wizard.Option, def string) (string, error) {
	shown := make([]Option, 0, len(opts))
	for _, o := range opts {
		shown = append(shown, Option{Value: o.Value, Label: o.Label})
	}
	a, err := u.ask(Step{Kind: KindSelect, Title: title, Body: body, Options: shown, Default: def}, nil, func(a Answer) string {
		for _, o := range opts {
			if o.Value == a.Value {
				return ""
			}
		}
		return "choose one of the options"
	})
	return a.Value, err
}

// Input implements wizard.UI.
func (u *UI) Input(title, body, def string, validate func(string) error) (string, error) {
	a, err := u.ask(Step{Kind: KindInput, Title: title, Body: body, Default: def}, nil, func(a Answer) string {
		if validate == nil {
			return ""
		}
		if err := validate(a.Value); err != nil {
			return err.Error()
		}
		return ""
	})
	return a.Value, err
}

// Secret implements wizard.UI: an empty secret goes back (as in the
// terminal interfaces). While it waits for the caller, the value is
// registered with the log redaction.
func (u *UI) Secret(title, body string, validate func([]byte) error) ([]byte, error) {
	a, err := u.ask(Step{Kind: KindSecret, Title: title, Body: body}, nil, func(a Answer) string {
		if len(a.secret) == 0 || validate == nil {
			return ""
		}
		unregister := logging.DefaultRegistry.Register(a.secret)
		defer unregister()
		if err := validate(a.secret); err != nil {
			return err.Error()
		}
		return ""
	})
	if err != nil {
		return nil, err
	}
	if len(a.secret) == 0 {
		return nil, wizard.ErrBack
	}
	return a.secret, nil
}

// Confirm implements wizard.UI.
func (u *UI) Confirm(title, body string, def bool) (bool, error) {
	d := "no"
	if def {
		d = "yes"
	}
	a, err := u.ask(Step{Kind: KindConfirm, Title: title, Body: body, Default: d}, nil, func(a Answer) string {
		if a.Value != "yes" && a.Value != "no" {
			return "answer yes or no"
		}
		return ""
	})
	return a.Value == "yes", err
}

// Out implements wizard.UI: the report text, bounded.
func (u *UI) Out() io.Writer { return outWriter{u} }

type outWriter struct{ u *UI }

func (w outWriter) Write(p []byte) (int, error) {
	w.u.mu.Lock()
	defer w.u.mu.Unlock()
	w.u.out.Write(p)
	return len(p), nil
}

// boundedBuffer keeps the first maxOut bytes and notes the truncation.
type boundedBuffer struct {
	buf       bytes.Buffer
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) {
	if room := maxOut - b.buf.Len(); len(p) > room {
		p, b.truncated = p[:max(room, 0)], true
	}
	b.buf.Write(p)
}

func (b *boundedBuffer) String() string {
	if b.truncated {
		return b.buf.String() + "\n[output truncated]\n"
	}
	return b.buf.String()
}
