package s3store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/smithy-go"

	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/storagetest"
)

func TestPutMultipartHeadersAndConcurrency(t *testing.T) {
	f, srv := newFakeS3(t)
	f.partDelay = 50 * time.Millisecond
	d := f.open(t, srv.URL, func(c *Config) {
		c.SSE, c.KMSKeyID, c.StorageClass = SSEKMS, "alias/bekup", "STANDARD_IA"
	})
	size := 4*MinPartSize + 17
	info, err := d.Put(t.Context(), storage.MustParseKey("j/obj"), storagetest.Reader(1, size), storage.PutOptions{})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	h := sha256.New()
	mustCopy(t, h, storagetest.Reader(1, size))
	if info.Size != size || !bytes.Equal(info.SHA256, h.Sum(nil)) {
		t.Fatalf("info = %+v", info)
	}
	if got := int64(len(f.objects["j/obj"])); got != size {
		t.Fatalf("stored %d bytes", got)
	}
	if f.count("part") != 5 || f.completed != 1 || f.aborted != 0 {
		t.Fatalf("parts=%d completed=%d aborted=%d", f.count("part"), f.completed, f.aborted)
	}
	if f.maxFlight > 2 {
		t.Fatalf("%d parts in flight, concurrency is 2", f.maxFlight)
	}
	create := f.headers["create"][0]
	if create.Get("X-Amz-Server-Side-Encryption") != "aws:kms" ||
		create.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id") != "alias/bekup" ||
		create.Get("X-Amz-Storage-Class") != "STANDARD_IA" ||
		create.Get("X-Amz-Checksum-Algorithm") != "SHA256" {
		t.Fatalf("create headers = %v", create)
	}
	for _, ph := range f.headers["part"] {
		if ph.Get("X-Amz-Checksum-Sha256") == "" {
			t.Fatalf("part without SHA-256 checksum: %v", ph)
		}
	}
	if f.headers["complete"][0].Get("If-None-Match") != "*" {
		t.Fatal("complete without If-None-Match")
	}
}

