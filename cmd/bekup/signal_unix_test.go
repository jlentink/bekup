//go:build unix

package main

import (
	"syscall"
	"testing"
	"time"
)

// TestReloadSignals: SIGHUP reaches the reload channel instead of
// terminating the process, and a burst collapses into one pending value.
func TestReloadSignals(t *testing.T) {
	c, stop := reloadSignals()
	defer stop()
	for range 3 {
		if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatal("no reload after SIGHUP")
	}
	// At most one further value can be pending (buffer of one).
	time.Sleep(100 * time.Millisecond)
	n := 0
	for {
		select {
		case <-c:
			n++
			continue
		default:
		}
		break
	}
	if n > 1 {
		t.Fatalf("%d pending reloads after a burst, want at most 1", n)
	}
	// Another signal after draining is delivered again.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatal("no reload after the second SIGHUP")
	}
}
