//go:build integration

package pinning

// Integration tests of task 2.7a (AC): per engine (PostgreSQL, MySQL,
// MariaDB) a TLS server with certificates from test CAs, the capture, the
// pre-flight (direct, IP literal, through an SSH bastion) and the native
// client in its pinned image with the hardened spec and the real stdin
// preamble carrying TrustAnchor's PEM as its only CA file. The certificate
// is swapped in place (reload) for one signed by another CA, for another
// leaf of the same CA and, with a self-signed leaf pin, for another
// self-signed leaf.

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"golang.org/x/crypto/ssh"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/preamble"
	"github.com/jlentink/bekup/internal/runner/spec"
	"github.com/jlentink/bekup/internal/runner/validate"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/testutil/dockertest"
	"github.com/jlentink/bekup/internal/testutil/imagepull"
	"github.com/jlentink/bekup/internal/tunnel"
)

const (
	// opensshImage is the bastion image of docker-compose.dev.yml.
	opensshImage = "lscr.io/linuxserver/openssh-server:10.3_p1-r1-ls240@sha256:b07414bb329b86a00525c9256711a7855fc3a369e9f62c36f1de8d1923197580"
	itDBName     = "pintest"
	itUser       = "bekup"
	itSSHUser    = "tunnel"
	itHost       = "db.test"
)

const enableForwarding = `#!/bin/bash
set -euo pipefail
f=/config/sshd/sshd_config
sed -i 's/^AllowTcpForwarding no$/AllowTcpForwarding yes/' "$f"
grep -qx 'AllowTcpForwarding yes' "$f"
echo "bastion: AllowTcpForwarding enabled"
`

type itEngine struct {
	kind      engine.Kind
	key       string // catalog key (server and client image)
	port      int
	env       func(pw string) []string
	entry     []string // entrypoint
	reloadCmd string   // shell command run as root after new files are in /certs
	lastOctet byte
}

var itEngines = []itEngine{
	{
		kind: engine.KindPostgres, key: "17", port: 5432, lastOctet: 10,
		env: func(pw string) []string {
			return []string{"POSTGRES_USER=" + itUser, "POSTGRES_PASSWORD=" + pw, "POSTGRES_DB=" + itDBName}
		},
		entry: []string{"sh", "-c", "install -o postgres -g postgres -m 0600 /certs/server.key /tmp/server.key && " +
			"install -o postgres -g postgres -m 0644 /certs/server.crt /tmp/server.crt && " +
			"exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tmp/server.crt -c ssl_key_file=/tmp/server.key"},
		reloadCmd: "install -o postgres -g postgres -m 0600 /certs/server.key /tmp/server.key && " +
			"install -o postgres -g postgres -m 0644 /certs/server.crt /tmp/server.crt && kill -HUP 1",
	},
	{
		kind: engine.KindMySQL, key: "8.4", port: 3306, lastOctet: 11,
		env: func(pw string) []string {
			return []string{"MYSQL_ROOT_PASSWORD=" + pw, "MYSQL_DATABASE=" + itDBName, "MYSQL_USER=" + itUser, "MYSQL_PASSWORD=" + pw}
		},
		entry: []string{"docker-entrypoint.sh", "mysqld", "--ssl-cert=/certs/server.crt", "--ssl-key=/certs/server.key",
			"--ssl-ca=/certs/ca.pem", "--require-secure-transport=ON"},
		reloadCmd: `mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -e 'ALTER INSTANCE RELOAD TLS'`,
	},
	{
		kind: engine.KindMariaDB, key: "11.8", port: 3306, lastOctet: 12,
		env: func(pw string) []string {
			return []string{"MARIADB_ROOT_PASSWORD=" + pw, "MARIADB_DATABASE=" + itDBName, "MARIADB_USER=" + itUser, "MARIADB_PASSWORD=" + pw}
		},
		entry: []string{"docker-entrypoint.sh", "mariadbd", "--ssl-cert=/certs/server.crt", "--ssl-key=/certs/server.key",
			"--require-secure-transport=ON"},
		reloadCmd: `mariadb -uroot -p"$MARIADB_ROOT_PASSWORD" -e 'FLUSH SSL'`,
	},
}

