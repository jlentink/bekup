// Package configreload re-reads bekup.toml at runtime and applies the keys
// that are safe to change without a restart (D-200, task 6.13).
//
// A Reloader belongs to one process (serve or `bekup worker`). Reload:
//
//  1. loads the file with the startup load options, the config path pinned
//     to the absolute path loaded at startup (same environment, flags and
//     D-041 file checks; a new ./bekup.toml can never shadow it);
//  2. diffs it against the running config and splits the changed keys into
//     live keys (config tag reload:"live") and restart-only keys;
//  3. re-validates the running config with only the live keys taken from
//     the file (config.WithLive) under the startup dev mode, so a file that
//     turns dev mode on cannot get its live keys validated with relaxed
//     rules;
//  4. prepares every Applier whose keys changed (a failure commits
//     nothing);
//  5. names the applied keys whose change weakens security (Weakened; a
//     test requires a rule for every live key);
//  6. records the outcome fail closed: the audit entry (config.reloaded or
//     config.reload_failed, key names only) and the result row in one
//     transaction; if that fails nothing is applied;
//  7. commits the appliers (atomic stores only) and swaps the running
//     config.
//
// An invalid or unsafe file never replaces the running config. Results,
// audit entries, logs of outcomes and the API carry key names and an error
// kind only: never values and never the file path. The full load error
// goes to the redacting log only.
//
// Triggers: the admin API (API.Request: a request row, an audit entry and
// a NOTIFY carrying only the request id on pubsub.SettingsEvents in one
// transaction; every process's Sync acts on it only when a matching recent
// "ui" request row exists, so a forged NOTIFY does nothing) and SIGHUP on
// one process (a "signal" request row, never broadcast).
//
// To add a live key: tag it reload:"live" in internal/config, give it a
// weakening rule in weaken.go and cover it with an Applier in
// internal/app (or declare it unused in that process). New refuses a
// Reloader whose appliers do not cover every live key exactly once.
package configreload
