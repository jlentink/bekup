// Package externalapi is `bekup external-api` (D-220): the
// token-authenticated REST API that lets CI pipelines start a backup job's
// run and poll its result.
//
//   - NewHandler (handler.go) serves POST /api/v1/jobs/{jobId}/runs,
//     GET /api/v1/requests/{requestId} and GET /api/v1/self behind
//     httpserver.NewAPIServer (request IDs, the trusted-proxy client
//     address, access log, security headers, per-IP rate and body limits).
//     Tokens (internal/apikey) are accepted only from the Authorization
//     header; a query string is refused. Every authentication failure is
//     the same 401 with WWW-Authenticate; failures are throttled per client
//     IP (failedAuth, limits.go) and requests per token. Jobs and requests
//     outside a token's scope are 404, never 403.
//   - Store (store.go) is the only database access: the SECURITY DEFINER
//     api.* functions as bekup_external (DBStore). Each call authenticates
//     the token by id and secret hash and checks the client address
//     against the token's allowlist inside the database (D-220 amendment
//     1); this process holds no table privilege, no master key, no runner
//     certificate and never reads bekup.toml.
//   - Aggregate (state.go) turns the raw facts of a request (outcome,
//     River state, latest run by attempt) into the state CI scripts poll:
//     pending, rejected, queued, running, retrying, success, partial,
//     failed, canceled or skipped; done stays false while River holds a
//     retry and success is true only for success.
//   - Run (process.go) connects, refuses to start unless
//     db.CheckExternalRole and db.CheckExternalSchemaVersion pass, and
//     serves /healthz and /readyz (schema version per probe, the role's
//     privilege audit at most every minute) next to the API.
//
// Run requests are decided by the workers (internal/apiaccess.Consumer).
package externalapi