type env struct {
	c       *client.Client
	netName string
	netID   string
	prefix  netip.Prefix
	cat     *engine.Catalog
	// bastion
	sshPort   int
	sshKeyPEM []byte
	hostKey   string
}

func itDocker(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	t.Cleanup(func() { discardErr(c.Close()) })
	if _, err := c.Ping(t.Context(), client.PingOptions{}); err != nil {
		dockertest.Skipf(t, "docker not available: %v", err)
	}
	return c
}

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func tarOf(t *testing.T, files map[string][]byte, mode int64) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), ModTime: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func ensureImage(t *testing.T, c *client.Client, ref string) {
	t.Helper()
	imagepull.Ensure(t, c, ref)
}

func createContainer(t *testing.T, c *client.Client, opts client.ContainerCreateOptions) string {
	t.Helper()
	res, err := c.ContainerCreate(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := c.ContainerRemove(context.Background(), res.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
		discardErr(err)
	})
	return res.ID
}

func execIn(t *testing.T, c *client.Client, id string, cmd ...string) string {
	t.Helper()
	ex, err := c.ExecCreate(t.Context(), id, client.ExecCreateOptions{User: "root", Cmd: cmd, AttachStdout: true, AttachStderr: true})
	if err != nil {
		t.Fatal(err)
	}
	att, err := c.ExecAttach(t.Context(), ex.ID, client.ExecAttachOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer att.Close()
	var out bytes.Buffer
	_, err = stdcopy.StdCopy(&out, &out, att.Reader)
	discardErr(err)
	ins, err := c.ExecInspect(t.Context(), ex.ID, client.ExecInspectOptions{})
	if err != nil || ins.ExitCode != 0 {
		t.Fatalf("exec %v: %v exit %d: %s", cmd, err, ins.ExitCode, out.String())
	}
	return out.String()
}

func logsOf(c *client.Client, id string) string {
	rc, err := c.ContainerLogs(context.Background(), id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return ""
	}
	defer func() { discardErr(rc.Close()) }()
	var out bytes.Buffer
	_, err = stdcopy.StdCopy(&out, &out, rc)
	discardErr(err)
	return out.String()
}

func mappedPort(t *testing.T, c *client.Client, id string, port int) int {
	t.Helper()
	ins, err := c.ContainerInspect(t.Context(), id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for p, bs := range ins.Container.NetworkSettings.Ports {
		if int(p.Num()) == port && len(bs) > 0 {
			n, err := strconv.Atoi(bs[0].HostPort)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
	}
	t.Fatalf("port %d not published", port)
	return 0
}

func publish(port int) (network.PortSet, network.PortMap) {
	p := network.MustParsePort(strconv.Itoa(port) + "/tcp")
	return network.PortSet{p: struct{}{}}, network.PortMap{p: []network.PortBinding{{HostIP: netip.MustParseAddr("127.0.0.1")}}}
}

// newEnv creates the bridge network (with a fixed subnet so the database
// containers get known IP literals) and the SSH bastion.
func newEnv(t *testing.T) *env {
	t.Helper()
	c := itDocker(t)
	sv, err := c.ServerVersion(t.Context(), client.ServerVersionOptions{})
	if err != nil || sv.Os != "linux" {
		dockertest.Skipf(t, "needs a linux Docker daemon (%v)", err)
	}
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	e := &env{c: c, cat: cat, netName: "bekup-it-pin-" + randHex(t, 4)}
	for attempt := 0; ; attempt++ {
		var b [1]byte
		if _, err := rand.Read(b[:]); err != nil {
			t.Fatal(err)
		}
		pfx := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 231, b[0], 0}), 24)
		res, err := c.NetworkCreate(t.Context(), e.netName, client.NetworkCreateOptions{
			Driver: "bridge", Labels: map[string]string{"bekup.integration": "pinning"},
			IPAM: &network.IPAM{Config: []network.IPAMConfig{{Subnet: pfx}}},
		})
		if err != nil {
			if attempt < 8 {
				continue
			}
			t.Fatalf("network: %v", err)
		}
		e.netID, e.prefix = res.ID, pfx
		break
	}
	t.Cleanup(func() {
		_, err := c.NetworkRemove(context.Background(), e.netID, client.NetworkRemoveOptions{})
		discardErr(err)
	})

	ensureImage(t, c, opensshImage)
	_, clientKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	e.sshKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	exposed, bindings := publish(2222)
	bastion := createContainer(t, c, client.ContainerCreateOptions{
		Name: "bekup-it-pin-bastion-" + randHex(t, 4),
		Config: &container.Config{Image: opensshImage, ExposedPorts: exposed, Env: []string{
			"PUID=1000", "PGID=1000", "TZ=Etc/UTC", "USER_NAME=" + itSSHUser,
			"PUBLIC_KEY=" + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))),
			"PASSWORD_ACCESS=false", "SUDO_ACCESS=false", "LOG_STDOUT=true",
		}},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(e.netID), PortBindings: bindings},
	})
	if _, err := c.CopyToContainer(t.Context(), bastion, client.CopyToContainerOptions{DestinationPath: "/",
		Content: tarOf(t, map[string][]byte{"custom-cont-init.d/10-allow-tcp-forwarding": []byte(enableForwarding)}, 0o555)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ContainerStart(t.Context(), bastion, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Minute)
	for !strings.Contains(logsOf(c, bastion), "bastion: AllowTcpForwarding enabled") {
		if time.Now().After(deadline) {
			t.Fatalf("bastion not ready:\n%s", logsOf(c, bastion))
		}
		time.Sleep(500 * time.Millisecond)
	}
	e.hostKey = strings.TrimSpace(execIn(t, c, bastion, "cat", "/config/ssh_host_keys/ssh_host_ed25519_key.pub"))
	if f := strings.Fields(e.hostKey); len(f) >= 2 {
		e.hostKey = f[0] + " " + f[1]
	}
	e.sshPort = mappedPort(t, c, bastion, 2222)
	return e
}

// certSet holds the server certificates of one engine.
type certSet struct {
	caA, caB           *testCA
	l1, l2, l3, s1, s2 *testCA
	ip                 netip.Addr
}

func newCertSet(t *testing.T, ip netip.Addr) *certSet {
	t.Helper()
	o := leafOpts{dns: []string{itHost}, ips: []net.IP{net.IP(ip.AsSlice())}}
	cs := &certSet{ip: ip, caA: newCA(t, "bekup integration CA A", nil), caB: newCA(t, "ISRG Root X1", nil)}
	cs.l1 = newLeaf(t, cs.caA, o)
	cs.l2 = newLeaf(t, cs.caA, o)
	cs.l3 = newLeaf(t, cs.caB, o)
	cs.s1 = newLeaf(t, nil, o)
	cs.s2 = newLeaf(t, nil, o)
	return cs
}

// files returns server.crt (leaf + chain) and server.key.
func files(t *testing.T, leaf *testCA, chain ...*x509.Certificate) map[string][]byte {
	t.Helper()
	crt := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.cert.Raw})
	for _, c := range chain {
		crt = append(crt, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	k, err := x509.MarshalPKCS8PrivateKey(leaf.key)
	if err != nil {
		t.Fatal(err)
	}
	// ca.pem (the issuer, or the leaf itself when self-signed) is only
	// for MySQL's own reload check (ALTER INSTANCE RELOAD TLS validates the
	// server certificate against ssl_ca); clients never see it.
	ca := leaf.cert
	if len(chain) > 0 {
		ca = chain[len(chain)-1]
	}
	return map[string][]byte{
		"certs/server.crt": crt, "certs/server.key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: k}),
		"certs/ca.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}),
	}
}

type dbServer struct {
	e      *env
	eng    itEngine
	id     string
	port   int // published on 127.0.0.1
	pw     string
	ip     netip.Addr
	entry  engine.Entry
	checks *Checker
}

func (s *dbServer) direct(host string) Target {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(s.port))
	return Target{Kind: s.eng.kind, Host: host, Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}}
}

