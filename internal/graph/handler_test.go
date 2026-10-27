package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/99designs/gqlgen/graphql"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/httpserver"
)

const testOrigin = "https://bekup.example.com"

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type testEnv struct {
	gql  *Handler
	h    http.Handler
	logs *lockedBuffer
	// principal, when set, is attached to every request context (the
	// test-only stand-in for the session middleware of task 1.5).
	principal *auth.Principal
}

func defaultOptions(logger *slog.Logger) Options {
	return Options{
		Logger:        logger,
		ServerInfo:    model.ServerInfo{Version: "1.2.3", InsecureTransport: false, DevMode: false},
		MaxComplexity: 1000,
		MaxDepth:      12,
		MaxBodyBytes:  4096,
	}
}

// newEnv mounts the GraphQL handler behind the real HTTP layer so request
// IDs, the Origin check and the body limit are in place.
func newEnv(t *testing.T, mutate func(*Options)) *testEnv {
	t.Helper()
	logs := &lockedBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	opts := defaultOptions(logger)
	if mutate != nil {
		mutate(&opts)
	}
	gql, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := httpserver.New(httpserver.Options{
		BaseURL:      testOrigin,
		Dev:          opts.Dev,
		MaxBodyBytes: 1 << 20,
		RateLimit:    httpserver.RateLimitOptions{PerSecond: 1000, Burst: 1000, MaxClients: 100},
		Logger:       logger,
		Ready:        func(context.Context) error { return nil },
		GraphQL:      gql,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{gql: gql, h: srv.Handler(), logs: logs}
}

func (e *testEnv) raw(method, contentType string, body io.Reader) *httptest.ResponseRecorder {
	ctx := context.Background()
	if e.principal != nil {
		ctx = auth.WithPrincipal(ctx, *e.principal)
	}
	r := httptest.NewRequestWithContext(ctx, method, "/graphql", body)
	r.RemoteAddr = "192.0.2.10:1234"
	r.Header.Set("Origin", testOrigin)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

type gqlResponse struct {
	Data   map[string]any `json:"data"`
	Errors []struct {
		Message    string         `json:"message"`
		Extensions map[string]any `json:"extensions"`
	} `json:"errors"`
}

func (e *testEnv) query(t *testing.T, q string) (*httptest.ResponseRecorder, gqlResponse) {
	t.Helper()
	b, err := json.Marshal(map[string]any{"query": q})
	if err != nil {
		t.Fatal(err)
	}
	w := e.raw(http.MethodPost, "application/json", bytes.NewReader(b))
	var resp gqlResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return w, resp
}

func errorCode(resp gqlResponse) string {
	if len(resp.Errors) == 0 {
		return ""
	}
	if c, ok := resp.Errors[0].Extensions[ExtCode].(string); ok {
		return c
	}
	return ""
}

func TestServerInfo(t *testing.T) {
	env := newEnv(t, nil)
	w, resp := env.query(t, "{ serverInfo { version insecureTransport devMode } }")
	if w.Code != http.StatusOK || len(resp.Errors) != 0 {
		t.Fatalf("status %d, errors %v", w.Code, resp.Errors)
	}
	info, ok := resp.Data["serverInfo"].(map[string]any)
	if !ok || info["version"] != "1.2.3" || info["insecureTransport"] != false || info["devMode"] != false {
		t.Fatalf("serverInfo = %v", resp.Data)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/") || !strings.Contains(ct, "json") {
		t.Errorf("content type %q", ct)
	}
	if w.Header().Get("Content-Security-Policy") != httpserver.ContentSecurityPolicy {
		t.Error("security headers missing")
	}
}

func TestTransportRejections(t *testing.T) {
	env := newEnv(t, nil)
	valid := `{"query":"{ serverInfo { version } }"}`
	cases := []struct {
		name        string
		method      string
		contentType string
		body        string
		status      int
	}{
		{"GET", http.MethodGet, "", "", http.StatusMethodNotAllowed},
		{"HEAD", http.MethodHead, "", "", http.StatusMethodNotAllowed},
		{"PUT", http.MethodPut, "application/json", valid, http.StatusMethodNotAllowed},
		{"multipart", http.MethodPost, "multipart/form-data; boundary=x", "--x--", http.StatusUnsupportedMediaType},
		{"form", http.MethodPost, "application/x-www-form-urlencoded", "query=%7Bx%7D", http.StatusUnsupportedMediaType},
		{"text/plain", http.MethodPost, "text/plain", valid, http.StatusUnsupportedMediaType},
		{"application/graphql", http.MethodPost, "application/graphql", "{ serverInfo { version } }", http.StatusUnsupportedMediaType},
		{"missing content type", http.MethodPost, "", valid, http.StatusUnsupportedMediaType},
		{"latin1", http.MethodPost, "application/json; charset=latin1", valid, http.StatusUnsupportedMediaType},
		{"batch", http.MethodPost, "application/json", "[" + valid + "]", http.StatusBadRequest},
		{"unknown field", http.MethodPost, "application/json", `{"query":"{ serverInfo { version } }","id":"x"}`, http.StatusBadRequest},
		{"trailing data", http.MethodPost, "application/json", valid + valid, http.StatusBadRequest},
		{"empty query", http.MethodPost, "application/json", `{"query":" "}`, http.StatusBadRequest},
		{"not json", http.MethodPost, "application/json", "query=SECRETVALUE", http.StatusBadRequest},
		{"charset utf-8", http.MethodPost, "application/json; charset=UTF-8", valid, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := env.raw(c.method, c.contentType, strings.NewReader(c.body))
			if w.Code != c.status {
				t.Fatalf("status %d, want %d (%s)", w.Code, c.status, w.Body.String())
			}
			if c.status == http.StatusMethodNotAllowed && w.Header().Get("Allow") != http.MethodPost {
				t.Errorf("Allow = %q", w.Header().Get("Allow"))
			}
			if strings.Contains(w.Body.String(), "SECRETVALUE") {
				t.Error("request body echoed in the response")
			}
		})
	}
}

func TestBodyTooLarge(t *testing.T) {
	env := newEnv(t, nil)
	big := `{"query":"{ serverInfo { version } }","variables":{"x":"` + strings.Repeat("a", 5000) + `"}}`
	if w := env.raw(http.MethodPost, "application/json", strings.NewReader(big)); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("Content-Length: status %d", w.Code)
	}
	// Without a Content-Length (streamed), the reader limit applies.
	body := struct{ io.Reader }{strings.NewReader(big)}
	if w := env.raw(http.MethodPost, "application/json", body); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("streamed: status %d", w.Code)
	}
}

