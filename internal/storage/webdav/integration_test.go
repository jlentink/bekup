//go:build integration

package webdav

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/storagetest"
	"github.com/jlentink/bekup/internal/storage/webdav/webdavtest"
)

// One container per server kind for the whole test binary.
type shared struct {
	once sync.Once
	srv  *webdavtest.Server
	err  error
}

var (
	apache, rclone, owncloud shared
	sharedMu                 sync.Mutex
	started                  []*webdavtest.Server
)

func (s *shared) get(t *testing.T, image string, start func(context.Context) (*webdavtest.Server, error)) *webdavtest.Server {
	t.Helper()
	webdavtest.Ensure(t, image)
	s.once.Do(func() {
		s.srv, s.err = start(context.Background())
		if s.err == nil {
			sharedMu.Lock()
			started = append(started, s.srv)
			sharedMu.Unlock()
		}
	})
	if s.err != nil {
		t.Fatalf("start %s: %v", image, s.err)
	}
	return s.srv
}

func TestMain(m *testing.M) {
	code := m.Run()
	for _, s := range started {
		discard(s.Terminate(context.Background()))
	}
	os.Exit(code)
}

func itConfig(t *testing.T, s *webdavtest.Server, dir string) Config {
	t.Helper()
	return Config{
		URL: s.URL, Username: s.User, Password: crypto.NewSecret([]byte(s.Password)),
		Path: dir, CABundlePEM: s.CertPEM, Guard: testGuard(t),
	}
}

func conformance(t *testing.T, size int64, cfg func(t *testing.T) Config) {
	t.Helper()
	storagetest.Run(t, storagetest.Config{
		LargeSize: size,
		New: func(t *testing.T, o storagetest.Options) storage.Destination {
			c := cfg(t)
			c.NoDelete = o.NoDelete
			d, err := Open(c)
			if err != nil {
				t.Fatal(err)
			}
			return d
		},
	})
}

func TestIntegrationApache(t *testing.T) {
	s := apache.get(t, webdavtest.ApacheImage, webdavtest.StartApache)
	t.Run("conformance", func(t *testing.T) {
		conformance(t, 50<<20, func(t *testing.T) Config { return itConfig(t, s, webdavtest.UniqueDir()) })
	})
	t.Run("probe", func(t *testing.T) {
		d := open(t, itConfig(t, s, webdavtest.UniqueDir()))
		if err := d.ProbeOverwrite(ctxT(t)); err != nil {
			t.Fatalf("Apache mod_dav must refuse Overwrite: F: %v", err)
		}
		if !d.Capabilities().ExclusiveCreate {
			t.Error("ExclusiveCreate not set")
		}
		if got := d.DetectServer(ctxT(t)); got != ServerUnknown {
			t.Errorf("DetectServer = %q", got)
		}
	})
	t.Run("pin", func(t *testing.T) {
		cfg := itConfig(t, s, webdavtest.UniqueDir())
		cfg.CABundlePEM = nil
		rep := Inspect(ctxT(t), InspectConfig{URL: cfg.URL, Guard: cfg.Guard})
		if rep.Trusted || rep.Certificate == nil || !rep.Certificate.SelfSigned {
			t.Fatalf("untrusted inspection: %+v", rep)
		}
		cert, err := rep.Certificate.Parsed()
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckPinnable(cert, "127.0.0.1", time.Now()); err != nil {
			t.Fatalf("pinnable: %v", err)
		}
		cfg.Pin = &Pin{CertificatePEM: rep.Certificate.PEM(), SPKISHA256: rep.Certificate.SPKISHA256}
		d := open(t, cfg)
		if res := d.Test(ctxT(t), storage.Key{}); !res.OK {
			t.Fatalf("pinned test: %+v", res)
		}
	})
	t.Run("cleanup and prune", func(t *testing.T) {
		dir := webdavtest.UniqueDir()
		d := open(t, itConfig(t, s, dir))
		ctx := ctxT(t)
		if _, err := d.Put(ctx, storage.MustParseKey("job/2026-11-03T010000Z/f"), strings.NewReader("x"), storage.PutOptions{}); err != nil {
			t.Fatal(err)
		}
		base := "/usr/local/apache2/dav/backups/" + dir
		if _, err := s.Exec(ctx, "mkdir -p "+base+"/job/2026-10-01T010000Z && touch -d '2026-10-01 00:00:00' "+base+"/job/2026-10-01T010000Z "+
			"&& printf x > "+base+"/job/."+strings.Repeat("d", 32)+".partial && touch -d '2026-10-01 00:00:00' "+base+"/job/."+strings.Repeat("d", 32)+".partial "+
			"&& chown -R daemon:daemon "+base); err != nil {
			t.Fatal(err)
		}
		n, err := d.CleanupPartials(ctx, 24*time.Hour)
		if err != nil || n != 1 {
			t.Fatalf("cleanup: %d, %v", n, err)
		}
		pruned, err := d.PruneEmptyDirs(ctx, 24*time.Hour, nil)
		if err != nil || pruned != 1 {
			t.Fatalf("prune: %d, %v", pruned, err)
		}
		if _, err := d.Stat(ctx, storage.MustParseKey("job/2026-11-03T010000Z/f")); err != nil {
			t.Fatalf("object gone: %v", err)
		}
	})
}

