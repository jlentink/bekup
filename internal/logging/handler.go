package logging

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"unicode"
)

// Redacted replaces secret values in log output.
const Redacted = "[REDACTED]"

// Handler is a slog.Handler that redacts secrets before passing records to
// the wrapped handler:
//
//   - attributes whose key looks secret (see IsSecretKey) get the value
//     Redacted, including inside groups;
//   - credentials in URLs (scheme://user:pass@host) and key=value DSNs
//     (password=...) are removed from string values;
//   - every value registered in the Registry is scrubbed from the message
//     and from string, error, []byte and other attribute values.
//
// Attributes added with WithAttrs are redacted at Handle time, so secrets
// registered later are scrubbed from them too.
type Handler struct {
	inner slog.Handler
	reg   *Registry
	ops   []handlerOp
}

type handlerOp struct {
	group string
	attrs []slog.Attr
}

var _ slog.Handler = (*Handler)(nil)

// NewHandler wraps inner with redaction using reg (nil: DefaultRegistry).
func NewHandler(inner slog.Handler, reg *Registry) *Handler {
	if reg == nil {
		reg = DefaultRegistry
	}
	return &Handler{inner: inner, reg: reg}
}

// Enabled implements slog.Handler.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// WithAttrs implements slog.Handler.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	return h.with(handlerOp{attrs: attrs})
}

// WithGroup implements slog.Handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return h.with(handlerOp{group: name})
}

func (h *Handler) with(op handlerOp) *Handler {
	ops := make([]handlerOp, len(h.ops), len(h.ops)+1)
	copy(ops, h.ops)
	return &Handler{inner: h.inner, reg: h.reg, ops: append(ops, op)}
}

// Handle implements slog.Handler.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	inner := h.inner
	for _, op := range h.ops {
		if op.group != "" {
			inner = inner.WithGroup(op.group)
			continue
		}
		inner = inner.WithAttrs(h.redactAttrs(op.attrs))
	}
	out := slog.NewRecord(r.Time, r.Level, h.scrubString(r.Message), r.PC)
	attrs := make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	out.AddAttrs(h.redactAttrs(attrs)...)
	return inner.Handle(ctx, out)
}

func (h *Handler) redactAttrs(attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, h.redactAttr(a))
	}
	return out
}

func (h *Handler) redactAttr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	if IsSecretKey(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	switch v.Kind() {
	case slog.KindGroup:
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(h.redactAttrs(v.Group())...)}
	case slog.KindString:
		return slog.String(a.Key, h.scrubString(v.String()))
	case slog.KindAny:
		return slog.Attr{Key: a.Key, Value: h.redactAny(v)}
	default:
		return slog.Attr{Key: a.Key, Value: v}
	}
}

func (h *Handler) redactAny(v slog.Value) slog.Value {
	switch t := v.Any().(type) {
	case error:
		return slog.StringValue(h.scrubString(t.Error()))
	case []byte:
		s := string(t)
		if scrubbed := h.scrubString(s); scrubbed != s {
			return slog.StringValue(scrubbed)
		}
		return v
	default:
		s := fmt.Sprintf("%+v", t)
		if scrubbed := h.scrubString(s); scrubbed != s {
			return slog.StringValue(scrubbed)
		}
		return v
	}
}

func (h *Handler) scrubString(s string) string {
	return h.reg.Scrub(RedactCredentials(s))
}

var (
	// urlCredsRE matches scheme://user:password@ where the password runs up
	// to the LAST '@' of the URL token (passwords may contain unescaped '@',
	// ':' or '/'). The token ends at whitespace, quotes or angle brackets, so
	// an '@' later in the same token (e.g. in the query) is treated as part
	// of the password: over-redaction is the safe direction.
	urlCredsRE = regexp.MustCompile("([A-Za-z][A-Za-z0-9+.\\-]*://[^:/@\\s\"'<>`]*):[^\\s\"'<>`]*@")
	// kvCredsRE matches secret parameters in libpq key=value DSNs and URL
	// query strings (?sslpassword=...&): passwords, secrets, tokens and the
	// libpq key-material/credential-file parameters (sslkey, sslcert,
	// sslpassword, passfile). Quoted values may contain escaped quotes.
	kvCredsRE = regexp.MustCompile(`(?i)\b([A-Za-z0-9_.\-]*(?:password|passwd|pwd|secret|token)|sslkey|sslcert|passfile)(\s*=\s*)('(?:[^'\\]|\\.|'')*'|"(?:[^"\\]|\\.)*"|[^\s&;]+)`)
)

