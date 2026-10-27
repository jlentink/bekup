package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// healthz is the liveness probe: the process serves HTTP. No I/O.
func healthz(w http.ResponseWriter, _ *http.Request) {
	writeText(w, http.StatusOK, "ok\n")
}

// readyz is the readiness probe. The reason for a failure is logged, never
// returned to the client.
func readyz(ready ReadyFunc, timeout time.Duration, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		if err := ready(ctx); err != nil {
			logger.LogAttrs(r.Context(), slog.LevelWarn, "readiness check failed",
				slog.Any("error", err), slog.String("request_id", RequestID(r.Context())))
			writeText(w, http.StatusServiceUnavailable, "not ready\n")
			return
		}
		writeText(w, http.StatusOK, "ok\n")
	}
}
