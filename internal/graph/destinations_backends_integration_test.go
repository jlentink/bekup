//go:build integration

package graph_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
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
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Images of docker-compose.dev.yml (pinned by digest).
const (
	destMinioImage = "cgr.dev/chainguard/minio:latest@sha256:268d68a2a3b6fc65f905a61f84d7eab6345ce6bff14f551e2b028ec31e6a1341"
	destAtmozImage = "atmoz/sftp:alpine@sha256:43dd1c023cf1bae487127d4b54660b0aee6e94c188a6599be36fb89ba115f961"
	minioRootUser  = "bekupgraphadmin"
	minioRootPass  = "MINIO-ROOT-PASSWORD-graph-7e6d"
)

// tlsPKI returns a CA and a server certificate for 127.0.0.1.
func tlsPKI(t *testing.T) (caPEM, certPEM, keyPEM []byte) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "bekup graph test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
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
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:    x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})
}

// startTLSMinio starts minio with TLS and returns its https endpoint, the
// CA and an admin client.
func startTLSMinio(t *testing.T) (string, []byte, *s3.Client) {
	t.Helper()
	ctx := context.Background()
	caPEM, certPEM, keyPEM := tlsPKI(t)
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{Started: true, ContainerRequest: testcontainers.ContainerRequest{
		Image:        destMinioImage,
		ExposedPorts: []string{"9000/tcp"},
		Env:          map[string]string{"MINIO_ROOT_USER": minioRootUser, "MINIO_ROOT_PASSWORD": minioRootPass},
		Cmd:          []string{"server", "/tmp/data", "--address", ":9000", "--certs-dir", "/tmp/certs"},
		Files: []testcontainers.ContainerFile{
			{Reader: bytes.NewReader(certPEM), ContainerFilePath: "/tmp/certs/public.crt", FileMode: 0o644},
			{Reader: bytes.NewReader(keyPEM), ContainerFilePath: "/tmp/certs/private.key", FileMode: 0o644},
		},
		WaitingFor: wait.ForListeningPort("9000/tcp").WithStartupTimeout(90 * time.Second),
	}})
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	port, err := c.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "https://127.0.0.1:" + port.Port()
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	client := s3.New(s3.Options{
		Region: "us-east-1",
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: minioRootUser, SecretAccessKey: minioRootPass}, nil
		}),
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: true,
		HTTPClient:   &http.Client{Timeout: time.Minute, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}},
	})
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := client.ListBuckets(ctx, &s3.ListBucketsInput{}); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("minio not ready: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return endpoint, caPEM, client
}