// RedactCredentials removes passwords from URLs (scheme://user:pass@host),
// and secret parameters from key=value connection strings and URL query
// strings (password=..., sslpassword=..., sslkey=..., sslcert=...,
// passfile=..., *_secret=..., *_token=...).
func RedactCredentials(s string) string {
	if strings.Contains(s, "@") {
		s = urlCredsRE.ReplaceAllString(s, "${1}:"+Redacted+"@")
	}
	if strings.Contains(s, "=") {
		s = kvCredsRE.ReplaceAllString(s, "${1}${2}"+Redacted)
	}
	return s
}

// secretTokens are key words that mark an attribute as secret.
var secretTokens = map[string]bool{
	"password": true, "passwd": true, "pass": true, "passphrase": true,
	"secret": true, "secrets": true, "token": true, "tokens": true,
	"key": true, "keys": true, "apikey": true, "privkey": true, "private": true,
	"authorization": true, "cookie": true, "cookies": true, "dsn": true,
	"credential": true, "credentials": true, "creds": true,
	"otp": true, "totp": true,
}

// secretSubstrings catch compound words without separators.
var secretSubstrings = []string{
	"password", "passwd", "passphrase", "secret", "token", "apikey",
	"privatekey", "authorization", "cookie", "credential", "connstr",
	"connectionstring", "databaseurl", "dburl",
}

// safeSuffixes are final key words that describe metadata about a secret
// rather than the secret itself (master_key_file, key_id, token_ttl).
var safeSuffixes = map[string]bool{
	"file": true, "path": true, "id": true, "ids": true, "fingerprint": true,
	"version": true, "count": true, "len": true, "length": true, "name": true,
	"type": true, "ttl": true, "age": true, "expiry": true, "expires": true,
	"at": true, "configured": true, "set": true, "present": true,
}

// nonSecretKeys are exact attribute names (as snake_case words) whose
// "key" is an identifier or a flag, not key material: catalog release
// series and image keys of the engine catalog, and the booleans that
// record a public SSH host-key pin being cleared or changed (audit
// details). Matching is on the complete name, so "version_key" is plain
// while "version_private_key", "key", "host_key" or "image_key_pem" stay
// redacted; add a name here only for values that are never secret. Used by
// the log handler and the audit redactor (both call IsSecretKey).
var nonSecretKeys = map[string]bool{
	"version_key":        true,
	"source_version_key": true,
	"target_version_key": true,
	"client_version_key": true,
	"image_key":          true,
	"catalog_key":        true,
	"host_key_cleared":   true,
	"host_key_changed":   true,
	"host_key_confirmed": true,
}

// IsSecretKey reports whether an attribute key names a secret value. Keys
// are split into words on separators and camelCase boundaries; a key is
// secret when any word is a secret word (password, secret, token, key, ...)
// or contains one, unless the last word only describes the secret (for
// example master_key_file, key_id or token_ttl) or the whole name is a
// known identifier (nonSecretKeys: version_key, image_key). Keys naming
// connection strings (dsn, database_url, connection_string) are secret as
// well.
func IsSecretKey(key string) bool {
	words := splitWords(key)
	if len(words) == 0 {
		return false
	}
	if nonSecretKeys[strings.Join(words, "_")] {
		return false
	}
	joined := strings.Join(words, "")
	switch joined {
	case "databaseurl", "dburl", "connectionstring", "connstring", "connstr", "dsn":
		return true
	}
	last := words[len(words)-1]
	secret := false
	for _, w := range words {
		if secretTokens[w] {
			secret = true
			break
		}
		for _, sub := range secretSubstrings {
			if strings.Contains(w, sub) {
				secret = true
				break
			}
		}
	}
	if !secret {
		return false
	}
	return !safeSuffixes[last] || len(words) == 1
}

func splitWords(key string) []string {
	var words []string
	var cur bytes.Buffer
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}
	var prev rune
	for _, r := range key {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)):
			flush()
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
		prev = r
	}
	flush()
	return words
}
