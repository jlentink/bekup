package externalapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/apikey"
	"github.com/jlentink/bekup/internal/httpserver"
)

// Options configure NewHandler.
type Options struct {
	// Store is the database access (DBStore; tests use a fake).
	Store Store
	// Logger receives errors (never tokens or request bodies).
	Logger *slog.Logger
	// TokenLimit is the per-token rate limit (keyed by the presented
	// token's hash, so wrong secrets never drain a real token's bucket).
	TokenLimit httpserver.RateLimitOptions
	// FailedAuth is the per-IP throttle of failed authentications.
	FailedAuth FailedAuthOptions
	// Now replaces time.Now (Retry-After of rate_limited; tests).
	Now func() time.Time
}

// Fixed answers.
const (
	// pendingRetryAfter is the Retry-After of too_many_pending: the
	// workers decide requests within a few seconds (5 s sweep).
	pendingRetryAfter = 5 * time.Second
	// wwwAuthenticate is the challenge of every 401, whatever failed.
	wwwAuthenticate = `Bearer realm="bekup"`
	// IdempotencyHeader names the optional idempotency key.
	IdempotencyHeader = "Idempotency-Key"
	// ReplayedHeader is set on an idempotent replay.
	ReplayedHeader = "Idempotent-Replayed"
)

// idempotencyKeyRE is the accepted Idempotency-Key.
var idempotencyKeyRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type handler struct {
	store  Store
	log    *slog.Logger
	tokens *httpserver.RateLimiter[[32]byte]
	failed *failedAuth
	now    func() time.Time
}

// NewHandler returns the API routes, to be mounted at
// httpserver.APIBasePath:
//
//	POST /jobs/{jobId}/runs        start a run (202 + request)
//	GET  /requests/{requestId}     the state of one request
//	GET  /self                     the token's name, expiry, jobs and the client address seen
//
// Tokens are accepted only as "Authorization: Bearer <token>". Every
// authentication failure (no header, malformed, unknown, wrong secret,
// revoked, expired, refused client address) answers the same 401; jobs
// and requests outside the token's scope answer 404 like unknown ones.
func NewHandler(o Options) (http.Handler, error) {
	if o.Store == nil {
		return nil, errors.New("externalapi: Store is required")
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	tokens, err := httpserver.NewRateLimiter[[32]byte](o.TokenLimit)
	if err != nil {
		return nil, err
	}
	failed, err := newFailedAuth(o.FailedAuth)
	if err != nil {
		return nil, err
	}
	h := &handler{store: o.Store, log: o.Logger, tokens: tokens, failed: failed, now: o.Now}
	r := chi.NewRouter()
	r.Use(noQuery)
	r.Post("/jobs/{jobId}/runs", h.requestRun)
	r.Get("/requests/{requestId}", h.requestStatus)
	r.Get("/self", h.self)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) { httpserver.WriteAPIError(w, http.StatusNotFound) })
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		httpserver.WriteAPIError(w, http.StatusMethodNotAllowed)
	})
	return r, nil
}

// noQuery refuses every query string before anything else: no endpoint
// takes parameters, and a token must never be accepted from a URL (URLs end
// up in proxy logs and shell histories). The query is never logged.
func noQuery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" || r.URL.ForceQuery {
			msg := "query parameters are not accepted"
			if apikey.Contains(r.URL.RawQuery) || strings.Contains(strings.ToLower(r.URL.RawQuery), "token") {
				msg = "a token in the URL is not accepted: send it only as Authorization: Bearer <token> (and revoke a token that was sent in a URL)"
			}
			httpserver.WriteAPIErrorCode(w, http.StatusBadRequest, "invalid_request", msg)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// caller is an admitted request: the presented credential and the client
// address, with the failed-authentication token still reserved.
type caller struct {
	cred Credential
	ip   netip.Addr
	// released: the reserved token went back (at most once).
	released bool
}

