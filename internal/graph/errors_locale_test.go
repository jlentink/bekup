package graph

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/streams"
	"github.com/jlentink/bekup/internal/i18n"
)

// TestErrorPresenterLanguage: the presenter renders an API error in the
// account's language, else the request's Accept-Language, else English
// (D-238); code and field never change.
func TestErrorPresenterLanguage(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	present := errorPresenter(logger)
	err := apierror.Invalidf("first", "first must be between 1 and %d", 100)
	en := "first must be between 1 and 100"
	nl := i18n.M("first must be between 1 and %d", 100).In(i18n.NL)
	if nl == en {
		t.Fatal("the test text has no Dutch entry")
	}
	principal := func(l i18n.Locale) auth.Principal {
		return auth.Principal{UserID: uuid.New(), SessionID: uuid.New(), Role: auth.RoleAdmin, Locale: l}
	}
	for _, tc := range []struct {
		name   string
		accept string
		p      *auth.Principal
		want   string
	}{
		{"no language", "", nil, en},
		{"accept-language nl", "nl-NL,nl;q=0.9", nil, nl},
		{"accept-language other", "de-DE", nil, en},
		{"account nl wins", "en", new(principal(i18n.NL)), nl},
		{"account en wins", "nl", new(principal(i18n.EN)), en},
		{"account follows browser", "nl", new(principal("")), nl},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := auth.WithRequestMeta(context.Background(), auth.RequestMeta{}.WithAcceptLanguage(tc.accept))
			if tc.p != nil {
				ctx = auth.WithPrincipal(ctx, *tc.p)
			}
			ctx = graphql.WithResponseContext(ctx, present, recoverFunc(logger))
			got := present(ctx, err)
			if got.Message != tc.want {
				t.Errorf("message = %q, want %q", got.Message, tc.want)
			}
			if got.Extensions[ExtCode] != string(apierror.Validation) || got.Extensions[ExtField] != "first" {
				t.Errorf("extensions changed: %v", got.Extensions)
			}
		})
	}
}

// TestProtocolErrorLanguage: errors outside field execution follow the
// request language too.
func TestProtocolErrorLanguage(t *testing.T) {
	t.Parallel()
	ctx := auth.WithRequestMeta(context.Background(), auth.RequestMeta{}.WithAcceptLanguage("nl"))
	m := i18n.M("introspection is disabled")
	if got := protocolError(ctx, apierror.IntrospectionDisabled, m); got.Message != m.In(i18n.NL) {
		t.Errorf("message = %q", got.Message)
	}
	if got := apiProtocolError(ctx, streams.ErrAccessChanged); got.Message == streams.ErrAccessChanged.Message {
		t.Errorf("stream end not translated: %q", got.Message)
	}
}

// TestTransportErrorLanguage: a request refused before execution gets its
// message in the request's language.
func TestTransportErrorLanguage(t *testing.T) {
	t.Parallel()
	ctx := auth.WithRequestMeta(context.Background(), auth.RequestMeta{}.WithAcceptLanguage("nl"))
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/graphql", http.NoBody)
	w := httptest.NewRecorder()
	writeTransportError(w, r, http.StatusMethodNotAllowed, i18n.M("only POST is supported"))
	var resp struct {
		Errors []struct {
			Message    string         `json:"message"`
			Extensions map[string]any `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || len(resp.Errors) != 1 {
		t.Fatalf("body %q: %v", w.Body.String(), err)
	}
	if want := i18n.M("only POST is supported").In(i18n.NL); resp.Errors[0].Message != want || want == "only POST is supported" {
		t.Errorf("message = %q, want %q", resp.Errors[0].Message, want)
	}
}

// TestPresenterInUseJobs: an in-use conflict carries the job names as
// data (extensions.jobs), so clients never parse the message.
func TestPresenterInUseJobs(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	present := errorPresenter(logger)
	ctx := graphql.WithResponseContext(context.Background(), present, recoverFunc(logger))
	err := apierror.InUse(i18n.M("the connection is still used by these jobs: %s", "a, b"), []string{"a", "b"})
	got := present(ctx, err)
	jobs, ok := got.Extensions[ExtJobs].([]string)
	if !ok || len(jobs) != 2 || jobs[0] != "a" || got.Extensions[ExtCode] != string(apierror.Conflict) {
		t.Errorf("extensions = %v", got.Extensions)
	}
	if plain := present(ctx, apierror.New(apierror.Conflict, "x")); plain.Extensions[ExtJobs] != nil {
		t.Errorf("jobs on a plain conflict: %v", plain.Extensions)
	}
}
