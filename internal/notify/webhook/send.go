package webhook

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/netguard"
)

// Sender limits.
const (
	// DefaultTimeout bounds one delivery attempt (connect, TLS, request,
	// response headers and the discarded body).
	DefaultTimeout = 15 * time.Second
	// MaxTimeout is the largest accepted SenderOptions.Timeout.
	MaxTimeout = 60 * time.Second
	// MaxPayloadBytes caps a delivery body.
	MaxPayloadBytes = 64 << 10
	// maxDrainBytes is how much of a response body is read (and discarded)
	// so the connection can be reused; the rest is never read.
	maxDrainBytes = 4 << 10
	// MaxRetryAfter caps an honored Retry-After header.
	MaxRetryAfter = time.Hour
)

// Delivery errors (safe to show).
var (
	ErrPayloadInvalid = errors.New("webhook: payload must be a JSON value of at most 64 KiB")
	ErrEventInvalid   = errors.New("webhook: invalid event type or id")
	ErrHTTPStatus     = errors.New("webhook: receiver returned a non-2xx status")
	ErrRedirect       = errors.New("webhook: receiver answered with a redirect, which is never followed")
)

var (
	eventTypeRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)
	eventIDRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	versionRe   = regexp.MustCompile(`^[A-Za-z0-9.+-]{1,64}$`)
)

func checkEventType(s string) error {
	if len(s) > 64 || !eventTypeRe.MatchString(s) {
		return ErrEventInvalid
	}
	return nil
}

func checkEventID(s string) error {
	if len(s) > 128 || !eventIDRe.MatchString(s) {
		return ErrEventInvalid
	}
	return nil
}

// Delivery is one webhook POST.
type Delivery struct {
	// URL is the stored, validated webhook URL (ValidateURL). It is never
	// logged.
	URL string
	// HMACKey signs the request; Send does not retain or modify it.
	HMACKey []byte
	// EventID and EventType identify the event (X-Bekup-Event carries the
	// type). EventType uses the D-161 catalog names.
	EventID   string
	EventType string
	// DeliveryID is X-Bekup-Delivery. Pass the same value on every retry
	// of one (event, channel) delivery so receivers can deduplicate; empty
	// means a random 128-bit hex ID.
	DeliveryID string
	// Payload is the JSON body (BuildPayload).
	Payload []byte
}

// Outcome classifies one attempt.
type Outcome int

// Outcomes.
const (
	// OutcomeSuccess: the receiver answered 2xx.
	OutcomeSuccess Outcome = iota + 1
	// OutcomeRetryable: network error, timeout, 5xx, 408 or 429; retry with
	// backoff (Result.RetryAfter is the receiver's hint, if any).
	OutcomeRetryable
	// OutcomePermanent: a redirect, another 4xx, a blocked address, a TLS
	// error or an invalid delivery; retrying the same request is pointless.
	OutcomePermanent
	// OutcomeAborted: the caller's context ended (shutdown, job timeout).
	// It says nothing about the receiver and must not count as a failure.
	OutcomeAborted
)

func (o Outcome) String() string {
	switch o {
	case OutcomeSuccess:
		return "success"
	case OutcomeRetryable:
		return "retryable"
	case OutcomePermanent:
		return "permanent"
	case OutcomeAborted:
		return "aborted"
	default:
		return "unknown"
	}
}

// Code is a fixed, user-safe reason for a result. It never distinguishes
// refused, timed-out and unresolvable destinations (netguard.Normalize),
// so deliveries cannot be used as a port scanner.
type Code string

// Codes.
const (
	CodeOK          Code = "ok"
	CodeHTTPStatus  Code = "http_status"
	CodeRedirect    Code = "redirect"
	CodeNotAllowed  Code = "not_allowed"
	CodeTLS         Code = "tls"
	CodeUnreachable Code = "unreachable"
	CodeFailed      Code = "failed"
	CodeInvalid     Code = "invalid_delivery"
	CodeCanceled    Code = "canceled"
)