func TestPutSingleHeaders(t *testing.T) {
	f, srv := newFakeS3(t)
	d := f.open(t, srv.URL, nil)
	data := []byte("small object")
	if _, err := d.Put(t.Context(), storage.MustParseKey("s/obj"), bytes.NewReader(data), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	ph := f.headers["put"][0]
	sum := sha256.Sum256(data)
	if ph.Get("If-None-Match") != "*" || ph.Get("X-Amz-Meta-Bekup-Sha256") != hex.EncodeToString(sum[:]) ||
		ph.Get("X-Amz-Checksum-Sha256") == "" || ph.Get("Content-Type") != defaultContentType {
		t.Fatalf("put headers = %v", ph)
	}
	if f.count("create") != 0 {
		t.Fatal("small object used multipart")
	}
	_, err := d.Put(t.Context(), storage.MustParseKey("s/obj"), bytes.NewReader(data), storage.PutOptions{})
	if !errors.Is(err, storage.ErrAlreadyExists) {
		t.Fatalf("second put: %v", err)
	}
}

func TestPutWhenRequiredSendsNoChecksum(t *testing.T) {
	f, srv := newFakeS3(t)
	d := f.open(t, srv.URL, func(c *Config) { c.Checksum = ChecksumWhenRequired })
	if _, err := d.Put(t.Context(), storage.MustParseKey("r/obj"), storagetest.Reader(2, MinPartSize+1), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"create", "part"} {
		for _, h := range f.headers[op] {
			if h.Get("X-Amz-Checksum-Sha256") != "" || h.Get("X-Amz-Checksum-Algorithm") != "" ||
				h.Get("Content-Md5") != "" || h.Get("X-Amz-Sdk-Checksum-Algorithm") != "" {
				t.Fatalf("%s sent checksum headers in when_required mode: %v", op, h)
			}
		}
	}
	if d.Capabilities().NativeChecksum != "" {
		t.Fatal("native checksum reported in when_required mode")
	}
}

func TestPutAbortsOnPartFailure(t *testing.T) {
	f, srv := newFakeS3(t)
	f.failPart = 2
	d := f.open(t, srv.URL, nil)
	_, err := d.Put(t.Context(), storage.MustParseKey("f/obj"), storagetest.Reader(3, 3*MinPartSize), storage.PutOptions{})
	if !errors.Is(err, storage.ErrPermission) {
		t.Fatalf("put: %v", err)
	}
	if f.aborted != 1 || len(f.uploads) != 0 || len(f.objects) != 0 {
		t.Fatalf("aborted=%d uploads=%d objects=%d", f.aborted, len(f.uploads), len(f.objects))
	}
}

func TestPutAbortsOnSourceErrorAndMismatch(t *testing.T) {
	f, srv := newFakeS3(t)
	d := f.open(t, srv.URL, nil)
	boom := errors.New("producer died")
	_, err := d.Put(t.Context(), storage.MustParseKey("e/obj"),
		io.MultiReader(storagetest.Reader(4, 2*MinPartSize+5), &errReader{boom}), storage.PutOptions{})
	if !errors.Is(err, boom) || !errors.Is(err, storage.ErrSource) {
		t.Fatalf("put: %v", err)
	}
	wrong := sha256.Sum256([]byte("x"))
	_, err = d.Put(t.Context(), storage.MustParseKey("e/obj2"), storagetest.Reader(5, MinPartSize+1),
		storage.PutOptions{ExpectedSHA256: wrong[:]})
	if !errors.Is(err, storage.ErrChecksumMismatch) {
		t.Fatalf("mismatch: %v", err)
	}
	if f.aborted != 2 || len(f.uploads) != 0 || len(f.objects) != 0 || f.completed != 0 {
		t.Fatalf("aborted=%d uploads=%d objects=%d completed=%d", f.aborted, len(f.uploads), len(f.objects), f.completed)
	}
}

type errReader struct{ err error }

func (e *errReader) Read([]byte) (int, error) { return 0, e.err }

func TestPutCancelStillAborts(t *testing.T) {
	f, srv := newFakeS3(t)
	d := f.open(t, srv.URL, nil)
	ctx, cancel := context.WithCancel(t.Context())
	pr, pw := io.Pipe()
	go func() {
		ok := feed(pw, io.LimitReader(storagetest.Reader(6, 1<<40), MinPartSize+100))
		cancel()
		if ok {
			feed(pw, storagetest.Reader(7, 1<<40))
		}
	}()
	_, err := d.Put(ctx, storage.MustParseKey("c/obj"), pr, storage.PutOptions{})
	stopPipe(t, pr)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("put: %v", err)
	}
	// The cancel happens only after the second part's bytes were read, which
	// is after CreateMultipartUpload returned: the upload must be aborted.
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.headers["create"]) != 1 || f.aborted != 1 || len(f.uploads) != 0 || len(f.objects) != 0 || f.completed != 0 {
		t.Fatalf("create=%d aborted=%d uploads=%d objects=%d completed=%d",
			len(f.headers["create"]), f.aborted, len(f.uploads), len(f.objects), f.completed)
	}
}

// putMultipart uploads a two-part object and returns its SHA-256.
func putMultipart(ctx context.Context, d *Destination, key string, seed uint64, opts storage.PutOptions) (storage.ObjectInfo, []byte, error) {
	size := MinPartSize + 123
	h := sha256.New()
	if _, err := io.Copy(h, storagetest.Reader(seed, size)); err != nil {
		return storage.ObjectInfo{}, nil, err
	}
	info, err := d.Put(ctx, storage.MustParseKey(key), storagetest.Reader(seed, size), opts)
	return info, h.Sum(nil), err
}

func TestPutCompleteSurvivesCancelAfterCommit(t *testing.T) {
	f, srv := newFakeS3(t)
	d := f.open(t, srv.URL, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.onCommit = cancel // the caller gives up right after the server committed
	info, sum, err := putMultipart(ctx, d, "k/obj", 11, storage.PutOptions{})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if !bytes.Equal(info.SHA256, sum) || f.completed != 1 || f.aborted != 0 || len(f.objects) != 1 {
		t.Fatalf("sha ok=%v completed=%d aborted=%d objects=%d", bytes.Equal(info.SHA256, sum), f.completed, f.aborted, len(f.objects))
	}
}

func TestPutCompleteLostAnswerIsVerified(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*Config)
		opts func(sum []byte) storage.PutOptions
	}{
		{"native composite checksum", nil, nil},
		{"composite etag", func(c *Config) { c.Checksum = ChecksumWhenRequired }, nil},
		{"sha256 metadata", func(c *Config) { c.Checksum, c.SSE = ChecksumWhenRequired, SSEKMS },
			func(sum []byte) storage.PutOptions { return storage.PutOptions{ExpectedSHA256: sum} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, srv := newFakeS3(t)
			d := f.open(t, srv.URL, tc.mod)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			// Committed, but the answer is a 500 and the caller cancels: the
			// SDK's retry gets NoSuchUpload, the HEAD proves the commit.
			f.failDone, f.onCommit = 1, cancel
			var opts storage.PutOptions
			if tc.opts != nil {
				h := sha256.New()
				mustCopy(t, h, storagetest.Reader(12, MinPartSize+123))
				opts = tc.opts(h.Sum(nil))
			}
			info, sum, err := putMultipart(ctx, d, "v/obj", 12, opts)
			if err != nil {
				t.Fatalf("put: %v", err)
			}
			if !bytes.Equal(info.SHA256, sum) || f.completed != 1 || f.count("complete") < 2 || f.count("head") < 2 {
				t.Fatalf("completed=%d complete calls=%d heads=%d", f.completed, f.count("complete"), f.count("head"))
			}
		})
	}
}

