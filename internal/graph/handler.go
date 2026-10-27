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
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/auth/passkey"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/graph/generated"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/graph/resolvers"
	"github.com/jlentink/bekup/internal/httpserver"
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
	// WebSocket enables subscriptions over WebSocket on GET /graphql
	// (task 1.10); nil: no WebSocket transport (GET answers 405).
	WebSocket *WebSocketOptions
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
}

// Handler is the /graphql endpoint.
type Handler struct {
	opts Options
	srv  *handler.Server
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
		},
		Directives: directives(opts.Access),
	})
	if err := CheckAccessPolicy(es.Schema(), PublicRootFields, RootFieldOperations); err != nil {
		return nil, err
	}
	if err := CheckSingleRootFields(es.Schema(), SingleRootFields, PublicRootFields, RootFieldOperations, CredentialOperations); err != nil {
		return nil, err
	}

	srv := handler.New(es)
	// POST + JSON for queries and mutations. GET (CSRF via links,
	// cacheable), multipart and form posts (CSRF without preflight) are
	// never added; persisted queries (APQ) are not used. The only other
	// transport is the WebSocket for subscriptions (task 1.10): a GET
	// upgrade with graphql-transport-ws, admitted by serveWebSocket
	// (exact Origin, cookie session, connection caps).
	srv.AddTransport(transport.POST{})
	if opts.WebSocket != nil {
		if err := opts.WebSocket.validate(); err != nil {
			return nil, err
		}
		srv.AddTransport(websocketTransport(*opts.WebSocket, opts.Logger))
	}
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
	srv.Use(wsOperationGate{public: pubSet(PublicRootFields), ops: RootFieldOperations, access: access})
	srv.Use(subscriptionSlots{})
	srv.Use(extension.FixedComplexityLimit(opts.MaxComplexity))
	srv.AroundRootFields(rootFieldGuard(PublicRootFields, RootFieldOperations, access))
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
// for anything that is not exactly one JSON request object.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.opts.WebSocket != nil && isWebSocketRequest(r) {
		h.serveWebSocket(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeTransportError(w, http.StatusMethodNotAllowed, "only POST is supported")
		return
	}
	if len(r.Header.Values("Upgrade")) > 0 {
		// gqlgen would hand any request with an Upgrade header to the
		// WebSocket transport.
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
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	h.srv.ServeHTTP(w, r)
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
