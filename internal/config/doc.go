// Package config loads and validates bekup.toml (serve/worker) and
// runner.toml (runner).
//
// Files are located via --config / BEKUP_CONFIG (a missing explicit path is a
// hard error), then ./<name>, then /etc/bekup/<name>; the first match wins and
// files are never merged. Every file passes ownership and permission checks
// and is parsed strictly with go-toml/v2 (unknown keys are errors with
// line/column). Values are layered as defaults < file < BEKUP_<SECTION>_<KEY>
// env < CLI flags, with the source of every key recorded. Secrets are only
// accepted as *_file paths (read with ReadSecretFile); inline secrets are a
// dev-mode-only escape hatch. Validation reports all errors at once.
//
// See docs/configuration.md and D-019, D-041, D-073.
package config