func TestPutCompleteFailureIsReported(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		f, srv := newFakeS3(t)
		d := f.open(t, srv.URL, nil)
		f.refuse = true
		_, _, err := putMultipart(t.Context(), d, "r/obj", 13, storage.PutOptions{})
		if storage.Classify(err) != storage.CodeNetwork {
			t.Fatalf("put: %v (code %s)", err, storage.Classify(err))
		}
		if f.aborted != 1 || len(f.objects) != 0 || len(f.uploads) != 0 {
			t.Fatalf("aborted=%d objects=%d uploads=%d", f.aborted, len(f.objects), len(f.uploads))
		}
	})
	t.Run("different object of the same size", func(t *testing.T) {
		f, srv := newFakeS3(t)
		d := f.open(t, srv.URL, nil)
		f.failDone = 1
		f.onCommit = func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			b := f.objects["m/obj"]
			b[0] ^= 0xff
			f.attrs["m/obj"] = objAttrs{etag: "0123456789abcdef0123456789abcdef-2", checksum: "AAAA-2"}
		}
		_, _, err := putMultipart(t.Context(), d, "m/obj", 14, storage.PutOptions{})
		if err == nil {
			t.Fatal("tampered object accepted as the upload")
		}
		if f.aborted != 1 {
			t.Fatalf("aborted=%d", f.aborted)
		}
	})
}

func TestPutCompletePreconditionAborts(t *testing.T) {
	f, srv := newFakeS3(t)
	d := f.open(t, srv.URL, nil)
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := d.Put(t.Context(), storage.MustParseKey("p/obj"), pr, storage.PutOptions{})
		done <- err
	}()
	mustCopy(t, pw, io.LimitReader(storagetest.Reader(8, 1<<40), MinPartSize+1))
	f.mu.Lock()
	f.objects["p/obj"] = []byte("winner")
	f.mu.Unlock()
	closeLog(t, pw)
	if err := <-done; !errors.Is(err, storage.ErrAlreadyExists) {
		t.Fatalf("put: %v", err)
	}
	if f.aborted != 1 || string(f.objects["p/obj"]) != "winner" {
		t.Fatalf("aborted=%d object=%q", f.aborted, f.objects["p/obj"])
	}
}

func TestPutTooLargeForPartSize(t *testing.T) {
	f, srv := newFakeS3(t)
	d := f.open(t, srv.URL, nil)
	_, err := d.Put(t.Context(), storage.MustParseKey("big"), strings.NewReader("x"),
		storage.PutOptions{SizeHint: MinPartSize*MaxParts + 1})
	if !errors.Is(err, storage.ErrUnsupported) {
		t.Fatalf("put: %v", err)
	}
}

func TestOpenRejectsRedirectAndBlockedEndpoints(t *testing.T) {
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redir.Close)
	cfg := baseConfig(t)
	cfg.Endpoint, cfg.AllowHTTP, cfg.PathStyle = redir.URL, true, true
	if _, err := Open(t.Context(), cfg); err == nil {
		t.Fatal("redirect followed")
	}

	g, err := netguard.New(netguard.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, ep := range []string{"https://10.1.2.3:9000", "https://169.254.169.254", "https://127.0.0.1:9000"} {
		cfg := baseConfig(t)
		cfg.Endpoint, cfg.Guard = ep, g
		_, err := Open(t.Context(), cfg)
		if !errors.Is(err, netguard.ErrBlockedAddress) || storage.Classify(err) != storage.CodeNetwork {
			t.Fatalf("%s: %v (code %s)", ep, err, storage.Classify(err))
		}
	}
}

