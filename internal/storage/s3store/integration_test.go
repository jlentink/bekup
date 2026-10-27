//go:build integration

package s3store

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/storagetest"
)

// minioImage is the digest-pinned image of docker-compose.dev.yml.
const minioImage = "cgr.dev/chainguard/minio:latest@sha256:268d68a2a3b6fc65f905a61f84d7eab6345ce6bff14f551e2b028ec31e6a1341"

const (
	testUser = "bekuptestadmin"
	testPass = "bekup-test-password-not-used-anywhere"
	testPart = MinPartSize
)

type minio struct {
	endpoint string // http(s)://127.0.0.1:port
	admin    *s3.Client
	caPEM    []byte // TLS only
}

func startMinio(t *testing.T, withTLS bool) *minio {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        minioImage,
		ExposedPorts: []string{"9000/tcp"},
		Env:          map[string]string{"MINIO_ROOT_USER": testUser, "MINIO_ROOT_PASSWORD": testPass},
		Cmd:          []string{"server", "/tmp/data", "--address", ":9000"},
		WaitingFor:   wait.ForListeningPort("9000/tcp").WithStartupTimeout(90 * time.Second),
	}
	m := &minio{}
	if withTLS {
		caPEM, certPEM, keyPEM := testPKI(t, true)
		m.caPEM = caPEM
		req.Cmd = append(req.Cmd, "--certs-dir", "/tmp/certs")
		req.Files = []testcontainers.ContainerFile{
			{Reader: bytes.NewReader(certPEM), ContainerFilePath: "/tmp/certs/public.crt", FileMode: 0o644},
			{Reader: bytes.NewReader(keyPEM), ContainerFilePath: "/tmp/certs/private.key", FileMode: 0o644},
		}
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	port, err := c.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatal(err)
	}
	scheme := "http"
	hc := &http.Client{Timeout: time.Minute}
	if withTLS {
		scheme = "https"
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(m.caPEM)
		hc.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	}
	m.endpoint = fmt.Sprintf("%s://127.0.0.1:%s", scheme, port.Port())
	m.admin = s3.New(s3.Options{
		Region:       "us-east-1",
		Credentials:  staticProvider{id: testUser, secret: crypto.NewSecret([]byte(testPass))},
		BaseEndpoint: aws.String(m.endpoint),
		UsePathStyle: true,
		HTTPClient:   hc,
	})
	deadline := time.Now().Add(60 * time.Second)
	for {
		_, err := m.admin.ListBuckets(ctx, &s3.ListBucketsInput{})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("minio not ready: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return m
}

var bucketSeq int

func (m *minio) bucket(ctx context.Context, t *testing.T, lock bool) string {
	t.Helper()
	bucketSeq++
	name := fmt.Sprintf("bk-%d-%d", time.Now().UnixNano()%1_000_000_000, bucketSeq)
	_, err := m.admin.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(name), ObjectLockEnabledForBucket: aws.Bool(lock),
	})
	if err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	return name
}

func loopGuard(t *testing.T) *netguard.Guard {
	t.Helper()
	g, err := netguard.New(netguard.Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func (m *minio) config(t *testing.T, bucket string) Config {
	t.Helper()
	return Config{
		Endpoint:        m.endpoint,
		Region:          "us-east-1",
		Bucket:          bucket,
		PathStyle:       true,
		AccessKeyID:     testUser,
		SecretAccessKey: crypto.NewSecret([]byte(testPass)),
		CABundlePEM:     m.caPEM,
		PartSize:        testPart,
		Concurrency:     3,
		AllowHTTP:       true,
		Guard:           loopGuard(t),
	}
}

func open(ctx context.Context, t *testing.T, cfg Config) *Destination {
	t.Helper()
	d, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { closeLog(t, d) })
	return d
}

func (m *minio) assertNoUploads(ctx context.Context, t *testing.T, bucket string) {
	t.Helper()
	out, err := m.admin.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatalf("list uploads: %v", err)
	}
	if len(out.Uploads) != 0 {
		t.Errorf("%d multipart uploads left in progress", len(out.Uploads))
	}
}