func TestDestinationsS3Minio(t *testing.T) {
	endpoint, caPEM, client := startTLSMinio(t)
	ctx := t.Context()
	for _, b := range []struct {
		name string
		lock bool
	}{{"plain-bucket", false}, {"locked-bucket", true}} {
		if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(b.name), ObjectLockEnabledForBucket: aws.Bool(b.lock)}); err != nil {
			t.Fatal(err)
		}
	}
	e := newDestEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	adminC := e.mustLogin("admin@example.com")
	maintC := e.mustLogin("maint@example.com")
	e.freshStepUp(admin.ID)

	s3In := func(bucket, secret string, extra map[string]any) map[string]any {
		m := map[string]any{
			"endpoint": endpoint, "region": "us-east-1", "bucket": bucket, "prefix": "bekup/prod", "pathStyle": true,
			"accessKeyId": minioRootUser, "caBundlePem": string(caPEM),
		}
		if secret != "" {
			m["secretAccessKey"] = secret
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	// One location per destination (D-209): the wrong-key destination
	// gets its own prefix in the same bucket.
	wrongPrefix := map[string]any{"prefix": "bekup/wrong-key"}
	var plain, locked, wrong destModel
	e.mustGQL(adminC, createDestMut, map[string]any{"in": map[string]any{"name": "minio", "type": "S3", "s3": s3In("plain-bucket", minioRootPass, nil)}}).
		field(t, "createDestination", &plain)
	if plain.Settings.S3.CaBundlePem == nil || !strings.Contains(*plain.Settings.S3.CaBundlePem, "BEGIN CERTIFICATE") {
		t.Fatalf("CA bundle not shown: %+v", plain.Settings.S3)
	}
	e.mustGQL(adminC, createDestMut, map[string]any{"in": map[string]any{"name": "minio locked", "type": "S3",
		"s3": s3In("locked-bucket", minioRootPass, map[string]any{"objectLockMode": "GOVERNANCE", "objectLockRetentionDays": 1})}}).
		field(t, "createDestination", &locked)
	e.mustGQL(adminC, createDestMut, map[string]any{"in": map[string]any{"name": "minio wrong key", "type": "S3", "s3": s3In("plain-bucket", "not-the-right-secret", wrongPrefix)}}).
		field(t, "createDestination", &wrong)

	var res testResultModel
	e.mustGQL(maintC, testDestMut, map[string]any{"id": plain.ID}).field(t, "testDestination", &res)
	if !res.OK || len(res.Steps) != 4 || res.ProbeRemains || res.ProbeKey == nil || !strings.HasPrefix(*res.ProbeKey, "bekup-probe/") {
		t.Fatalf("plain test: %+v", res)
	}
	// The probe went below the configured prefix and was deleted.
	out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String("plain-bucket")})
	if err != nil || len(out.Contents) != 0 {
		t.Fatalf("probe left behind: %v %d", err, len(out.Contents))
	}
	e.mustGQL(adminC, testDestMut, map[string]any{"id": locked.ID}).field(t, "testDestination", &res)
	if !res.OK {
		t.Fatalf("locked test: %+v", res)
	}
	e.mustGQL(adminC, testDestMut, map[string]any{"id": wrong.ID}).field(t, "testDestination", &res)
	if res.OK || res.Steps[0].Name != "connect" || res.Steps[0].Code != "permission_denied" {
		t.Fatalf("wrong key test: %+v", res)
	}

	// No-delete mode: the probe stays.
	e.mustGQL(adminC, updateDestMut, map[string]any{"id": plain.ID, "in": map[string]any{"noDelete": true}})
	e.mustGQL(adminC, testDestMut, map[string]any{"id": plain.ID}).field(t, "testDestination", &res)
	if !res.OK || !res.ProbeRemains {
		t.Fatalf("no-delete test: %+v", res)
	}
	// A destination with the CA removed fails TLS: the custom CA is used.
	e.mustGQL(adminC, updateDestMut, map[string]any{"id": wrong.ID, "in": map[string]any{"s3": s3In("plain-bucket", minioRootPass, map[string]any{"caBundlePem": nil, "prefix": "bekup/wrong-key"})}})
	e.mustGQL(adminC, testDestMut, map[string]any{"id": wrong.ID}).field(t, "testDestination", &res)
	if res.OK || res.Steps[0].Code != "network" {
		t.Fatalf("test without the CA: %+v", res)
	}

	e.assertNoSecrets(minioRootPass, "not-the-right-secret")
}

type atmozServer struct {
	c       testcontainers.Container
	port    int
	hostKey ssh.PublicKey
}

const atmozUser = "bekup"

func startAtmozServer(t *testing.T) *atmozServer {
	t.Helper()
	ctx := context.Background()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(hostPriv, "")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	// The account has no password login (empty password field) and no
	// keys yet; the generated public key is installed by the test.
	c, err := testcontainers.Run(ctx, destAtmozImage,
		testcontainers.WithCmd(atmozUser+"::1001:1001:upload"),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader: bytes.NewReader(pem.EncodeToMemory(block)), ContainerFilePath: "/etc/ssh/ssh_host_ed25519_key", FileMode: 0o600,
		}),
		testcontainers.WithExposedPorts("22/tcp"),
		testcontainers.WithWaitStrategyAndDeadline(3*time.Minute, wait.ForLog("Server listening on"), wait.ForListeningPort("22/tcp")),
	)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start atmoz/sftp: %v", err)
	}
	mp, err := c.MappedPort(ctx, "22/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return &atmozServer{c: c, port: int(mp.Num()), hostKey: signer.PublicKey()}
}

