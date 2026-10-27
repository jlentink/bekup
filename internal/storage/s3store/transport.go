package s3store

import (
	"io"
	"net/http"

	"github.com/jlentink/bekup/internal/netguard"
)

// Response body limits for everything but object downloads. A list page of
// 1000 keys of at most 1 KiB each is well below maxListBody.
const (
	maxErrorBody = 1 << 20
	maxListBody  = 16 << 20
)

// limitTransport caps response bodies so a hostile or broken endpoint
// cannot make the SDK buffer unbounded XML. Object downloads (GetObject,
// tagged by the SDK with x-id=GetObject) are streamed to the caller
// unlimited when successful.
type limitTransport struct {
	next http.RoundTripper
}

func (t limitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil || resp.Body == nil {
		return resp, err
	}
	switch {
	case resp.StatusCode >= 300:
		resp.Body = limitedBody{Reader: netguard.LimitReader(resp.Body, maxErrorBody), c: resp.Body}
	case req.Method == http.MethodGet && req.URL.Query().Get("x-id") == "GetObject":
		// Object content: unlimited.
	default:
		resp.Body = limitedBody{Reader: netguard.LimitReader(resp.Body, maxListBody), c: resp.Body}
	}
	return resp, nil
}

type limitedBody struct {
	io.Reader
	c io.Closer
}

func (b limitedBody) Close() error { return b.c.Close() }

// newHTTPClient wraps the guarded transport. Redirects are never followed.
func newHTTPClient(base *http.Client) *http.Client {
	return &http.Client{
		Transport:     limitTransport{next: base.Transport},
		CheckRedirect: base.CheckRedirect,
		Timeout:       base.Timeout,
	}
}
