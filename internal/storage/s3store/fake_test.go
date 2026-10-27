package s3store

import (
	"context"
	"crypto/md5" //nolint:gosec // G501: the fake reproduces S3 ETags
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/crypto"
)

// fakeS3 is a minimal path-style S3 for unit tests of the upload logic.
type fakeS3 struct {
	t      *testing.T
	bucket string

	mu        sync.Mutex
	objects   map[string][]byte
	uploads   map[string]map[int][]byte
	nextID    int
	aborted   int
	completed int
	inflight  int
	maxFlight int
	headers   map[string][]http.Header // op -> request headers
	failPart  int                      // part number answered with 403
	partDelay time.Duration
	lockXML   string // object-lock config response ("" = not configured)

	attrs    map[string]objAttrs // object key -> stored attributes
	upMeta   map[string]objAttrs // upload ID -> attributes from Create
	failDone int                 // Complete answers 500 after committing this many times
	refuse   bool                // Complete always answers 500 without committing
	onCommit func()              // called after a Complete committed (outside the lock)
}

// objAttrs are the object attributes the fake returns on HEAD.
type objAttrs struct {
	etag, checksum, sha string
	native              bool
}

func newFakeS3(t *testing.T) (*fakeS3, *httptest.Server) {
	t.Helper()
	f := &fakeS3{
		t: t, bucket: "fake-bucket",
		objects: map[string][]byte{}, uploads: map[string]map[int][]byte{},
		headers: map[string][]http.Header{},
		attrs:   map[string]objAttrs{}, upMeta: map[string]objAttrs{},
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeS3) record(op string, r *http.Request) {
	f.mu.Lock()
	f.headers[op] = append(f.headers[op], r.Header.Clone())
	f.mu.Unlock()
}

func (f *fakeS3) count(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.headers[op])
}

// write sends a fixed response body (never request-derived).
func (f *fakeS3) write(w http.ResponseWriter, body string) {
	if _, err := io.WriteString(w, body); err != nil {
		f.t.Errorf("write response: %v", err)
	}
}

func s3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	if _, err := fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, code); err != nil {
		return
	}
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	bucket, key, _ := strings.Cut(path, "/")
	if bucket != f.bucket {
		s3Error(w, http.StatusNotFound, "NoSuchBucket")
		return
	}
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/xml")
	switch {
	case key == "" && q.Has("versioning"):
		f.record("versioning", r)
		f.write(w, `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></VersioningConfiguration>`)
	case key == "" && q.Has("object-lock"):
		f.record("objectlock", r)
		if f.lockXML == "" {
			s3Error(w, http.StatusNotFound, "ObjectLockConfigurationNotFoundError")
			return
		}
		f.write(w, f.lockXML)
	case r.Method == http.MethodHead:
		f.record("head", r)
		f.mu.Lock()
		b, ok := f.objects[key]
		a := f.attrs[key]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		if a.etag != "" {
			w.Header().Set("ETag", `"`+a.etag+`"`)
		}
		if a.sha != "" {
			w.Header().Set("X-Amz-Meta-Bekup-Sha256", a.sha)
		}
		if a.checksum != "" && r.Header.Get("X-Amz-Checksum-Mode") == "ENABLED" {
			w.Header().Set("X-Amz-Checksum-Sha256", a.checksum)
		}
	case r.Method == http.MethodPost && q.Has("uploads"):
		f.record("create", r)
		f.mu.Lock()
		f.nextID++
		id := fmt.Sprintf("up-%d", f.nextID)
		f.uploads[id] = map[int][]byte{}
		f.upMeta[id] = objAttrs{
			sha:    r.Header.Get("X-Amz-Meta-Bekup-Sha256"),
			native: r.Header.Get("X-Amz-Checksum-Algorithm") == "SHA256",
		}
		f.mu.Unlock()
		f.write(w, "<InitiateMultipartUploadResult><UploadId>"+id+"</UploadId></InitiateMultipartUploadResult>")
	case r.Method == http.MethodPut && q.Has("uploadId"):
		f.record("part", r)
		num, err := strconv.Atoi(q.Get("partNumber"))
		if err != nil {
			s3Error(w, http.StatusBadRequest, "InvalidArgument")
			return
		}
		f.mu.Lock()
		f.inflight++
		f.maxFlight = max(f.maxFlight, f.inflight)
		fail := num == f.failPart
		f.mu.Unlock()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			// The client aborted the part (e.g. a sibling part failed and
			// the upload context was canceled): never store a partial part.
			f.mu.Lock()
			f.inflight--
			f.mu.Unlock()
			return
		}
		time.Sleep(f.partDelay)
		f.mu.Lock()
		f.inflight--
		if up, ok := f.uploads[q.Get("uploadId")]; ok && !fail {
			up[num] = body
		}
		f.mu.Unlock()
		if fail {
			s3Error(w, http.StatusForbidden, "AccessDenied")
			return
		}
		m := md5.Sum(body) //nolint:gosec // G401: S3 ETag semantics
		w.Header().Set("ETag", `"`+hex.EncodeToString(m[:])+`"`)
		if r.Header.Get("X-Amz-Checksum-Sha256") != "" {
			c := sha256.Sum256(body)
			w.Header().Set("X-Amz-Checksum-Sha256", base64.StdEncoding.EncodeToString(c[:]))
		}
	case r.Method == http.MethodPost && q.Has("uploadId"):
		f.record("complete", r)
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			f.t.Errorf("read complete: %v", err)
		}
		if !f.completeLocked(w, r, key, q.Get("uploadId")) {
			return
		}
		if f.onCommit != nil {
			f.onCommit()
		}
		f.mu.Lock()
		fail := f.failDone > 0
		if fail {
			f.failDone--
		}
		f.mu.Unlock()
		if fail {
			s3Error(w, http.StatusInternalServerError, "InternalError")
			return
		}
		f.write(w, `<CompleteMultipartUploadResult><ETag>"x"</ETag></CompleteMultipartUploadResult>`)
	case r.Method == http.MethodDelete && q.Has("uploadId"):
		f.record("abort", r)
		f.mu.Lock()
		delete(f.uploads, q.Get("uploadId"))
		f.aborted++
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPut:
		f.record("put", r)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			f.t.Errorf("read put: %v", err)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, exists := f.objects[key]; exists && r.Header.Get("If-None-Match") == "*" {
			s3Error(w, http.StatusPreconditionFailed, "PreconditionFailed")
			return
		}
		f.objects[key] = body
		m := md5.Sum(body) //nolint:gosec // G401: S3 ETag semantics
		f.attrs[key] = objAttrs{etag: hex.EncodeToString(m[:]), sha: r.Header.Get("X-Amz-Meta-Bekup-Sha256")}
		w.Header().Set("ETag", `"x"`)
	default:
		s3Error(w, http.StatusNotImplemented, "NotImplemented")
	}
}

