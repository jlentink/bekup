package webdav

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	xwebdav "golang.org/x/net/webdav"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/netguard"
)

const (
	testUser = "bekup"
	testPass = "test-password-7c1e"
)

// testServer is an in-process WebDAV server (golang.org/x/net/webdav over
// a temporary directory) behind HTTP Basic authentication.
type testServer struct {
	ts   *httptest.Server
	root string // file system root of the WebDAV tree
	// authFailures counts requests with missing or wrong credentials.
	authFailures atomic.Int32
	// requests counts requests per method.
	mu       sync.Mutex
	requests map[string]int
}

// middleware wraps the WebDAV handler (fakes for misbehaving servers).
type middleware func(next http.Handler, s *testServer) http.Handler

// startServer starts a TLS server whose WebDAV tree is served below
// /dav/ (the folder /dav/backups exists).
func startServer(t *testing.T, mw middleware) *testServer {
	t.Helper()
	s := &testServer{root: t.TempDir(), requests: map[string]int{}}
	if err := os.MkdirAll(filepath.Join(s.root, "backups"), 0o700); err != nil {
		t.Fatal(err)
	}
	var h http.Handler = &xwebdav.Handler{Prefix: "/dav", FileSystem: xwebdav.Dir(s.root), LockSystem: xwebdav.NewMemLS()}
	if mw != nil {
		h = mw(h, s)
	}
	s.ts = httptest.NewTLSServer(s.auth(h))
	t.Cleanup(s.ts.Close)
	return s
}

