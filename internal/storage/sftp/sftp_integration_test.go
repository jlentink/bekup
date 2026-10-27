//go:build integration

package sftp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/storagetest"
)

// atmozImage is the SFTP image of docker-compose.dev.yml (pinned by digest).
const atmozImage = "atmoz/sftp:alpine@sha256:43dd1c023cf1bae487127d4b54660b0aee6e94c188a6599be36fb89ba115f961"

const atmozPassword = "integration-password-51d0"

// The chrooted account sees /upload; on the container file system that is
// /home/bekup/upload.
const (
	atmozBase     = "/upload"
	atmozFSUpload = "/home/bekup/upload"
)

type atmoz struct {
	c         testcontainers.Container
	host      string
	port      int
	hostKey   ssh.PublicKey
	clientPEM []byte
}

var (
	atmozOnce sync.Once
	atmozInst *atmoz
	errAtmoz  error
)

// startAtmoz starts one container for the whole test binary.
func startAtmoz(t *testing.T) *atmoz {
	t.Helper()
	atmozOnce.Do(func() { atmozInst, errAtmoz = runAtmoz() })
	if errAtmoz != nil {
		t.Fatalf("start atmoz/sftp: %v", errAtmoz)
	}
	return atmozInst
}

func runAtmoz() (*atmoz, error) {
	ctx := context.Background()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(hostPriv, "")
	if err != nil {
		return nil, err
	}
	hostPEM := pem.EncodeToMemory(block)
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		return nil, err
	}
	kp, err := crypto.GenerateSSHKeypair("bekup-integration")
	if err != nil {
		return nil, err
	}
	clientPEM := append([]byte(nil), kp.PrivateKeyPEM.Bytes()...)
	kp.PrivateKeyPEM.Destroy()

	c, err := testcontainers.Run(ctx, atmozImage,
		testcontainers.WithCmd(testUser+":"+atmozPassword+":1001:1001:upload"),
		testcontainers.WithFiles(
			testcontainers.ContainerFile{
				Reader:            bytes.NewReader(hostPEM),
				ContainerFilePath: "/etc/ssh/ssh_host_ed25519_key",
				FileMode:          0o600,
			},
			testcontainers.ContainerFile{
				Reader:            strings.NewReader(kp.AuthorizedKey + "\n"),
				ContainerFilePath: "/home/" + testUser + "/.ssh/keys/bekup.pub",
				FileMode:          0o644,
			},
		),
		testcontainers.WithExposedPorts("22/tcp"),
		testcontainers.WithWaitStrategyAndDeadline(3*time.Minute,
			wait.ForLog("Server listening on"),
			wait.ForListeningPort("22/tcp"),
		),
	)
	if err != nil {
		if c != nil {
			discard(c.Terminate(ctx))
		}
		return nil, err
	}
	host, err := c.Host(ctx)
	if err != nil {
		return nil, err
	}
	mp, err := c.MappedPort(ctx, "22/tcp")
	if err != nil {
		return nil, err
	}
	return &atmoz{c: c, host: host, port: int(mp.Num()), hostKey: hostSigner.PublicKey(), clientPEM: clientPEM}, nil
}

func (a *atmoz) config(t *testing.T, dir string) Config {
	t.Helper()
	return Config{
		Host:       a.host,
		Port:       a.port,
		User:       testUser,
		Auth:       Auth{PrivateKey: crypto.NewSecret(append([]byte(nil), a.clientPEM...))},
		HostKeyPin: HostKeyPin{AuthorizedKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(a.hostKey)))},
		BasePath:   atmozBase,
		Dir:        dir,
		Guard:      loopbackGuard(t),
	}
}

// exec runs a shell command as root in the container.
func (a *atmoz) exec(t *testing.T, script string) string {
	t.Helper()
	code, r, err := a.c.Exec(context.Background(), []string{"sh", "-c", script})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("exec output: %v", err)
	}
	if code != 0 {
		t.Fatalf("exec %q: exit %d: %s", script, code, out)
	}
	return string(out)
}

func uniqueDir(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return "it-" + hex.EncodeToString(b[:])
}

func TestMain(m *testing.M) {
	code := m.Run()
	if atmozInst != nil {
		discard(atmozInst.c.Terminate(context.Background()))
	}
	os.Exit(code)
}

func TestIntegrationConformance(t *testing.T) {
	a := startAtmoz(t)
	storagetest.Run(t, storagetest.Config{
		LargeSize: 50 << 20,
		New: func(t *testing.T, o storagetest.Options) storage.Destination {
			cfg := a.config(t, uniqueDir(t))
			cfg.NoDelete = o.NoDelete
			d, err := Open(cfg)
			if err != nil {
				t.Fatal(err)
			}
			return d
		},
	})
}