// completeLocked commits a multipart upload; it answers the error itself
// and reports false when nothing was committed.
func (f *fakeS3) completeLocked(w http.ResponseWriter, r *http.Request, key, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse {
		s3Error(w, http.StatusInternalServerError, "InternalError")
		return false
	}
	up, ok := f.uploads[id]
	if !ok {
		s3Error(w, http.StatusNotFound, "NoSuchUpload")
		return false
	}
	if _, exists := f.objects[key]; exists && r.Header.Get("If-None-Match") == "*" {
		s3Error(w, http.StatusPreconditionFailed, "PreconditionFailed")
		return false
	}
	var all, md5s, shas []byte
	for i := 1; i <= len(up); i++ {
		all = append(all, up[i]...)
		m := md5.Sum(up[i]) //nolint:gosec // G401: S3 ETag semantics
		c := sha256.Sum256(up[i])
		md5s, shas = append(md5s, m[:]...), append(shas, c[:]...)
	}
	a := f.upMeta[id]
	m := md5.Sum(md5s) //nolint:gosec // G401: S3 ETag semantics
	a.etag = hex.EncodeToString(m[:]) + "-" + strconv.Itoa(len(up))
	if a.native {
		c := sha256.Sum256(shas)
		a.checksum = base64.StdEncoding.EncodeToString(c[:]) + "-" + strconv.Itoa(len(up))
	}
	f.objects[key] = all
	f.attrs[key] = a
	delete(f.uploads, id)
	delete(f.upMeta, id)
	f.completed++
	return true
}

func (f *fakeS3) open(t *testing.T, srvURL string, mod func(*Config)) *Destination {
	t.Helper()
	cfg := Config{
		Endpoint:        srvURL,
		AllowHTTP:       true,
		Region:          "us-east-1",
		Bucket:          f.bucket,
		PathStyle:       true,
		AccessKeyID:     "fakekey",
		SecretAccessKey: crypto.NewSecret([]byte("fake-secret-value")),
		PartSize:        MinPartSize,
		Concurrency:     2,
		Guard:           testGuard(t),
	}
	if mod != nil {
		mod(&cfg)
	}
	d, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { closeLog(t, d) })
	return d
}