func TestOriginCheckStillApplies(t *testing.T) {
	env := newEnv(t, nil)
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/graphql", strings.NewReader(`{"query":"{ serverInfo { version } }"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	env.h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST: %d", w.Code)
	}
}

func TestComplexityLimit(t *testing.T) {
	env := newEnv(t, func(o *Options) { o.MaxComplexity = 3 })
	w, resp := env.query(t, "{ serverInfo { version insecureTransport devMode } }")
	if errorCode(resp) != string(apierror.QueryTooComplex) || w.Code != http.StatusUnprocessableEntity || resp.Data != nil {
		t.Fatalf("status %d, resp %+v", w.Code, resp)
	}
	if _, resp := env.query(t, "{ serverInfo { version } }"); len(resp.Errors) != 0 {
		t.Fatalf("simple query rejected: %+v", resp.Errors)
	}
}

func TestDepthLimit(t *testing.T) {
	env := newEnv(t, func(o *Options) { o.MaxDepth = 1 })
	for _, q := range []string{
		"{ serverInfo { version } }",
		"query { ...F } fragment F on Query { serverInfo { version } }",
		"query { ... on Query { serverInfo { version } } }",
	} {
		w, resp := env.query(t, q)
		if errorCode(resp) != string(apierror.QueryTooDeep) || w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, resp %+v", q, w.Code, resp)
		}
	}
	if _, resp := env.query(t, "{ __typename }"); len(resp.Errors) != 0 {
		t.Fatalf("depth 0 rejected: %+v", resp.Errors)
	}
}

