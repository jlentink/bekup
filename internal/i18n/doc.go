// Package i18n holds bekup's languages on the server side (D-235, D-236):
// the Locale type, the stored per-account choice (users.locale) and the
// request language parsed from Accept-Language. The web UI has its own
// tables (web/src/lib/i18n.ts); this package only decides which language
// a server-rendered text (task 8.3: e-mails, notification contents,
// GraphQL messages) uses.
package i18n
