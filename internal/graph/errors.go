package graph

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/httpserver"
)

// MaskedMessage is the message of every masked internal error.
const MaskedMessage = "internal error"

// Extension keys of GraphQL errors.
const (
	ExtCode      = "code"
	ExtRequestID = "requestId"
	ExtField     = "field"
)

// errPanic is what the recover func turns a panic into; it is masked.
var errPanic = errors.New("graph: resolver panic")

// errorPresenter decides what the client sees (D-105):
//
//   - *apierror.Error anywhere in the chain: its message and code (plus
//     extensions.field for validation errors).
//   - outside field execution, gqlerror.Error values without a wrapped
//     error: protocol errors from parsing, validation and the limit
//     extensions; they describe the query only and pass unchanged.
//   - everything else (resolver errors, panics, gqlgen runtime errors):
//     "internal error" with code INTERNAL and the request ID; the real error
//     is logged with the same request ID.
func errorPresenter(logger *slog.Logger) graphql.ErrorPresenterFunc {
	return func(ctx context.Context, err error) *gqlerror.Error {
		if err == nil {
			return nil
		}
		var gqlErr *gqlerror.Error
		if !errors.As(err, &gqlErr) {
			gqlErr = gqlerror.WrapPath(graphql.GetPath(ctx), err)
		}
		if ae, ok := apierror.As(err); ok {
			ext := map[string]any{ExtCode: string(ae.Code)}
			if ae.Field != "" {
				ext[ExtField] = ae.Field
			}
			return &gqlerror.Error{
				Message:    ae.Message,
				Path:       gqlErr.Path,
				Locations:  gqlErr.Locations,
				Extensions: ext,
			}
		}
		if gqlErr.Err == nil && graphql.GetFieldContext(ctx) == nil {
			return gqlErr
		}
		rid := httpserver.RequestID(ctx)
		logger.ErrorContext(ctx, "graphql internal error",
			slog.String("request_id", rid),
			slog.String("path", gqlErr.Path.String()),
			slog.String("error", err.Error()))
		return &gqlerror.Error{
			Message:    MaskedMessage,
			Path:       gqlErr.Path,
			Locations:  gqlErr.Locations,
			Extensions: map[string]any{ExtCode: string(apierror.Internal), ExtRequestID: rid},
		}
	}
}

// recoverFunc logs a resolver panic with its stack; the client gets the
// masked error.
func recoverFunc(logger *slog.Logger) graphql.RecoverFunc {
	return func(ctx context.Context, p any) error {
		logger.ErrorContext(ctx, "graphql resolver panic",
			slog.String("request_id", httpserver.RequestID(ctx)),
			slog.String("panic", fmt.Sprint(p)),
			slog.String("stack", string(debug.Stack())))
		return errPanic
	}
}