// tunneled returns a Target through the SSH bastion (Connect), with host
// resolved by the bastion.
func (s *dbServer) tunneled(t *testing.T, host string) Target {
	t.Helper()
	dial, done, err := Connect(t.Context(), Upstream{
		Host: host, Port: s.eng.port, Guard: &net.Dialer{},
		SSH: &validate.SSH{
			Bastion: validate.Endpoint{Host: "127.0.0.1", Port: s.e.sshPort},
			User:    itSSHUser, Pin: tunnel.HostKeyPin{AuthorizedKey: s.e.hostKey}, KeyAuth: true,
		},
		Tunnel: &runnerpb.SshTunnel{PrivateKeyPem: bytes.Clone(s.e.sshKeyPEM)},
	})
	if err != nil {
		t.Fatalf("tunnel: %v", err)
	}
	t.Cleanup(done)
	return Target{Kind: s.eng.kind, Host: host, Dial: dial}
}

func startDB(t *testing.T, e *env, eng itEngine, cs *certSet) *dbServer {
	t.Helper()
	entry, ok := e.cat.Lookup(eng.kind, eng.key)
	if !ok {
		t.Fatalf("no catalog entry %s %s", eng.kind, eng.key)
	}
	ref := spec.ImageRef(entry)
	ensureImage(t, e.c, ref)
	s := &dbServer{e: e, eng: eng, pw: randHex(t, 12), ip: cs.ip, entry: entry, checks: New(Options{})}
	exposed, bindings := publish(eng.port)
	s.id = createContainer(t, e.c, client.ContainerCreateOptions{
		Name:       "bekup-it-pin-" + string(eng.kind) + "-" + randHex(t, 4),
		Config:     &container.Config{Image: ref, Env: eng.env(s.pw), Entrypoint: eng.entry[:1], Cmd: eng.entry[1:], ExposedPorts: exposed},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(e.netID), PortBindings: bindings},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			e.netID: {Aliases: []string{itHost}, IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: cs.ip}},
		}},
	})
	if _, err := e.c.CopyToContainer(t.Context(), s.id, client.CopyToContainerOptions{DestinationPath: "/",
		Content: tarOf(t, files(t, cs.l1, cs.caA.cert), 0o644)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.c.ContainerStart(t.Context(), s.id, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}
	s.port = mappedPort(t, e.c, s.id, eng.port)
	s.waitLeaf(t, cs.l1.cert, 4*time.Minute)
	return s
}

// waitLeaf polls the capture until the server presents leaf.
func (s *dbServer) waitLeaf(t *testing.T, leaf *x509.Certificate, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		chain, err := s.checks.Fetch(ctx, s.direct(itHost))
		cancel()
		if err == nil && chain[0].Equal(leaf) {
			return
		}
		last = err
		time.Sleep(time.Second)
	}
	t.Fatalf("%s did not present the expected leaf (last error %v):\n%s", s.eng.kind, last, logsOf(s.e.c, s.id))
}

