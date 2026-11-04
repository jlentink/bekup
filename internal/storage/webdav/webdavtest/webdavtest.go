// Package webdavtest starts real WebDAV servers for integration tests
// (internal/storage/webdav, internal/destinations, internal/graph): Apache
// httpd with mod_dav (generic WebDAV, TLS with a self-signed certificate
// for 127.0.0.1), rclone serve webdav (the server of the dev compose
// file) and ownCloud 10 (the base of TransIP STACK; chunked uploads).
//
// Images are pinned by digest and pulled through imagepull with the
// rate-limit retry; ports are published on 127.0.0.1 only. Callers
// terminate the servers (Terminate).
package webdavtest

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"

	"github.com/jlentink/bekup/internal/testutil/dockertest"
	"github.com/jlentink/bekup/internal/testutil/imagepull"
)

// Images (pinned by digest). RcloneImage is the one of
// docker-compose.dev.yml.
const (
	ApacheImage   = "httpd:2.4.69@sha256:c6b7745aa371570cc8460a9f08c39989583d4aa7bc8e3ed8bd77a492803ea704"
	RcloneImage   = "rclone/rclone:1.74.0@sha256:d2e0e88359d0b2e67cfcd2c43d5405185eb8adfc207079df27c42da82c5207bc"
	OwnCloudImage = "owncloud/server:10.16.4@sha256:791bec59061be9f14e7bdf19ef4d8da58f157357d19190ef9ed709ce94e09b35"
)

// Account facts.
const (
	User     = "bekup"
	Password = "integration-webdav-3f9a"
	// OwnCloudUser is ownCloud's admin account (its user id too).
	OwnCloudUser = "admin"
)

const startupDeadline = 4 * time.Minute

// Server is a running WebDAV server.
type Server struct {
	Container testcontainers.Container
	// Root is the scheme, host and port (https://127.0.0.1:<port>, or
	// http for ownCloud).
	Root string
	// URL is the generic collection URL that holds the backups (Apache,
	// rclone) or the server root (ownCloud).
	URL string
	// CertPEM is the server's self-signed certificate (nil for ownCloud,
	// which serves plain http here: dev-mode AllowHTTP).
	CertPEM []byte
	// User and Password are the Basic credentials.
	User, Password string
}

// Terminate removes the container.
func (s *Server) Terminate(ctx context.Context) error { return s.Container.Terminate(ctx) }

// Exec runs a shell script in the container and returns its output.
func (s *Server) Exec(ctx context.Context, script string) (string, error) {
	code, r, err := s.Container.Exec(ctx, []string{"sh", "-c", script})
	if err != nil {
		return "", err
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return string(out), fmt.Errorf("exec: exit %d", code)
	}
	return string(out), nil
}

// Ensure pulls image (imagepull's retry policy) or skips the test without
// Docker (fails it in CI).
func Ensure(t testing.TB, image string) {
	t.Helper()
	c, err := client.New(client.FromEnv)
	if err != nil {
		dockertest.Skipf(t, "docker: %v", err)
		return
	}
	defer func() {
		if err := c.Close(); err != nil {
			t.Logf("close docker client: %v", err)
		}
	}()
	if _, err := c.Ping(t.Context(), client.PingOptions{}); err != nil {
		dockertest.Skipf(t, "docker daemon not reachable: %v", err)
		return
	}
	imagepull.Ensure(t, c, image)
}

// SelfSigned returns a self-signed ECDSA certificate and key for
// 127.0.0.1 (and "localhost").
func SelfSigned() (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "bekup webdav test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(7 * 24 * time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), nil
}

// htpasswd returns a bcrypt htpasswd line ($2y$, as Apache's htpasswd -B
// writes it).
func htpasswd(user, password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		return "", err
	}
	return user + ":" + strings.Replace(string(h), "$2a$", "$2y$", 1) + "\n", nil
}

// apacheConf serves /usr/local/apache2/dav with mod_dav over TLS and
// Basic authentication; the folder "backups" holds the backups (the
// Debian image: the Alpine one lacks the DBM driver of the lock database).
const apacheConf = `ServerRoot "/usr/local/apache2"
Listen 8443
LoadModule mpm_event_module modules/mod_mpm_event.so
LoadModule unixd_module modules/mod_unixd.so
LoadModule authn_core_module modules/mod_authn_core.so
LoadModule authn_file_module modules/mod_authn_file.so
LoadModule authz_core_module modules/mod_authz_core.so
LoadModule authz_user_module modules/mod_authz_user.so
LoadModule auth_basic_module modules/mod_auth_basic.so
LoadModule dav_module modules/mod_dav.so
LoadModule dav_fs_module modules/mod_dav_fs.so
LoadModule ssl_module modules/mod_ssl.so
LoadModule socache_shmcb_module modules/mod_socache_shmcb.so
LoadModule log_config_module modules/mod_log_config.so
User daemon
Group daemon
ServerName localhost
UseCanonicalName Off
ErrorLog /proc/self/fd/2
LogLevel warn
DAVLockDB /usr/local/apache2/var/DavLock
SSLEngine on
SSLProtocol -all +TLSv1.2 +TLSv1.3
SSLCertificateFile /usr/local/apache2/conf/server.crt
SSLCertificateKeyFile /usr/local/apache2/conf/server.key
DocumentRoot "/usr/local/apache2/dav"
<Directory "/usr/local/apache2/dav">
    Dav On
    Options None
    AllowOverride None
    AuthType Basic
    AuthName "bekup"
    AuthUserFile /usr/local/apache2/conf/htpasswd
    Require valid-user
</Directory>
`

