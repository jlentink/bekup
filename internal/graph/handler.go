package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/executor"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/auth/passkey"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/calendar"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/downloads"
	"github.com/jlentink/bekup/internal/graph/generated"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/graph/resolvers"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/runstate"
)

// Fixed limits (not configurable: no legitimate client comes close).
const (
	// QueryCacheSize is the number of parsed and validated documents kept
	// in the LRU query cache.
	QueryCacheSize = 1000
	// ParserTokenLimit caps the tokens of one query document.
	ParserTokenLimit = 10000
)

// Options configure New.
type Options struct {
	// Logger receives masked errors and panics (required).
	Logger *slog.Logger
	// Dev mode allows introspection for everyone and field suggestions in
	// validation errors.
	Dev bool
	// ServerInfo is returned by Query.serverInfo.
	ServerInfo model.ServerInfo
	// MaxComplexity, MaxDepth and MaxBodyBytes are the graphql.* limits
	// (all must be positive).
	MaxComplexity int
	MaxDepth      int
	MaxBodyBytes  int64
	// Access decides the access directives, the root-field operation gate
	// and introspection (nil: DenyAll).
	Access Access
	// Policy is passed to the resolvers for object-level checks (nil: a
	// default deny-by-default policy).
	Policy *authz.Policy
	// AuditLog backs Query.auditLog (nil: the field fails).
	AuditLog resolvers.AuditLister
	// Auth backs login, logout, me, sessions and password changes (nil:
	// those fields fail with an internal error).
	Auth *auth.Service
	// Streams enables subscriptions as Server-Sent Events on POST
	// /graphql with Accept: text/event-stream (task 2.13); nil: no
	// streams (such requests get 406).
	Streams *StreamOptions
	// Users backs user administration (task 1.6; nil: those fields fail).
	Users *auth.UserAdmin
	// TOTP backs TOTP enrollment, recovery codes and myTwoFactorStatus
	// (task 1.7; nil: those fields fail).
	TOTP *auth.TOTP
	// Passkeys backs the passkey fields (task 1.8; nil: those fields fail).
	Passkeys *passkey.Service
	// SecondFactor backs the two-step login and step-up (task 1.9; nil:
	// those fields fail).
	SecondFactor *auth.SecondFactorFlow
	// TwoFactorPolicy backs the 2FA policy and the users' 2FA standing
	// (task 1.9; nil: the policy fields fail, standing reads as not
	// required).
	TwoFactorPolicy *auth.TwoFactorPolicies
	// Destinations backs the destination fields (task 2.12; nil: those
	// fields fail).
	Destinations *destinations.Service
	// Connections backs the connection fields (task 2.11; nil: those
	// fields fail).
	Connections *connections.Service
	// Jobs and JobKeys back the job and job encryption fields (tasks 3.5,
	// 3.2; nil: those fields fail).
	Jobs    *jobs.Service
	JobKeys *jobkeys.Service
	// Runs backs the run history fields and Bus (the bus the LISTEN
	// bridge feeds, carrying pubsub.RunEvents) the run subscriptions
	// (task 3.7; nil: those fields fail).
	Runs *runstate.Reader
	Bus  *pubsub.Bus
	// Downloads backs createDownloadTicket (task 3.9; nil: the field
	// fails).
	Downloads *downloads.Service
	// Calendar backs Query.calendar (task 3.14; nil: the field fails).
	Calendar *calendar.Service
	// Restores backs the restore fields (task 4.5; nil: those fields
	// fail). restoreUpdated also needs Bus (pubsub.RestoreEvents).
	Restores *restore.API
	// TestRestores backs the test-restore fields (task 4.6; nil: those
	// fields fail with an internal error).
	TestRestores *restore.TestRestoreAPI
}

// Handler is the /graphql endpoint.
type Handler struct {
	opts Options
	// srv is gqlgen's executor. The transports are this package's own
	// (serveJSON, serveStream): gqlgen's handler and transport packages
	// are not used, which also keeps their WebSocket library out of the
	// build (task 2.13).
	srv *executor.Executor
}