// authorize appends an authorized_keys line for the account.
func (a *atmozServer) authorize(t *testing.T, line string) {
	t.Helper()
	ctx := t.Context()
	dir := "/home/" + atmozUser + "/.ssh"
	script := fmt.Sprintf("mkdir -p %s && printf '%%s\\n' '%s' >> %s/authorized_keys && chown -R 1001:1001 %s && chmod 700 %s && chmod 600 %s/authorized_keys",
		dir, line, dir, dir, dir, dir)
	code, out, err := a.c.Exec(ctx, []string{"sh", "-c", script})
	if err != nil || code != 0 {
		b, rerr := io.ReadAll(out)
		t.Fatalf("install key: %v %d %s (%v)", err, code, b, rerr)
	}
}

// closedPort returns a loopback port nobody listens on.
func closedPort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port //nolint:errcheck // a TCP listener always has a *net.TCPAddr
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func TestDestinationsSFTPHostKeyAndKeypair(t *testing.T) {
	srv := startAtmozServer(t)
	e := newDestEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	adminC := e.mustLogin("admin@example.com")
	e.freshStepUp(admin.ID)

	var d destModel
	e.mustGQL(adminC, createDestMut, map[string]any{"in": map[string]any{"name": "atmoz", "type": "SFTP",
		"sftp": map[string]any{"host": "127.0.0.1", "port": srv.port, "user": atmozUser, "basePath": "/upload", "dir": "bekup"}}}).
		field(t, "createDestination", &d)
	if d.Ready || d.Settings.Sftp.HostKey != nil || d.Settings.Sftp.AuthMethod != "NONE" {
		t.Fatalf("created: %+v", d)
	}

	// TOFU: fetch the host key; it matches the server's real key.
	var fetched struct {
		Type              string
		FingerprintSha256 string
		AuthorizedKey     string
		ConfirmedAt       *string
		Host              string
		Port              int
	}
	e.mustGQL(adminC, `mutation($id: UUID!) { fetchDestinationHostKey(id: $id) { type fingerprintSha256 authorizedKey confirmedAt host port } }`,
		map[string]any{"id": d.ID}).field(t, "fetchDestinationHostKey", &fetched)
	if fetched.FingerprintSha256 != ssh.FingerprintSHA256(srv.hostKey) || fetched.Type != ssh.KeyAlgoED25519 || fetched.ConfirmedAt != nil ||
		fetched.Host != "127.0.0.1" || fetched.Port != srv.port {
		t.Fatalf("fetched: %+v", fetched)
	}
	// Sec L8: a closed port yields the one normalized "unreachable" error.
	var closed destModel
	e.mustGQL(adminC, createDestMut, map[string]any{"in": map[string]any{"name": "closed", "type": "SFTP",
		"sftp": map[string]any{"host": "127.0.0.1", "port": closedPort(t), "user": atmozUser, "basePath": "/upload"}}}).
		field(t, "createDestination", &closed)
	resp := e.gql(adminC, `mutation($id: UUID!) { fetchDestinationHostKey(id: $id) { type } }`, map[string]any{"id": closed.ID})
	if resp.code() != "CONNECTION_FAILED" || !strings.Contains(fmt.Sprint(resp.Errors), "could not be reached") {
		t.Fatalf("closed port: %+v", resp.Errors)
	}
	// Fetching does not pin anything.
	var view *destModel
	e.mustGQL(adminC, viewDestQuery, map[string]any{"id": d.ID}).field(t, "destination", &view)
	if view.Settings.Sftp.HostKey != nil {
		t.Fatal("fetch pinned the key")
	}

	// A wrong pin first: the test must fail (host-key mismatch).
	other, err := ssh.NewSignerFromKey(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	wrongLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(other.PublicKey())))
	confirmMut := `mutation($id: UUID!, $k: String!, $h: String!, $p: Int!) { confirmDestinationHostKey(id: $id, authorizedKey: $k, expectedHost: $h, expectedPort: $p) { ` + destFields + ` } }`
	e.mustGQL(adminC, confirmMut, map[string]any{"id": d.ID, "k": wrongLine, "h": fetched.Host, "p": fetched.Port})
	// Code M10: a confirmation for another endpoint than the stored one is
	// refused.
	if r := e.gql(adminC, confirmMut, map[string]any{"id": d.ID, "k": wrongLine, "h": fetched.Host, "p": fetched.Port + 1}); r.code() != "CONFLICT" {
		t.Fatalf("confirm for another port: %+v", r.Errors)
	}

	// Generate the keypair: only the public key comes back.
	var kp struct {
		PublicKey         string
		FingerprintSha256 string
		Destination       destModel
	}
	r := e.mustGQL(adminC, `mutation($id: UUID!) { generateDestinationSshKeypair(id: $id) { publicKey fingerprintSha256 destination { `+destFields+` } } }`,
		map[string]any{"id": d.ID})
	r.field(t, "generateDestinationSshKeypair", &kp)
	if !strings.HasPrefix(kp.PublicKey, "ssh-ed25519 ") || !strings.HasPrefix(kp.FingerprintSha256, "SHA256:") ||
		!kp.Destination.Ready || kp.Destination.Settings.Sftp.AuthMethod != "PRIVATE_KEY" ||
		kp.Destination.Settings.Sftp.PublicKey == nil || *kp.Destination.Settings.Sftp.PublicKey != kp.PublicKey {
		t.Fatalf("keypair: %+v", kp)
	}
	srv.authorize(t, kp.PublicKey)

	var res testResultModel
	e.mustGQL(adminC, testDestMut, map[string]any{"id": d.ID}).field(t, "testDestination", &res)
	if res.OK || res.Steps[len(res.Steps)-1].Code != "host_key_mismatch" {
		t.Fatalf("test with a wrong host key pin: %+v", res)
	}
	// Code M12: a security event, audited.
	if n := countAudit(t, e.auditor, audit.ActionDestinationHostKeyMismatch); n != 1 {
		t.Fatalf("host key mismatch audited %d times", n)
	}

	// Confirm the real key; now the generated key authenticates.
	var confirmed destModel
	e.mustGQL(adminC, confirmMut,
		map[string]any{"id": d.ID, "k": fetched.AuthorizedKey + " some comment", "h": fetched.Host, "p": fetched.Port}).field(t, "confirmDestinationHostKey", &confirmed)
	hk := confirmed.Settings.Sftp.HostKey
	if hk == nil || hk.FingerprintSha256 != fetched.FingerprintSha256 || hk.ConfirmedAt == nil || hk.AuthorizedKey != fetched.AuthorizedKey {
		t.Fatalf("confirmed: %+v", hk)
	}
	e.mustGQL(adminC, testDestMut, map[string]any{"id": d.ID}).field(t, "testDestination", &res)
	if !res.OK || len(res.Steps) != 4 {
		t.Fatalf("sftp test: %+v", res)
	}

	// Changing the port removes the pin.
	var moved destModel
	e.mustGQL(adminC, updateDestMut, map[string]any{"id": d.ID, "in": map[string]any{
		"sftp": map[string]any{"host": "127.0.0.1", "port": srv.port + 1, "user": atmozUser, "basePath": "/upload"}}}).
		field(t, "updateDestination", &moved)
	if moved.Settings.Sftp.HostKey != nil || moved.Ready || moved.Settings.Sftp.AuthMethod != "PRIVATE_KEY" {
		t.Fatalf("after a port change: %+v", moved.Settings.Sftp)
	}
	if n := countAudit(t, e.auditor, audit.ActionDestinationHostKeyConfirmed); n != 2 {
		t.Fatalf("host key confirmations audited: %d", n)
	}
	if n := countAudit(t, e.auditor, audit.ActionDestinationSSHKeyGenerated); n != 1 {
		t.Fatalf("keypair generation audited: %d", n)
	}
	for _, b := range e.bodies {
		if strings.Contains(b, "PRIVATE KEY") || strings.Contains(b, "b3BlbnNzaC1rZXktdjE") {
			t.Fatalf("a response contains private key material: %s", b)
		}
	}
}
