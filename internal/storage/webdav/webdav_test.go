package webdav

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	xwebdav "golang.org/x/net/webdav"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/storagetest"
)

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}

var dirSeq atomic.Int64

func nextDir() string { return fmt.Sprintf("t%d", dirSeq.Add(1)) }

func TestConformanceGeneric(t *testing.T) {
	t.Parallel()
	srv := startServer(t, nil)
	storagetest.Run(t, storagetest.Config{
		LargeSize: 12 << 20,
		New: func(t *testing.T, o storagetest.Options) storage.Destination {
			cfg := srv.config(t, nextDir())
			cfg.NoDelete = o.NoDelete
			d, err := Open(cfg)
			if err != nil {
				t.Fatal(err)
			}
			d.sizeDelays = []time.Duration{time.Millisecond}
			return d
		},
	})
	if srv.authFailures.Load() != 0 {
		t.Errorf("%d requests without valid credentials", srv.authFailures.Load())
	}
}

func TestConformanceNextcloudChunked(t *testing.T) {
	t.Parallel()
	nc := startNextcloud(t, "user-1", nil)
	storagetest.Run(t, storagetest.Config{
		LargeSize: 5<<20 + 12345,
		New: func(t *testing.T, o storagetest.Options) storage.Destination {
			cfg := nc.config(t, "Backups/"+nextDir())
			cfg.NoDelete = o.NoDelete
			d, err := Open(cfg)
			if err != nil {
				t.Fatal(err)
			}
			d.sizeDelays = []time.Duration{time.Millisecond}
			return d
		},
	})
	nc.mu.Lock()
	defer nc.mu.Unlock()
	if nc.assembled == 0 || nc.chunkPuts < 6 || nc.maxChunk != MinChunkSize || !nc.sawDestOnMk {
		t.Errorf("chunked uploads not used: assembled=%d chunks=%d max=%d destination on MKCOL=%v",
			nc.assembled, nc.chunkPuts, nc.maxChunk, nc.sawDestOnMk)
	}
}