func TestConformance(t *testing.T) {
	m := startMinio(t, false)
	for _, tc := range []struct {
		name   string
		lock   bool
		prefix string
		ck     ChecksumMode
	}{
		{"plain", false, "", ChecksumWhenSupported},
		{"prefixed-when-required", false, "tenant/a", ChecksumWhenRequired},
		{"object-lock-bucket", true, "locked", ChecksumWhenSupported},
		{"object-lock-when-required", true, "", ChecksumWhenRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buckets []string
			storagetest.Run(t, storagetest.Config{
				New: func(t *testing.T, o storagetest.Options) storage.Destination {
					b := m.bucket(t.Context(), t, tc.lock)
					buckets = append(buckets, b)
					cfg := m.config(t, b)
					cfg.Prefix, cfg.NoDelete, cfg.Checksum = tc.prefix, o.NoDelete, tc.ck
					d, err := Open(t.Context(), cfg)
					if err != nil {
						t.Fatalf("open: %v", err)
					}
					return d
				},
				LargeSize: 3*testPart + 12345,
			})
			for _, b := range buckets {
				m.assertNoUploads(t.Context(), t, b)
			}
		})
	}
}

func TestObjectLock(t *testing.T) {
	m := startMinio(t, false)
	ctx := t.Context()

	t.Run("RetentionRefusesDelete", func(t *testing.T) {
		b := m.bucket(ctx, t, true)
		d := open(ctx, t, m.config(t, b))
		caps := d.Capabilities()
		if !caps.SupportsObjectLock || !caps.Versioning || !caps.ExclusiveCreate {
			t.Fatalf("capabilities = %+v", caps)
		}
		key := storage.MustParseKey("job/2026-10-27T030000Z/db.dump")
		until := time.Now().Add(time.Hour).Truncate(time.Second)
		if _, err := d.Put(ctx, key, strings.NewReader("locked"), storage.PutOptions{
			LockMode: storage.LockGovernance, RetainUntil: until,
		}); err != nil {
			t.Fatalf("put: %v", err)
		}
		st, err := d.Stat(ctx, key)
		if err != nil || st.LockMode != storage.LockGovernance || st.RetainUntil.Before(until.Add(-time.Second)) {
			t.Fatalf("stat = %+v, %v", st, err)
		}
		if err := d.Delete(ctx, key); !errors.Is(err, storage.ErrDeleteNotPermitted) {
			t.Fatalf("delete: %v, want ErrDeleteNotPermitted", err)
		}
		if _, err := d.Stat(ctx, key); err != nil {
			t.Fatalf("object gone after refused delete: %v", err)
		}
		// A large (multipart) locked object as well.
		bigKey := storage.MustParseKey("job/big")
		if _, err := d.Put(ctx, bigKey, storagetest.Reader(5, 2*testPart+1), storage.PutOptions{
			LockMode: storage.LockCompliance, RetainUntil: until,
		}); err != nil {
			t.Fatalf("multipart put: %v", err)
		}
		if err := d.Delete(ctx, bigKey); !errors.Is(err, storage.ErrDeleteNotPermitted) {
			t.Fatalf("delete big: %v", err)
		}
	})

	t.Run("LegalHold", func(t *testing.T) {
		d := open(ctx, t, m.config(t, m.bucket(ctx, t, true)))
		key := storage.MustParseKey("hold/obj")
		if _, err := d.Put(ctx, key, strings.NewReader("x"), storage.PutOptions{LegalHold: true}); err != nil {
			t.Fatalf("put: %v", err)
		}
		if err := d.Delete(ctx, key); !errors.Is(err, storage.ErrDeleteNotPermitted) {
			t.Fatalf("delete: %v", err)
		}
	})

	t.Run("DefaultRetention", func(t *testing.T) {
		cfg := m.config(t, m.bucket(ctx, t, true))
		cfg.LockMode, cfg.RetentionDays = storage.LockGovernance, 1
		d := open(ctx, t, cfg)
		key := storage.MustParseKey("def/obj")
		info, err := d.Put(ctx, key, strings.NewReader("x"), storage.PutOptions{})
		if err != nil {
			t.Fatalf("put: %v", err)
		}
		if info.LockMode != storage.LockGovernance || time.Until(info.RetainUntil) < 23*time.Hour {
			t.Fatalf("info = %+v", info)
		}
		if err := d.Delete(ctx, key); !errors.Is(err, storage.ErrDeleteNotPermitted) {
			t.Fatalf("delete: %v", err)
		}
		// The probe is written without the default retention and deleted.
		res := d.Test(ctx, storage.MustParseKey("p"))
		if !res.OK || res.ProbeRemains || len(res.Steps) != 3 {
			t.Fatalf("test = %+v", res)
		}
	})

	t.Run("BucketDefaultRetentionProbe", func(t *testing.T) {
		b := m.bucket(ctx, t, true)
		_, err := m.admin.PutObjectLockConfiguration(ctx, &s3.PutObjectLockConfigurationInput{
			Bucket: aws.String(b),
			ObjectLockConfiguration: &types.ObjectLockConfiguration{
				ObjectLockEnabled: types.ObjectLockEnabledEnabled,
				Rule: &types.ObjectLockRule{DefaultRetention: &types.DefaultRetention{
					Mode: types.ObjectLockRetentionModeGovernance, Days: aws.Int32(1),
				}},
			},
		})
		if err != nil {
			t.Fatalf("lock config: %v", err)
		}
		d := open(ctx, t, m.config(t, b))
		res := d.Test(ctx, storage.Key{})
		if !res.OK || !res.ProbeRemains || res.Note != lockNote || len(res.Steps) != 3 ||
			res.Steps[2].Code != storage.CodeDeleteNotPermitted {
			t.Fatalf("test = %+v", res)
		}
	})

	t.Run("LockModeNeedsLockBucket", func(t *testing.T) {
		cfg := m.config(t, m.bucket(ctx, t, false))
		cfg.LockMode, cfg.RetentionDays = storage.LockCompliance, 1
		if _, err := Open(ctx, cfg); !errors.Is(err, storage.ErrUnsupported) {
			t.Fatalf("open: %v, want ErrUnsupported", err)
		}
		d := open(ctx, t, m.config(t, m.bucket(ctx, t, false)))
		_, err := d.Put(ctx, storage.MustParseKey("x"), strings.NewReader("x"), storage.PutOptions{
			LockMode: storage.LockGovernance, RetainUntil: time.Now().Add(time.Hour),
		})
		if !errors.Is(err, storage.ErrUnsupported) {
			t.Fatalf("put with lock on plain bucket: %v", err)
		}
	})
}