// admit throttles failed authentications, parses the bearer token and
// applies the per-token limit. On false the response is written. The
// handler must end with h.authenticated or h.unauthenticated (or
// h.unavailable).
func (h *handler) admit(w http.ResponseWriter, r *http.Request) (*caller, bool) {
	ip := httpserver.ClientIP(r.Context())
	if ok, wait := h.failed.reserve(ip); !ok {
		tooMany(w, wait, "rate_limited", "too many failed authentications from this address; retry later")
		return nil, false
	}
	raw, present := bearer(r.Header)
	if !present {
		// No credential at all is not a guess: it does not count.
		h.failed.release(ip)
		unauthenticated(w)
		return nil, false
	}
	key, err := apikey.Parse(raw)
	if err != nil {
		unauthenticated(w)
		return nil, false
	}
	c := &caller{cred: Credential{TokenID: key.ID, SecretHash: key.Hash()}, ip: ip}
	key.Zero()
	if ok, wait := h.tokens.Allow(c.cred.SecretHash); !ok {
		h.failed.release(ip)
		tooMany(w, wait, "rate_limited", "too many requests for this token; retry later")
		return nil, false
	}
	return c, true
}

// bearer returns the token of the one Authorization header with the
// Bearer scheme (case-insensitive) and a single space; present is false
// without an Authorization header. Anything else yields a value that
// fails apikey.Parse.
func bearer(h http.Header) (token string, present bool) {
	vals := h.Values("Authorization")
	switch len(vals) {
	case 0:
		return "", false
	case 1:
	default:
		return "", true
	}
	scheme, tok, ok := strings.Cut(vals[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", true
	}
	return tok, true
}

// authenticated releases the failed-authentication token.
func (h *handler) authenticated(c *caller) {
	if !c.released {
		c.released = true
		h.failed.release(c.ip)
	}
}

// unauthenticated answers 401 (the failure keeps its reserved token).
func unauthenticated(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", wwwAuthenticate)
	httpserver.WriteAPIError(w, http.StatusUnauthorized)
}

// unavailable answers a database failure with 503 and logs it; the
// attempt does not count as a failed authentication.
func (h *handler) unavailable(w http.ResponseWriter, r *http.Request, c *caller, err error) {
	h.authenticated(c)
	h.log.LogAttrs(r.Context(), slog.LevelError, "external API database call failed",
		slog.Any("error", err), slog.String("request_id", httpserver.RequestID(r.Context())))
	w.Header().Set("Retry-After", "5")
	httpserver.WriteAPIError(w, http.StatusServiceUnavailable)
}

func tooMany(w http.ResponseWriter, wait time.Duration, code, msg string) {
	w.Header().Set("Retry-After", strconv.FormatInt(httpserver.RetryAfterSeconds(wait), 10))
	httpserver.WriteAPIErrorCode(w, http.StatusTooManyRequests, code, msg)
}

// authenticateOnly checks the token where no other call does it (a
// malformed path id, GET status before the lookup): false means the
// response is written.
func (h *handler) authenticateOnly(w http.ResponseWriter, r *http.Request, c *caller) (Token, bool) {
	t, err := h.store.Authenticate(r.Context(), c.cred, c.ip)
	switch {
	case errors.Is(err, ErrUnauthenticated):
		unauthenticated(w)
		return Token{}, false
	case err != nil:
		h.unavailable(w, r, c, err)
		return Token{}, false
	}
	h.authenticated(c)
	return t, true
}

// --- POST /jobs/{jobId}/runs ---------------------------------------------------

// RunAccepted is the 202 answer of POST /jobs/{jobId}/runs.
type RunAccepted struct {
	RequestID uuid.UUID `json:"requestId"`
	JobID     uuid.UUID `json:"jobId"`
	State     string    `json:"state"`
	StatusURL string    `json:"statusUrl"`
	CreatedAt time.Time `json:"createdAt"`
}

func statusPath(id uuid.UUID) string {
	return httpserver.APIBasePath + "/requests/" + id.String()
}

func (h *handler) requestRun(w http.ResponseWriter, r *http.Request) {
	if status, msg := checkEmptyJSONBody(r); status != 0 {
		httpserver.WriteAPIErrorCode(w, status, codeFor(status), msg)
		return
	}
	idem, ok := idempotencyKey(r.Header)
	if !ok {
		httpserver.WriteAPIErrorCode(w, http.StatusBadRequest, "invalid_request",
			"Idempotency-Key must be 1 to 64 characters of A-Z, a-z, 0-9, _ and - (one header)")
		return
	}
	c, ok := h.admit(w, r)
	if !ok {
		return
	}
	jobID, err := uuid.Parse(chi.URLParam(r, "jobId"))
	if err != nil {
		if _, ok := h.authenticateOnly(w, r, c); ok {
			httpserver.WriteAPIErrorCode(w, http.StatusNotFound, "not_found", "no such job")
		}
		return
	}
	res, err := h.store.RequestRun(r.Context(), c.cred, jobID, idem, c.ip)
	if err != nil {
		h.unavailable(w, r, c, err)
		return
	}
	if res.Refusal == RefusalUnauthenticated {
		unauthenticated(w)
		return
	}
	h.authenticated(c)
	switch res.Refusal {
	case "":
	case RefusalNotFound:
		httpserver.WriteAPIErrorCode(w, http.StatusNotFound, "not_found", "no such job")
		return
	case RefusalInvalidRequest:
		httpserver.WriteAPIErrorCode(w, http.StatusBadRequest, "invalid_request", "the request is malformed")
		return
	case RefusalIdempotencyMismatch:
		httpserver.WriteAPIErrorCode(w, http.StatusConflict, RefusalIdempotencyMismatch,
			"this Idempotency-Key was used for another job within the last 24 hours")
		return
	case RefusalJobDisabled:
		httpserver.WriteAPIErrorCode(w, http.StatusConflict, RefusalJobDisabled, "the job is disabled")
		return
	case RefusalRunActive:
		httpserver.WriteAPIErrorCode(w, http.StatusConflict, RefusalRunActive,
			"a run of this job is already queued, running or waiting for a retry")
		return
	case RefusalTooManyPending:
		tooMany(w, pendingRetryAfter, RefusalTooManyPending, "this token has 3 requests that are not decided yet; retry later")
		return
	case RefusalRateLimited:
		wait := time.Minute
		if res.RetryAt != nil {
			wait = res.RetryAt.Sub(h.now())
		}
		tooMany(w, wait, RefusalRateLimited, "this token filed 30 requests within the last hour; retry later")
		return
	default:
		h.log.LogAttrs(r.Context(), slog.LevelError, "unknown refusal of api.request_run",
			slog.String("refusal", res.Refusal), slog.String("request_id", httpserver.RequestID(r.Context())))
		httpserver.WriteAPIError(w, http.StatusInternalServerError)
		return
	}
	body := RunAccepted{
		RequestID: res.RequestID, JobID: jobID, State: StatePending,
		StatusURL: statusPath(res.RequestID), CreatedAt: res.CreatedAt.UTC(),
	}
	if res.Replayed {
		// A replay reports the original request's current state; when
		// that cannot be read, 503 (retry) rather than a "pending" that
		// may be false.
		f, err := h.store.RequestStatus(r.Context(), c.cred, res.RequestID, c.ip)
		if err != nil {
			h.unavailable(w, r, c, err)
			return
		}
		w.Header().Set(ReplayedHeader, "true")
		body.State = Aggregate(f).State
	}
	w.Header().Set("Location", body.StatusURL)
	httpserver.WriteJSON(w, http.StatusAccepted, body)
}

// checkEmptyJSONBody accepts no body or an empty JSON object
// (Content-Type application/json); it returns the error status otherwise.
func checkEmptyJSONBody(r *http.Request) (int, string) {
	if r.Body == nil || r.Body == http.NoBody {
		return 0, ""
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		if httpserver.IsBodyTooLarge(err) {
			return http.StatusRequestEntityTooLarge, "the request body is too large"
		}
		return http.StatusBadRequest, "the request body could not be read"
	}
	if len(data) == 0 {
		return 0, ""
	}
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return http.StatusUnsupportedMediaType, "the body must be application/json (or empty)"
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	// A pointer: JSON null decodes into a nil pointer without an error,
	// so only an object leaves it set.
	var empty *struct{}
	if err := dec.Decode(&empty); err != nil || empty == nil {
		return http.StatusBadRequest, "the body must be empty or {}"
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return http.StatusBadRequest, "the body must be empty or {}"
	}
	return 0, ""
}

func codeFor(status int) string {
	switch status {
	case http.StatusRequestEntityTooLarge:
		return "body_too_large"
	case http.StatusUnsupportedMediaType:
		return "unsupported_media_type"
	default:
		return "invalid_request"
	}
}

// idempotencyKey returns the optional key; ok is false for a malformed one
// or several headers.
func idempotencyKey(h http.Header) (string, bool) {
	vals := h.Values(IdempotencyHeader)
	switch len(vals) {
	case 0:
		return "", true
	case 1:
		return vals[0], idempotencyKeyRE.MatchString(vals[0])
	default:
		return "", false
	}
}

// --- GET /requests/{requestId} ---------------------------------------------------

func (h *handler) requestStatus(w http.ResponseWriter, r *http.Request) {
	c, ok := h.admit(w, r)
	if !ok {
		return
	}
	// Authenticate first: request_status answers "no row" alike for a
	// refused token and an unknown request.
	if _, ok := h.authenticateOnly(w, r, c); !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "requestId"))
	if err != nil {
		httpserver.WriteAPIErrorCode(w, http.StatusNotFound, "not_found", "no such request")
		return
	}
	f, err := h.store.RequestStatus(r.Context(), c.cred, id, c.ip)
	switch {
	case errors.Is(err, ErrNotFound):
		httpserver.WriteAPIErrorCode(w, http.StatusNotFound, "not_found", "no such request")
		return
	case err != nil:
		h.unavailable(w, r, c, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, Aggregate(f))
}

