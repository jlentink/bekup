package httpserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

// reservedPrefixes never fall back to index.html: API, GraphQL, health and
// asset requests must get a real 404 instead of an HTML page.
var reservedPrefixes = []string{"/api", "/graphql", "/healthz", "/readyz", "/assets/"}

// hashedAsset matches Vite's content-hashed output names (assets/name-HASH.ext).
var hashedAsset = regexp.MustCompile(`^assets/.+-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9]+$`)

// maxUIFileSize guards against accidentally embedding huge files.
const maxUIFileSize = 32 << 20

type staticFile struct {
	data        []byte
	contentType string
	etag        string
	immutable   bool
}

// spa serves the embedded UI from memory (the embedded FS is small and
// read-only, so it is loaded once at startup).
type spa struct {
	files map[string]*staticFile // keyed by slash path without leading "/"
}

func newSPA(fsys fs.FS) (*spa, error) {
	s := &spa{files: map[string]*staticFile{}}
	if fsys == nil {
		return s, nil
	}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasPrefix(path.Base(p), ".") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxUIFileSize {
			return fmt.Errorf("UI file %s is larger than %d bytes", p, maxUIFileSize)
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		ct := mime.TypeByExtension(path.Ext(p))
		if ct == "" {
			ct = http.DetectContentType(data)
		}
		sum := sha256.Sum256(data)
		s.files[p] = &staticFile{
			data:        data,
			contentType: ct,
			etag:        `"` + hex.EncodeToString(sum[:16]) + `"`,
			immutable:   hashedAsset.MatchString(p),
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load embedded UI: %w", err)
	}
	return s, nil
}

func isReserved(p string) bool {
	for _, r := range reservedPrefixes {
		if p == strings.TrimSuffix(r, "/") || strings.HasPrefix(p, strings.TrimSuffix(r, "/")+"/") {
			return true
		}
	}
	return false
}

func acceptsHTML(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept") {
		for part := range strings.SplitSeq(v, ",") {
			mt, _, _ := strings.Cut(part, ";")
			if strings.EqualFold(strings.TrimSpace(mt), "text/html") {
				return true
			}
		}
	}
	return false
}

func (s *spa) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusNotFound)
		return
	}
	clean := path.Clean("/" + r.URL.Path)
	name := strings.TrimPrefix(clean, "/")
	if name == "" {
		name = "index.html"
	}
	if f, ok := s.files[name]; ok {
		s.serveFile(w, r, f)
		return
	}
	// Client-side route: only for browser navigations outside the reserved
	// prefixes.
	if idx, ok := s.files["index.html"]; ok && !isReserved(clean) && acceptsHTML(r) {
		s.serveFile(w, r, idx)
		return
	}
	writeError(w, http.StatusNotFound)
}

func (s *spa) serveFile(w http.ResponseWriter, r *http.Request, f *staticFile) {
	h := w.Header()
	h.Set("Content-Type", f.contentType)
	h.Set("ETag", f.etag)
	if f.immutable {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// Always revalidate (index.html, theme-init.js, favicon) so a new
		// release is picked up immediately.
		h.Set("Cache-Control", "no-cache")
	}
	// ServeContent handles If-None-Match, Range and HEAD.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(f.data))
}
