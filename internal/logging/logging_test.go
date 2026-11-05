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

// TestRegistryScope: a scope registers into its parent (the parent's
// handlers scrub the value) and Close ends exactly its own registrations.
func TestRegistryScope(t *testing.T) {
	t.Parallel()
	parent := NewRegistry()
	keep := parent.Register([]byte("long-lived-secret"))
	defer keep()
	scope := parent.Scope()
	scope.Register([]byte("scoped-secret-1"))
	u := scope.Register([]byte("scoped-secret-2"))
	u() // an early unregister stays harmless at Close
	if parent.Len() != 2 || scope.Len() != 2 {
		t.Fatalf("len parent %d scope %d", parent.Len(), scope.Len())
	}
	if got := scope.Scrub("a scoped-secret-1 b"); got != "a "+Redacted+" b" {
		t.Fatalf("scope scrub %q", got)
	}
	if got := parent.Scrub("scoped-secret-1"); got != Redacted {
		t.Fatalf("parent scrub %q", got)
	}
	scope.Close()
	if parent.Len() != 1 || parent.Scrub("scoped-secret-1") != "scoped-secret-1" || parent.Scrub("long-lived-secret") != Redacted {
		t.Fatalf("after Close: len %d", parent.Len())
	}
	scope.Close()
	NewRegistry().Close() // not a scope: no-op
}
