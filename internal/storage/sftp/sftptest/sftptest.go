// Package sftptest starts the atmoz/sftp server of docker-compose.dev.yml
// for integration tests (internal/storage/sftp, restore staging, ...).
//
// The harness is hardened against the ways the container start failed on
// Docker Desktop:
//
//   - The image is amd64 only. It is requested as linux/amd64 explicitly,
//     so arm64 hosts run it under emulation deterministically instead of
//     depending on the daemon's platform fallback, and the startup
//     deadline allows for the slower emulated sshd start.
//   - 22/tcp is published on 127.0.0.1 only (no dual-stack wildcard
//     binding, which Docker Desktop's port forwarder intermittently failed
//     to set up, leaving "22/tcp" unmapped), and the tests dial 127.0.0.1.
//   - Readiness is "sshd logged Server listening on" plus a real SSH
//     banner on the published port (wait.ForListeningPort alone accepts
//     the forwarder's socket before sshd answers).
//   - A start that still fails (port never mapped, container gone, reaper
//     hiccup) is retried with a fresh container a bounded number of times;
//     the error carries the attempts and the container log.
package sftptest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh"

	"github.com/jlentink/bekup/internal/crypto"
)

// Image is the SFTP image of docker-compose.dev.yml (pinned by digest).
const Image = "atmoz/sftp:alpine@sha256:43dd1c023cf1bae487127d4b54660b0aee6e94c188a6599be36fb89ba115f961"

// Account facts. The chrooted account sees BasePath; on the container file
// system that is FSUpload.
const (
	User     = "bekup"
	Password = "integration-password-51d0"
	BasePath = "/upload"
	FSUpload = "/home/bekup/upload"
)

const (
	attempts        = 3
	startupDeadline = 3 * time.Minute
)

// Server is a running atmoz/sftp container.
type Server struct {
	Container testcontainers.Container
	// Host and Port are the published SSH endpoint (127.0.0.1).
	Host string
	Port int
	// HostKey is the server's ed25519 host key (generated per start).
	HostKey ssh.PublicKey
	// ClientKeyPEM is the OpenSSH private key authorized for User.
	ClientKeyPEM []byte
}

// HostKeyAuthorized is HostKey in authorized_keys form (for host key pins).
func (s *Server) HostKeyAuthorized() string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.HostKey)))
}

// Terminate removes the container.
func (s *Server) Terminate(ctx context.Context) error {
	return s.Container.Terminate(ctx)
}

// Start starts a server, retrying a failed start with a fresh container.
func Start(ctx context.Context) (*Server, error) {
	var errs []error
	for i := 1; i <= attempts; i++ {
		s, err := startOnce(ctx)
		if err == nil {
			return s, nil
		}
		errs = append(errs, fmt.Errorf("attempt %d: %w", i, err))
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.Join(errs...)
}

func startOnce(ctx context.Context) (*Server, error) {
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

	port := network.MustParsePort("22/tcp")
	c, err := testcontainers.Run(ctx, Image,
		testcontainers.WithImagePlatform("linux/amd64"),
		testcontainers.WithCmd(User+":"+Password+":1001:1001:upload"),
		testcontainers.WithFiles(
			testcontainers.ContainerFile{
				Reader:            bytes.NewReader(hostPEM),
				ContainerFilePath: "/etc/ssh/ssh_host_ed25519_key",
				FileMode:          0o600,
			},
			testcontainers.ContainerFile{
				Reader:            strings.NewReader(kp.AuthorizedKey + "\n"),
				ContainerFilePath: "/home/" + User + "/.ssh/keys/bekup.pub",
				FileMode:          0o644,
			},
		),
		testcontainers.WithExposedPorts("22/tcp"),
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.PortBindings = network.PortMap{port: {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "0"}}}
		}),
		testcontainers.WithWaitStrategyAndDeadline(startupDeadline,
			wait.ForLog("Server listening on"),
			wait.ForMappedPort("22/tcp"),
		),
	)
	if err != nil {
		return nil, withLogs(ctx, c, fmt.Errorf("run: %w", err))
	}
	mp, err := c.MappedPort(ctx, "22/tcp")
	if err != nil {
		return nil, withLogs(ctx, c, fmt.Errorf("mapped port: %w", err))
	}
	s := &Server{Container: c, Host: "127.0.0.1", Port: int(mp.Num()), HostKey: hostSigner.PublicKey(), ClientKeyPEM: clientPEM}
	if err := waitBanner(ctx, s.Host, s.Port, startupDeadline); err != nil {
		return nil, withLogs(ctx, c, err)
	}
	return s, nil
}

// waitBanner waits until the published port answers with an SSH banner.
func waitBanner(ctx context.Context, host string, port int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	var last error
	for {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			err = readBanner(conn)
			if err == nil {
				return nil
			}
		}
		last = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("ssh banner on %s: %w (last: %w)", addr, ctx.Err(), last)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// readBanner reads the server's identification line and closes conn.
func readBanner(conn net.Conn) error {
	derr := conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, rerr := bufio.NewReader(conn).ReadString('\n')
	cerr := conn.Close()
	if rerr == nil && derr == nil && strings.HasPrefix(line, "SSH-2.0-") {
		return nil
	}
	return fmt.Errorf("no SSH banner: %w", errors.Join(rerr, derr, cerr))
}

// withLogs terminates c (if any) and adds its last log lines to err.
func withLogs(ctx context.Context, c *testcontainers.DockerContainer, err error) error {
	if c == nil {
		return err
	}
	var tail string
	if rc, lerr := c.Logs(ctx); lerr == nil {
		b, rerr := io.ReadAll(io.LimitReader(rc, 16<<10))
		tail = string(b)
		if rerr != nil {
			tail += fmt.Sprintf("\n(reading the log failed: %v)", rerr)
		}
		if cerr := rc.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close log: %w", cerr))
		}
	}
	if terr := c.Terminate(context.WithoutCancel(ctx)); terr != nil {
		err = errors.Join(err, fmt.Errorf("terminate: %w", terr))
	}
	return fmt.Errorf("%w\ncontainer log:\n%s", err, tail)
}
