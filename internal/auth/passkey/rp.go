package passkey

import (
	"net"
	"net/url"
	"strings"
)

// DevOrigin is the Vite dev server origin, accepted as an additional
// WebAuthn origin in dev mode when the RP ID is localhost (localhost is a
// secure context, so passkeys work there over plain http).
const DevOrigin = "http://localhost:5173"

// Reasons why passkeys are unavailable (Status.Reason). They are shown to
// users as is.
const (
	ReasonInsecureContext = "Passkeys need a secure context: server.base_url must use https (or localhost). " +
		"Use an authenticator app (TOTP) instead, or put bekup behind the TLS reverse proxy."
	ReasonIPHost     = "Passkeys need a domain name: server.base_url uses an IP address, which cannot be a WebAuthn relying party ID."
	ReasonInvalidURL = "Passkeys are unavailable: server.base_url is not a valid URL."
)

// RelyingParty is the WebAuthn relying party derived from server.base_url.
type RelyingParty struct {
	// Available reports whether passkeys can be used at all.
	Available bool
	// Reason explains why not (empty when Available).
	Reason string
	// ID is the RP ID: the base URL's host name, lowercase, without port.
	ID string
	// Origins are the accepted origins: the base URL's origin, plus
	// DevOrigin in dev mode for a localhost RP ID.
	Origins []string
}

// RelyingPartyFor derives the relying party from base (server.base_url).
// Passkeys need a secure context (D-095): an https base URL, or http on
// localhost. Over plain http on any other host, and for IP-address hosts
// (not allowed as RP IDs), passkeys are unavailable.
func RelyingPartyFor(base string, dev bool) RelyingParty {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return RelyingParty{Reason: ReasonInvalidURL}
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return RelyingParty{Reason: ReasonInvalidURL}
	}
	localhost := host == "localhost" || strings.HasSuffix(host, ".localhost")
	switch {
	case scheme == "https":
	case scheme == "http" && localhost:
	case scheme == "http":
		return RelyingParty{Reason: ReasonInsecureContext}
	default:
		return RelyingParty{Reason: ReasonInvalidURL}
	}
	if net.ParseIP(host) != nil || strings.Contains(host, ":") {
		return RelyingParty{Reason: ReasonIPHost}
	}
	origin := scheme + "://" + strings.ToLower(u.Host)
	if p := u.Port(); (scheme == "https" && p == "443") || (scheme == "http" && p == "80") {
		origin = scheme + "://" + host
	}
	origins := []string{origin}
	if dev && host == "localhost" && origin != DevOrigin {
		origins = append(origins, DevOrigin)
	}
	return RelyingParty{Available: true, ID: host, Origins: origins}
}
