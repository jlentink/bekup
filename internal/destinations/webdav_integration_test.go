//go:build integration

package destinations

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	xwebdav "golang.org/x/net/webdav"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/webdav"
)

// davServer is an in-process TLS WebDAV server below /dav/backups whose
// MOVE ignores "Overwrite: F" while ignore is set.
func davServer(t *testing.T) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "backups"), 0o700); err != nil {
		t.Fatal(err)
	}
	h := &xwebdav.Handler{Prefix: "/dav", FileSystem: xwebdav.Dir(root), LockSystem: xwebdav.NewMemLS()}
	var ignore atomic.Bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "bekup" || p != "dav-password-9a8b" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == "MOVE" && ignore.Load() {
			r.Header.Set("Overwrite", "T")
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &ignore
}

// TestWebDAVService: the CA bundle and the D-140 rules at save time, a
// server that ignores "Overwrite: F" makes the destination not ready
// (Test stays allowed, Open refuses) until a passing test, and a worker's
// pin mismatch is audited.
func TestWebDAVService(t *testing.T) {
	srv, ignore := davServer(t)
	e := newSvcEnv(t)
	ctx := t.Context()
	caPEM := string(webdav.CertificateOf(srv.Certificate()).PEM())
	in := WebDAVInput{URL: srv.URL + "/dav/backups", Username: "bekup", Path: "svc", CABundlePEM: caPEM, Password: []byte("dav-password-9a8b")}
	d, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "dav", Type: TypeWebDAV, WebDAV: &in})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Ready || d.WebDAV.Flavor != "generic" || !d.HasPassword || d.WebDAV.CABundlePEM == "" {
		t.Fatalf("created: %+v %+v", d, d.WebDAV)
	}
	res, err := e.svc.Test(ctx, e.actor, d.ID)
	if err != nil || !res.OK || len(res.Steps) != 5 || res.WebDAV == nil || res.WebDAV.Overwrite != OverwriteRefused ||
		res.WebDAV.Trust != webdav.TrustCABundle || !res.WebDAV.Trusted {
		t.Fatalf("test: %+v %+v %v", res, res.WebDAV, err)
	}

	// The server starts replacing files: not ready after the test.
	ignore.Store(true)
	res, err = e.svc.Test(ctx, e.actor, d.ID)
	if err != nil || res.OK || res.Steps[1].Name != StepOverwrite || res.Steps[1].Code != storage.CodeOverwriteIgnored ||
		res.WebDAV.Overwrite != OverwriteIgnored {
		t.Fatalf("ignoring server: %+v %v", res, err)
	}
	got, err := e.svc.Get(ctx, d.ID)
	if err != nil || got.Ready || got.NotReadyReason != overwriteIgnoredReason {
		t.Fatalf("after the failed probe: ready=%v reason=%q %v", got.Ready, got.NotReadyReason, err)
	}
	var nr *NotReadyError
	if _, err := e.svc.Open(ctx, d.ID); !errors.As(err, &nr) {
		t.Fatalf("open: %v", err)
	}
	// Fixed: Test is still allowed and makes it ready again.
	ignore.Store(false)
	if res, err := e.svc.Test(ctx, e.actor, d.ID); err != nil || !res.OK {
		t.Fatalf("test after the fix: %+v %v", res, err)
	}
	if got, err := e.svc.Get(ctx, d.ID); err != nil || !got.Ready {
		t.Fatalf("not ready after a passing test: %+v %v", got, err)
	}

	// D-140: a new username needs the password again.
	in2 := in
	in2.Username, in2.Password = "other", nil
	var fe *FieldError
	if _, err := e.svc.Update(ctx, e.actor, d.ID, UpdateInput{WebDAV: &in2}); !errors.As(err, &fe) || fe.Field != "webdav.password" {
		t.Fatalf("username change without the password: %v", err)
	}
	// An invalid URL and a CA bundle with a key are refused at save time.
	bad := in
	bad.URL = "https://user:pw@" + strings.TrimPrefix(srv.URL, "https://")
	if _, err := e.svc.Update(ctx, e.actor, d.ID, UpdateInput{WebDAV: &bad}); !errors.As(err, &fe) {
		t.Fatalf("URL with user info: %v", err)
	}
	bad = in
	bad.CABundlePEM = "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"
	if _, err := e.svc.Update(ctx, e.actor, d.ID, UpdateInput{WebDAV: &bad}); !errors.As(err, &fe) || fe.Field != "webdav.caBundlePem" {
		t.Fatalf("CA bundle with a key: %v", err)
	}

	// A worker's pin mismatch is a security event.
	if err := e.svc.RecordRunFailure(ctx, d.ID, storage.CodeTLSPinMismatch); err != nil {
		t.Fatal(err)
	}
	if n := len(e.auditEntries(t, audit.ActionDestinationTLSPinMismatch)); n != 1 {
		t.Fatalf("mismatch audited %d times", n)
	}
	// Another location below the same folder overlaps.
	in3 := in
	in3.Path = "svc/nested"
	if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "dav2", Type: TypeWebDAV, WebDAV: &in3}); !errors.As(err, &fe) || fe.Field != "webdav.url" {
		t.Fatalf("overlapping location: %v", err)
	}
}

