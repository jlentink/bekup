package executor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/runner/spec"
)

// Reap removes leftovers of earlier runs: containers and networks labeled
// bekup.managed=true whose run is not active here. Items of this runner
// are removed after ReapGrace (immediately at startup, when no run can be
// active); items of another runner only once that runner's container no
// longer exists. The runner is disconnected from stale networks before
// they are removed. Errors are collected; reaping continues.
func (e *Executor) Reap(ctx context.Context, startup bool) error {
	if !e.Ready() {
		return ErrNotReady
	}
	return e.reap(ctx, startup)
}

// reap is Reap without the readiness check (Init's startup reap runs
// before the executor admits runs).
func (e *Executor) reap(ctx context.Context, startup bool) error {
	e.mu.Lock()
	selfID := e.self.ID
	active := make(map[string]bool, len(e.runs)+len(e.offline))
	for _, r := range e.runs {
		active[r.runID] = true
	}
	for _, id := range e.offline {
		active[id] = true
	}
	e.mu.Unlock()

	now := e.now()
	gone := map[string]bool{} // other runner -> container no longer exists
	stale := func(labels map[string]string, created time.Time) bool {
		if labels[spec.LabelManaged] != "true" || active[labels[spec.LabelRunID]] {
			return false
		}
		owner := labels[LabelRunner]
		if owner != "" && owner != selfID {
			g, ok := gone[owner]
			if !ok {
				_, err := e.d.ContainerInspect(ctx, owner)
				g = IsNotFound(err)
				gone[owner] = g
			}
			if !g {
				return false
			}
		}
		if startup && owner == selfID {
			return true
		}
		return now.Sub(created) >= ReapGrace
	}

	var errs []error
	containers, err := e.d.ContainerList(ctx, spec.LabelManaged+"=true")
	if err != nil {
		errs = append(errs, fmt.Errorf("list containers: %w", err))
	}
	removed := 0
	for _, c := range containers {
		if !strings.HasPrefix(c.Name, NetworkPrefix) || !stale(c.Labels, c.Created) {
			continue
		}
		if err := e.d.ContainerRemove(ctx, c.ID); err != nil && !IsNotFound(err) {
			errs = append(errs, fmt.Errorf("remove container %s: %w", shortID(c.ID), err))
			continue
		}
		removed++
	}

	nets, listErr := e.d.NetworkList(ctx, spec.LabelManaged+"=true")
	if listErr != nil {
		errs = append(errs, fmt.Errorf("list networks: %w", listErr))
	}
	present := map[string]bool{}
	for i := range nets {
		n := &nets[i]
		present[n.Name] = true
		if !strings.HasPrefix(n.Name, NetworkPrefix) || !stale(n.Labels, n.Created) {
			continue
		}
		info, err := e.d.NetworkInspect(ctx, n.ID)
		if err != nil {
			if !IsNotFound(err) {
				errs = append(errs, fmt.Errorf("inspect network %s: %w", n.Name, err))
			}
			continue
		}
		for _, cid := range info.Containers {
			if err := e.d.NetworkDisconnect(ctx, n.ID, cid); err != nil && !IsNotFound(err) {
				errs = append(errs, fmt.Errorf("disconnect %s from %s: %w", shortID(cid), n.Name, err))
			}
		}
		if err := e.d.NetworkRemove(ctx, n.ID); err != nil && !IsNotFound(err) {
			errs = append(errs, fmt.Errorf("remove network %s: %w", n.Name, err))
			continue
		}
		delete(present, n.Name)
		removed++
	}

	// Subnets whose run cleanup failed stay denied until their network is
	// gone.
	if listErr == nil {
		e.mu.Lock()
		changed := false
		for name := range e.zombie {
			if !present[name] {
				delete(e.zombie, name)
				changed = true
			}
		}
		if changed {
			if aerr := e.applyOwnLocked(); aerr != nil {
				errs = append(errs, aerr)
			}
		}
		e.mu.Unlock()
	}
	if removed > 0 {
		e.log.InfoContext(ctx, "runner reaped leftovers", slog.Int("removed", removed), slog.Bool("startup", startup))
	}
	return errors.Join(errs...)
}
