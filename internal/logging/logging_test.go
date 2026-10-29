package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestNewLevelVar(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	w := writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	})
	lv := new(slog.LevelVar)
	l, err := New(w, Options{Level: "warn", Format: "text", LevelVar: lv, Registry: NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	if lv.Level() != slog.LevelWarn {
		t.Fatalf("LevelVar = %v, want warn", lv.Level())
	}
	l.Info("hidden")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // a concurrent change (config reload) is race-free
		defer wg.Done()
		lv.Set(slog.LevelDebug)
	}()
	wg.Wait()
	l.Debug("shown")
	mu.Lock()
	out := buf.String()
	mu.Unlock()
	if strings.Contains(out, "hidden") || !strings.Contains(out, "shown") {
		t.Fatalf("unexpected output %q", out)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
