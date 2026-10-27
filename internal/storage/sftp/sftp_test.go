package sftp

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/storagetest"
)

func TestConformance(t *testing.T) {
	srv := startServer(t)
	base := t.TempDir()
	n := 0
	storagetest.Run(t, storagetest.Config{
		LargeSize: 8 << 20,
		New: func(t *testing.T, o storagetest.Options) storage.Destination {
			n++
			cfg := srv.keyConfig(t, base)
			cfg.Dir = "dest-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + string(rune('a'+n%26))
			cfg.NoDelete = o.NoDelete
			d, err := Open(cfg)
			if err != nil {
				t.Fatal(err)
			}
			return d
		},
	})
}

func TestPartialNeverVisibleAndCleanup(t *testing.T) {
	srv := startServer(t)
	base := t.TempDir()
	d := mustOpen(t, srv.keyConfig(t, base))
	key := storage.MustParseKey("job/2026-10-26T030000Z/db.dump")
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := d.Put(ctxT(t), key, pr, storage.PutOptions{})
		done <- err
	}()
	if _, err := pw.Write(bytes.Repeat([]byte("p"), 128<<10)); err != nil {
		t.Fatal(err)
	}
	// The partial exists on the server but is not an object.
	partials := findPartials(t, base)
	if len(partials) != 1 {
		t.Fatalf("partials during put = %v", partials)
	}
	if fi, err := os.Stat(partials[0]); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("partial mode = %v, %v", fi, err)
	}
	if fi, err := os.Stat(filepath.Join(base, "job")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, %v", fi, err)
	}
	page, err := d.List(ctxT(t), storage.Key{}, storage.ListOptions{})
	if err != nil || len(page.Objects) != 0 {
		t.Fatalf("list during put = %+v, %v", page, err)
	}
	// Fail the source mid-stream: no object, no partial.
	pw.CloseWithError(errors.New("producer died"))
	if err := <-done; !errors.Is(err, storage.ErrSource) {
		t.Fatalf("put = %v, want source error", err)
	}
	if _, err := d.Stat(ctxT(t), key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("stat = %v", err)
	}
	if p := findPartials(t, base); len(p) != 0 {
		t.Fatalf("partials left: %v", p)
	}

	// A crash leftover is removed by CleanupPartials once old enough.
	leftover := filepath.Join(base, "job", ".0123456789abcdef0123456789abcdef.partial")
	if err := os.WriteFile(leftover, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := d.CleanupPartials(ctxT(t), time.Hour); err != nil || n != 0 {
		t.Fatalf("cleanup fresh = %d, %v", n, err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(leftover, old, old); err != nil {
		t.Fatal(err)
	}
	if n, err := d.CleanupPartials(ctxT(t), time.Hour); err != nil || n != 1 {
		t.Fatalf("cleanup old = %d, %v", n, err)
	}
	if _, err := os.Stat(leftover); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("leftover still there: %v", err)
	}
}

func findPartials(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if partialName.MatchString(e.Name()) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWrongHostKeyRejected(t *testing.T) {
	srv := startServer(t)
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(other)
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	for name, pin := range map[string]HostKeyPin{
		"other key":        {AuthorizedKey: string(ssh.MarshalAuthorizedKey(pub))},
		"fingerprint only": {Type: ssh.KeyAlgoED25519, FingerprintSHA256: ssh.FingerprintSHA256(pub)},
		// The real fingerprint under another type: the server does not
		// offer an ECDSA key, which is a pin violation too.
		"wrong type": {Type: ssh.KeyAlgoECDSA256, FingerprintSHA256: ssh.FingerprintSHA256(srv.hostKey)},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := srv.keyConfig(t, base)
			cfg.HostKeyPin = pin
			d := mustOpen(t, cfg)
			_, err := d.Put(ctxT(t), storage.MustParseKey("x"), strings.NewReader("x"), storage.PutOptions{})
			if err == nil {
				t.Fatal("put succeeded with a wrong pin")
			}
			if !errors.Is(err, ErrHostKeyMismatch) {
				t.Fatalf("err = %v, want ErrHostKeyMismatch", err)
			}
			if code := storage.Classify(err); code != storage.CodeHostKeyMismatch {
				t.Fatalf("code = %s, want %s", code, storage.CodeHostKeyMismatch)
			}
			res := d.Test(ctxT(t), storage.Key{})
			if res.OK || len(res.Steps) != 1 || res.Steps[0].Code != storage.CodeHostKeyMismatch || strings.Contains(res.Steps[0].Message, srv.host) {
				t.Fatalf("test = %+v", res)
			}
		})
	}
	if entries, err := os.ReadDir(base); err != nil || len(entries) != 0 {
		t.Fatalf("wrote with a wrong pin: %v", entries)
	}
}

