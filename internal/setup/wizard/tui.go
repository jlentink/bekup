package wizard

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// TUI is the terminal interface (Bubble Tea v2 + huh v2; v2 queries the
// terminal only while a program runs, never at package initialization,
// so the other bekup commands of the same binary are unaffected): one
// form per screen,
// Esc goes back, Ctrl+C quits. Choices are marked with the cursor, errors
// are spelled out, so nothing depends on color; the theme is huh's base
// theme. Secrets are read outside Bubble Tea without echo (into a []byte
// that is zeroed after use, never a Go string).
type TUI struct {
	in           io.Reader
	out          io.Writer
	readPassword func() ([]byte, error)
	theme        huh.Theme
	keymap       *huh.KeyMap
	// options are extra program options (tests: no renderer, fixed size).
	options []tea.ProgramOption
}

// NewTUI returns the terminal interface on in/out; readPassword reads a
// secret without echo.
func NewTUI(in io.Reader, out io.Writer, readPassword func() ([]byte, error)) *TUI {
	km := huh.NewDefaultKeyMap()
	// Esc means back on every screen: no filter mode that would claim it.
	km.Select.Filter = key.NewBinding(key.WithDisabled())
	km.Select.SetFilter = key.NewBinding(key.WithDisabled())
	km.Select.ClearFilter = key.NewBinding(key.WithDisabled())
	return &TUI{in: in, out: out, readPassword: readPassword, theme: huh.ThemeFunc(huh.ThemeBase), keymap: km}
}

// maxWidth keeps every screen inside an 80-column terminal.
const maxWidth = 78

// screen wraps one huh form: Esc ends it as "back".
type screen struct {
	form  *huh.Form
	back  bool
	done  bool
	sized bool
}

func (s *screen) Init() tea.Cmd { return s.form.Init() }

func (s *screen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		if m.Code == tea.KeyEscape {
			s.back, s.done = true, true
			return s, tea.Quit
		}
	case tea.WindowSizeMsg:
		if !s.sized {
			s.sized = true
			s.form = s.form.WithWidth(min(m.Width, maxWidth))
		}
	}
	f, cmd := s.form.Update(msg)
	if ff, ok := f.(*huh.Form); ok {
		s.form = ff
	}
	if s.form.State != huh.StateNormal {
		s.done = true
		return s, tea.Quit
	}
	return s, cmd
}

func (s *screen) View() tea.View {
	content := ""
	if !s.done {
		content = s.form.View() + "\n  Esc: back   Ctrl+C: quit\n"
	}
	return tea.NewView(content)
}

// run shows one field until it is answered.
func (u *TUI) run(field huh.Field) error {
	form := huh.NewForm(huh.NewGroup(field)).WithTheme(u.theme).WithKeyMap(u.keymap).WithShowHelp(true).WithWidth(maxWidth)
	s := &screen{form: form}
	opts := append([]tea.ProgramOption{tea.WithInput(u.in), tea.WithOutput(u.out)}, u.options...)
	if _, err := tea.NewProgram(s, opts...).Run(); err != nil {
		if errors.Is(err, tea.ErrInterrupted) || errors.Is(err, tea.ErrProgramKilled) {
			return ErrAborted
		}
		return fmt.Errorf("terminal: %w", err)
	}
	switch {
	case s.back:
		return ErrBack
	case s.form.State == huh.StateAborted:
		return ErrAborted
	case s.form.State != huh.StateCompleted:
		return ErrAborted // input ended
	}
	return nil
}

// escapeNote keeps huh's note markup (_ * ` \) out of plain text.
func escapeNote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "_", `\_`, "*", `\*`, "`", "\\`")
	return r.Replace(s)
}

// Note implements UI.
func (u *TUI) Note(title, body string) error {
	return u.run(huh.NewNote().Title(title).Description(escapeNote(body)).Next(true).NextLabel("Continue"))
}

// Escape sequences of the sensitive screen: the alternate screen
// (discarded by the terminal afterwards, so nothing reaches the
// scrollback), erased before it is left.
const (
	altScreenOn  = "\x1b[?1049h\x1b[2J\x1b[H"
	altScreenOff = "\x1b[2J\x1b[H\x1b[?1049l"
)