func TestNextcloudFallbackWithoutUploads(t *testing.T) {
	t.Parallel()
	nc := startNextcloud(t, "fallback", func(n *ncServer) { n.noUploads = true; n.product = "ownCloud" })
	d := open(t, nc.config(t, "x"))
	if _, err := d.Put(ctxT(t), storage.MustParseKey("job/2026-11-03T010000Z/db.dump"), bytes.NewReader([]byte("data")), storage.PutOptions{}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if d.UploadMode() != UploadSingle {
		t.Errorf("upload mode = %q, want single", d.UploadMode())
	}
	if got := d.DetectServer(ctxT(t)); got != ServerOwnCloud {
		t.Errorf("DetectServer = %q", got)
	}
}

func TestNextcloudUserIDFromPrincipal(t *testing.T) {
	t.Parallel()
	// The login name (testUser) differs from the user id.
	nc := startNextcloud(t, "a1b2-uid", nil)
	d := open(t, nc.config(t, ""))
	key := storage.MustParseKey("j/2026-11-03T010000Z/f")
	if _, err := d.Put(ctxT(t), key, bytes.NewReader([]byte("x")), storage.PutOptions{}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := os.Stat(filepath.Join(nc.root, "files", "j", "2026-11-03T010000Z", "f")); err != nil {
		t.Fatalf("object not below the user id: %v", err)
	}
	if d.UploadMode() != UploadChunked {
		t.Errorf("upload mode = %q", d.UploadMode())
	}
	if got := d.DetectServer(ctxT(t)); got != ServerNextcloud {
		t.Errorf("DetectServer = %q", got)
	}
	if uid, err := d.UserID(ctxT(t)); err != nil || uid != "a1b2-uid" {
		t.Errorf("UserID = %q, %v", uid, err)
	}
	g := open(t, startServer(t, nil).config(t, "g"))
	if uid, err := g.UserID(ctxT(t)); err != nil || uid != "" {
		t.Errorf("generic UserID = %q, %v", uid, err)
	}
}

// TestNextcloudEmptyObjectFallsBackToSinglePut: an empty source uploads
// no chunk, so Put falls back to a plain PUT instead of asking the server
// to assemble zero chunks (finding 3 of the D-220 review).
func TestNextcloudEmptyObjectFallsBackToSinglePut(t *testing.T) {
	t.Parallel()
	nc := startNextcloud(t, "empty", nil)
	d := open(t, nc.config(t, "e"))
	key := storage.MustParseKey("j/2026-11-03T010000Z/empty")
	info, err := d.Put(ctxT(t), key, bytes.NewReader(nil), storage.PutOptions{})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if info.Size != 0 {
		t.Errorf("size = %d", info.Size)
	}
	// The probe uploads one chunk but never assembles (412).
	nc.mu.Lock()
	assembled := nc.assembled
	nc.mu.Unlock()
	if assembled != 0 {
		t.Errorf("an empty object was assembled from zero chunks (%d)", assembled)
	}
	fi, err := os.Stat(filepath.Join(nc.root, "files", "e", "j", "2026-11-03T010000Z", "empty"))
	if err != nil || fi.Size() != 0 {
		t.Fatalf("empty object not stored: %v", err)
	}
	if ents, err := os.ReadDir(filepath.Join(nc.root, "uploads")); err != nil || len(ents) != 0 {
		t.Errorf("upload collection left behind: %v %v", ents, err)
	}
}

// respellListing rewrites the hrefs of Depth 1 PROPFIND answers from
// "/dav/backups/" to the given spelling (a server that names paths
// differently than requested).
func respellListing(to string) middleware {
	return func(next http.Handler, _ *testServer) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "PROPFIND" || r.Header.Get("Depth") != "1" {
				next.ServeHTTP(w, r)
				return
			}
			rec := httptest.NewRecorder()
			next.ServeHTTP(rec, r)
			for k, v := range rec.Header() {
				if k != "Content-Length" {
					w.Header()[k] = v
				}
			}
			w.WriteHeader(rec.Code)
			if _, err := w.Write(bytes.ReplaceAll(rec.Body.Bytes(), []byte("/dav/backups/"), []byte(to))); err != nil {
				discard(err) // the client sees a short answer
			}
		})
	}
}

// TestListingSpelledDifferently: a server that spells the folder in
// another letter case still lists it; one whose listing names neither the
// folder nor a member fails the listing instead of returning nothing
// (finding 4 of the D-220 review).
func TestListingSpelledDifferently(t *testing.T) {
	t.Parallel()
	key := storage.MustParseKey("job/2026-11-03T010000Z/db.dump")
	for to, wantErr := range map[string]bool{"/DAV/Backups/": false, "/elsewhere/": true} {
		srv := startServer(t, respellListing(to))
		d := open(t, srv.config(t, ""))
		if _, err := d.Put(ctxT(t), key, bytes.NewReader([]byte("data")), storage.PutOptions{}); err != nil {
			t.Fatalf("%s: put: %v", to, err)
		}
		page, err := d.List(ctxT(t), storage.Key{}, storage.ListOptions{})
		if wantErr {
			if err == nil || !strings.Contains(err.Error(), "names neither the folder") || storage.Classify(err) != storage.CodeInternal {
				t.Errorf("%s: list = %v, %v; want the mismatch error", to, page.Objects, err)
			}
			continue
		}
		if err != nil || len(page.Objects) != 1 || page.Objects[0].Key != key {
			t.Errorf("%s: list = %v, %v", to, page.Objects, err)
		}
	}
}

// ignoreOverwrite makes MOVE ignore "Overwrite: F" (a broken server or
// proxy).
func ignoreOverwrite(next http.Handler, _ *testServer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "MOVE" {
			r.Header.Set("Overwrite", "T")
		}
		next.ServeHTTP(w, r)
	})
}

