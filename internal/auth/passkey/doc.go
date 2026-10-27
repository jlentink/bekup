// Package passkey implements WebAuthn passkeys as a second factor (task
// 1.8, D-109) on top of github.com/go-webauthn/webauthn.
//
// The relying party comes from server.base_url (RelyingPartyFor): the RP
// ID is its host name and the only origin its origin (plus the Vite dev
// origin in dev mode on localhost). Passkeys need a secure context, so an
// http base URL on any host but localhost, or an IP-address host, makes
// them unavailable (D-095); Status reports why and every ceremony fails
// with ErrUnavailable.
//
// Ceremony state (challenge, user handle, allowed passkeys, user
// verification, expiry) is kept server-side in webauthn_ceremonies, bound
// to the session that began it, valid for CeremonyTimeout and consumed by
// the finish step whatever its outcome. Registration (BeginRegistration,
// FinishRegistration) and management (Rename, Delete) need a recent
// step-up. The assertion API (BeginAssertion, FinishAssertion) is used by
// the two-step login and step-up of task 1.9; it refuses signature counter
// regressions (possible cloned authenticators). Every change and every
// assertion outcome is audited; registrations, removals and counter
// anomalies are reported to a Notifier (security alerts, task 5.5).
//
// Service implements auth.FactorChecker, so auth.SecondFactors sees
// enrolled passkeys.
package passkey
