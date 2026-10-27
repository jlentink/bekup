//go:build integration

package tunnel

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/goleak"
	"golang.org/x/crypto/ssh"
)

// opensshImage is the bastion image of docker-compose.dev.yml.
const opensshImage = "lscr.io/linuxserver/openssh-server:10.3_p1-r1-ls240@sha256:b07414bb329b86a00525c9256711a7855fc3a369e9f62c36f1de8d1923197580"

// enableForwarding is the image's custom-init hook (as in
// docker-compose.dev.yml): the image ships AllowTcpForwarding no.
const enableForwarding = `#!/bin/bash
set -euo pipefail
f=/config/sshd/sshd_config
sed -i 's/^AllowTcpForwarding no$/AllowTcpForwarding yes/' "$f"
grep -qx 'AllowTcpForwarding yes' "$f"
echo "bastion: AllowTcpForwarding enabled"
`

const integrationPassword = "integration-password-7c1e"

type bastion struct {
	host      string
	port      int
	clientKey ed25519.PrivateKey
	hostKey   ssh.PublicKey // read out of band from the container
}

func startBastion(t *testing.T) *bastion {
	t.Helper()
	ctx := context.Background()
	_, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(clientPriv)
	if err != nil {
		t.Fatal(err)
	}
	c, err := testcontainers.Run(ctx, opensshImage,
		testcontainers.WithEnv(map[string]string{
			"PUID": "1000", "PGID": "1000", "TZ": "Etc/UTC",
			"USER_NAME":       testUser,
			"PUBLIC_KEY":      authorizedLine(signer.PublicKey()),
			"PASSWORD_ACCESS": "true",
			"USER_PASSWORD":   integrationPassword,
			"SUDO_ACCESS":     "false",
			"LOG_STDOUT":      "true",
		}),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            strings.NewReader(enableForwarding),
			ContainerFilePath: "/custom-cont-init.d/10-allow-tcp-forwarding",
			FileMode:          0o555,
		}),
		testcontainers.WithExposedPorts("2222/tcp"),
		testcontainers.WithWaitStrategyAndDeadline(3*time.Minute,
			wait.ForLog("bastion: AllowTcpForwarding enabled"),
			wait.ForListeningPort("2222/tcp"),
		),
	)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatal(err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mp, err := c.MappedPort(ctx, "2222/tcp")
	if err != nil {
		t.Fatal(err)
	}
	rc, err := c.CopyFileFromContainer(ctx, "/config/ssh_host_keys/ssh_host_ed25519_key.pub")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { discardErr(rc.Close()) }()
	pubLine, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey(pubLine)
	if err != nil {
		t.Fatal(err)
	}
	b := &bastion{host: host, port: int(mp.Num()), clientKey: clientPriv, hostKey: hostKey}
	b.waitReady(t)
	return b
}

// waitReady retries until sshd accepts the client (the port may listen
// before the init scripts finished).
func (b *bastion) waitReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		tun, err := Dial(context.Background(), b.config(t))
		if err == nil {
			discardErr(tun.Close())
			return
		}
		if time.Now().After(deadline) || errors.Is(err, ErrHostKeyMismatch) {
			t.Fatalf("bastion not ready: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// config forwards to the bastion's own sshd (127.0.0.1:2222 inside the
// container), so the target answers with an SSH banner.
func (b *bastion) config(t *testing.T) *Config {
	t.Helper()
	return &Config{
		Host: b.host, Port: b.port, User: testUser,
		Auth:       Auth{PrivateKey: pemKey(t, b.clientKey, "")},
		HostKeyPin: HostKeyPin{AuthorizedKey: authorizedLine(b.hostKey)},
		TargetHost: "127.0.0.1", TargetPort: 2222,
		Dialer: &countingDialer{},
	}
}

func readBanner(t *testing.T, c net.Conn) {
	t.Helper()
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "SSH-2.0-OpenSSH") {
		t.Fatalf("banner = %q", line)
	}
}

func TestIntegrationOpenSSH(t *testing.T) {
	b := startBastion(t)

	t.Run("fetch host key without auth", func(t *testing.T) {
		info, err := FetchHostKey(context.Background(), b.host, b.port, &countingDialer{})
		if err != nil {
			t.Fatal(err)
		}
		if info.Type != ssh.KeyAlgoED25519 || info.FingerprintSHA256 != ssh.FingerprintSHA256(b.hostKey) {
			t.Fatalf("fetched %+v, want %s", info, ssh.FingerprintSHA256(b.hostKey))
		}
	})

	t.Run("key auth and forwarding", func(t *testing.T) {
		tun, err := Dial(context.Background(), b.config(t))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { discardErr(tun.Close()) }()
		c, err := tun.DialTarget(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { discardErr(c.Close()) }()
		readBanner(t, c)
	})

	t.Run("password auth", func(t *testing.T) {
		cfg := b.config(t)
		cfg.Auth = Auth{Password: secretOf(integrationPassword)}
		tun, err := Dial(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		discardErr(tun.Close())
	})

	t.Run("wrong password", func(t *testing.T) {
		cfg := b.config(t)
		cfg.Auth = Auth{Password: secretOf("not the password")}
		if _, err := Dial(context.Background(), cfg); !errors.Is(err, ErrAuthFailed) {
			t.Fatalf("err = %v, want ErrAuthFailed", err)
		}
	})

	t.Run("wrong host key rejected", func(t *testing.T) {
		other, _ := ed25519Signer(t)
		cfg := b.config(t)
		cfg.HostKeyPin = HostKeyPin{Type: ssh.KeyAlgoED25519, FingerprintSHA256: ssh.FingerprintSHA256(other.PublicKey())}
		_, err := Dial(context.Background(), cfg)
		var m *HostKeyMismatchError
		if !errors.As(err, &m) || m.PresentedFingerprint != ssh.FingerprintSHA256(b.hostKey) {
			t.Fatalf("err = %v, want HostKeyMismatchError", err)
		}
	})

	t.Run("pinned type not offered", func(t *testing.T) {
		cfg := b.config(t)
		cfg.HostKeyPin = HostKeyPin{AuthorizedKey: authorizedLine(ecdsaSigner(t).PublicKey())}
		cfg.Auth = Auth{Password: secretOf(integrationPassword)}
		_, err := Dial(context.Background(), cfg)
		if !errors.Is(err, ErrHostKeyMismatch) {
			// OpenSSH also offers ECDSA by default; then the key differs.
			t.Fatalf("err = %v, want ErrHostKeyMismatch", err)
		}
	})

	t.Run("forward and cancel", func(t *testing.T) {
		defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
		tun, err := Dial(context.Background(), b.config(t))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { discardErr(tun.Close()) }()
		ln, err := listenTCP(t)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- tun.Forward(ctx, ln) }()
		c, err := dialTCP(t, ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { discardErr(c.Close()) }()
		discardErr(c.SetDeadline(time.Now().Add(10 * time.Second)))
		readBanner(t, c)
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Forward = %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Forward did not stop")
		}
	})
}