// lieAboutOverwrite answers 412 but replaces the target anyway.
func lieAboutOverwrite(next http.Handler, _ *testServer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "MOVE" {
			r.Header.Set("Overwrite", "T")
			rec := httptest.NewRecorder()
			next.ServeHTTP(rec, r)
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func TestOverwriteIgnoredIsNeverWritten(t *testing.T) {
	t.Parallel()
	for name, mw := range map[string]middleware{"ignores": ignoreOverwrite, "lies": lieAboutOverwrite} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := startServer(t, mw)
			d := open(t, srv.config(t, ""))
			err := d.ProbeOverwrite(ctxT(t))
			if storage.Classify(err) != storage.CodeOverwriteIgnored {
				t.Fatalf("probe: %v (code %s)", err, storage.Classify(err))
			}
			if d.Capabilities().ExclusiveCreate {
				t.Error("ExclusiveCreate after a failed probe")
			}
			_, err = d.Put(ctxT(t), storage.MustParseKey("j/2026-11-03T010000Z/f"), bytes.NewReader([]byte("x")), storage.PutOptions{})
			if storage.Classify(err) != storage.CodeOverwriteIgnored {
				t.Fatalf("put: %v", err)
			}
			if !strings.Contains(storage.Describe(storage.Classify(err), err), "Overwrite: F") {
				t.Errorf("message %q", storage.Describe(storage.Classify(err), err))
			}
			if res := d.Test(ctxT(t), storage.Key{}); res.OK || res.Steps[0].Code != storage.CodeOverwriteIgnored {
				t.Errorf("test = %+v", res)
			}
			// Nothing but the probe's own cleanup touched the tree.
			ents, err := os.ReadDir(filepath.Join(srv.root, "backups"))
			if err != nil {
				t.Fatal(err)
			}
			if len(ents) != 0 {
				t.Errorf("left %d entries", len(ents))
			}
		})
	}
}

func TestProbeHonouredSetsExclusiveCreate(t *testing.T) {
	t.Parallel()
	srv := startServer(t, nil)
	d := open(t, srv.config(t, "nested/dir"))
	if d.Capabilities().ExclusiveCreate {
		t.Fatal("ExclusiveCreate before the probe")
	}
	if err := d.ProbeOverwrite(ctxT(t)); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !d.Capabilities().ExclusiveCreate {
		t.Error("ExclusiveCreate not set")
	}
	ents, err := os.ReadDir(filepath.Join(srv.root, "backups", "nested", "dir"))
	if err != nil || len(ents) != 0 {
		t.Errorf("probe left %v (%v)", ents, err)
	}
}

// zeroChunkedPUT stores 0 bytes for PUTs without a Content-Length (a PHP
// setup that drops chunked request bodies).
func zeroChunkedPUT(next http.Handler, _ *testServer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.ContentLength < 0 {
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(nil))
		}
		next.ServeHTTP(w, r)
	})
}

func TestDroppedBodyFailsBeforeMove(t *testing.T) {
	t.Parallel()
	srv := startServer(t, zeroChunkedPUT)
	d := open(t, srv.config(t, ""))
	key := storage.MustParseKey("j/2026-11-03T010000Z/f")
	_, err := d.Put(ctxT(t), key, bytes.NewReader(bytes.Repeat([]byte("a"), 4096)), storage.PutOptions{})
	if !errors.Is(err, storage.ErrChecksumMismatch) {
		t.Fatalf("put: %v", err)
	}
	if !strings.Contains(storage.Detail(err), "0 B of 4.0 KiB") {
		t.Errorf("detail %q", storage.Detail(err))
	}
	if _, err := d.Stat(ctxT(t), key); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("object published: %v", err)
	}
}

// TestRedirectRefused: every request is answered with a redirect to
// another server, which is never followed (no credentials reach it).
func TestRedirectRefused(t *testing.T) {
	t.Parallel()
	var leaked atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(other.Close)
	srv := startServer(t, func(http.Handler, *testServer) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL+"/elsewhere", http.StatusTemporaryRedirect)
		})
	})
	d := open(t, srv.config(t, ""))
	_, err := d.Put(ctxT(t), storage.MustParseKey("j/2026-11-03T010000Z/f"), bytes.NewReader([]byte("x")), storage.PutOptions{})
	if storage.Classify(err) != storage.CodeNetwork || !strings.Contains(storage.Detail(err), "redirect") {
		t.Fatalf("put: %v (detail %q)", err, storage.Detail(err))
	}
	if _, err := d.List(ctxT(t), storage.Key{}, storage.ListOptions{}); storage.Classify(err) != storage.CodeNetwork {
		t.Fatalf("list: %v", err)
	}
	if leaked.Load() != 0 {
		t.Fatal("credentials reached the redirect target")
	}
}