func TestPasswordAuth(t *testing.T) {
	srv := startServer(t)
	base := t.TempDir()
	cfg := srv.keyConfig(t, base)
	cfg.Auth = Auth{Password: crypto.NewSecret([]byte(testPassword))}
	d := mustOpen(t, cfg)
	if res := d.Test(ctxT(t), storage.Key{}); !res.OK {
		t.Fatalf("password auth test = %+v", res)
	}

	cfg = srv.keyConfig(t, base)
	cfg.Auth = Auth{Password: crypto.NewSecret([]byte("wrong"))}
	d = mustOpen(t, cfg)
	res := d.Test(ctxT(t), storage.Key{})
	if res.OK || res.Steps[0].Code != storage.CodePermission {
		t.Fatalf("wrong password test = %+v", res)
	}
}

func TestNetguardBlocksLoopback(t *testing.T) {
	srv := startServer(t)
	g, err := netguard.New(netguard.Options{LoopbackAllowable: true}) // not allowlisted
	if err != nil {
		t.Fatal(err)
	}
	cfg := srv.keyConfig(t, t.TempDir())
	cfg.Guard = g
	d := mustOpen(t, cfg)
	_, err = d.Stat(ctxT(t), storage.MustParseKey("x"))
	if !errors.Is(err, netguard.ErrBlockedAddress) || storage.Classify(err) != storage.CodeNetwork {
		t.Fatalf("err = %v (%s), want blocked", err, storage.Classify(err))
	}
}