func TestIntegrationRclone(t *testing.T) {
	s := rclone.get(t, webdavtest.RcloneImage, webdavtest.StartRclone)
	conformance(t, 20<<20, func(t *testing.T) Config { return itConfig(t, s, webdavtest.UniqueDir()) })
}

// ownCloud 10 is the base of TransIP STACK: the Nextcloud flavor's chunked
// uploads (ownCloud "new chunking") and the generic flavor on its WebDAV
// endpoint.
func TestIntegrationOwnCloud(t *testing.T) {
	s := owncloud.get(t, webdavtest.OwnCloudImage, webdavtest.StartOwnCloud)
	ncConfig := func(t *testing.T) Config {
		t.Helper()
		return Config{
			URL: s.URL, Flavor: FlavorNextcloud, Username: s.User, Password: crypto.NewSecret([]byte(s.Password)),
			Path: "bekup/" + webdavtest.UniqueDir(), ChunkSize: MinChunkSize, AllowHTTP: true, Guard: testGuard(t),
		}
	}
	t.Run("chunked", func(t *testing.T) {
		d := open(t, ncConfig(t))
		if err := d.ProbeOverwrite(ctxT(t)); err != nil {
			t.Fatalf("probe: %v", err)
		}
		if d.UploadMode() != UploadChunked {
			t.Fatalf("upload mode %q", d.UploadMode())
		}
		if got := d.DetectServer(ctxT(t)); got != ServerOwnCloud {
			t.Errorf("DetectServer = %q", got)
		}
		key := storage.MustParseKey("job/2026-11-03T010000Z/big")
		data := bytes.Repeat([]byte("0123456789abcdef"), (3<<20+7)/16)
		if _, err := d.Put(ctxT(t), key, bytes.NewReader(data), storage.PutOptions{}); err != nil {
			t.Fatalf("put: %v", err)
		}
		if st, err := d.Stat(ctxT(t), key); err != nil || st.Size != int64(len(data)) {
			t.Fatalf("stat %+v %v", st, err)
		}
		_, err := d.Put(ctxT(t), key, bytes.NewReader(data[:10]), storage.PutOptions{})
		if !errors.Is(err, storage.ErrAlreadyExists) {
			t.Fatalf("second put: %v", err)
		}
	})
	t.Run("conformance chunked", func(t *testing.T) {
		conformance(t, 6<<20+123, ncConfig)
	})
	t.Run("conformance generic", func(t *testing.T) {
		conformance(t, 6<<20+123, func(t *testing.T) Config {
			return Config{
				URL: s.Root + "/remote.php/webdav", Username: s.User, Password: crypto.NewSecret([]byte(s.Password)),
				Path: "generic/" + webdavtest.UniqueDir(), AllowHTTP: true, Guard: testGuard(t),
			}
		})
	})
}