func TestPlainHTTPRefused(t *testing.T) {
	t.Parallel()
	plain := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(plain.Close)
	cfg := Config{URL: plain.URL + "/dav", Username: testUser, Password: crypto.NewSecret([]byte(testPass)), Guard: testGuard(t)}
	if _, err := Open(cfg); !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "https") {
		t.Fatalf("open http: %v", err)
	}
	if cfg.Password.Len() != 0 {
		t.Error("password not destroyed after a failed Open")
	}
	if err := ValidateTarget(plain.URL, FlavorGeneric, testUser, "", false); err == nil {
		t.Fatal("ValidateTarget accepted http")
	}
	if err := ValidateTarget(plain.URL, FlavorGeneric, testUser, "", true); err != nil {
		t.Fatalf("dev mode: %v", err)
	}
}

func TestPlainHTTPDevMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := &testServer{root: dir, requests: map[string]int{}}
	plain := httptest.NewServer(s.auth(&xwebdav.Handler{Prefix: "/dav", FileSystem: xwebdav.Dir(dir), LockSystem: xwebdav.NewMemLS()}))
	t.Cleanup(plain.Close)
	d := open(t, Config{URL: plain.URL + "/dav/b", Username: testUser, Password: crypto.NewSecret([]byte(testPass)), Guard: testGuard(t), AllowHTTP: true})
	if d.Trust() != TrustPlainHTTP {
		t.Fatalf("trust %q", d.Trust())
	}
	if res := d.Test(ctxT(t), storage.Key{}); !res.OK {
		t.Fatalf("test: %+v", res)
	}
	if rep := d.InspectTLS(ctxT(t)); !rep.Trusted || rep.Certificate != nil {
		t.Fatalf("inspect: %+v", rep)
	}
}

func TestWrongPasswordIsPermission(t *testing.T) {
	t.Parallel()
	srv := startServer(t, nil)
	cfg := srv.config(t, "")
	cfg.Password = crypto.NewSecret([]byte("wrong"))
	d := open(t, cfg)
	err := d.ProbeOverwrite(ctxT(t))
	if storage.Classify(err) != storage.CodePermission || !strings.Contains(storage.Detail(err), "HTTP 401") {
		t.Fatalf("probe: %v (%q)", err, storage.Detail(err))
	}
}

// --- TLS trust -----------------------------------------------------------------