// Sensitive implements UI: written directly (outside Bubble Tea, so the
// secret is never copied into a string or a renderer buffer) on the
// alternate screen, which the terminal discards afterwards; Enter
// continues.
func (u *TUI) Sensitive(title string, secret []byte, body string) (retErr error) {
	if _, err := io.WriteString(u.out, altScreenOn); err != nil {
		return err
	}
	defer func() {
		if _, err := io.WriteString(u.out, altScreenOff); err != nil && retErr == nil {
			retErr = err
		}
	}()
	if _, err := io.WriteString(u.out, "\n  "+titleStyle.Render(title)+"\n\n    "); err != nil {
		return err
	}
	if _, err := u.out.Write(secret); err != nil {
		return err
	}
	if _, err := io.WriteString(u.out, "\n\n"+indent(body)+"\n\n  Enter: I have stored it "); err != nil {
		return err
	}
	return u.waitEnter()
}

// HidesSensitive reports true: the alternate screen keeps a secret out of
// the scrollback.
func (u *TUI) HidesSensitive() bool { return true }

// waitEnter reads up to the end of a line (the terminal is in its normal
// line mode outside Bubble Tea); end of input aborts.
func (u *TUI) waitEnter() error {
	var b [1]byte
	for empty := 0; ; {
		n, err := u.in.Read(b[:])
		if n == 1 && (b[0] == '\n' || b[0] == '\r') {
			return nil
		}
		if n == 0 && err == nil {
			empty++
			if empty >= 100 {
				return io.ErrNoProgress
			}
			continue
		}
		empty = 0
		if err != nil {
			if errors.Is(err, io.EOF) {
				return ErrAborted
			}
			return err
		}
	}
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

// Select implements UI.
func (u *TUI) Select(title, body string, opts []Option, def string) (string, error) {
	v := def
	hopts := make([]huh.Option[string], 0, len(opts))
	for _, o := range opts {
		hopts = append(hopts, huh.NewOption(o.Label, o.Value))
	}
	sel := huh.NewSelect[string]().Title(title).Description(body).Options(hopts...).Value(&v).Filtering(false)
	if err := u.run(sel); err != nil {
		return "", err
	}
	return v, nil
}

// Input implements UI.
func (u *TUI) Input(title, body, def string, validate func(string) error) (string, error) {
	v := def
	in := huh.NewInput().Title(title).Description(body).Value(&v).CharLimit(maxLine)
	if validate != nil {
		in = in.Validate(validate)
	}
	if err := u.run(in); err != nil {
		return "", err
	}
	return strings.TrimSpace(v), nil
}

var titleStyle = lipgloss.NewStyle().Bold(true)

// Secret implements UI: read without echo outside Bubble Tea.
func (u *TUI) Secret(title, body string, validate func([]byte) error) ([]byte, error) {
	fmt.Fprintf(u.out, "\n%s\n", titleStyle.Render(title))
	if body != "" {
		fmt.Fprintln(u.out, body)
	}
	for range 5 {
		fmt.Fprint(u.out, "> (no echo; empty Enter: back) ")
		b, err := u.readPassword()
		fmt.Fprintln(u.out)
		if err != nil {
			return nil, err
		}
		if len(b) == 0 {
			return nil, ErrBack
		}
		if validate != nil {
			if err := validate(b); err != nil {
				clear(b)
				fmt.Fprintf(u.out, "  error: %v\n", err)
				continue
			}
		}
		return b, nil
	}
	return nil, errors.New("too many invalid answers")
}

// Confirm implements UI.
func (u *TUI) Confirm(title, body string, def bool) (bool, error) {
	v := def
	if err := u.run(huh.NewConfirm().Title(title).Description(body).Affirmative("Yes").Negative("No").Value(&v)); err != nil {
		return false, err
	}
	return v, nil
}

// Out implements UI.
func (u *TUI) Out() io.Writer { return u.out }
