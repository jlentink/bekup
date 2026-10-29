package webhook

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/jlentink/bekup/internal/hostname"
	"github.com/jlentink/bekup/internal/netguard"
)

// MaxURLLength caps a webhook URL in bytes.
const MaxURLLength = 2048

// URL validation errors. Their messages are safe to show to the user who
// entered the URL; they never echo the URL or a resolved address.
var (
	ErrInvalidURL = errors.New("invalid webhook URL")
	ErrURLTooLong = errors.New("webhook URL is too long")
	// ErrInsecureScheme: the URL is not https (http is accepted in dev
	// mode only).
	ErrInsecureScheme = errors.New("webhook URL must use https")
	ErrUserinfo       = errors.New("webhook URL must not contain a user name or password")
	ErrFragment       = errors.New("webhook URL must not contain a fragment")
	// ErrHostNotAllowed: the host resolves to an address the SSRF guard
	// refuses. It wraps netguard.ErrNotAllowed.
	ErrHostNotAllowed = fmt.Errorf("webhook host is not allowed: %w", netguard.ErrNotAllowed)
	// ErrHostUnreachable: the host could not be resolved. It wraps
	// netguard.ErrUnreachable.
	ErrHostUnreachable = fmt.Errorf("webhook host could not be resolved: %w", netguard.ErrUnreachable)
)

// Resolver checks a host at input time; *netguard.Guard implements it.
type Resolver interface {
	ResolveAndCheck(ctx context.Context, host string) ([]netip.Addr, error)
}

// URLOptions configures ValidateURL.
type URLOptions struct {
	// AllowHTTP accepts plain http URLs. Set it only in dev mode
	// (dev.enabled); every accepted http URL logs a warning.
	AllowHTTP bool
	// Logger receives the dev-mode warning; nil means slog.Default().
	Logger *slog.Logger
}

// ValidateURL checks a webhook URL when it is entered or changed: https
// only (http in dev mode only), an absolute URL with a host, no userinfo,
// no fragment, at most MaxURLLength bytes, and a host whose every address
// passes the SSRF guard now (the guard checks again at every dial). It
// returns the parsed URL.
func ValidateURL(ctx context.Context, raw string, guard Resolver, opts URLOptions) (*url.URL, error) {
	u, err := parseURL(raw, opts.AllowHTTP)
	if err != nil {
		return nil, err
	}
	if guard == nil {
		return nil, errors.New("webhook: no resolver")
	}
	if _, err := guard.ResolveAndCheck(ctx, u.Hostname()); err != nil {
		switch nerr := netguard.Normalize(err); {
		case errors.Is(nerr, context.Canceled):
			return nil, context.Canceled
		case errors.Is(nerr, netguard.ErrNotAllowed):
			return nil, ErrHostNotAllowed
		default:
			return nil, ErrHostUnreachable
		}
	}
	if u.Scheme == "http" {
		logger := opts.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.WarnContext(ctx, "webhook_insecure_http_url_accepted",
			slog.String("reason", "dev mode allows plain http webhook URLs"))
	}
	return u, nil
}

// parseURL applies the syntactic rules of ValidateURL without resolving
// the host; Send uses it again at delivery time.
func parseURL(raw string, allowHTTP bool) (*url.URL, error) {
	if len(raw) > MaxURLLength {
		return nil, ErrURLTooLong
	}
	if raw == "" {
		return nil, ErrInvalidURL
	}
	for i := range len(raw) {
		if c := raw[i]; c <= 0x20 || c >= 0x7f {
			// Only printable ASCII: no spaces, control characters or raw
			// Unicode (IDNs must be entered in punycode).
			return nil, ErrInvalidURL
		}
	}
	if strings.Contains(raw, "#") {
		return nil, ErrFragment
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, ErrInvalidURL
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !allowHTTP {
			return nil, ErrInsecureScheme
		}
	default:
		return nil, ErrInsecureScheme
	}
	if u.Opaque != "" || u.Host == "" {
		return nil, ErrInvalidURL
	}
	if u.User != nil || strings.Contains(u.Host, "@") {
		return nil, ErrUserinfo
	}
	if hostname.Validate(hostname.Normalize(u.Hostname())) != nil {
		// One host-name rule for every component (D-140): no IP-like
		// names such as "2130706433", no underscores, no zones.
		return nil, ErrInvalidURL
	}
	if p := u.Port(); p != "" {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil || n == 0 {
			return nil, ErrInvalidURL
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return nil, ErrInvalidURL
	}
	return u, nil
}

// DisplayURL returns a form of a webhook URL that is safe to show and log:
// scheme, host and port only, with the path and query (which often carry
// a bearer credential) replaced by "/…". Unparsable input yields "".
func DisplayURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	out := u.Scheme + "://" + u.Host
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		out += "/…"
	}
	return out
}