func TestTLSTrust(t *testing.T) {
	t.Parallel()
	srv := startServer(t, nil)
	leaf := srv.ts.Certificate()

	t.Run("untrusted without bundle", func(t *testing.T) {
		t.Parallel()
		cfg := srv.config(t, "")
		cfg.CABundlePEM = nil
		d := open(t, cfg)
		rep := d.InspectTLS(ctxT(t))
		if rep.Trusted || rep.Trust != TrustSystem || rep.Certificate == nil || !bytes.Equal(rep.Certificate.DER, leaf.Raw) {
			t.Fatalf("inspect: %+v", rep)
		}
		if storage.Classify(rep.Err) != storage.CodeNetwork || !strings.Contains(storage.Detail(rep.Err), "not trusted") {
			t.Errorf("inspect error %v (%q)", rep.Err, storage.Detail(rep.Err))
		}
		err := d.ProbeOverwrite(ctxT(t))
		if storage.Classify(err) != storage.CodeNetwork || !strings.Contains(storage.Detail(err), "pin it") {
			t.Fatalf("probe: %v (%q)", err, storage.Detail(err))
		}
	})
	t.Run("ca bundle", func(t *testing.T) {
		t.Parallel()
		d := open(t, srv.config(t, nextDir()))
		if rep := d.InspectTLS(ctxT(t)); !rep.Trusted || rep.Trust != TrustCABundle {
			t.Fatalf("inspect: %+v", rep)
		}
		if res := d.Test(ctxT(t), storage.Key{}); !res.OK {
			t.Fatalf("test: %+v", res)
		}
	})
	t.Run("pinned", func(t *testing.T) {
		t.Parallel()
		cfg := srv.config(t, nextDir())
		cfg.CABundlePEM = nil
		c := CertificateOf(leaf)
		cfg.Pin = &Pin{CertificatePEM: c.PEM(), SPKISHA256: c.SPKISHA256}
		d := open(t, cfg)
		rep := d.InspectTLS(ctxT(t))
		if !rep.Trusted || rep.Trust != TrustPinned || !rep.PinMatches {
			t.Fatalf("inspect: %+v", rep)
		}
		if res := d.Test(ctxT(t), storage.Key{}); !res.OK {
			t.Fatalf("test: %+v", res)
		}
	})
	t.Run("pin mismatch", func(t *testing.T) {
		t.Parallel()
		other := selfSigned(t, "127.0.0.1")
		cfg := srv.config(t, "")
		c := CertificateOf(other.Leaf)
		cfg.Pin = &Pin{CertificatePEM: c.PEM(), SPKISHA256: c.SPKISHA256}
		d := open(t, cfg)
		rep := d.InspectTLS(ctxT(t))
		if rep.Trusted || rep.PinMatches || storage.Classify(rep.Err) != storage.CodeTLSPinMismatch || rep.Certificate == nil {
			t.Fatalf("inspect: %+v", rep)
		}
		if err := d.ProbeOverwrite(ctxT(t)); storage.Classify(err) != storage.CodeTLSPinMismatch {
			t.Fatalf("probe: %v", err)
		}
	})
}

// TestPinRules: what can be pinned, and a pinned leaf of an unpresented CA.
func TestPinRules(t *testing.T) {
	t.Parallel()
	a := selfSigned(t, "127.0.0.1")
	if err := CheckPinnable(a.Leaf, "127.0.0.1", time.Now()); err != nil {
		t.Fatalf("pinnable: %v", err)
	}
	if err := CheckPinnable(a.Leaf, "nas.example", time.Now()); err == nil || !strings.Contains(err.Error(), "host") {
		t.Fatalf("other host: %v", err)
	}
	if err := CheckPinnable(a.Leaf, "127.0.0.1", time.Now().Add(400*24*time.Hour)); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired: %v", err)
	}
	// A leaf issued by a CA the server does not present (NAS default
	// certificates) is pinnable as its own anchor.
	issued := issuedLeaf(t, "127.0.0.1")
	if err := CheckPinnable(issued.Leaf, "127.0.0.1", time.Now()); err != nil {
		t.Fatalf("issued leaf: %v", err)
	}
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{issued}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	c := CertificateOf(issued.Leaf)
	rep := Inspect(ctxT(t), InspectConfig{URL: srv.URL, Guard: testGuard(t), Pin: &Pin{CertificatePEM: c.PEM(), SPKISHA256: c.SPKISHA256}})
	if !rep.Trusted || !rep.PinMatches {
		t.Fatalf("pinned issued leaf: %+v", rep)
	}
	// A pin whose certificate does not match its fingerprint is refused.
	if _, err := parsePin(&Pin{CertificatePEM: c.PEM(), SPKISHA256: make([]byte, 32)}); err == nil {
		t.Fatal("mismatching pin accepted")
	}
}

func keyPair(t *testing.T, tmpl, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (tls.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if parent == nil {
		parent, parentKey = tmpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}))
	if err != nil {
		t.Fatal(err)
	}
	pair.Leaf = leaf
	return pair, key
}

func leafTemplate(serial int64, ip string) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "nas"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(365 * 24 * time.Hour),
		IPAddresses: []net.IP{net.ParseIP(ip)},
		KeyUsage:    x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
}

func selfSigned(t *testing.T, ip string) tls.Certificate {
	t.Helper()
	pair, _ := keyPair(t, leafTemplate(7, ip), nil, nil)
	return pair
}

func issuedLeaf(t *testing.T, ip string) tls.Certificate {
	t.Helper()
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "NAS Inc. CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour * 365),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	ca, caKey := keyPair(t, caTmpl, nil, nil)
	pair, _ := keyPair(t, leafTemplate(9, ip), ca.Leaf, caKey)
	return pair
}