// New builds the GraphQL handler: POST + application/json only, the limits,
// the access policy and the masking error presenter (D-050, D-105). It
// fails when a root field is neither guarded nor public.
func New(opts Options) (*Handler, error) {
	if opts.Logger == nil {
		return nil, errors.New("graph: Logger is required")
	}
	if opts.MaxComplexity <= 0 || opts.MaxDepth <= 0 || opts.MaxBodyBytes <= 0 {
		return nil, errors.New("graph: MaxComplexity, MaxDepth and MaxBodyBytes must be positive")
	}
	if opts.Access == nil {
		opts.Access = DenyAll{}
	}
	if opts.Policy == nil {
		opts.Policy = authz.New(authz.Options{})
	}
	es := generated.NewExecutableSchema(generated.Config{
		Resolvers: &resolvers.Resolver{
			ServerInfo: opts.ServerInfo,
			Authz:      opts.Policy,
			AuditLog:   opts.AuditLog,
			Auth:       opts.Auth,
			Users:      opts.Users,
			TOTP:       opts.TOTP,
			Passkeys:   opts.Passkeys,

			SecondFactor:    opts.SecondFactor,
			TwoFactorPolicy: opts.TwoFactorPolicy,
			Destinations:    opts.Destinations,
			Connections:     opts.Connections,
			Jobs:            opts.Jobs,
			JobKeys:         opts.JobKeys,
			Runs:            opts.Runs,
			Bus:             opts.Bus,
			Downloads:       opts.Downloads,
			Calendar:        opts.Calendar,
			Restores:        opts.Restores,
			TestRestores:    opts.TestRestores,
		},
		Directives: directives(opts.Access),
	})
	if err := CheckAccessPolicy(es.Schema(), PublicRootFields, RootFieldOperations); err != nil {
		return nil, err
	}
	if err := CheckSingleRootFields(es.Schema(), SingleRootFields, PublicRootFields, RootFieldOperations, CredentialOperations); err != nil {
		return nil, err
	}

	if opts.Streams != nil {
		if err := opts.Streams.validate(); err != nil {
			return nil, err
		}
	}
	// Transports (ServeHTTP): POST + JSON for queries and mutations, and
	// POST + JSON with Accept: text/event-stream for subscriptions (task
	// 2.13). GET (CSRF via links, cacheable), multipart and form posts
	// (CSRF without preflight), WebSockets and persisted queries (APQ)
	// are never supported.
	srv := executor.New(es)
	srv.SetQueryCache(lru.New[*ast.QueryDocument](QueryCacheSize))
	srv.SetParserTokenLimit(ParserTokenLimit)
	// "Did you mean ...?" suggestions would reveal the schema to callers
	// who may not introspect it.
	srv.SetDisableSuggestion(!opts.Dev)
	srv.SetErrorPresenter(errorPresenter(opts.Logger))
	srv.SetRecoverFunc(recoverFunc(opts.Logger))

	access := opts.Access
	dev := opts.Dev
	srv.Use(introspectionGate{Allowed: func(ctx context.Context) bool {
		return dev || access.IntrospectionAllowed(ctx)
	}})
	srv.Use(depthLimit{Max: opts.MaxDepth})
	srv.Use(rootFieldGate{schema: es.Schema(), single: pubSet(SingleRootFields)})
	srv.Use(transportGate{public: pubSet(PublicRootFields), ops: RootFieldOperations, access: access})
	srv.Use(extension.FixedComplexityLimit(opts.MaxComplexity))
	srv.AroundRootFields(rootFieldGuard(PublicRootFields, RootFieldOperations, access))
	// Per-operation batch loader of the run fields (task 3.7): queries
	// and mutations only, so subscription events never read a cache.
	srv.AroundOperations(func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		if graphql.HasOperationContext(ctx) {
			if oc := graphql.GetOperationContext(ctx); oc.Operation != nil && oc.Operation.Operation != ast.Subscription {
				ctx = resolvers.WithRunLoader(ctx)
			}
		}
		return next(ctx)
	})
	return &Handler{opts: opts, srv: srv}, nil
}

// request is the only accepted body shape (the GraphQL-over-HTTP fields
// gqlgen reads, without persisted-query or batching variants).
type request struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName,omitempty"`
	Variables     map[string]any `json:"variables,omitempty"`
	Extensions    map[string]any `json:"extensions,omitempty"`
}

// ServeHTTP enforces the transport rules before gqlgen sees the request:
// 405 for any method but POST, 415 for any content type but
// application/json (charset utf-8 at most), 413 above MaxBodyBytes and 400
// for anything that is not exactly one JSON request object. A request
// that accepts text/event-stream becomes an event stream (serveStream,
// subscriptions only); every other one gets one JSON response (serveJSON,
// queries and mutations only).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeTransportError(w, http.StatusMethodNotAllowed, "only POST is supported")
		return
	}
	if len(r.Header.Values("Upgrade")) > 0 {
		// No protocol switches (WebSocket, h2c) on /graphql.
		writeTransportError(w, http.StatusBadRequest, "unexpected Upgrade header")
		return
	}
	if !isJSONContentType(r.Header.Values("Content-Type")) {
		writeTransportError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}
	if r.ContentLength > h.opts.MaxBodyBytes {
		writeTransportError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.opts.MaxBodyBytes))
	if err != nil {
		if httpserver.IsBodyTooLarge(err) {
			writeTransportError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeTransportError(w, http.StatusBadRequest, "could not read the request body")
		return
	}
	if err := checkRequestBody(body); err != nil {
		writeTransportError(w, http.StatusBadRequest, err.Error())
		return
	}
	if acceptsEventStream(r) {
		if h.opts.Streams == nil {
			writeTransportError(w, http.StatusNotAcceptable, "event streams are not enabled")
			return
		}
		h.serveStream(w, r, body)
		return
	}
	h.serveJSON(w, r, body)
}

// isJSONContentType accepts exactly one Content-Type of application/json
// with no parameter other than charset=utf-8.
func isJSONContentType(values []string) bool {
	if len(values) != 1 {
		return false
	}
	mt, params, err := mime.ParseMediaType(values[0])
	if err != nil || mt != "application/json" {
		return false
	}
	for k, v := range params {
		if k != "charset" || !strings.EqualFold(v, "utf-8") {
			return false
		}
	}
	return true
}

// errBadBody is the generic 400 message; it never echoes the body.
var errBadBody = errors.New("the request body must be one JSON object with a query string")

// checkRequestBody accepts exactly one JSON object with the known fields
// and a non-empty query. It runs before gqlgen, whose own decode errors
// would echo the body back.
func checkRequestBody(body []byte) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var req request
	if err := dec.Decode(&req); err != nil {
		return errBadBody
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errBadBody
	}
	if strings.TrimSpace(req.Query) == "" {
		return errBadBody
	}
	return nil
}

// writeTransportError writes a GraphQL-shaped error for a request rejected
// before execution (code BAD_REQUEST).
func writeTransportError(w http.ResponseWriter, status int, msg string) {
	resp := graphql.Response{Errors: []*gqlerror.Error{{
		Message:    msg,
		Extensions: map[string]any{ExtCode: "BAD_REQUEST"},
	}}}
	b, err := json.Marshal(resp)
	if err != nil {
		b = []byte(`{"errors":[{"message":"bad request"}]}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(status)
	if _, err := w.Write(b); err != nil {
		return
	}
}
