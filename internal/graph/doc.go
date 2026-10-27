// Package graph is the GraphQL API of `bekup serve` (task 1.2, D-050,
// D-105): the gqlgen schema (schema/*.graphqls), generated executor
// (generated/), models and scalars (model/), resolvers (resolvers/) and the
// hardened /graphql handler (New).
//
// # Transport
//
// Only POST with Content-Type application/json is accepted (405 for other
// methods, 415 for other content types, 413 above graphql.max_body_bytes,
// 400 for anything but one JSON request object). There is no GET,
// multipart, form-encoded or persisted-query support. internal/httpserver
// applies the Origin/Sec-Fetch-Site check, rate limit and body limit before
// this handler. Subscriptions use a WebSocket on GET /graphql (task 1.10,
// D-108, ws.go): graphql-transport-ws only, the cookie session and an exact
// Origin at the upgrade, connection caps, only subscriptions over the
// socket; internal/graph/wsconn watches the session and provides the
// delivery-time authorization helpers for subscription resolvers.
//
// # Limits
//
// graphql.max_depth (depthLimit), graphql.max_complexity (gqlgen's
// FixedComplexityLimit, default complexity 1 per field), a parser token
// limit and an LRU cache of parsed documents. Introspection is allowed only
// when Access.IntrospectionAllowed (unrestricted authenticated principals)
// or in dev mode; field suggestions are off outside dev mode. Root fields in
// SingleRootFields (credential checks and argon2id) must be the only root
// field of their operation, and every further root field is charged to
// the per-user rate limit (rootFieldGate, D-117).
//
// # Access control (convention for tasks 1.4, 1.5 and 1.9)
//
// Deny by default. Every root field (Query, Mutation, Subscription) must
// carry @authenticated, @hasRole(roles: [...]) or @stepUp, or be listed in
// PublicRootFields, and every guarded root field must map to an
// internal/authz Operation in RootFieldOperations (D-106). New refuses to
// start otherwise (CheckAccessPolicy), and a root-field middleware denies
// unmapped fields at runtime and runs Access.CheckOperation (the policy
// precheck) before the field resolves. PolicyAccess implements Access on
// top of internal/authz and the auth.Principal in the request context
// (attached by the session middleware, task 1.5); without a principal
// everything answers UNAUTHENTICATED. Use @hasRole for role checks (it
// implies @authenticated) and add @stepUp after it for sensitive
// operations; per-object ownership is checked in the resolver with
// authz.Policy.Authorize and the job loaded from the database. Nested
// fields inherit the access of their root field; a nested field that needs
// stricter access gets its own directive.
//
// # Errors
//
// Resolvers return *apierror.Error for user-facing errors; the code is in
// extensions.code (UNAUTHENTICATED, FORBIDDEN, STEP_UP_REQUIRED,
// REQUIRES_2FA_ENROLLMENT, VALIDATION, NOT_FOUND, RATE_LIMITED, CONFLICT,
// PASSWORD_CHANGE_REQUIRED).
// Every other error and every panic is masked as "internal error" with code
// INTERNAL and extensions.requestId; the real error is logged with the same
// request ID. Resolvers must not build gqlerror values themselves.
package graph