// --- deletes, listing, cleanup ---------------------------------------------------

func TestDeleteNeverRemovesCollections(t *testing.T) {
	t.Parallel()
	srv := startServer(t, nil)
	d := open(t, srv.config(t, ""))
	if _, err := d.Put(ctxT(t), storage.MustParseKey("j/2026-11-03T010000Z/f"), bytes.NewReader([]byte("x")), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(ctxT(t), storage.MustParseKey("j")); err != nil {
		t.Fatalf("delete collection key: %v", err)
	}
	if _, err := d.Stat(ctxT(t), storage.MustParseKey("j/2026-11-03T010000Z/f")); err != nil {
		t.Fatalf("collection was deleted: %v", err)
	}
}

func TestCleanupAndPrune(t *testing.T) {
	t.Parallel()
	nc := startNextcloud(t, "u", nil)
	d := open(t, nc.config(t, ""))
	ctx := ctxT(t)
	if _, err := d.Put(ctx, storage.MustParseKey("job-a/2026-11-03T010000Z/keep"), bytes.NewReader([]byte("x")), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	files := filepath.Join(nc.root, "files")
	old := time.Now().Add(-48 * time.Hour)
	mk := func(p string, dir bool) {
		t.Helper()
		full := filepath.Join(files, filepath.FromSlash(p))
		if dir {
			if err := os.MkdirAll(full, 0o700); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(full, []byte("p"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldPartial := "job-a/2026-11-03T010000Z/." + strings.Repeat("a", 32) + ".partial"
	newPartial := "job-a/." + strings.Repeat("b", 32) + ".partial"
	mk(oldPartial, false)
	mk(newPartial, false)
	mk("job-a/2026-10-01T010000Z", true) // empty old run directory
	mk("job-b", true)                    // empty old job directory: kept
	mk("job-a/2026-10-02T010000Z", true) // empty but recent: kept
	for _, p := range []string{oldPartial, "job-a/2026-10-01T010000Z", "job-b"} {
		if err := os.Chtimes(filepath.Join(files, p), old, old); err != nil {
			t.Fatal(err)
		}
	}
	ups := filepath.Join(nc.root, "uploads")
	for _, n := range []string{"bekup-" + strings.Repeat("c", 32), "desktop-client-upload"} {
		if err := os.Mkdir(filepath.Join(ups, n), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(ups, n), old, old); err != nil {
			t.Fatal(err)
		}
	}
	n, err := d.CleanupPartials(ctx, 24*time.Hour)
	if err != nil || n != 2 {
		t.Fatalf("cleanup removed %d: %v", n, err)
	}
	for p, want := range map[string]bool{oldPartial: false, newPartial: true} {
		if _, err := os.Stat(filepath.Join(files, p)); (err == nil) != want {
			t.Errorf("%s exists=%v", p, err == nil)
		}
	}
	if _, err := os.Stat(filepath.Join(ups, "desktop-client-upload")); err != nil {
		t.Error("a foreign upload collection was removed")
	}
	pruned, err := d.PruneEmptyDirs(ctx, 24*time.Hour, nil)
	if err != nil || pruned != 1 {
		t.Fatalf("pruned %d: %v", pruned, err)
	}
	for p, want := range map[string]bool{"job-a/2026-10-01T010000Z": false, "job-b": true, "job-a/2026-10-02T010000Z": true, "job-a/2026-11-03T010000Z/keep": true} {
		if _, err := os.Stat(filepath.Join(files, p)); (err == nil) != want {
			t.Errorf("%s exists=%v", p, err == nil)
		}
	}
}

func TestCloseDestroysPassword(t *testing.T) {
	t.Parallel()
	srv := startServer(t, nil)
	cfg := srv.config(t, "")
	pw := cfg.Password
	d := open(t, cfg)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if pw.Len() != 0 {
		t.Error("password survives Close")
	}
	if _, err := d.Stat(ctxT(t), storage.MustParseKey("a")); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("stat after close: %v", err)
	}
}
