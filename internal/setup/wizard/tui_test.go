package wizard

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// keys feeds keystrokes to the program, one chunk per write (a lone ESC
// must arrive alone to be the Esc key).
func keys(t *testing.T, chunks ...string) io.Reader {
	t.Helper()
	r, w := io.Pipe()
	go func() {
		for _, c := range chunks {
			time.Sleep(30 * time.Millisecond)
			if _, err := w.Write([]byte(c)); err != nil {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
		if err := w.Close(); err != nil {
			return
		}
	}()
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}

func testTUI(t *testing.T, in io.Reader) (*TUI, *bytes.Buffer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	var out bytes.Buffer
	u := NewTUI(in, &out, func() ([]byte, error) { return []byte("typed without echo"), nil })
	u.options = []tea.ProgramOption{tea.WithContext(ctx), tea.WithoutSignalHandler(), tea.WithWindowSize(80, 24)}
	return u, &out
}

func TestTUISelectInputConfirm(t *testing.T) {
	u, out := testTUI(t, keys(t, "\x1b[B", "\r"))
	v, err := u.Select("Reverse proxy", "Who terminates HTTPS?", []Option{{"a", "first"}, {"b", "second"}, {"c", "third"}}, "a")
	if err != nil || v != "b" {
		t.Fatalf("select %q %v", v, err)
	}
	if !strings.Contains(out.String(), "Esc: back") || !strings.Contains(out.String(), "second") {
		t.Fatalf("screen:\n%s", out.String())
	}

	u, _ = testTUI(t, keys(t, "x", "y", "\r", "o", "k", ".", "e", "x", "\r"))
	v, err = u.Input("Host", "", "", func(s string) error {
		if !strings.Contains(s, ".") {
			return errors.New("needs a dot")
		}
		return nil
	})
	if err != nil || v != "xyok.ex" {
		t.Fatalf("input %q %v", v, err)
	}

	u, _ = testTUI(t, keys(t, "\x1b[D", "\r"))
	if ok, err := u.Confirm("Sure?", "", false); err != nil || !ok {
		t.Fatalf("confirm %v %v", ok, err)
	}
}

func TestTUIBackAndQuit(t *testing.T) {
	u, _ := testTUI(t, keys(t, "\x1b"))
	if _, err := u.Input("Domain", "", "", nil); !errors.Is(err, ErrBack) {
		t.Fatalf("esc: %v", err)
	}
	u, _ = testTUI(t, keys(t, "\x03"))
	if _, err := u.Select("Pick", "", []Option{{"a", "A"}}, "a"); !errors.Is(err, ErrAborted) {
		t.Fatalf("ctrl+c: %v", err)
	}
	u, _ = testTUI(t, keys(t, "\r"))
	if err := u.Note("Note", "a_b *c* `d`"); err != nil {
		t.Fatal(err)
	}
	// Secrets are read outside Bubble Tea, an empty one goes back.
	u, _ = testTUI(t, strings.NewReader(""))
	b, err := u.Secret("Password", "", nil)
	if err != nil || string(b) != "typed without echo" {
		t.Fatalf("secret %q %v", b, err)
	}
	u.readPassword = func() ([]byte, error) { return nil, nil }
	if _, err := u.Secret("Password", "", nil); !errors.Is(err, ErrBack) {
		t.Fatalf("empty secret: %v", err)
	}
}

func TestEscapeNote(t *testing.T) {
	if got := escapeNote("bekup_app *x* `y` a\\b"); got != "bekup\\_app \\*x\\* \\`y\\` a\\\\b" {
		t.Fatalf("%q", got)
	}
}

func TestTUISensitive(t *testing.T) {
	u, out := testTUI(t, strings.NewReader("\n"))
	secret := []byte("the generated passphrase")
	if err := u.Sensitive("Shown once", secret, "Store it."); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	on, sec, off := strings.Index(text, altScreenOn), strings.Index(text, string(secret)), strings.LastIndex(text, altScreenOff)
	if on < 0 || sec < on || off < sec || !strings.Contains(text, "Store it.") {
		t.Fatalf("the secret must be shown only on the alternate screen:\n%q", text)
	}
	if !u.HidesSensitive() {
		t.Fatal("HidesSensitive")
	}
	u, out = testTUI(t, strings.NewReader(""))
	if err := u.Sensitive("Shown once", secret, ""); !errors.Is(err, ErrAborted) || !strings.HasSuffix(out.String(), altScreenOff) {
		t.Fatalf("end of input: %v %q", err, out.String())
	}
}
