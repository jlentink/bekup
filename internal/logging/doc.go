// Package logging builds bekup's log/slog loggers. Every logger goes through
// the redaction Handler, which replaces the values of secret-looking
// attribute keys (password, secret, token, key, passphrase, authorization,
// cookie, dsn, ...), strips credentials from URLs and scrubs every secret
// value registered in a Registry from messages and attributes (D-060).
package logging