func TestDeleteAndNoDelete(t *testing.T) {
	f, srv := newFakeS3(t)
	d := f.open(t, srv.URL, func(c *Config) { c.NoDelete = true })
	if err := d.Delete(t.Context(), storage.MustParseKey("x")); !errors.Is(err, storage.ErrDeleteNotPermitted) {
		t.Fatalf("delete: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Stat(t.Context(), storage.MustParseKey("x")); !errors.Is(err, storage.ErrClosed) {
		t.Fatalf("stat after close: %v", err)
	}
}

func TestLimitTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fail") != "" {
			w.WriteHeader(http.StatusInternalServerError)
		}
		if _, err := w.Write(bytes.Repeat([]byte("a"), maxListBody+10)); err != nil {
			return
		}
	}))
	t.Cleanup(srv.Close)
	c := &http.Client{Transport: limitTransport{next: http.DefaultTransport}}
	read := func(q string) error {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/b/k?"+q, http.NoBody)
		if err != nil {
			return err
		}
		resp, err := c.Do(req)
		if err != nil {
			return err
		}
		defer closeLog(t, resp.Body)
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	if err := read("list-type=2"); !errors.Is(err, netguard.ErrResponseTooLarge) {
		t.Fatalf("list body: %v", err)
	}
	if err := read("fail=1&x-id=GetObject"); !errors.Is(err, netguard.ErrResponseTooLarge) {
		t.Fatalf("error body: %v", err)
	}
	if err := read("x-id=GetObject"); err != nil {
		t.Fatalf("object body limited: %v", err)
	}
}

type apiError struct {
	code, msg string
}

func (e apiError) Error() string                 { return e.code + ": " + e.msg }
func (e apiError) ErrorCode() string             { return e.code }
func (e apiError) ErrorMessage() string          { return e.msg }
func (e apiError) ErrorFault() smithy.ErrorFault { return smithy.FaultServer }

type statusError struct{ status int }

func (e statusError) Error() string       { return "status" }
func (e statusError) HTTPStatusCode() int { return e.status }

func TestClassify(t *testing.T) {
	ctx := context.Background()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	tests := []struct {
		name string
		ctx  context.Context
		o    op
		err  error
		want storage.ErrorCode
	}{
		{"no such key", ctx, opOther, apiError{"NoSuchKey", ""}, storage.CodeNotFound},
		{"head 404", ctx, opOther, statusError{404}, storage.CodeNotFound},
		{"no such bucket hidden", ctx, opOther, apiError{"NoSuchBucket", ""}, storage.CodePermission},
		{"access denied", ctx, opOther, apiError{"AccessDenied", "Access Denied"}, storage.CodePermission},
		{"bad signature", ctx, opPut, apiError{"SignatureDoesNotMatch", ""}, storage.CodePermission},
		{"locked delete aws", ctx, opDelete, apiError{"AccessDenied", "Access Denied because object protected by object lock."}, storage.CodeDeleteNotPermitted},
		{"locked delete minio", ctx, opDelete, apiError{"AccessDenied", "Object is WORM protected and cannot be overwritten"}, storage.CodeDeleteNotPermitted},
		{"plain denied delete", ctx, opDelete, apiError{"AccessDenied", "Access Denied"}, storage.CodePermission},
		{"precondition put", ctx, opPut, apiError{"PreconditionFailed", ""}, storage.CodeAlreadyExists},
		{"conflict put", ctx, opPut, apiError{"ConditionalRequestConflict", ""}, storage.CodeAlreadyExists},
		{"bad digest", ctx, opPut, apiError{"BadDigest", ""}, storage.CodeChecksumMismatch},
		{"not implemented", ctx, opOther, apiError{"NotImplemented", ""}, storage.CodeUnsupported},
		{"slow down", ctx, opPut, apiError{"SlowDown", ""}, storage.CodeNetwork},
		{"5xx", ctx, opPut, statusError{503}, storage.CodeNetwork},
		{"timeout", ctx, opPut, apiError{"RequestTimeout", ""}, storage.CodeTimeout},
		{"transport", ctx, opPut, errors.New("dial tcp: connection refused"), storage.CodeNetwork},
		{"blocked", ctx, opOther, &netguard.BlockedAddressError{}, storage.CodeNetwork},
		{"canceled", canceled, opPut, context.Canceled, storage.CodeCanceled},
		{"source", ctx, opPut, storage.ErrSource, storage.CodeInternal},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.ctx, tc.o, tc.err)
			if c := storage.Classify(got); c != tc.want {
				t.Fatalf("classify(%v) = %s (%v), want %s", tc.err, c, got, tc.want)
			}
		})
	}
}