func (s *testServer) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests[r.Method]++
		s.mu.Unlock()
		if r.URL.Path == "/status.php" {
			next.ServeHTTP(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(testUser)) != 1 || subtle.ConstantTimeCompare([]byte(p), []byte(testPass)) != 1 {
			s.authFailures.Add(1)
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// certPEM is the server certificate (httptest's self-signed one).
func (s *testServer) certPEM() []byte {
	return CertificateOf(s.ts.Certificate()).PEM()
}

func testGuard(t *testing.T) *netguard.Guard {
	t.Helper()
	g, err := netguard.New(netguard.Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// config returns a generic configuration for the server's /dav/backups
// collection trusted through the CA bundle.
func (s *testServer) config(t *testing.T, dir string) Config {
	t.Helper()
	return Config{
		URL: s.ts.URL + "/dav/backups", Username: testUser, Password: crypto.NewSecret([]byte(testPass)),
		Path: dir, CABundlePEM: s.certPEM(), Guard: testGuard(t),
	}
}

func open(t *testing.T, cfg Config) *Destination {
	t.Helper()
	d, err := Open(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	d.sizeDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return d
}

// --- a Nextcloud/ownCloud fake ----------------------------------------------

// ncServer emulates the parts of Nextcloud/ownCloud bekup uses: status.php,
// the current-user-principal, the files tree below
// /remote.php/dav/files/<uid>/ and chunked uploads below
// /remote.php/dav/uploads/<uid>/.
type ncServer struct {
	*testServer
	uid       string
	noUploads bool // the server has no chunked-upload endpoint
	product   string

	mu          sync.Mutex
	chunkPuts   int
	maxChunk    int64
	assembled   int
	sawDestOnMk bool
}

func startNextcloud(t *testing.T, uid string, configure func(*ncServer)) *ncServer {
	t.Helper()
	nc := &ncServer{uid: uid, product: "Nextcloud"}
	if configure != nil {
		configure(nc)
	}
	s := &testServer{root: t.TempDir(), requests: map[string]int{}}
	nc.testServer = s
	for _, d := range []string{"files", "uploads"} {
		if err := os.MkdirAll(filepath.Join(s.root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := &xwebdav.Handler{
		Prefix: "/remote.php/dav/files/" + uid, FileSystem: xwebdav.Dir(filepath.Join(s.root, "files")),
		LockSystem: xwebdav.NewMemLS(),
	}
	s.ts = httptest.NewTLSServer(s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/status.php":
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]any{
				"installed": true, "maintenance": false, "version": "28.0.1.1", "versionstring": "28.0.1", "productname": nc.product,
			}); err != nil {
				t.Errorf("status.php: %v", err)
			}
		case p == "/remote.php/dav/" && r.Method == "PROPFIND":
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			fmt.Fprintf(w, `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:"><d:response><d:href>/remote.php/dav/</d:href>`+
				`<d:propstat><d:prop><d:current-user-principal><d:href>/remote.php/dav/principals/users/%s/</d:href>`+
				`</d:current-user-principal></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`,
				url.PathEscape(uid))
		case strings.HasPrefix(p, "/remote.php/dav/files/"+uid+"/") || p == "/remote.php/dav/files/"+uid:
			files.ServeHTTP(w, r)
		case strings.HasPrefix(p, "/remote.php/dav/uploads/"+uid+"/"):
			if nc.noUploads {
				http.NotFound(w, r)
				return
			}
			nc.uploads(w, r, strings.TrimPrefix(p, "/remote.php/dav/uploads/"+uid+"/"))
		default:
			http.NotFound(w, r)
		}
	})))
	t.Cleanup(s.ts.Close)
	return nc
}

func (nc *ncServer) config(t *testing.T, dir string) Config {
	t.Helper()
	return Config{
		URL: nc.ts.URL, Flavor: FlavorNextcloud, Username: testUser, Password: crypto.NewSecret([]byte(testPass)),
		Path: dir, CABundlePEM: nc.certPEM(), Guard: testGuard(t), ChunkSize: MinChunkSize,
	}
}

// uploads implements MKCOL / PUT / MOVE .file / DELETE / PROPFIND of the
// upload area.
func (nc *ncServer) uploads(w http.ResponseWriter, r *http.Request, rel string) {
	rel = strings.TrimSuffix(rel, "/")
	parts := strings.Split(rel, "/")
	base := filepath.Join(nc.root, "uploads")
	nc.mu.Lock()
	defer nc.mu.Unlock()
	switch {
	case r.Method == "PROPFIND" && rel == "":
		nc.listUploads(w)
	case r.Method == "MKCOL" && len(parts) == 1:
		if r.Header.Get("Destination") != "" {
			nc.sawDestOnMk = true
		}
		if err := os.Mkdir(filepath.Join(base, parts[0]), 0o700); err != nil { //nolint:gosec // G703: test fake below its temp dir
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodPut && len(parts) == 2:
		f, err := os.Create(filepath.Join(base, parts[0], parts[1])) //nolint:gosec // G304: test fake below its temp dir
		if err != nil {
			w.WriteHeader(http.StatusConflict)
			return
		}
		n, err := io.Copy(f, r.Body)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		nc.chunkPuts++
		nc.maxChunk = max(nc.maxChunk, n)
		w.WriteHeader(http.StatusCreated)
	case r.Method == "MOVE" && len(parts) == 2 && parts[1] == ".file":
		nc.assemble(w, r, filepath.Join(base, parts[0]))
	case r.Method == http.MethodDelete && len(parts) == 1:
		if err := os.RemoveAll(filepath.Join(base, parts[0])); err != nil { //nolint:gosec // G703: test fake below its temp dir
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (nc *ncServer) listUploads(w http.ResponseWriter) {
	ents, err := os.ReadDir(filepath.Join(nc.root, "uploads"))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusMultiStatus)
	fmt.Fprint(w, `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">`)
	fmt.Fprintf(w, `<d:response><d:href>/remote.php/dav/uploads/%s/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, nc.uid)
	for _, e := range ents {
		fi, err := e.Info()
		if err != nil {
			continue
		}
		fmt.Fprintf(w, `<d:response><d:href>/remote.php/dav/uploads/%s/%s/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype><d:getlastmodified>%s</d:getlastmodified></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`,
			nc.uid, url.PathEscape(e.Name()), fi.ModTime().UTC().Format(http.TimeFormat))
	}
	fmt.Fprint(w, `</d:multistatus>`)
}

// assemble emulates the chunking plugin's MOVE of .file (Sabre's
// Overwrite check first, then OC-Total-Length, then an atomic publish).
func (nc *ncServer) assemble(w http.ResponseWriter, r *http.Request, dir string) {
	u, err := url.Parse(r.Header.Get("Destination"))
	prefix := "/remote.php/dav/files/" + nc.uid + "/"
	if err != nil || !strings.HasPrefix(u.Path, prefix) {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	rel := strings.TrimPrefix(u.Path, prefix)
	target := filepath.Join(nc.root, "files", filepath.FromSlash(path.Clean("/"+rel)))
	exists := false
	if _, err := os.Stat(target); err == nil { //nolint:gosec // G703: test fake below its temp dir
		exists = true
	}
	if exists && r.Header.Get("Overwrite") == "F" {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	slices.SortFunc(names, func(a, b string) int { return chunkNumber(a) - chunkNumber(b) })
	tmp := filepath.Join(filepath.Dir(target), ".fake-"+filepath.Base(target)+".part") // hidden from listings
	out, err := os.Create(tmp)                                                         //nolint:gosec // G304: test fake below its temp dir
	if err != nil {
		w.WriteHeader(http.StatusConflict)
		return
	}
	var total int64
	for _, n := range names {
		f, err := os.Open(filepath.Join(dir, n)) //nolint:gosec // G304: test fake below its temp dir
		if err != nil {
			continue
		}
		m, err := io.Copy(out, f)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		total += m
	}
	if err := out.Close(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if want := r.Header.Get("OC-Total-Length"); want != strconv.FormatInt(total, 10) {
		discard(os.Remove(tmp)) //nolint:gosec // G703: test fake below its temp dir
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := os.Rename(tmp, target); err != nil { //nolint:gosec // G703: test fake below its temp dir
		w.WriteHeader(http.StatusConflict)
		return
	}
	discard(os.RemoveAll(dir)) //nolint:gosec // G703: test fake below its temp dir
	nc.assembled++
	if exists {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// chunkNumber parses a chunk name (0 for anything else).
func chunkNumber(name string) int {
	n, err := strconv.Atoi(name)
	if err != nil {
		return 0
	}
	return n
}