// publish binds port on 127.0.0.1 only.
func publish(port string) testcontainers.CustomizeRequestOption {
	p := network.MustParsePort(port)
	return testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
		hc.PortBindings = network.PortMap{p: {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "0"}}}
	})
}

func mappedRoot(ctx context.Context, c testcontainers.Container, port, scheme string) (string, error) {
	mp, err := c.MappedPort(ctx, port)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s://127.0.0.1:%d", scheme, mp.Num()), nil
}

// StartApache starts Apache httpd with mod_dav.
func StartApache(ctx context.Context) (*Server, error) {
	certPEM, keyPEM, err := SelfSigned()
	if err != nil {
		return nil, err
	}
	pw, err := htpasswd(User, Password)
	if err != nil {
		return nil, err
	}
	c, err := testcontainers.Run(ctx, ApacheImage,
		testcontainers.WithFiles(
			testcontainers.ContainerFile{Reader: strings.NewReader(apacheConf), ContainerFilePath: "/usr/local/apache2/conf/httpd.conf", FileMode: 0o644},
			testcontainers.ContainerFile{Reader: bytes.NewReader(certPEM), ContainerFilePath: "/usr/local/apache2/conf/server.crt", FileMode: 0o644},
			testcontainers.ContainerFile{Reader: bytes.NewReader(keyPEM), ContainerFilePath: "/usr/local/apache2/conf/server.key", FileMode: 0o600},
			testcontainers.ContainerFile{Reader: strings.NewReader(pw), ContainerFilePath: "/usr/local/apache2/conf/htpasswd", FileMode: 0o644},
		),
		testcontainers.WithEntrypoint("sh", "-c",
			"mkdir -p /usr/local/apache2/var /usr/local/apache2/dav/backups && "+
				"chown -R daemon:daemon /usr/local/apache2/var /usr/local/apache2/dav && exec httpd-foreground"),
		testcontainers.WithExposedPorts("8443/tcp"),
		publish("8443/tcp"),
		testcontainers.WithWaitStrategyAndDeadline(startupDeadline, wait.ForMappedPort("8443/tcp"), wait.ForLog("resuming normal operations")),
	)
	if err != nil {
		return nil, withLogs(ctx, c, fmt.Errorf("run apache: %w", err))
	}
	root, err := mappedRoot(ctx, c, "8443/tcp", "https")
	if err != nil {
		return nil, withLogs(ctx, c, err)
	}
	s := &Server{Container: c, Root: root, URL: root + "/backups", CertPEM: certPEM, User: User, Password: Password}
	if err := waitTLS(ctx, s); err != nil {
		return nil, withLogs(ctx, c, err)
	}
	return s, nil
}

// StartRclone starts rclone serve webdav as in docker-compose.dev.yml (the
// password read from a file inside the container, never on the argv).
func StartRclone(ctx context.Context) (*Server, error) {
	certPEM, keyPEM, err := SelfSigned()
	if err != nil {
		return nil, err
	}
	c, err := testcontainers.Run(ctx, RcloneImage,
		testcontainers.WithFiles(
			testcontainers.ContainerFile{Reader: bytes.NewReader(certPEM), ContainerFilePath: "/certs/tls.crt", FileMode: 0o644},
			testcontainers.ContainerFile{Reader: bytes.NewReader(keyPEM), ContainerFilePath: "/certs/tls.key", FileMode: 0o644},
			testcontainers.ContainerFile{Reader: strings.NewReader(Password + "\n"), ContainerFilePath: "/run/secrets/webdav_password", FileMode: 0o644},
		),
		testcontainers.WithEntrypoint("/bin/sh", "-ec",
			`RCLONE_PASS="$(cat /run/secrets/webdav_password)"; export RCLONE_PASS; mkdir -p /data/backups; `+
				`exec rclone serve webdav /data --addr :8443 --cert /certs/tls.crt --key /certs/tls.key --user `+User),
		testcontainers.WithExposedPorts("8443/tcp"),
		publish("8443/tcp"),
		testcontainers.WithWaitStrategyAndDeadline(startupDeadline, wait.ForMappedPort("8443/tcp")),
	)
	if err != nil {
		return nil, withLogs(ctx, c, fmt.Errorf("run rclone: %w", err))
	}
	root, err := mappedRoot(ctx, c, "8443/tcp", "https")
	if err != nil {
		return nil, withLogs(ctx, c, err)
	}
	s := &Server{Container: c, Root: root, URL: root + "/backups", CertPEM: certPEM, User: User, Password: Password}
	if err := waitTLS(ctx, s); err != nil {
		return nil, withLogs(ctx, c, err)
	}
	return s, nil
}