// swap installs a new certificate and reloads the server's TLS context.
func (s *dbServer) swap(t *testing.T, leaf *testCA, chain ...*x509.Certificate) {
	t.Helper()
	if _, err := s.e.c.CopyToContainer(t.Context(), s.id, client.CopyToContainerOptions{DestinationPath: "/",
		Content: tarOf(t, files(t, leaf, chain...), 0o644)}); err != nil {
		t.Fatal(err)
	}
	execIn(t, s.e.c, s.id, "sh", "-c", s.eng.reloadCmd)
	s.waitLeaf(t, leaf.cert, time.Minute)
}

type nativeResult struct {
	exit   int64
	stderr string
}

// native runs the engine's real dump tool in its pinned image with the
// hardened spec; the preamble carries the password file and TrustAnchor's
// PEM as the only CA file (sslrootcert / --ssl-ca, verify-full /
// VERIFY_IDENTITY / --ssl-verify-server-cert).
func (s *dbServer) native(t *testing.T, host string, pins []validate.Pin) nativeResult {
	t.Helper()
	eng, err := engine.ForKind(s.eng.kind)
	if err != nil {
		t.Fatal(err)
	}
	tgt := engine.Target{Host: host, Port: s.eng.port, User: itUser, TLS: engine.TLSPinned}
	cmd, err := eng.Dump(s.entry, tgt, itDBName)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := s.checks.TrustAnchor(pins)
	if err != nil {
		t.Fatal(err)
	}
	m := &preamble.Material{CA: ca}
	if s.eng.kind == engine.KindPostgres {
		m.PGPass, err = engine.PGPassLine(host, s.eng.port, itUser, []byte(s.pw))
	} else {
		m.MyCnf, err = engine.MySQLOptionFile([]byte(s.pw))
	}
	if err != nil {
		t.Fatal(err)
	}
	recs, err := preamble.ForCommand(cmd, m)
	if err != nil {
		t.Fatal(err)
	}
	pre, err := preamble.Encode(recs)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	runID := h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
	sp, err := spec.Build(spec.Params{RunID: runID, Entry: s.entry, Command: cmd, Network: s.e.netName,
		Limits: spec.Limits{MemoryMiB: 512, CPUs: 1, PidsLimit: 256}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	created, err := s.e.c.ContainerCreate(ctx, client.ContainerCreateOptions{Config: sp.Config, HostConfig: sp.HostConfig, NetworkingConfig: sp.Networking})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() {
		_, err := s.e.c.ContainerRemove(context.Background(), created.ID, client.ContainerRemoveOptions{Force: true})
		discardErr(err)
	}()
	att, err := s.e.c.ContainerAttach(ctx, created.ID, client.ContainerAttachOptions{Stream: true, Stdin: true, Stdout: true, Stderr: true})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer att.Close()
	if _, err := s.e.c.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	go func() {
		_, err := att.Conn.Write(pre)
		discardErr(err)
		discardErr(att.CloseWrite())
	}()
	var errb bytes.Buffer
	if _, err := stdcopy.StdCopy(io.Discard, &errb, att.Reader); err != nil {
		t.Fatalf("output: %v", err)
	}
	w := s.e.c.ContainerWait(ctx, created.ID, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case r := <-w.Result:
		return nativeResult{exit: r.StatusCode, stderr: errb.String()}
	case err := <-w.Error:
		t.Fatalf("wait: %v", err)
	}
	return nativeResult{}
}

func wantNative(t *testing.T, r nativeResult, ok bool, what string) {
	t.Helper()
	if (r.exit == 0) != ok {
		t.Fatalf("native client (%s): exit %d, want success=%v\n%s", what, r.exit, ok, r.stderr)
	}
	t.Logf("native client (%s): exit %d %s", what, r.exit, strings.TrimSpace(r.stderr))
	if !ok && !regexpTLS(r.stderr) {
		t.Fatalf("native client (%s) failed for another reason:\n%s", what, r.stderr)
	}
}

// regexpTLS reports whether stderr is a certificate verification failure
// (libpq: "certificate verify failed", MySQL: "certificate verify failed",
// MariaDB: "self-signed certificate ...").
func regexpTLS(s string) bool {
	return strings.Contains(strings.ToLower(s), "certificate")
}

func wantMismatch(t *testing.T, err error, reason, what string) {
	t.Helper()
	var me *MismatchError
	if !errors.As(err, &me) || me.Reason != reason {
		t.Fatalf("pre-flight (%s): err = %v, want mismatch %s", what, err, reason)
	}
}

func TestIntegrationPinning(t *testing.T) {
	e := newEnv(t)
	for _, eng := range itEngines {
		t.Run(string(eng.kind), func(t *testing.T) {
			ip := netip.AddrFrom4([4]byte{e.prefix.Addr().As4()[0], e.prefix.Addr().As4()[1], e.prefix.Addr().As4()[2], eng.lastOctet})
			cs := newCertSet(t, ip)
			s := startDB(t, e, eng, cs)
			ctx := t.Context()
			ipHost := ip.String()
			pinA := pinOf(cs.caA.cert, cs.l1.cert)
			pinB := pinOf(cs.caB.cert, cs.l3.cert)

			// Capture: the chain, the leaf SPKI and the CA suggestion.
			chain, err := s.checks.Fetch(ctx, s.direct(itHost))
			if err != nil {
				t.Fatal(err)
			}
			d := Describe(chain)
			if !bytes.Equal(d.GetLeafSpkiSha256(), spkiOf(cs.l1.cert)) || !bytes.Equal(d.GetChain()[0].GetDer(), cs.l1.cert.Raw) {
				t.Fatal("captured leaf / SPKI mismatch")
			}
			if len(chain) > 1 {
				if d.GetSuggestedAnchorIndex() != 1 || !chain[1].Equal(cs.caA.cert) {
					t.Fatalf("suggestion = %d", d.GetSuggestedAnchorIndex())
				}
			} else if d.GetSuggestedAnchorIndex() != -1 {
				t.Fatalf("suggestion = %d without a presented CA", d.GetSuggestedAnchorIndex())
			}
			if tc, err := s.checks.Fetch(ctx, s.tunneled(t, itHost)); err != nil || !tc[0].Equal(cs.l1.cert) {
				t.Fatalf("fetch through the tunnel: %v", err)
			}

			// Pinned CA + leaf SPKI: pre-flight and native client pass,
			// directly, by IP literal (IP SAN) and through the bastion.
			for name, tgt := range map[string]Target{
				"direct": s.direct(itHost), "ip literal": s.direct(ipHost),
				"tunnel": s.tunneled(t, itHost), "tunnel ip literal": s.tunneled(t, ipHost),
			} {
				rep, err := s.checks.Preflight(ctx, tgt, []validate.Pin{pinA})
				if err != nil || rep.PinIndex != 0 {
					t.Fatalf("pre-flight %s: %v", name, err)
				}
			}
			wantNative(t, s.native(t, itHost, []validate.Pin{pinA}), true, "pinned CA")
			wantNative(t, s.native(t, ipHost, []validate.Pin{pinA}), true, "pinned CA, IP literal")
			// A wrong IP literal (not in the certificate) fails both.
			other := netip.AddrFrom4([4]byte{ip.As4()[0], ip.As4()[1], ip.As4()[2], 99})
			_, err = s.checks.Preflight(ctx, s.direct(other.String()), []validate.Pin{pinA})
			wantMismatch(t, err, ReasonHostname, "wrong IP")

			// The staged backup pin: with [B, A] the server's A chain
			// matches the second pin.
			rep, err := s.checks.Preflight(ctx, s.direct(itHost), []validate.Pin{pinB, pinA})
			if err != nil || rep.PinIndex != 1 || !contains(rep.Warnings, WarningStagedPinInUse) {
				t.Fatalf("backup pin: %+v %v", rep, err)
			}
			wantNative(t, s.native(t, itHost, []validate.Pin{pinB, pinA}), true, "backup pin")

			// Swap: a certificate from another CA fails both.
			s.swap(t, cs.l3, cs.caB.cert)
			_, err = s.checks.Preflight(ctx, s.direct(itHost), []validate.Pin{pinA})
			wantMismatch(t, err, ReasonUntrusted, "other CA")
			_, err = s.checks.Preflight(ctx, s.tunneled(t, itHost), []validate.Pin{pinA})
			wantMismatch(t, err, ReasonUntrusted, "other CA through the tunnel")
			wantNative(t, s.native(t, itHost, []validate.Pin{pinA}), false, "other CA")
			// ... and the staged pin of the new CA is accepted.
			if rep, err := s.checks.Preflight(ctx, s.direct(itHost), []validate.Pin{pinA, pinB}); err != nil || rep.PinIndex != 1 {
				t.Fatalf("staged pin after rotation: %v", err)
			}

			// Swap: another leaf of the same CA. The chain verifies (so
			// a CA-anchored native client would connect), but the
			// pre-flight's leaf SPKI pin refuses it, before any container
			// is started.
			s.swap(t, cs.l2, cs.caA.cert)
			_, err = s.checks.Preflight(ctx, s.direct(itHost), []validate.Pin{pinA})
			wantMismatch(t, err, ReasonLeafSPKI, "other leaf, same CA")
			wantNative(t, s.native(t, itHost, []validate.Pin{pinA}), true, "other leaf, same CA (chain still valid: the pre-flight is the gate)")

			// Self-signed leaf pin: another self-signed leaf fails both
			// the pre-flight and the native client.
			pinS1 := pinOf(cs.s1.cert, cs.s1.cert)
			s.swap(t, cs.s1)
			if _, err := s.checks.Preflight(ctx, s.direct(itHost), []validate.Pin{pinS1}); err != nil {
				t.Fatalf("self-signed pin: %v", err)
			}
			wantNative(t, s.native(t, itHost, []validate.Pin{pinS1}), true, "self-signed leaf")
			s.swap(t, cs.s2)
			_, err = s.checks.Preflight(ctx, s.direct(itHost), []validate.Pin{pinS1})
			wantMismatch(t, err, ReasonUntrusted, "other self-signed leaf")
			wantNative(t, s.native(t, itHost, []validate.Pin{pinS1}), false, "other self-signed leaf")
			t.Logf("%s: all pinning checks passed", eng.kind)
		})
	}
}
