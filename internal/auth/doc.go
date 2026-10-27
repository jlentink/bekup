// Package auth implements authentication: argon2id passwords, server-side
// sessions with AAL and step-up tracking, lockout, TOTP, WebAuthn passkeys,
// recovery codes and the configurable 2FA policy.
//
// Implemented so far:
//
//   - Principal (task 1.4): the authenticated caller that the session
//     middleware (internal/httpserver) attaches to the request context and
//     that internal/authz authorizes.
//   - Hasher (task 1.5): argon2id in the PHC string format with
//     configurable parameters (never below MinArgon2Params), constant-time
//     verification, rehash detection, a bounded semaphore (ErrBusy) and a
//     dummy verification for timing-safe failures.
//   - PasswordPolicy: length bounds, the embedded common-password list
//     (common_passwords.txt, SecLists, MIT; see NOTICE), account strings
//     and a zxcvbn strength score.
//   - Sessions: 256-bit tokens of which only sha256 is stored, idle and
//     absolute expiry with a throttled sliding refresh, rotation
//     (RotateTx, ElevateAAL), step-up marks, revocation and listing.
//     CookiePolicy builds the __Host- (https) or plain (http, D-095)
//     session cookie.
//   - Service: login (per-IP and per-account limiters, per-account lockout
//     with backoff whose attempts are reserved atomically before argon2id
//     runs, generic errors, audit with aggregated refusals while locked;
//     D-117), logout, password change (revokes every session), own-session
//     listing and revocation.
//   - UserAdmin (task 1.6, user_admin.go): admin and break-glass CLI
//     account management (temporary passwords, role/disable/delete with
//     session revocation, the last-active-admin invariant, lost-factor
//     reset of all second factors, audit). The first admin is created only
//     with `bekup admin create` (D-113).
//   - SecondFactors (factors.go): the registry of FactorCheckers (one per
//     factor kind: TOTP here, passkeys in internal/auth/passkey, task 1.8)
//     that implements authz.SecondFactors, and FactorRemovalGuard, the
//     last-factor hook of task 1.9's 2FA policy.
//
// TOTP, passkeys, recovery codes and the 2FA policy follow in tasks 1.7 to
// 1.9 (they complete LoginResult.SecondFactorRequired and call
// Sessions.ElevateAAL).
package auth