func TestSymlinksNotFollowed(t *testing.T) {
	srv := startServer(t)
	root := t.TempDir()
	base := filepath.Join(root, "base")
	outside := filepath.Join(root, "outside")
	for _, p := range []string{base, outside} {
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "victim"), []byte("outside data"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A directory link and a file link escaping the base.
	if err := os.Symlink(outside, filepath.Join(base, "esc")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "victim"), filepath.Join(base, "file")); err != nil {
		t.Fatal(err)
	}
	d := mustOpen(t, srv.keyConfig(t, base))
	ctx := ctxT(t)

	if _, err := d.Put(ctx, storage.MustParseKey("esc/new"), strings.NewReader("x"), storage.PutOptions{}); !errors.Is(err, storage.ErrPermission) {
		t.Fatalf("put through dir link = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("put escaped the base")
	}
	for _, k := range []string{"esc/victim", "file"} {
		key := storage.MustParseKey(k)
		if _, _, err := d.Get(ctx, key); !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("get %s = %v", k, err)
		}
		if _, err := d.Stat(ctx, key); !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("stat %s = %v", k, err)
		}
		if err := d.Delete(ctx, key); err != nil {
			t.Errorf("delete %s = %v", k, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "victim")); err != nil {
		t.Fatal("delete followed a link")
	}
	if _, err := d.Put(ctx, storage.MustParseKey("file"), strings.NewReader("x"), storage.PutOptions{}); !errors.Is(err, storage.ErrAlreadyExists) {
		t.Fatalf("put over a file link = %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(outside, "victim")); err != nil || string(b) != "outside data" { //nolint:gosec // G304: test temp dir
		t.Fatal("put wrote through a file link")
	}
	page, err := d.List(ctx, storage.Key{}, storage.ListOptions{})
	if err != nil || len(page.Objects) != 0 {
		t.Fatalf("list = %+v, %v", page, err)
	}
	if page, err := d.List(ctx, storage.MustParseKey("esc"), storage.ListOptions{}); err != nil || len(page.Objects) != 0 {
		t.Fatalf("list link prefix = %+v, %v", page, err)
	}
	if n, err := d.CleanupPartials(ctx, 0); err != nil || n != 0 {
		t.Fatalf("cleanup = %d, %v", n, err)
	}
}

func TestBasePathSymlinkResolvedOnce(t *testing.T) {
	// The configured base itself may be a link on the server (admin
	// choice); it is resolved and then used as the root.
	srv := startServer(t)
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	d := mustOpen(t, srv.keyConfig(t, link))
	if _, err := d.Put(ctxT(t), storage.MustParseKey("a/b"), strings.NewReader("x"), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(realDir, "a", "b")); err != nil {
		t.Fatal(err)
	}
	cfg := srv.keyConfig(t, filepath.Join(root, "missing"))
	d = mustOpen(t, cfg)
	if _, err := d.Stat(ctxT(t), storage.MustParseKey("a/b")); err == nil {
		t.Fatal("missing base path accepted")
	}
}

func TestRenameFallback(t *testing.T) {
	srv := startServer(t)
	d := mustOpen(t, srv.keyConfig(t, t.TempDir()))
	d.testNoHardlink = true
	key := storage.MustParseKey("r/obj")
	if _, err := d.Put(ctxT(t), key, strings.NewReader("first"), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if d.Capabilities().ExclusiveCreate {
		t.Error("exclusive create reported without hardlink")
	}
	if _, err := d.Put(ctxT(t), key, strings.NewReader("second"), storage.PutOptions{}); !errors.Is(err, storage.ErrAlreadyExists) {
		t.Fatalf("second put = %v", err)
	}
	rc, _, err := d.Get(ctxT(t), key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(rc)
	discard(rc.Close())
	if err != nil || string(b) != "first" {
		t.Fatalf("content = %q", b)
	}
}

func TestReconnectAfterDrop(t *testing.T) {
	srv := startServer(t)
	d := mustOpen(t, srv.keyConfig(t, t.TempDir()))
	key := storage.MustParseKey("re/obj")
	if _, err := d.Put(ctxT(t), key, strings.NewReader("x"), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	srv.dropAll()
	deadline := time.Now().Add(5 * time.Second)
	for d.live() != nil {
		if time.Now().After(deadline) {
			t.Fatal("session not marked dead")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := d.Stat(ctxT(t), key); err != nil {
		t.Fatalf("stat after reconnect: %v", err)
	}
}

func TestClose(t *testing.T) {
	srv := startServer(t)
	pw := crypto.NewSecret([]byte(testPassword))
	cfg := srv.keyConfig(t, t.TempDir())
	cfg.Auth = Auth{Password: pw}
	d, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Stat(ctxT(t), storage.MustParseKey("x")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if pw.Len() != 0 {
		t.Error("password not destroyed by Close")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Stat(ctxT(t), storage.MustParseKey("x")); !errors.Is(err, storage.ErrClosed) {
		t.Fatalf("stat after close = %v", err)
	}
	if _, err := d.List(ctxT(t), storage.Key{}, storage.ListOptions{}); !errors.Is(err, storage.ErrClosed) {
		t.Fatalf("list after close = %v", err)
	}
}

func TestOpenDestroysKeyMaterial(t *testing.T) {
	srv := startServer(t)
	cfg := srv.keyConfig(t, t.TempDir())
	key := cfg.Auth.PrivateKey
	d, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { discard(d.Close()) }()
	if key.Len() != 0 {
		t.Error("private key PEM not destroyed by Open")
	}
	bad := srv.keyConfig(t, "relative")
	bkey := bad.Auth.PrivateKey
	if _, err := Open(bad); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("open = %v", err)
	}
	if bkey.Len() != 0 {
		t.Error("private key PEM not destroyed on failure")
	}
}

func TestConfigValidation(t *testing.T) {
	srv := startServer(t)
	cases := map[string]func(c *Config){
		"no pin":           func(c *Config) { c.HostKeyPin = HostKeyPin{} },
		"no guard":         func(c *Config) { c.Guard = nil },
		"no auth":          func(c *Config) { c.Auth = Auth{} },
		"key and password": func(c *Config) { c.Auth.Password = crypto.NewSecret([]byte("x")) },
		"relative base":    func(c *Config) { c.BasePath = "upload" },
		"unclean base":     func(c *Config) { c.BasePath = "/upload/../etc" },
		"trailing slash":   func(c *Config) { c.BasePath = "/upload/" },
		"bad host":         func(c *Config) { c.Host = "evil host" },
		"empty host":       func(c *Config) { c.Host = "" },
		"bad port":         func(c *Config) { c.Port = 70000 },
		"bad user":         func(c *Config) { c.User = "a b" },
		"dial timeout":     func(c *Config) { c.DialTimeout = time.Hour },
		"keepalive":        func(c *Config) { c.KeepaliveInterval = time.Millisecond },
		"requests":         func(c *Config) { c.MaxConcurrentRequests = 1000 },
		"bad dir":          func(c *Config) { c.Dir = "../x" },
		"passphrase w/o key": func(c *Config) {
			c.Auth = Auth{Password: crypto.NewSecret([]byte("x")), Passphrase: crypto.NewSecret([]byte("y"))}
		},
		"dsa pin": func(c *Config) {
			c.HostKeyPin = HostKeyPin{Type: "ssh-dss", FingerprintSHA256: ssh.FingerprintSHA256(srv.hostKey)}
		},
		"fp without type": func(c *Config) { c.HostKeyPin = HostKeyPin{FingerprintSHA256: ssh.FingerprintSHA256(srv.hostKey)} },
		"bad fp":          func(c *Config) { c.HostKeyPin = HostKeyPin{Type: ssh.KeyAlgoED25519, FingerprintSHA256: "MD5:aa"} },
		"fp mismatch line": func(c *Config) {
			c.HostKeyPin.FingerprintSHA256 = "SHA256:" + strings.Repeat("A", 43)
		},
		"type mismatch line": func(c *Config) { c.HostKeyPin.Type = ssh.KeyAlgoRSA },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := srv.keyConfig(t, "/upload")
			mutate(&cfg)
			if _, err := Open(cfg); !errors.Is(err, storage.ErrInvalidArgument) && !errors.Is(err, storage.ErrInvalidKey) {
				t.Fatalf("open = %v", err)
			}
		})
	}
	// Valid combinations.
	cfg := srv.keyConfig(t, "/")
	cfg.HostKeyPin.Type = ssh.KeyAlgoED25519
	cfg.HostKeyPin.FingerprintSHA256 = ssh.FingerprintSHA256(srv.hostKey)
	d, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	discard(d.Close())
}

func TestAuthKeys(t *testing.T) {
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecDER, err := x509.MarshalECPrivateKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}
	ecPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: ecDER})
	weak, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // G403: the test checks that weak keys are refused
	if err != nil {
		t.Fatal(err)
	}
	weakPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(weak)})
	_, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encBlock, err := ssh.MarshalPrivateKeyWithPassphrase(edPriv, "c", []byte("pass"))
	if err != nil {
		t.Fatal(err)
	}
	encPEM := pem.EncodeToMemory(encBlock)

	for name, tc := range map[string]struct {
		auth Auth
		ok   bool
	}{
		"ecdsa":              {Auth{PrivateKey: crypto.NewSecret(ecPEM)}, true},
		"weak rsa":           {Auth{PrivateKey: crypto.NewSecret(weakPEM)}, false},
		"garbage":            {Auth{PrivateKey: crypto.NewSecret([]byte("not a key"))}, false},
		"encrypted":          {Auth{PrivateKey: crypto.NewSecret(slices.Clone(encPEM))}, false},
		"wrong passphrase":   {Auth{PrivateKey: crypto.NewSecret(slices.Clone(encPEM)), Passphrase: crypto.NewSecret([]byte("nope"))}, false},
		"correct passphrase": {Auth{PrivateKey: crypto.NewSecret(slices.Clone(encPEM)), Passphrase: crypto.NewSecret([]byte("pass"))}, true},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := parseAuth(&tc.auth)
			if tc.ok != (err == nil) {
				t.Fatalf("parseAuth = %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "asn1") {
				t.Errorf("parser details leaked: %v", err)
			}
			if c != nil {
				c.destroy()
			}
		})
	}
}

func TestMapErrClassification(t *testing.T) {
	if got := storage.Classify(mapErr("op", os.ErrNotExist)); got != storage.CodeNotFound {
		t.Errorf("not exist = %s", got)
	}
	if got := storage.Classify(mapErr("op", os.ErrPermission)); got != storage.CodePermission {
		t.Errorf("permission = %s", got)
	}
	if got := storage.Classify(dialError("handshake", errors.New("ssh: unable to authenticate, attempted methods [none publickey]"))); got != storage.CodePermission {
		t.Errorf("auth = %s", got)
	}
	if got := storage.Classify(dialError("handshake", errors.New("ssh: handshake failed: no common algorithm"))); got != storage.CodeNetwork {
		t.Errorf("handshake = %s", got)
	}
}
