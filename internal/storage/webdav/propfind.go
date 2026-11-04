package webdav

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
)

// PROPFIND parsing limits (hostile servers): the body is bounded by
// maxPropfindBody, the number of <response> elements by maxEntries and the
// nesting outside them by maxXMLDepth; encoding/xml resolves no external
// entities and no DTDs, and refuses other charsets than UTF-8.
const (
	maxEntries  = 100000
	maxXMLDepth = 32
	maxHrefLen  = 8 << 10
)

// errBadMultistatus: the server's PROPFIND answer is malformed or too
// large.
var errBadMultistatus = errors.New("webdav: malformed PROPFIND answer")

// propfindBody asks for the properties bekup uses only (allprop is
// expensive on Nextcloud and may include large dead properties).
const propfindBody = `<?xml version="1.0" encoding="utf-8"?>` +
	`<d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><d:getcontentlength/>` +
	`<d:getlastmodified/><d:getetag/></d:prop></d:propfind>`

// principalBody asks for the current user's principal (Nextcloud user id).
const principalBody = `<?xml version="1.0" encoding="utf-8"?>` +
	`<d:propfind xmlns:d="DAV:"><d:prop><d:current-user-principal/></d:prop></d:propfind>`

// entry is one resource of a PROPFIND answer.
type entry struct {
	// segs are the decoded path segments of the href.
	segs  []string
	coll  bool
	size  int64 // -1 when not reported
	mod   time.Time
	etag  string
	princ string // current-user-principal href
}

func (e *entry) name() string {
	if len(e.segs) == 0 {
		return ""
	}
	return e.segs[len(e.segs)-1]
}

type xmlProp struct {
	ResourceType struct {
		Collection *struct{} `xml:"DAV: collection"`
	} `xml:"DAV: resourcetype"`
	ContentLength string `xml:"DAV: getcontentlength"`
	LastModified  string `xml:"DAV: getlastmodified"`
	ETag          string `xml:"DAV: getetag"`
	Principal     struct {
		Href string `xml:"DAV: href"`
	} `xml:"DAV: current-user-principal"`
}

type xmlResponse struct {
	Hrefs    []string `xml:"DAV: href"`
	Status   string   `xml:"DAV: status"`
	Propstat []struct {
		Prop   xmlProp `xml:"DAV: prop"`
		Status string  `xml:"DAV: status"`
	} `xml:"DAV: propstat"`
}

// parseMultistatus reads a 207 answer. Responses whose status is not 2xx
// are skipped; only the properties of 2xx propstats are used.
func parseMultistatus(r io.Reader) ([]entry, error) {
	dec := xml.NewDecoder(netguard.LimitReader(r, maxPropfindBody))
	dec.Strict = true
	var (
		out   []entry
		depth int
		root  bool
	)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if errors.Is(err, netguard.ErrResponseTooLarge) {
				return nil, fmt.Errorf("%w: answer too large", errBadMultistatus)
			}
			return nil, fmt.Errorf("%w: %w", errBadMultistatus, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth > maxXMLDepth {
				return nil, fmt.Errorf("%w: nested too deeply", errBadMultistatus)
			}
			switch {
			case depth == 1:
				if t.Name.Space != "DAV:" || t.Name.Local != "multistatus" {
					return nil, fmt.Errorf("%w: not a multistatus", errBadMultistatus)
				}
				root = true
			case depth == 2 && t.Name.Space == "DAV:" && t.Name.Local == "response":
				if len(out) >= maxEntries {
					return nil, fmt.Errorf("%w: too many entries", errBadMultistatus)
				}
				var x xmlResponse
				if err := dec.DecodeElement(&x, &t); err != nil {
					if errors.Is(err, netguard.ErrResponseTooLarge) {
						return nil, fmt.Errorf("%w: answer too large", errBadMultistatus)
					}
					return nil, fmt.Errorf("%w: %w", errBadMultistatus, err)
				}
				depth--
				if e, ok := toEntry(&x); ok {
					out = append(out, e)
				}
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			// A DTD; encoding/xml does not process it, but nothing
			// legitimate sends one.
			return nil, fmt.Errorf("%w: unexpected directive", errBadMultistatus)
		}
	}
	if !root {
		return nil, fmt.Errorf("%w: empty answer", errBadMultistatus)
	}
	return out, nil
}

// statusOK reports a "HTTP/1.1 2xx ..." status line (an empty one counts as
// OK: the status is optional on the response level).
func statusOK(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return true
	}
	f := strings.Fields(line)
	if len(f) < 2 {
		return false
	}
	n, err := strconv.Atoi(f[1])
	return err == nil && n >= 200 && n < 300
}

func toEntry(x *xmlResponse) (entry, bool) {
	if len(x.Hrefs) != 1 || len(x.Hrefs[0]) > maxHrefLen || !statusOK(x.Status) {
		return entry{}, false
	}
	segs, ok := hrefSegments(x.Hrefs[0])
	if !ok {
		return entry{}, false
	}
	e := entry{segs: segs, size: -1}
	for i := range x.Propstat {
		ps := &x.Propstat[i]
		if !statusOK(ps.Status) {
			continue
		}
		p := &ps.Prop
		if p.ResourceType.Collection != nil {
			e.coll = true
		}
		if v := strings.TrimSpace(p.ContentLength); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
				e.size = n
			}
		}
		if v := strings.TrimSpace(p.LastModified); v != "" {
			if t, err := http.ParseTime(v); err == nil {
				e.mod = t
			}
		}
		if v := strings.TrimSpace(p.ETag); v != "" && len(v) <= 256 && !strings.ContainsAny(v, "\r\n") {
			e.etag = v
		}
		if v := strings.TrimSpace(p.Principal.Href); v != "" {
			e.princ = v
		}
	}
	return e, true
}

