// Package apiaccess is bekup's own side of the external CI API (D-220):
// what the workers do with the run requests that `bekup external-api`
// files through the api.* functions as bekup_external.
//
//   - Consumer (consumer.go) decides pending run requests. It runs where
//     the River workers run (serve without --no-worker, bekup worker),
//     wakes up on the bekup_api_requests channel and sweeps every 5 s. A
//     notification is only a wake-up: its payload is ignored and the
//     consumer claims pending rows itself (FOR UPDATE SKIP LOCKED, one
//     request per transaction, safe with several instances), so a forged
//     NOTIFY costs at most one extra claim query. Each request is
//     re-authorized by the SQL scope rule inside its transaction
//     (jobs.Service.RunFromAPI); requests older than 10 minutes are
//     rejected as expired. It also processes the access denials the api
//     functions record when a valid token is presented from an address
//     outside its allowlist (D-220 amendment 1; same wake-up, payload
//     "denial:<id>", likewise ignored): one transaction each audits
//     api_token.ip_denied, whose audit observer raises the
//     api_token_ip_denied security alert to the admins and the token's
//     creator, and marks the denial processed.
//   - PruneWorker (prune.go) deletes decided requests and processed
//     denials after 30 days (periodic River job, maintenance queue).
//   - Service (service.go, addresses.go) is token management for the
//     GraphQL API (serve): createApiToken generates the token with
//     internal/apikey, stores only its hash and returns the plaintext once
//     (never logged or stored); apiTokens lists what the caller may see
//     (admins every token, backup owners the tokens covering their jobs,
//     other jobs of a scope only counted); revokeApiToken and
//     updateApiTokenAddresses. Every call authorizes its caller with the
//     policy (api_token.*: admins, or backup owners for the jobs they own;
//     never maintainers; changes need a fresh step-up) and audits
//     api_token.created|revoked|addresses_changed in the change's
//     transaction, whose observer raises the api_token_created,
//     api_token_any_address and api_token_addresses_widened security
//     alerts (a widening also to the token's creator, naming the actor).
//     ParseAddresses mirrors the database CHECKs of the client address
//     allowlist (no IPv4 network wider than /8, no IPv6 network wider than
//     /32) with presentable field errors.
package apiaccess