// Result is the outcome of one attempt. Err is nil on success and
// otherwise a safe error (no URL, no address, no response content).
type Result struct {
	Outcome    Outcome
	Code       Code
	StatusCode int
	// RetryAfter is the receiver's Retry-After on 429/503, clamped to
	// MaxRetryAfter; 0 when absent or unparsable.
	RetryAfter time.Duration
	DeliveryID string
	Timestamp  int64
	Duration   time.Duration
	Err        error
}

// SenderOptions configures a Sender.
type SenderOptions struct {
	// Version is sent as "User-Agent: bekup/<Version>".
	Version string
	// AllowHTTP accepts http URLs at send time (dev mode only).
	AllowHTTP bool
	// Timeout bounds one attempt; 0 means DefaultTimeout, at most
	// MaxTimeout.
	Timeout time.Duration
	// TLSConfig is cloned for the transport; nil means system roots.
	// The netguard transport enforces TLS 1.2 or later.
	TLSConfig *tls.Config
	// Now returns the current time (tests); nil means time.Now.
	Now func() time.Time
	// Logger receives debug messages (never the URL, key or payload);
	// nil means slog.Default().
	Logger *slog.Logger
}

// Sender posts deliveries through a netguard HTTP client. It is safe for
// concurrent use and keeps no per-delivery state.
type Sender struct {
	client    *http.Client
	userAgent string
	allowHTTP bool
	now       func() time.Time
	logger    *slog.Logger
}

// NewSender returns a Sender whose every connection is checked by guard.
func NewSender(guard *netguard.Guard, opts SenderOptions) (*Sender, error) {
	if guard == nil {
		return nil, errors.New("webhook: netguard is required")
	}
	if !versionRe.MatchString(opts.Version) {
		return nil, errors.New("webhook: invalid version for the User-Agent")
	}
	timeout := opts.Timeout
	switch {
	case timeout == 0:
		timeout = DefaultTimeout
	case timeout < 0 || timeout > MaxTimeout:
		return nil, fmt.Errorf("webhook: timeout must be between 0 and %s", MaxTimeout)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Sender{
		client:    guard.NewHTTPClient(opts.TLSConfig, timeout),
		userAgent: "bekup/" + opts.Version,
		allowHTTP: opts.AllowHTTP,
		now:       now,
		logger:    logger,
	}, nil
}

// CloseIdleConnections releases pooled connections.
func (s *Sender) CloseIdleConnections() { s.client.CloseIdleConnections() }

// Send performs one signed POST and classifies the result. It never
// retries and never follows redirects.
func (s *Sender) Send(ctx context.Context, d Delivery) Result {
	start := s.now()
	res := Result{DeliveryID: d.DeliveryID, Timestamp: start.Unix()}
	defer func() { res.Duration = s.now().Sub(start) }()

	if err := ctx.Err(); err != nil {
		return aborted(res)
	}
	u, err := parseURL(d.URL, s.allowHTTP)
	if err != nil {
		return permanent(res, CodeInvalid, err)
	}
	if err := checkKey(d.HMACKey); err != nil {
		return permanent(res, CodeInvalid, err)
	}
	if err := checkEventType(d.EventType); err != nil {
		return permanent(res, CodeInvalid, err)
	}
	if err := checkEventID(d.EventID); err != nil {
		return permanent(res, CodeInvalid, err)
	}
	if len(d.Payload) == 0 || len(d.Payload) > MaxPayloadBytes || !json.Valid(d.Payload) {
		return permanent(res, CodeInvalid, ErrPayloadInvalid)
	}
	if res.DeliveryID == "" {
		res.DeliveryID, err = newDeliveryID()
		if err != nil {
			return permanent(res, CodeInvalid, err)
		}
	} else if checkEventID(res.DeliveryID) != nil {
		return permanent(res, CodeInvalid, ErrEventInvalid)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(d.Payload))
	if err != nil {
		return permanent(res, CodeInvalid, ErrInvalidURL)
	}
	h := req.Header
	h.Set("Content-Type", "application/json")
	h.Set("User-Agent", s.userAgent)
	h.Set(HeaderEvent, d.EventType)
	h.Set(HeaderDelivery, res.DeliveryID)
	h.Set(HeaderTimestamp, strconv.FormatInt(res.Timestamp, 10))
	h.Set(HeaderSignature, Sign(d.HMACKey, res.Timestamp, res.DeliveryID, d.EventType, d.Payload))

	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return aborted(res)
		}
		return classifyError(res, err)
	}
	// The response content is ignored: read a little so the connection
	// can be reused, never more.
	res = classifyStatus(res, resp.StatusCode, resp.Header.Get("Retry-After"), s.now())
	// Read at most maxDrainBytes so a small response lets the connection
	// be reused; closing with an unread remainder drops the connection
	// instead of reading it.
	_, drainErr := io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))
	if err := errors.Join(drainErr, resp.Body.Close()); err != nil {
		// The status already decided the outcome; only note the failure
		// (normalized: the raw error may contain the URL).
		s.logger.DebugContext(ctx, "webhook_response_discard_failed",
			slog.String("error", netguard.Normalize(err).Error()))
	}
	return res
}