func TestIntegrationServerExtensions(t *testing.T) {
	a := startAtmoz(t)
	d := mustOpen(t, a.config(t, uniqueDir(t)))
	if _, err := d.Put(ctxT(t), storage.MustParseKey("x/y"), strings.NewReader("x"), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	s := d.live()
	if s == nil || !s.hardlink || !s.fsync {
		t.Fatalf("OpenSSH extensions not used: %+v", s)
	}
	if !d.Capabilities().ExclusiveCreate {
		t.Error("exclusive create not reported")
	}
	// Modes on the server: directories 0700, files 0600.
	out := a.exec(t, "stat -c '%a %n' "+atmozFSUpload+"/"+strings.TrimPrefix(s.root, atmozBase+"/")+"/x "+atmozFSUpload+"/"+strings.TrimPrefix(s.root, atmozBase+"/")+"/x/y")
	if !strings.Contains(out, "700 ") || !strings.Contains(out, "600 ") {
		t.Fatalf("modes: %s", out)
	}
}

func TestIntegrationWrongHostKey(t *testing.T) {
	a := startAtmoz(t)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	cfg := a.config(t, uniqueDir(t))
	cfg.HostKeyPin = HostKeyPin{Type: ssh.KeyAlgoED25519, FingerprintSHA256: ssh.FingerprintSHA256(sp)}
	d := mustOpen(t, cfg)
	_, err = d.Put(ctxT(t), storage.MustParseKey("x"), strings.NewReader("x"), storage.PutOptions{})
	var mm *HostKeyMismatchError
	if !errors.As(err, &mm) || mm.PresentedFingerprint != ssh.FingerprintSHA256(a.hostKey) {
		t.Fatalf("err = %v, want a host key mismatch", err)
	}
	if res := d.Test(ctxT(t), storage.Key{}); res.OK || res.Steps[0].Code != storage.CodeHostKeyMismatch {
		t.Fatalf("test = %+v", res)
	}
}

func TestIntegrationAuth(t *testing.T) {
	a := startAtmoz(t)
	t.Run("key", func(t *testing.T) {
		d := mustOpen(t, a.config(t, uniqueDir(t)))
		if res := d.Test(ctxT(t), storage.Key{}); !res.OK {
			t.Fatalf("test = %+v", res)
		}
	})
	t.Run("password", func(t *testing.T) {
		cfg := a.config(t, uniqueDir(t))
		cfg.Auth = Auth{Password: crypto.NewSecret([]byte(atmozPassword))}
		d := mustOpen(t, cfg)
		if res := d.Test(ctxT(t), storage.Key{}); !res.OK {
			t.Fatalf("test = %+v", res)
		}
	})
	t.Run("wrong password", func(t *testing.T) {
		cfg := a.config(t, uniqueDir(t))
		cfg.Auth = Auth{Password: crypto.NewSecret([]byte("wrong"))}
		d := mustOpen(t, cfg)
		if res := d.Test(ctxT(t), storage.Key{}); res.OK || res.Steps[0].Code != storage.CodePermission {
			t.Fatalf("test = %+v", res)
		}
	})
}

type failAfter struct {
	n   int
	err error
}

func (f *failAfter) Read(b []byte) (int, error) {
	if f.n <= 0 {
		return 0, f.err
	}
	m := min(len(b), f.n)
	for i := range m {
		b[i] = 'f'
	}
	f.n -= m
	return m, nil
}

func TestIntegrationPartialHandling(t *testing.T) {
	a := startAtmoz(t)
	dir := uniqueDir(t)
	d := mustOpen(t, a.config(t, dir))
	key := storage.MustParseKey("job/2026-10-26T030000Z/db.dump.zst.age")
	fsDir := atmozFSUpload + "/" + dir

	// While streaming, only a hidden partial exists.
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := d.Put(ctxT(t), key, pr, storage.PutOptions{})
		done <- err
	}()
	if _, err := pw.Write(bytes.Repeat([]byte("p"), 256<<10)); err != nil {
		t.Fatal(err)
	}
	out := a.exec(t, "find "+fsDir+" -type f")
	if !strings.Contains(out, ".partial") || strings.Contains(out, "db.dump.zst.age\n") {
		t.Fatalf("files during put: %s", out)
	}
	if page, err := d.List(ctxT(t), storage.Key{}, storage.ListOptions{}); err != nil || len(page.Objects) != 0 {
		t.Fatalf("list during put = %+v, %v", page, err)
	}
	if _, err := d.Stat(ctxT(t), key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("stat during put = %v", err)
	}
	if _, err := pw.Write([]byte("tail")); err != nil {
		t.Fatal(err)
	}
	discard(pw.Close())
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if out := a.exec(t, "find "+fsDir+" -name '*.partial*'"); strings.TrimSpace(out) != "" {
		t.Fatalf("partial left after success: %s", out)
	}

	// A failure mid-stream leaves no final object and no partial.
	bad := storage.MustParseKey("job/2026-10-26T030000Z/other.dump.zst.age")
	_, err := d.Put(ctxT(t), bad, &failAfter{n: 5 << 20, err: errors.New("dump failed")}, storage.PutOptions{})
	if !errors.Is(err, storage.ErrSource) {
		t.Fatalf("put = %v", err)
	}
	if _, err := d.Stat(ctxT(t), bad); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("stat = %v", err)
	}
	if out := a.exec(t, "find "+fsDir+" -name '*.partial*' -o -name 'other*'"); strings.TrimSpace(out) != "" {
		t.Fatalf("leftovers after failure: %s", out)
	}

	// A crash leftover (old partial) is removed by CleanupPartials.
	a.exec(t, "touch -d '2020-01-01' "+fsDir+"/job/.00112233445566778899aabbccddeeff.partial")
	if n, err := d.CleanupPartials(ctxT(t), time.Hour); err != nil || n != 1 {
		t.Fatalf("cleanup = %d, %v", n, err)
	}
}

