package app

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jlentink/bekup/internal/monitor"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Image posture (D-212). The bekup image counts as following a tag when
// setup says so (BEKUP_IMAGE_MODE=following) OR the runner reports that
// its own container was created from a reference without a digest
// (Health image_pinned = false): the runner runs from the same x-bekup-image
// as serve and worker, so a shell or .env override of BEKUP_IMAGE_MODE
// (or of BEKUP_IMAGE) cannot hide the weakening. The runner's verdict is
// asked once at startup (bounded, never blocking startup on an
// unavailable runner) and re-evaluated by every runner Health call of the
// health snapshot and the hourly security check, and by the image watch
// (every minute while unknown, hourly after). Dev mode ignores it.

// Runner verdicts of imageState.runner.
const (
	runnerImageUnknown int32 = iota
	runnerImagePinned
	runnerImageUnpinned
)

const (
	// imageProbeTimeout bounds the startup Health call (the runner client
	// connects lazily; an unavailable runner must not delay startup).
	imageProbeTimeout = 3 * time.Second
	// imageWatchRetry / imageWatchInterval: the image watch's period
	// while the verdict is unknown / once it is known.
	imageWatchRetry    = time.Minute
	imageWatchInterval = time.Hour
)

// imageState is the process's image posture.
type imageState struct {
	// runner is the runner's latest verdict (runnerImage*).
	runner atomic.Int32
	// mu serializes observations and the audit; audited is set once
	// security.unpinned_image was recorded by this process.
	mu      sync.Mutex
	audited bool
}

// imageFollowing reports whether the bekup image follows a tag: set up
// that way, or reported by the runner.
func (a *App) imageFollowing() bool {
	return a.opts.Deployment.ImageFollowing || a.image.runner.Load() == runnerImageUnpinned
}

// imageSource names where the following verdict came from (audit detail).
func (a *App) imageSource() string {
	env, runner := a.opts.Deployment.ImageFollowing, a.image.runner.Load() == runnerImageUnpinned
	switch {
	case env && runner:
		return "env_and_runner"
	case runner:
		return "runner"
	default:
		return "env"
	}
}

// probeRunnerImage asks the runner once for its verdict, bounded by
// imageProbeTimeout. Failures leave the verdict unknown (re-evaluated
// later); never in dev mode (the posture is not enforced there and tests
// run without a runner).
func (a *App) probeRunnerImage(ctx context.Context) {
	if a.opts.Dev || a.runner == nil {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, imageProbeTimeout)
	defer cancel()
	resp, err := a.runner.Health(pctx)
	if err != nil {
		// The error text may carry addresses.
		a.opts.Logger.InfoContext(ctx, "runner image posture unknown at startup (runner unavailable); re-checked by the image watch and the security check")
		return
	}
	a.image.mu.Lock()
	defer a.image.mu.Unlock()
	a.setRunnerImage(ctx, resp)
}

// setRunnerImage records the runner's verdict from resp (unset: no
// change) and reports whether the image newly counts as following. The
// caller holds a.image.mu (tests aside).
// Dev mode ignores the verdict (the posture is not enforced there, and
// development images are never pinned).
func (a *App) setRunnerImage(ctx context.Context, resp *runnerpb.HealthResponse) (newlyFollowing bool) {
	if a.opts.Dev || resp == nil || resp.ImagePinned == nil {
		return false
	}
	v := runnerImagePinned
	if !resp.GetImagePinned() {
		v = runnerImageUnpinned
	}
	was := a.imageFollowing()
	if a.image.runner.Swap(v) == v {
		return false
	}
	if v == runnerImageUnpinned {
		a.opts.Logger.WarnContext(ctx, "the runner reports that the bekup image follows a tag instead of a digest (D-212): whoever moves the tag decides what runs; treated as BEKUP_IMAGE_MODE=following; pin a digest with `bekup setup upgrade --image <repo>:<tag>@sha256:<digest>`",
			slog.Bool("image_mode_env_following", a.opts.Deployment.ImageFollowing))
	}
	return !was && a.imageFollowing()
}

// observeRunnerImage takes the verdict of a runner Health call after
// startup: when the image newly counts as following, it is audited
// (security.unpinned_image; the audit observer raises the alert) unless
// this process already did. An audit failure is logged and retried at
// the next observation (startup is over; the startup audit is the fail-
// closed one).
func (a *App) observeRunnerImage(ctx context.Context, resp *runnerpb.HealthResponse) {
	a.image.mu.Lock()
	defer a.image.mu.Unlock()
	a.setRunnerImage(ctx, resp)
	if a.image.audited || a.opts.Dev || !a.imageFollowing() || a.auditor == nil {
		return
	}
	if err := a.recordUnpinnedImage(ctx); err != nil {
		a.opts.Logger.ErrorContext(ctx, "recording security.unpinned_image failed; retried at the next runner check", slog.Any("error", err))
	}
}

// observedRunner is the runner client as a monitor.RunnerHealth that
// feeds every successful Health answer into the image posture.
type observedRunner struct{ a *App }

func (o observedRunner) Health(ctx context.Context) (*runnerpb.HealthResponse, error) {
	resp, err := o.a.runner.Health(ctx)
	if err == nil {
		o.a.observeRunnerImage(ctx, resp)
	}
	return resp, err
}

var _ monitor.RunnerHealth = observedRunner{}

// imageWatchComponent re-asks the runner for its image verdict: every
// imageWatchRetry while unknown, every imageWatchInterval after. Runner
// errors are only logged at debug level (the health snapshot reports an
// unavailable runner).
type imageWatchComponent struct{ a *App }

func (c imageWatchComponent) Name() string { return "image posture watch" }

func (c imageWatchComponent) Run(ctx context.Context) error {
	for {
		wait := imageWatchInterval
		if c.a.image.runner.Load() == runnerImageUnknown {
			wait = imageWatchRetry
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
		rctx, cancel := context.WithTimeout(ctx, imageProbeTimeout)
		if _, err := observedRunner(c).Health(rctx); err != nil && ctx.Err() == nil {
			c.a.opts.Logger.DebugContext(ctx, "image posture watch: runner health check failed")
		}
		cancel()
	}
}

// startImagePosture asks the runner for its verdict, records a followed
// image (fail closed) and, outside dev mode, adds the image watch.
func (a *App) startImagePosture(ctx context.Context) error {
	a.probeRunnerImage(ctx)
	if err := a.auditUnpinnedImage(ctx); err != nil {
		return err
	}
	if !a.opts.Dev && a.runner != nil {
		a.components = append(a.components, imageWatchComponent{a})
	}
	return nil
}
