package httpserver

import (
	"errors"
	"net/http"
	"net/url"
	"time"
)

// IsWebSocketUpgrade reports whether r asks for a WebSocket upgrade
// (Connection: upgrade and Upgrade: websocket), the same test the Origin
// middleware uses.
func IsWebSocketUpgrade(r *http.Request) bool { return isWebSocketUpgrade(r) }

// OriginAllowed applies the Origin/Sec-Fetch-Site rule of the Origin
// middleware (D-102) to r. The WebSocket handler of internal/graph runs it
// again at the upgrade (defense in depth).
func OriginAllowed(r *http.Request, allowed []string) bool { return originAllowed(r, allowed) }

// AllowedOrigins returns the origins New accepts: the origin of baseURL
// and, in dev mode, DevOrigins.
func AllowedOrigins(baseURL string, dev bool) ([]string, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return nil, errors.New("httpserver: BaseURL must be an absolute http(s) URL")
	}
	origins := []string{originOf(base)}
	if dev {
		origins = append(origins, DevOrigins...)
	}
	return origins, nil
}

// LiftDeadlines removes the server's read and write deadlines from the
// connection of r (server.read_timeout/write_timeout apply per request; a
// WebSocket lives longer). Call it before the upgrade; the WebSocket's own
// keepalive then detects dead peers. A ResponseWriter without deadline
// support (http.ErrNotSupported, e.g. in tests) is not an error.
func LiftDeadlines(w http.ResponseWriter) error {
	rc := http.NewResponseController(w)
	var errs []error
	for _, err := range []error{rc.SetReadDeadline(time.Time{}), rc.SetWriteDeadline(time.Time{})} {
		if err != nil && !errors.Is(err, http.ErrNotSupported) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