func TestIntegrationNeverOverwriteRace(t *testing.T) {
	a := startAtmoz(t)
	d := mustOpen(t, a.config(t, uniqueDir(t)))
	for round := range 10 {
		key := storage.MustParseKey(fmt.Sprintf("race/obj-%02d", round))
		var won, lost atomic.Int32
		var wg sync.WaitGroup
		for _, fill := range []byte("ab") {
			wg.Go(func() {
				payload := bytes.Repeat([]byte{fill}, 64<<10)
				_, err := d.Put(ctxT(t), key, bytes.NewReader(payload), storage.PutOptions{})
				switch {
				case err == nil:
					won.Add(1)
				case errors.Is(err, storage.ErrAlreadyExists):
					lost.Add(1)
				default:
					t.Errorf("put: %v", err)
				}
			})
		}
		wg.Wait()
		if won.Load() != 1 || lost.Load() != 1 {
			t.Fatalf("round %d: %d won, %d lost", round, won.Load(), lost.Load())
		}
		rc, _, err := d.Get(ctxT(t), key)
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		discard(rc.Close())
		if err != nil {
			t.Fatal(err)
		}
		if len(b) != 64<<10 || (!bytes.Equal(b, bytes.Repeat([]byte("a"), 64<<10)) && !bytes.Equal(b, bytes.Repeat([]byte("b"), 64<<10))) {
			t.Fatalf("round %d: mixed content", round)
		}
	}
}

func TestIntegrationNetguard(t *testing.T) {
	a := startAtmoz(t)
	g, err := netguard.New(netguard.Options{LoopbackAllowable: true}) // nothing allowlisted
	if err != nil {
		t.Fatal(err)
	}
	ip, err := a.c.ContainerIP(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for name, host := range map[string]string{"published port": a.host, "private container IP": ip} {
		t.Run(name, func(t *testing.T) {
			cfg := a.config(t, uniqueDir(t))
			cfg.Host = host
			if host == ip {
				cfg.Port = 22
			}
			cfg.Guard = g
			d := mustOpen(t, cfg)
			_, err := d.Stat(ctxT(t), storage.MustParseKey("x"))
			if !errors.Is(err, netguard.ErrBlockedAddress) {
				t.Fatalf("err = %v, want blocked", err)
			}
			if res := d.Test(ctxT(t), storage.Key{}); res.OK || res.Steps[0].Code != storage.CodeNetwork {
				t.Fatalf("test = %+v", res)
			}
		})
	}
	// The same server with the loopback allowlisted works (a.config).
	d := mustOpen(t, a.config(t, uniqueDir(t)))
	if res := d.Test(ctxT(t), storage.Key{}); !res.OK {
		t.Fatalf("allowlisted test = %+v", res)
	}
}

func TestIntegrationSymlinkEscape(t *testing.T) {
	a := startAtmoz(t)
	dir := uniqueDir(t)
	d := mustOpen(t, a.config(t, dir))
	// Connect once so the destination directory exists.
	if _, err := d.Stat(ctxT(t), storage.MustParseKey("none")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
	fsDir := atmozFSUpload + "/" + dir
	outside := atmozFSUpload + "/" + dir + "-outside"
	a.exec(t, "mkdir -p "+outside+" && echo 'outside data' > "+outside+"/victim && chown -R 1001:1001 "+outside+
		" && ln -s ../"+dir+"-outside "+fsDir+"/esc && ln -s ../"+dir+"-outside/victim "+fsDir+"/file")
	ctx := ctxT(t)

	if _, err := d.Put(ctx, storage.MustParseKey("esc/new"), strings.NewReader("x"), storage.PutOptions{}); !errors.Is(err, storage.ErrPermission) {
		t.Fatalf("put through a dir link = %v", err)
	}
	if _, err := d.Put(ctx, storage.MustParseKey("file"), strings.NewReader("x"), storage.PutOptions{}); !errors.Is(err, storage.ErrAlreadyExists) {
		t.Fatalf("put over a file link = %v", err)
	}
	for _, k := range []string{"esc/victim", "file"} {
		key := storage.MustParseKey(k)
		if _, _, err := d.Get(ctx, key); !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("get %s = %v", k, err)
		}
		if err := d.Delete(ctx, key); err != nil {
			t.Errorf("delete %s = %v", k, err)
		}
	}
	if page, err := d.List(ctx, storage.Key{}, storage.ListOptions{}); err != nil || len(page.Objects) != 0 {
		t.Fatalf("list = %+v, %v", page, err)
	}
	out := a.exec(t, "ls "+outside+" && cat "+outside+"/victim")
	if strings.Contains(out, "new") || !strings.Contains(out, "outside data") {
		t.Fatalf("outside directory changed: %s", out)
	}
}