// StartOwnCloud starts ownCloud 10 (SQLite, plain http on 8080: use the
// destination's dev-mode AllowHTTP). Installation takes a minute or two.
func StartOwnCloud(ctx context.Context) (*Server, error) {
	c, err := testcontainers.Run(ctx, OwnCloudImage,
		testcontainers.WithEnv(map[string]string{
			"OWNCLOUD_DOMAIN":          "127.0.0.1",
			"OWNCLOUD_TRUSTED_DOMAINS": "127.0.0.1,localhost",
			"OWNCLOUD_ADMIN_USERNAME":  OwnCloudUser,
			"OWNCLOUD_ADMIN_PASSWORD":  Password,
			"OWNCLOUD_DB_TYPE":         "sqlite",
		}),
		testcontainers.WithExposedPorts("8080/tcp"),
		publish("8080/tcp"),
		testcontainers.WithWaitStrategyAndDeadline(startupDeadline, wait.ForMappedPort("8080/tcp")),
	)
	if err != nil {
		return nil, withLogs(ctx, c, fmt.Errorf("run owncloud: %w", err))
	}
	root, err := mappedRoot(ctx, c, "8080/tcp", "http")
	if err != nil {
		return nil, withLogs(ctx, c, err)
	}
	s := &Server{Container: c, Root: root, URL: root, User: OwnCloudUser, Password: Password}
	if err := waitInstalled(ctx, root); err != nil {
		return nil, withLogs(ctx, c, err)
	}
	return s, nil
}

// waitTLS waits until the server answers an authenticated PROPFIND.
func waitTLS(ctx context.Context, s *Server) error {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(s.CertPEM)
	hc := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	return poll(ctx, func() error {
		req, err := http.NewRequestWithContext(ctx, "PROPFIND", s.URL+"/", http.NoBody)
		if err != nil {
			return err
		}
		req.Header.Set("Depth", "0")
		req.SetBasicAuth(s.User, s.Password)
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		discard(resp.Body.Close())
		if resp.StatusCode != http.StatusMultiStatus {
			return fmt.Errorf("PROPFIND: HTTP %d", resp.StatusCode)
		}
		return nil
	})
}

// waitInstalled waits until status.php reports an installed server.
func waitInstalled(ctx context.Context, root string) error {
	hc := &http.Client{Timeout: 5 * time.Second}
	return poll(ctx, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/status.php", http.NoBody)
		if err != nil {
			return err
		}
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		defer func() { discard(resp.Body.Close()) }()
		var st struct {
			Installed   bool `json:"installed"`
			Maintenance bool `json:"maintenance"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&st); err != nil {
			return err
		}
		if !st.Installed || st.Maintenance {
			return errors.New("not installed yet")
		}
		return nil
	})
}

func poll(ctx context.Context, try func() error) error {
	ctx, cancel := context.WithTimeout(ctx, startupDeadline)
	defer cancel()
	var last error
	for {
		if last = try(); last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("server not ready: %w (last: %w)", ctx.Err(), last)
		case <-time.After(time.Second):
		}
	}
}

// withLogs terminates c (if any) and adds its last log lines to err.
func withLogs(ctx context.Context, c *testcontainers.DockerContainer, err error) error {
	if c == nil {
		return err
	}
	var logs string
	if r, lerr := c.Logs(ctx); lerr == nil {
		b, rerr := io.ReadAll(io.LimitReader(r, 64<<10))
		discard(rerr)
		discard(r.Close())
		logs = string(b)
		if i := strings.LastIndexByte(logs[:max(len(logs)-4096, 0)], '\n'); i >= 0 {
			logs = logs[i+1:]
		}
	}
	discard(c.Terminate(context.WithoutCancel(ctx)))
	return fmt.Errorf("%w\ncontainer log (tail):\n%s", err, logs)
}

// UniqueDir returns a fresh folder name.
func UniqueDir() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "it-" + hex.EncodeToString(b[:])
}

func discard(error) {}