func newDeliveryID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("webhook: delivery id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func aborted(r Result) Result {
	r.Outcome, r.Code, r.Err = OutcomeAborted, CodeCanceled, context.Canceled
	return r
}

func permanent(r Result, c Code, err error) Result {
	r.Outcome, r.Code, r.Err = OutcomePermanent, c, err
	return r
}

func retryable(r Result, c Code, err error) Result {
	r.Outcome, r.Code, r.Err = OutcomeRetryable, c, err
	return r
}

// classifyError maps a transport error. The raw error (it embeds the URL)
// is dropped; only the netguard category survives.
func classifyError(r Result, err error) Result {
	switch n := netguard.Normalize(err); {
	case errors.Is(n, netguard.ErrNotAllowed):
		return permanent(r, CodeNotAllowed, n)
	case errors.Is(n, netguard.ErrTLS):
		return permanent(r, CodeTLS, n)
	case errors.Is(n, netguard.ErrRedirect):
		return permanent(r, CodeRedirect, ErrRedirect)
	case errors.Is(n, netguard.ErrUnreachable):
		return retryable(r, CodeUnreachable, n)
	case errors.Is(n, context.Canceled):
		return aborted(r)
	default:
		return retryable(r, CodeFailed, netguard.ErrFailed)
	}
}

// classifyStatus maps an HTTP status: 2xx success; 3xx permanent (never
// followed); 408 and 429 retryable; other 4xx permanent; 5xx retryable;
// anything else permanent.
func classifyStatus(r Result, status int, retryAfter string, now time.Time) Result {
	r.StatusCode = status
	switch {
	case status >= 200 && status <= 299:
		r.Outcome, r.Code = OutcomeSuccess, CodeOK
		return r
	case status >= 300 && status <= 399:
		return permanent(r, CodeRedirect, ErrRedirect)
	case status == http.StatusRequestTimeout:
		return retryable(r, CodeHTTPStatus, ErrHTTPStatus)
	case status == http.StatusTooManyRequests:
		r.RetryAfter = parseRetryAfter(retryAfter, now)
		return retryable(r, CodeHTTPStatus, ErrHTTPStatus)
	case status >= 400 && status <= 499:
		return permanent(r, CodeHTTPStatus, ErrHTTPStatus)
	case status >= 500 && status <= 599:
		if status == http.StatusServiceUnavailable {
			r.RetryAfter = parseRetryAfter(retryAfter, now)
		}
		return retryable(r, CodeHTTPStatus, ErrHTTPStatus)
	default:
		return permanent(r, CodeHTTPStatus, ErrHTTPStatus)
	}
}

// parseRetryAfter reads delay-seconds or an HTTP date, clamped to
// [0, MaxRetryAfter]; invalid or past values are 0.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 64 {
		return 0
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		if n <= 0 {
			return 0
		}
		if n >= int64(MaxRetryAfter/time.Second) {
			return MaxRetryAfter
		}
		return time.Duration(n) * time.Second
	}
	t, err := http.ParseTime(v)
	if err != nil {
		return 0
	}
	d := t.Sub(now)
	switch {
	case d <= 0:
		return 0
	case d > MaxRetryAfter:
		return MaxRetryAfter
	default:
		return d.Round(time.Second)
	}
}