// --- GET /self ---------------------------------------------------------------------

// Self is the answer of GET /api/v1/self.
type Self struct {
	Name string `json:"name"`
	// ExpiresAt is null when the token never expires (NeverExpires).
	ExpiresAt    *time.Time `json:"expiresAt"`
	NeverExpires bool       `json:"neverExpires"`
	Jobs         []SelfJob  `json:"jobs"`
	// ClientIP is the address bekup sees (and checks the token's address
	// allowlist against); null when unknown.
	ClientIP *string `json:"clientIp"`
}

// SelfJob is a job in the token's scope.
type SelfJob struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

func (h *handler) self(w http.ResponseWriter, r *http.Request) {
	c, ok := h.admit(w, r)
	if !ok {
		return
	}
	t, ok := h.authenticateOnly(w, r, c)
	if !ok {
		return
	}
	jobs, err := h.store.TokenJobs(r.Context(), c.cred, c.ip)
	if err != nil {
		h.unavailable(w, r, c, err)
		return
	}
	out := Self{Name: t.Name, ExpiresAt: t.ExpiresAt, NeverExpires: t.ExpiresAt == nil, Jobs: make([]SelfJob, 0, len(jobs))}
	for _, j := range jobs {
		out.Jobs = append(out.Jobs, SelfJob(j))
	}
	if c.ip.IsValid() {
		s := c.ip.String()
		out.ClientIP = &s
	}
	httpserver.WriteJSON(w, http.StatusOK, out)
}