type failAfter struct {
	r   io.Reader
	n   int64
	err error
}

func (f *failAfter) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, f.err
	}
	if int64(len(p)) > f.n {
		p = p[:f.n]
	}
	m, err := f.r.Read(p)
	f.n -= int64(m)
	return m, err
}

func TestMultipartAbort(t *testing.T) {
	m := startMinio(t, false)
	ctx := t.Context()
	b := m.bucket(ctx, t, false)
	d := open(ctx, t, m.config(t, b))

	t.Run("SourceFailure", func(t *testing.T) {
		key := storage.MustParseKey("abort/src")
		boom := errors.New("dump failed")
		_, err := d.Put(ctx, key, &failAfter{r: storagetest.Reader(1, 1<<40), n: 3*testPart + 100, err: boom}, storage.PutOptions{})
		if !errors.Is(err, boom) || !errors.Is(err, storage.ErrSource) {
			t.Fatalf("put: %v", err)
		}
		if _, err := d.Stat(ctx, key); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("stat: %v", err)
		}
		m.assertNoUploads(ctx, t, b)
	})

	t.Run("Cancel", func(t *testing.T) {
		key := storage.MustParseKey("abort/cancel")
		cctx, cancel := context.WithCancel(ctx)
		pr, pw := io.Pipe()
		go func() {
			ok := feed(pw, io.LimitReader(storagetest.Reader(2, 1<<40), 2*testPart+10))
			cancel()
			if ok {
				feed(pw, storagetest.Reader(3, 1<<40))
			}
		}()
		_, err := d.Put(cctx, key, pr, storage.PutOptions{})
		stopPipe(t, pr)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("put: %v", err)
		}
		if _, err := d.Stat(ctx, key); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("stat: %v", err)
		}
		m.assertNoUploads(ctx, t, b)
	})

	t.Run("ChecksumMismatch", func(t *testing.T) {
		key := storage.MustParseKey("abort/sum")
		wrong := sha256.Sum256([]byte("nope"))
		_, err := d.Put(ctx, key, storagetest.Reader(4, 2*testPart+1), storage.PutOptions{ExpectedSHA256: wrong[:]})
		if !errors.Is(err, storage.ErrChecksumMismatch) {
			t.Fatalf("put: %v", err)
		}
		if _, err := d.Stat(ctx, key); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("stat: %v", err)
		}
		m.assertNoUploads(ctx, t, b)
	})

	t.Run("ExpectedSHA256StoredForMultipart", func(t *testing.T) {
		key := storage.MustParseKey("meta/sum")
		h := sha256.New()
		mustCopy(t, h, storagetest.Reader(6, 2*testPart+7))
		sum := h.Sum(nil)
		if _, err := d.Put(ctx, key, storagetest.Reader(6, 2*testPart+7), storage.PutOptions{ExpectedSHA256: sum}); err != nil {
			t.Fatal(err)
		}
		st, err := d.Stat(ctx, key)
		if err != nil || hex.EncodeToString(st.SHA256) != hex.EncodeToString(sum) || st.NativeChecksum.Algorithm != "SHA256" {
			t.Fatalf("stat = %+v, %v", st, err)
		}
	})

	t.Run("ConditionalComplete", func(t *testing.T) {
		// The key appears while a multipart upload is running: Complete
		// with If-None-Match must refuse, and the upload is aborted.
		key := storage.MustParseKey("race/obj")
		pr, pw := io.Pipe()
		done := make(chan error, 1)
		go func() {
			_, err := d.Put(ctx, key, pr, storage.PutOptions{})
			done <- err
		}()
		mustCopy(t, pw, io.LimitReader(storagetest.Reader(7, 1<<40), testPart+10))
		if _, err := m.admin.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(b), Key: aws.String(key.String()), Body: strings.NewReader("winner"),
		}); err != nil {
			t.Fatal(err)
		}
		closeLog(t, pw)
		if err := <-done; !errors.Is(err, storage.ErrAlreadyExists) {
			t.Fatalf("put: %v, want ErrAlreadyExists", err)
		}
		rc, _, err := d.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(rc)
		closeLog(t, rc)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "winner" {
			t.Fatalf("object overwritten: %d bytes", len(got))
		}
		m.assertNoUploads(ctx, t, b)
	})

	t.Run("CleanupIncompleteUploads", func(t *testing.T) {
		_, err := m.admin.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String(b), Key: aws.String("left/over")})
		if err != nil {
			t.Fatal(err)
		}
		n, err := d.CleanupIncompleteUploads(ctx, time.Now().Add(time.Minute))
		if err != nil || n != 1 {
			t.Fatalf("cleanup = %d, %v", n, err)
		}
		m.assertNoUploads(ctx, t, b)
	})
}

