// Package egress manages the runtime (admin) part of the SSRF allowlist
// of the dial-time guards (internal/netguard, D-120, D-196, D-198).
//
// The effective allowlist is the bootstrap list of [netguard] in
// bekup.toml plus the admin list stored in settings under SettingKey.
// Service.Update validates the admin list with the guard's own rules (no
// /0, nothing overlapping an always-denied range, loopback only in dev
// mode, host names exact or "*.suffix"), stores it with an audit entry
// (setting.changed; a widening change raises a security.system alert)
// and a settings event on pubsub.SettingsEvents in one transaction, then
// applies it with netguard.Set.SetAllowlist. Every entry names the
// scopes it widens (destinations, smtp, shared_webhooks,
// personal_webhooks); entries without scopes and version-1 lists get every
// scope except personal_webhooks. Every process (serve and
// `bekup worker`) runs a Sync that applies the stored list at startup, on
// every settings event (identifiers only: the payload is the settings
// key) and periodically, so all instances converge even when a
// notification is lost.
package egress