func TestIntrospection(t *testing.T) {
	queries := []string{
		"{ __schema { queryType { name } } }",
		`{ __type(name: "ServerInfo") { name } }`,
		"query { ...F } fragment F on Query { __schema { queryType { name } } }",
		"{ serverInfo { version } __schema { types { name } } }",
	}
	t.Run("denied in production", func(t *testing.T) {
		env := newEnv(t, nil)
		for _, q := range queries {
			_, resp := env.query(t, q)
			if errorCode(resp) != string(apierror.IntrospectionDisabled) || resp.Data != nil {
				t.Errorf("%s: %+v", q, resp)
			}
		}
		if _, resp := env.query(t, "{ __typename serverInfo { __typename } }"); len(resp.Errors) != 0 {
			t.Errorf("__typename denied: %+v", resp.Errors)
		}
		// No "Did you mean" suggestions that would leak the schema.
		_, resp := env.query(t, "{ serverInf { version } }")
		if len(resp.Errors) == 0 || strings.Contains(resp.Errors[0].Message, "Did you mean") {
			t.Errorf("suggestion leaked: %+v", resp.Errors)
		}
	})
	t.Run("allowed in dev mode", func(t *testing.T) {
		env := newEnv(t, func(o *Options) { o.Dev = true })
		for _, q := range queries {
			if _, resp := env.query(t, q); len(resp.Errors) != 0 {
				t.Errorf("%s: %+v", q, resp.Errors)
			}
		}
	})
	t.Run("allowed by the access hook", func(t *testing.T) {
		env := newEnv(t, func(o *Options) { o.Access = allowAll{} })
		if _, resp := env.query(t, queries[0]); len(resp.Errors) != 0 {
			t.Errorf("%+v", resp.Errors)
		}
	})
}

// failField makes the resolution of ServerInfo.version fail with err (or
// panic when err is nil).
func failField(h *Handler, err error) {
	h.srv.AroundFields(func(ctx context.Context, next graphql.Resolver) (any, error) {
		if fc := graphql.GetFieldContext(ctx); fc != nil && fc.Object == "ServerInfo" && fc.Field.Name == "version" {
			if err == nil {
				panic("boom: postgres://user:pw@db/x")
			}
			return nil, err
		}
		return next(ctx)
	})
}

func TestInternalErrorMasked(t *testing.T) {
	for name, err := range map[string]error{
		"error": errors.New("pq: relation users does not exist at 10.0.0.5"),
		"panic": nil,
	} {
		t.Run(name, func(t *testing.T) {
			env := newEnv(t, nil)
			failField(env.gql, err)
			w, resp := env.query(t, "{ serverInfo { version } }")
			if len(resp.Errors) != 1 {
				t.Fatalf("errors: %+v", resp.Errors)
			}
			e := resp.Errors[0]
			rid := w.Header().Get("X-Request-ID")
			if e.Message != MaskedMessage || e.Extensions[ExtCode] != string(apierror.Internal) || rid == "" || e.Extensions[ExtRequestID] != rid {
				t.Fatalf("not masked: %+v (request id %q)", e, rid)
			}
			body := w.Body.String()
			if strings.Contains(body, "relation") || strings.Contains(body, "boom") {
				t.Fatalf("internal detail leaked: %s", body)
			}
			logs := env.logs.String()
			if !strings.Contains(logs, `"request_id":"`+rid+`"`) || (!strings.Contains(logs, "relation users") && !strings.Contains(logs, "boom")) {
				t.Fatalf("real error not logged with the request id: %s", logs)
			}
		})
	}
}

func TestTypedErrorPassesThrough(t *testing.T) {
	env := newEnv(t, nil)
	failField(env.gql, apierror.Invalid("name", "name is too long"))
	_, resp := env.query(t, "{ serverInfo { version } }")
	if len(resp.Errors) != 1 {
		t.Fatalf("errors: %+v", resp.Errors)
	}
	e := resp.Errors[0]
	if e.Message != "name is too long" || e.Extensions[ExtCode] != string(apierror.Validation) || e.Extensions[ExtField] != "name" {
		t.Fatalf("typed error changed: %+v", e)
	}
	if _, ok := e.Extensions[ExtRequestID]; ok {
		t.Error("typed error carries a request id")
	}
	// Wrapped typed errors keep their code too.
	env = newEnv(t, nil)
	failField(env.gql, fmtWrap(apierror.New(apierror.Forbidden, "no")))
	if _, resp := env.query(t, "{ serverInfo { version } }"); errorCode(resp) != string(apierror.Forbidden) {
		t.Fatalf("wrapped: %+v", resp.Errors)
	}
}

func fmtWrap(err error) error { return &wrapped{err} }

type wrapped struct{ err error }

func (w *wrapped) Error() string { return "context: " + w.err.Error() }
func (w *wrapped) Unwrap() error { return w.err }

func TestNewValidation(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	for name, mutate := range map[string]func(*Options){
		"no logger":     func(o *Options) { o.Logger = nil },
		"no complexity": func(o *Options) { o.MaxComplexity = 0 },
		"no depth":      func(o *Options) { o.MaxDepth = 0 },
		"no body limit": func(o *Options) { o.MaxBodyBytes = 0 },
	} {
		o := defaultOptions(logger)
		mutate(&o)
		if _, err := New(o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