func TestNetguardBlocksEndpoint(t *testing.T) {
	m := startMinio(t, false)
	cfg := m.config(t, m.bucket(t.Context(), t, false))
	g, err := netguard.New(netguard.Options{LoopbackAllowable: true}) // dev mode, but not allowlisted
	if err != nil {
		t.Fatal(err)
	}
	cfg.Guard = g
	_, err = Open(t.Context(), cfg)
	if !errors.Is(err, netguard.ErrBlockedAddress) || storage.Classify(err) != storage.CodeNetwork {
		t.Fatalf("open: %v (code %s), want a netguard denial", err, storage.Classify(err))
	}
	// Allowlisting the endpoint at runtime makes it reachable.
	if err := g.SetAllowlist([]string{"127.0.0.1/32"}, nil); err != nil {
		t.Fatal(err)
	}
	d, err := Open(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open after allowlisting: %v", err)
	}
	closeLog(t, d)
}

func TestCustomCA(t *testing.T) {
	m := startMinio(t, true)
	b := m.bucket(t.Context(), t, false)

	cfg := m.config(t, b)
	cfg.AllowHTTP = false
	d := open(t.Context(), t, cfg)
	res := d.Test(t.Context(), storage.Key{})
	if !res.OK {
		t.Fatalf("test over TLS = %+v", res)
	}

	wrongCA, _, _ := testPKI(t, false)
	cfg = m.config(t, b)
	cfg.CABundlePEM = wrongCA
	if _, err := Open(t.Context(), cfg); err == nil || storage.Classify(err) != storage.CodeNetwork {
		t.Fatalf("wrong CA: %v", err)
	}
	cfg.CABundlePEM = nil // system roots do not know the test CA either
	if _, err := Open(t.Context(), cfg); err == nil {
		t.Fatal("system roots accepted the test CA")
	}
	cfg = m.config(t, b)
	cfg.Endpoint = strings.Replace(m.endpoint, "https://", "http://", 1)
	cfg.AllowHTTP = false
	if _, err := Open(t.Context(), cfg); !errors.Is(err, storage.ErrInvalidArgument) {
		t.Fatalf("http without dev mode: %v", err)
	}
}

// testPKI returns a CA and (if leaf) a server certificate for 127.0.0.1.
func testPKI(t *testing.T, leaf bool) (caPEM, certPEM, keyPEM []byte) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "bekup test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	if !leaf {
		return caPEM, nil, nil
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "minio"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return caPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})
}