// hrefSegments decodes an href (absolute URL or absolute path) into path
// segments. Hrefs with "." or ".." segments, empty segments, encoded
// slashes or control characters are refused.
func hrefSegments(href string) ([]string, bool) {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil || u.Opaque != "" {
		return nil, false
	}
	p := u.EscapedPath()
	if !strings.HasPrefix(p, "/") {
		return nil, false
	}
	segs, err := splitURLPath(p)
	if err != nil {
		return nil, false
	}
	return segs, true
}

// children returns the entries directly below the resource segs (the
// resource itself and anything else are dropped) and the resource's own
// entry (nil when the answer did not include it).
func children(entries []entry, segs []string) ([]entry, *entry) {
	var (
		out  []entry
		self *entry
	)
	for i := range entries {
		e := &entries[i]
		switch {
		case slices.Equal(e.segs, segs):
			if self == nil {
				self = e
			}
		case len(e.segs) == len(segs)+1 && slices.Equal(e.segs[:len(segs)], segs):
			out = append(out, *e)
		}
	}
	return out, self
}

// errListingMismatch: a Depth 1 answer had entries, but none of them is
// the requested collection or one of its members, even after letter case
// and Unicode normalization (NFC).
var errListingMismatch = errors.New("webdav: the folder listing names neither the folder nor anything in it")

// listChildren is children for a folder listing (Depth 1), tolerant of
// servers that spell the requested path differently in their hrefs: a
// case-insensitive server (another letter case) or one that stores names
// decomposed (NFD, macOS-backed shares) while bekup asked in NFC. The
// exact match is tried first; then the segments are compared NFC-
// normalized and case-folded; a single entry is the resource itself (a
// Depth 1 answer always includes it). Entries that match nothing are an
// error, never an empty listing: an empty listing would hide every
// object (retention, scans and listings would see nothing).
func listChildren(entries []entry, segs []string) ([]entry, *entry, error) {
	kids, self := children(entries, segs)
	if self != nil || len(kids) > 0 || len(entries) == 0 {
		return kids, self, nil
	}
	want := make([]string, len(segs))
	for i, s := range segs {
		want[i] = foldSegment(s)
	}
	folded := func(e *entry, n int) bool {
		for i := range n {
			if foldSegment(e.segs[i]) != want[i] {
				return false
			}
		}
		return true
	}
	for i := range entries {
		e := &entries[i]
		switch {
		case len(e.segs) == len(segs) && folded(e, len(segs)):
			if self == nil {
				self = e
			}
		case len(e.segs) == len(segs)+1 && folded(e, len(segs)):
			kids = append(kids, *e)
		}
	}
	if self == nil && len(kids) == 0 && len(entries) == 1 {
		self = &entries[0]
	}
	if self == nil && len(kids) == 0 {
		return nil, nil, errListingMismatch
	}
	return kids, self, nil
}

// foldSegment is the comparison form of a path segment: NFC-normalized
// and case-folded.
func foldSegment(s string) string {
	return strings.ToLower(norm.NFC.String(s))
}

// propfind runs PROPFIND with depth on segs. A missing resource returns
// storage.ErrNotFound.
func (d *Destination) propfind(ctx context.Context, op string, segs []string, coll bool, depth string) ([]entry, error) {
	for i := 0; ; i++ {
		resp, err := d.send(ctx, op, request{
			method: "PROPFIND", segs: segs, coll: coll, depth: depth, bounded: true,
			body: strings.NewReader(propfindBody), length: int64(len(propfindBody)),
			header: http.Header{"Content-Type": {"application/xml; charset=utf-8"}},
		})
		if err != nil {
			return nil, err
		}
		switch {
		case resp.StatusCode == http.StatusMultiStatus:
			entries, err := parseMultistatus(resp.Body)
			drain(resp)
			if err != nil {
				return nil, storage.WithDetail(storage.WithCode(storage.CodeInternal, fmt.Errorf("webdav: %s: %w", op, err)),
					op+": the server's folder listing is malformed or too large")
			}
			return entries, nil
		case retryable(resp.StatusCode) && i < len(retryDelays):
			drain(resp)
			if err := sleep(ctx, retryDelays[i]); err != nil {
				return nil, err
			}
		default:
			code := resp.StatusCode
			drain(resp)
			return nil, statusErr(op, code)
		}
	}
}

// stat returns the entry of the resource segs (Depth 0).
func (d *Destination) stat(ctx context.Context, op string, segs []string, coll bool) (*entry, error) {
	entries, err := d.propfind(ctx, op, segs, coll, "0")
	if err != nil {
		return nil, err
	}
	_, self := children(entries, segs)
	if self == nil && len(entries) == 1 {
		// One answer for a Depth 0 request: the server names the resource
		// differently (another escaping or case); it is still the one asked
		// for.
		self = &entries[0]
	}
	if self == nil {
		return nil, storage.WithDetail(storage.WithCode(storage.CodeInternal, fmt.Errorf("webdav: %s: no entry for the resource", op)),
			op+": the server's answer did not describe the requested resource")
	}
	return self, nil
}