// TestWebDAVNextcloudUserIDOverlap: saving a Nextcloud destination records
// the user id the server resolves for its login, so two logins of one
// user (an e-mail address and the user name) cannot share a folder; a
// server that cannot be asked leaves the id empty and the check falls
// back to the login name (D-222 amendment).
func TestWebDAVNextcloudUserIDOverlap(t *testing.T) {
	var reachable atomic.Bool
	reachable.Store(true)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || (u != "alice@example.com" && u != "alice" && u != "bob") || p != "nc-password-1a2b" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !reachable.Load() || r.Method != "PROPFIND" || r.URL.Path != "/remote.php/dav/" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		uid := "a1b2"
		if u == "bob" {
			uid = "c3d4"
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		if _, err := w.Write([]byte(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:"><d:response><d:href>/remote.php/dav/</d:href>` +
			`<d:propstat><d:prop><d:current-user-principal><d:href>/remote.php/dav/principals/users/` + uid + `/</d:href>` +
			`</d:current-user-principal></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`)); err != nil {
			t.Errorf("principal answer: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	e := newSvcEnv(t)
	ctx := t.Context()
	caPEM := string(webdav.CertificateOf(srv.Certificate()).PEM())
	input := func(user, path string) *WebDAVInput {
		return &WebDAVInput{URL: srv.URL, Flavor: "nextcloud", Username: user, Path: path, CABundlePEM: caPEM, Password: []byte("nc-password-1a2b")}
	}
	a, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "nc-mail", Type: TypeWebDAV, WebDAV: input("alice@example.com", "backups")})
	if err != nil {
		t.Fatal(err)
	}
	if a.WebDAV == nil || a.WebDAV.UserID != "a1b2" {
		t.Fatalf("user id not recorded: %+v", a.WebDAV)
	}
	var fe *FieldError
	if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "nc-name", Type: TypeWebDAV, WebDAV: input("alice", "backups/nested")}); !errors.As(err, &fe) || fe.Field != "webdav.url" {
		t.Fatalf("second login of the same user: %v", err)
	}
	if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "nc-bob", Type: TypeWebDAV, WebDAV: input("bob", "backups")}); err != nil {
		t.Fatalf("another user: %v", err)
	}
	// Unreachable while saving: no id, the login name decides.
	reachable.Store(false)
	c, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "nc-offline", Type: TypeWebDAV, WebDAV: input("alice", "other")})
	if err != nil || c.WebDAV.UserID != "" {
		t.Fatalf("offline create: %+v %v", c.WebDAV, err)
	}
	// Once the server answers, saving again (nothing changed) records the
	// id, and moving onto the other login's folder is refused.
	reachable.Store(true)
	if _, err := e.svc.Update(ctx, e.actor, c.ID, UpdateInput{}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := e.svc.Get(ctx, c.ID)
	if err != nil || got.WebDAV.UserID != "a1b2" {
		t.Fatalf("user id after the update: %+v %v", got.WebDAV, err)
	}
	moved := input("alice", "backups")
	if _, err := e.svc.Update(ctx, e.actor, c.ID, UpdateInput{WebDAV: moved}); !errors.As(err, &fe) || fe.Field != "webdav.url" {
		t.Fatalf("moving onto the other login's folder: %v", err)
	}
}
