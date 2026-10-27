package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Session cookie names (D-047, D-095).
const (
	// SessionCookieName is used when server.base_url is https: the
	// __Host- prefix makes the browser require Secure, Path=/ and no
	// Domain, so no subdomain or plain-HTTP page can set or overwrite it.
	SessionCookieName = "__Host-bekup_session"
	// InsecureSessionCookieName is used only when server.base_url is plain
	// http (D-095): browsers refuse __Host- and Secure cookies there
	// (except on localhost).
	InsecureSessionCookieName = "bekup_session"
)

// CookiePolicy builds the session cookie for the configured transport.
type CookiePolicy struct {
	// Secure is true when server.base_url is https.
	Secure bool
}

// CookiePolicyFor returns the policy for base_url: Secure (and the __Host-
// name) unless the URL is plain http. An unparsable URL counts as https,
// the stricter choice (config validation rejects it anyway).
func CookiePolicyFor(baseURL string) CookiePolicy {
	u, err := url.Parse(baseURL)
	if err != nil {
		return CookiePolicy{Secure: true}
	}
	return CookiePolicy{Secure: !strings.EqualFold(u.Scheme, "http")}
}

// Name is the session cookie name for this transport.
func (c CookiePolicy) Name() string {
	if c.Secure {
		return SessionCookieName
	}
	return InsecureSessionCookieName
}

// Session returns the cookie carrying token: HttpOnly, SameSite=Strict,
// Path=/, Secure in https mode, no Domain, and no Expires/Max-Age (a
// browser-session cookie; the server enforces idle and absolute expiry).
func (c CookiePolicy) Session(token string) *http.Cookie {
	// Secure is false only for a plain-http base_url (D-095, browsers drop
	// Secure cookies there); HttpOnly and SameSite=Strict are always set.
	return &http.Cookie{ //nolint:gosec // G124: see above
		Name:     c.Name(),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   c.Secure,
		SameSite: http.SameSiteStrictMode,
	}
}

// Clear returns a cookie that deletes the session cookie.
func (c CookiePolicy) Clear() *http.Cookie {
	ck := c.Session("") //nolint:gosec // G124: same attributes as Session
	ck.MaxAge = -1
	return ck
}

// CookieSetter sets or clears the session cookie on the current response.
// The session middleware (internal/httpserver) puts one into every request
// context; login, logout and password changes use it.
type CookieSetter interface {
	SetSessionCookie(token string)
	ClearSessionCookie()
}

type cookieSetterKey struct{}

// WithCookieSetter returns ctx carrying s.
func WithCookieSetter(ctx context.Context, s CookieSetter) context.Context {
	return context.WithValue(ctx, cookieSetterKey{}, s)
}

// CookieSetterFrom returns the cookie setter of ctx.
func CookieSetterFrom(ctx context.Context) (CookieSetter, bool) {
	s, ok := ctx.Value(cookieSetterKey{}).(CookieSetter)
	return s, ok && s != nil
}

// MaxUserAgentRunes is how much of the User-Agent header a session keeps.
const MaxUserAgentRunes = 256

// RequestMeta are the client facts recorded on sessions and audit entries.
type RequestMeta struct {
	// IP is the client address resolved by the HTTP layer (trusted
	// proxies only).
	IP netip.Addr
	// UserAgent is the sanitized, truncated User-Agent header.
	UserAgent string
}

// NewRequestMeta returns meta for ip and a raw User-Agent header: control
// characters are dropped, invalid UTF-8 replaced and the value cut to
// MaxUserAgentRunes characters.
func NewRequestMeta(ip netip.Addr, userAgent string) RequestMeta {
	return RequestMeta{IP: ip.WithZone("").Unmap(), UserAgent: sanitizeUserAgent(userAgent)}
}

func sanitizeUserAgent(s string) string {
	s = strings.ToValidUTF8(s, "�")
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		if n == MaxUserAgentRunes {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

type requestMetaKey struct{}

// WithRequestMeta returns ctx carrying m.
func WithRequestMeta(ctx context.Context, m RequestMeta) context.Context {
	return context.WithValue(ctx, requestMetaKey{}, m)
}

// RequestMetaFrom returns the request meta of ctx (zero if none).
func RequestMetaFrom(ctx context.Context) RequestMeta {
	if m, ok := ctx.Value(requestMetaKey{}).(RequestMeta); ok {
		return m
	}
	return RequestMeta{}
}

// Session tokens: 256 random bits from crypto/rand, unpadded base64url in
// the cookie (43 characters). Only sha256 of the raw bytes is stored.
const (
	tokenBytes = 32
	tokenChars = 43
)

// newSessionToken returns a fresh token and its stored hash.
func newSessionToken() (string, []byte) {
	var b [tokenBytes]byte
	// crypto/rand.Read never returns an error (it panics instead).
	_, _ = rand.Read(b[:])
	sum := sha256.Sum256(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:]), sum[:]
}

// hashSessionToken returns the stored hash of token, or false when token is
// not a well-formed session token (no lookup is needed then).
func hashSessionToken(token string) ([]byte, bool) {
	if len(token) != tokenChars || !utf8.ValidString(token) {
		return nil, false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(b) != tokenBytes {
		return nil, false
	}
	sum := sha256.Sum256(b)
	return sum[:], true
}
