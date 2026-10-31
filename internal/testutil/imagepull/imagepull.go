// Package imagepull pulls the images of integration tests with one shared
// retry policy. Registries rate-limit bursts of pulls (Docker Hub answers
// "toomanyrequests" when many test packages start their containers at
// once, typically in CI); those are retried with a growing pause, every
// other error fails at once. Every test image is digest-pinned, so a retry
// or a registry mirror can never substitute other content.
package imagepull

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

// Client is the part of the moby client the helpers need.
type Client interface {
	ImageInspect(ctx context.Context, ref string, opts ...client.ImageInspectOption) (client.ImageInspectResult, error)
	ImagePull(ctx context.Context, ref string, opts client.ImagePullOptions) (client.ImagePullResponse, error)
}

// Policy is the retry schedule for rate-limited pulls.
type Policy struct {
	// Attempts is the total number of pulls (≥ 1).
	Attempts int
	// Base is the pause before the second attempt; it doubles per attempt
	// up to Max. Each pause gets up to 50% random jitter on top, so
	// parallel test packages do not retry in lockstep.
	Base, Max time.Duration
}

// DefaultPolicy waits about 5, 10, 20, 40, 60, 60 s (≈ 3 minutes in all)
// before giving up: Docker Hub's anonymous limit windows are short, and a
// failed pull fails the whole suite.
var DefaultPolicy = Policy{Attempts: 7, Base: 5 * time.Second, Max: time.Minute}

// IsRateLimited reports whether err is a registry rate-limit answer.
func IsRateLimited(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "toomanyrequests") ||
		strings.Contains(s, "too many requests") ||
		strings.Contains(s, "pull rate limit")
}

// delay returns the pause before attempt (1-based: the pause before the
// second pull is delay(1)), without jitter.
func (p Policy) delay(attempt int) time.Duration {
	d := p.Base
	for i := 1; i < attempt && d < p.Max; i++ {
		d *= 2
	}
	return min(d, p.Max)
}

// Retry runs pull until it succeeds, fails with an error that is not a
// rate limit, the attempts are used up or ctx ends. sleep waits for the
// given pause (nil: a timer bound to ctx).
func Retry(ctx context.Context, p Policy, pull func(context.Context) error, sleep func(context.Context, time.Duration) error) error {
	if sleep == nil {
		sleep = sleepCtx
	}
	attempts := max(p.Attempts, 1)
	var err error
	for attempt := range attempts {
		if attempt > 0 {
			d := p.delay(attempt)
			//nolint:gosec // G404: retry jitter for test image pulls, not security relevant.
			d += time.Duration(rand.Int64N(int64(d)/2 + 1))
			if serr := sleep(ctx, d); serr != nil {
				return fmt.Errorf("%w (last pull error: %w)", serr, err)
			}
		}
		if err = pull(ctx); err == nil || !IsRateLimited(err) {
			return err
		}
	}
	return fmt.Errorf("still rate limited after %d attempts: %w", attempts, err)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tm.C:
		return nil
	}
}

// Pull pulls ref with DefaultPolicy and waits until the pull finished.
func Pull(ctx context.Context, c Client, ref string) error {
	return Retry(ctx, DefaultPolicy, func(ctx context.Context) error {
		resp, err := c.ImagePull(ctx, ref, client.ImagePullOptions{})
		if err != nil {
			return err
		}
		werr := resp.Wait(ctx)
		if cerr := resp.Close(); werr == nil {
			werr = cerr
		}
		return werr
	}, nil)
}

// Ensure makes ref present on the daemon (pulled with Pull unless it is
// there already) and fails the test otherwise.
func Ensure(t testing.TB, c Client, ref string) {
	t.Helper()
	if _, err := c.ImageInspect(t.Context(), ref); err == nil {
		return
	}
	if err := Pull(t.Context(), c, ref); err != nil {
		t.Fatalf("pull %s: %v", ref, err)
	}
}
