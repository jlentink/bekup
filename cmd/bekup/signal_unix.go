//go:build unix

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// reloadSignals returns a channel that receives a value per SIGHUP (a
// runtime config reload of this process, D-200) and a stop function.
// The channel has a buffer of one, so a burst of signals collapses into
// one pending reload. Catching SIGHUP also means it no longer terminates
// serve or worker (bekup runs as PID 1 in its container).
func reloadSignals() (<-chan struct{}, func()) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP)
	out := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-sigs:
				select {
				case out <- struct{}{}:
				default: // a reload is already pending
				}
			}
		}
	}()
	return out, func() {
		signal.Stop(sigs)
		close(done)
	}
}
