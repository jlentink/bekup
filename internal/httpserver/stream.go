package httpserver

import (
	"errors"
	"net/http"
	"net/url"
	"time"
)

// OriginAllowed applies the Origin/Sec-Fetch-Site rule of the Origin
// middleware (D-102) to r. The event stream handler of internal/graph runs
// it again before a stream opens (defense in depth).
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
// connection of r (server.read_timeout/write_timeout apply per request; an
// event stream lives longer, and an expired read deadline would cancel
// the request context of an HTTP/1.1 stream). Call it once the request
// body is read; the stream then bounds each write itself and its
// keepalive detects dead peers. A ResponseWriter without deadline support
// (http.ErrNotSupported, e.g. in tests) is not an error.
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
