package webhook

import (
	"errors"
	"time"
)

// Default auto-disable thresholds.
const (
	DefaultMaxConsecutiveFailures = 10
	DefaultMinFailureSpan         = 24 * time.Hour
	DefaultMaxPermanentFailures   = 5
)

// DisableReason says why a webhook was disabled; the owner's inbox
// notification shows Message().
type DisableReason string

// Disable reasons.
const (
	DisableNone DisableReason = ""
	// DisableRepeatedFailures: MaxConsecutive failures in a row spanning at
	// least MinSpan.
	DisableRepeatedFailures DisableReason = "repeated_failures"
	// DisablePermanentFailures: MaxPermanent permanent failures (redirect,
	// 4xx, blocked address, TLS) since the last success.
	DisablePermanentFailures DisableReason = "permanent_failures"
)

// Message is a user-facing explanation.
func (r DisableReason) Message() string {
	switch r {
	case DisableRepeatedFailures:
		return "The webhook was disabled because its deliveries kept failing for more than a day. Check the receiver and enable it again."
	case DisablePermanentFailures:
		return "The webhook was disabled because the receiver repeatedly rejected deliveries (a redirect, a 4xx status, a refused address or a TLS error). Check the URL and enable it again."
	default:
		return ""
	}
}

// FailureState is the per-webhook counter set the notification core
// persists. The zero value is a healthy webhook.
type FailureState struct {
	// ConsecutiveFailures counts failed attempts since the last success.
	ConsecutiveFailures int
	// PermanentFailures counts permanent failures since the last success.
	PermanentFailures int
	// FirstFailureAt is the first failure after the last success (zero
	// when healthy); LastFailureAt the latest one.
	FirstFailureAt time.Time
	LastFailureAt  time.Time
	// LastCode is the code of the latest failure.
	LastCode Code
}

// FailurePolicy decides when a failing webhook is auto-disabled. It is
// storage-agnostic: the caller loads FailureState, calls Record with every
// attempt's Result, persists the new state and disables the webhook (and
// notifies its owner in the inbox) when the decision says so.
type FailurePolicy struct {
	// MaxConsecutive failures that also span at least MinSpan disable the
	// webhook (both conditions, so a burst of retries within one outage
	// does not).
	MaxConsecutive int
	MinSpan        time.Duration
	// MaxPermanent permanent failures since the last success disable it
	// regardless of time.
	MaxPermanent int
}

// DefaultFailurePolicy is 10 consecutive failures over at least 24h, or 5
// permanent failures.
func DefaultFailurePolicy() FailurePolicy {
	return FailurePolicy{
		MaxConsecutive: DefaultMaxConsecutiveFailures,
		MinSpan:        DefaultMinFailureSpan,
		MaxPermanent:   DefaultMaxPermanentFailures,
	}
}

// Validate checks the thresholds.
func (p FailurePolicy) Validate() error {
	if p.MaxConsecutive < 1 || p.MaxPermanent < 1 || p.MinSpan < 0 {
		return errors.New("webhook: failure policy thresholds must be positive")
	}
	if p.MaxConsecutive > 1000 || p.MaxPermanent > 1000 || p.MinSpan > 30*24*time.Hour {
		return errors.New("webhook: failure policy thresholds are too large")
	}
	return nil
}

// Decision is the result of Record.
type Decision struct {
	State   FailureState
	Disable bool
	Reason  DisableReason
}

// Record folds one attempt into st. A success resets the state; an
// aborted attempt (OutcomeAborted) leaves it unchanged; a retryable or
// permanent failure increments the counters and may disable the webhook.
func (p FailurePolicy) Record(st FailureState, r Result, now time.Time) Decision {
	switch r.Outcome {
	case OutcomeSuccess:
		return Decision{State: FailureState{}}
	case OutcomeRetryable, OutcomePermanent:
		st.ConsecutiveFailures++
		if r.Outcome == OutcomePermanent {
			st.PermanentFailures++
		}
		if st.FirstFailureAt.IsZero() {
			st.FirstFailureAt = now
		}
		st.LastFailureAt = now
		st.LastCode = r.Code
		return p.Evaluate(st)
	default:
		return p.Evaluate(st)
	}
}

// Evaluate decides on a stored state without recording anything.
func (p FailurePolicy) Evaluate(st FailureState) Decision {
	d := Decision{State: st}
	switch {
	case p.MaxPermanent > 0 && st.PermanentFailures >= p.MaxPermanent:
		d.Disable, d.Reason = true, DisablePermanentFailures
	case p.MaxConsecutive > 0 && st.ConsecutiveFailures >= p.MaxConsecutive &&
		!st.FirstFailureAt.IsZero() && st.LastFailureAt.Sub(st.FirstFailureAt) >= p.MinSpan:
		d.Disable, d.Reason = true, DisableRepeatedFailures
	}
	return d
}
