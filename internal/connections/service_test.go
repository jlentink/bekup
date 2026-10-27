package connections

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// --- certificates -------------------------------------------------------------

type testCert struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  string
	spki []byte
}

func newCert(t *testing.T, cn string, isCA bool, parent *testCert, notAfter time.Time) *testCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter, DNSNames: []string{cn},
		IsCA: isCA, BasicConstraintsValid: true,
	}
	if isCA {
		tpl.KeyUsage = x509.KeyUsageCertSign
		tpl.DNSNames = nil
	}
	signer, signerKey := tpl, key
	if parent != nil {
		signer, signerKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return &testCert{cert: c, key: key, pem: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), spki: sum[:]}
}

func testService(t *testing.T) *Service {
	t.Helper()
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	return &Service{o: Options{Catalog: cat, Now: time.Now, Logger: slog.New(slog.DiscardHandler)}}
}

// --- validation ---------------------------------------------------------------

func baseState() *state {
	return &state{
		name: "Prod", engine: engine.KindPostgres, versionKey: "16", imageKey: "17", host: "db.example.com",
		port: 5432, username: "backup", tlsMode: TLSPinned, sshAuth: SSHAuthNone, creds: &Credentials{},
	}
}

func TestValidate(t *testing.T) {
	s := testService(t)
	if err := s.validate(baseState()); err != nil {
		t.Fatalf("valid state: %v", err)
	}
	for name, tc := range map[string]struct {
		mut   func(*state)
		field string
	}{
		"unknown engine":       {func(st *state) { st.engine = "oracle" }, "engine"},
		"unknown version":      {func(st *state) { st.versionKey = "9" }, "versionKey"},
		"unknown image":        {func(st *state) { st.imageKey = "99" }, "imageKey"},
		"image of other kind":  {func(st *state) { st.imageKey = "8.4" }, "imageKey"},
		"pg client too old":    {func(st *state) { st.versionKey, st.imageKey = "17", "16" }, "imageKey"},
		"bad host":             {func(st *state) { st.host = "bad host" }, "host"},
		"bad port":             {func(st *state) { st.port = 70000 }, "port"},
		"bad user":             {func(st *state) { st.username = "-x y" }, "username"},
		"client cert, no TLS":  {func(st *state) { st.tlsMode, st.clientCertPEM = TLSDisabled, "x" }, "clientCertificatePem"},
		"ssh bad host":         {func(st *state) { st.ssh, st.sshHost, st.sshPort, st.sshUser = true, "", 22, "u" }, "ssh.host"},
		"ssh bad user":         {func(st *state) { st.ssh, st.sshHost, st.sshPort, st.sshUser = true, "b.example.com", 22, "-u" }, "ssh.user"},
		"D-132 ip literal":     {func(st *state) { st.host = "10.0.0.5" }, "host"},
		"D-132 ipv6 literal":   {func(st *state) { st.host = "2001:db8::5" }, "host"},
		"D-132 localhost":      {func(st *state) { st.host = "localhost" }, "host"},
		"D-132 sub.localhost":  {func(st *state) { st.host = "db.localhost" }, "host"},
		"unknown tls mode":     {func(st *state) { st.tlsMode = "weird" }, "tlsMode"},
		"mysql version on pg ": {func(st *state) { st.versionKey = "11.8" }, "versionKey"},
	} {
		t.Run(name, func(t *testing.T) {
			st := baseState()
			tc.mut(st)
			err := s.validate(st)
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Field != tc.field {
				t.Fatalf("got %v, want a field error on %s", err, tc.field)
			}
		})
	}
	// D-132: the IP-literal rule explains itself and does not apply with a
	// tunnel or without pinned TLS.
	st := baseState()
	st.host = "10.0.0.5"
	if err := s.validate(st); err == nil || !strings.Contains(err.Error(), "D-132") || !strings.Contains(err.Error(), "SSH tunnel") {
		t.Fatalf("D-132 message: %v", err)
	}
	st.ssh, st.sshHost, st.sshPort, st.sshUser = true, "bastion.example.com", 22, "tunnel"
	if err := s.validate(st); err != nil {
		t.Fatalf("ip literal behind a tunnel: %v", err)
	}
	st = baseState()
	st.host, st.tlsMode = "10.0.0.5", TLSUnverified
	if err := s.validate(st); err != nil {
		t.Fatalf("ip literal unverified: %v", err)
	}
	// MySQL: an older client only warns in the runner.
	st = baseState()
	st.engine, st.versionKey, st.imageKey, st.port = engine.KindMySQL, "9.7", "8.4", 3306
	if err := s.validate(st); err != nil {
		t.Fatalf("mysql older client: %v", err)
	}
}

func TestIsDowngrade(t *testing.T) {
	for _, tc := range []struct {
		from, to TLSMode
		want     bool
	}{
		{TLSPinned, TLSUnverified, true}, {TLSPinned, TLSDisabled, true}, {TLSUnverified, TLSDisabled, true},
		{TLSDisabled, TLSPinned, false}, {TLSUnverified, TLSPinned, false}, {TLSPinned, TLSPinned, false},
		{TLSDisabled, TLSUnverified, false},
	} {
		if got := IsDowngrade(tc.from, tc.to); got != tc.want {
			t.Errorf("%s -> %s: %v", tc.from, tc.to, got)
		}
	}
}

func TestPinStatus(t *testing.T) {
	now := time.Now()
	pin := func(d time.Duration) *Pin { return &Pin{NotAfter: now.Add(d)} }
	for _, tc := range []struct {
		mode TLSMode
		p    *Pin
		want PinStatus
	}{
		{TLSUnverified, nil, PinNotRequired},
		{TLSPinned, nil, PinMissing},
		{TLSPinned, pin(90 * 24 * time.Hour), PinValid},
		{TLSPinned, pin(20 * 24 * time.Hour), PinExpires30d},
		{TLSPinned, pin(3 * 24 * time.Hour), PinExpires7d},
		{TLSPinned, pin(-time.Minute), PinExpired},
	} {
		if got := pinStatus(tc.mode, tc.p, now); got != tc.want {
			t.Errorf("%s %v: %s, want %s", tc.mode, tc.p, got, tc.want)
		}
	}
}

func TestApplySSHAndUpdate(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(block)

	st := baseState()
	if err := applySSH(st, &SSHInput{Host: "bastion.example.com", User: "tunnel", Password: []byte("ssh-pw")}); err != nil {
		t.Fatal(err)
	}
	if !st.ssh || st.sshPort != 22 || st.sshAuth != SSHAuthPassword || string(st.creds.SSHPassword) != "ssh-pw" || st.clearHostKey {
		t.Fatalf("state: %+v", st)
	}
	// Switching to a key removes the password; moving the bastion clears
	// the host key.
	st.credsChanged = nil
	if err := applySSH(st, &SSHInput{Host: "other.example.com", Port: 2222, User: "tunnel", PrivateKey: keyPEM}); err != nil {
		t.Fatal(err)
	}
	if st.sshAuth != SSHAuthPrivateKey || st.creds.SSHPassword != nil || !st.clearHostKey || !strings.HasPrefix(st.sshPublicKey, "ssh-ed25519 ") {
		t.Fatalf("state: %+v", st)
	}
	if strings.Join(st.credsChanged, ",") != "ssh_password,ssh_private_key" {
		t.Fatalf("credsChanged = %v", st.credsChanged)
	}
	for name, in := range map[string]*SSHInput{
		"both":            {Host: "b.example.com", User: "u", Password: []byte("x"), PrivateKey: keyPEM},
		"passphrase only": {Host: "b.example.com", User: "u", Passphrase: []byte("x")},
		"bad key":         {Host: "b.example.com", User: "u", PrivateKey: []byte("nope")},
		"newline pw":      {Host: "b.example.com", User: "u", Password: []byte("a\nb")},
	} {
		if err := applySSH(baseState(), in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	disableSSH(st)
	if st.ssh || st.creds.SSHPrivateKey != nil || st.sshAuth != SSHAuthNone || st.sshPublicKey != "" {
		t.Fatalf("disable: %+v", st)
	}

	st = baseState()
	st.creds.Password = []byte("old")
	if err := applyUpdate(st, &UpdateInput{ClearPassword: true, Password: []byte("x")}); err == nil {
		t.Fatal("password and clearPassword accepted together")
	}
	st = baseState()
	st.creds.Password = []byte("old")
	if err := applyUpdate(st, &UpdateInput{ClearPassword: true}); err != nil || st.creds.Password != nil || st.credsChanged[0] != "password" {
		t.Fatalf("clear password: %v %+v", err, st)
	}
	if err := applyUpdate(baseState(), &UpdateInput{DisableSSH: true, SSH: &SSHInput{}}); err == nil {
		t.Fatal("ssh and disableSsh accepted together")
	}
}

func TestClientCertificateShape(t *testing.T) {
	leaf := newCert(t, "client", false, nil, time.Now().Add(time.Hour))
	keyDER, err := x509.MarshalPKCS8PrivateKey(leaf.key)
	if err != nil {
		t.Fatal(err)
	}
	key := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if _, err := checkClientCertificate(leaf.pem, key); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		cert string
		key  []byte
	}{
		"no cert":       {"", key},
		"no key":        {leaf.pem, nil},
		"key as cert":   {string(key), key},
		"cert as key":   {leaf.pem, []byte(leaf.pem)},
		"two keys":      {leaf.pem, append(append([]byte{}, key...), key...)},
		"encrypted key": {leaf.pem, []byte("-----BEGIN PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\n\nAAAA\n-----END PRIVATE KEY-----\n")},
		"trailing junk": {leaf.pem + "junk", key},
	} {
		if _, err := checkClientCertificate(tc.cert, tc.key); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseSPKI(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	for _, s := range []string{
		base64.StdEncoding.EncodeToString(sum[:]), base64.RawURLEncoding.EncodeToString(sum[:]), hex.EncodeToString(sum[:]),
	} {
		got, err := parseSPKI(s)
		if err != nil || !bytes.Equal(got, sum[:]) {
			t.Errorf("%q: %v", s, err)
		}
	}
	for _, s := range []string{"abc", base64.StdEncoding.EncodeToString(sum[:16]), strings.Repeat("z", 64)} {
		if _, err := parseSPKI(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
	if b, err := parseSPKI(" "); err != nil || b != nil {
		t.Errorf("empty: %v", err)
	}
}

func FuzzParseSPKI(f *testing.F) {
	f.Add("47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=")
	f.Add("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	f.Fuzz(func(t *testing.T, s string) {
		b, err := parseSPKI(s)
		if err == nil && b != nil && len(b) != sha256.Size {
			t.Fatalf("accepted %d bytes", len(b))
		}
	})
}

// --- pins ---------------------------------------------------------------------

// chainRunner answers FetchServerCertificate with a fixed chain.
type chainRunner struct {
	runnerpb.RunnerServiceClient
	chain []*testCert
	calls int
}

func (c *chainRunner) FetchServerCertificate(context.Context, *runnerpb.FetchServerCertificateRequest, ...grpc.CallOption) (*runnerpb.FetchServerCertificateResponse, error) {
	c.calls++
	resp := &runnerpb.FetchServerCertificateResponse{}
	for _, tc := range c.chain {
		resp.Chain = append(resp.Chain, &runnerpb.PeerCertificate{Der: tc.cert.Raw})
	}
	if len(c.chain) > 0 {
		resp.LeafSpkiSha256 = c.chain[0].spki
	}
	return resp, nil
}

func pinRow() *sqlc.Connection {
	return &sqlc.Connection{Engine: sqlc.ConnectionEnginePostgres, Host: "db.example.com", Port: 5432}
}

// noPublicRoots makes every test anchor private (the real check runs
// against the system store, where test CAs never are).
func noPublicRoots(t *testing.T) {
	t.Helper()
	old := publicAnchor
	publicAnchor = func(*x509.Certificate) bool { return false }
	t.Cleanup(func() { publicAnchor = old })
}

func TestChoosePinPEM(t *testing.T) {
	noPublicRoots(t)
	s := testService(t)
	row := pinRow()
	soon := time.Now().Add(48 * time.Hour)
	leaf := newCert(t, "db.example.com", false, nil, soon)
	ca := newCert(t, "Root CA", true, nil, soon)
	caLeaf := newCert(t, "db.example.com", false, ca, soon.Add(-time.Hour))
	inter := newCert(t, "Intermediate", true, ca, soon)
	expired := newCert(t, "old.example.com", false, nil, time.Now().Add(-time.Minute))
	b64 := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	runner := &chainRunner{chain: []*testCert{leaf}}
	s.o.Runner = runner

	pc, err := s.choosePin(t.Context(), Actor{}, row, PinInput{CertificatePEM: leaf.pem, LeafSPKI: b64(leaf.spki)})
	if err != nil || !bytes.Equal(pc.leafSPKI, leaf.spki) || pc.source != "pem" || !pc.notAfter.Equal(leaf.cert.NotAfter) {
		t.Fatalf("leaf pin: %v %+v", err, pc)
	}
	pc, err = s.choosePin(t.Context(), Actor{}, row, PinInput{CertificatePEM: ca.pem, LeafSPKI: b64(caLeaf.spki)})
	if err != nil || !bytes.Equal(pc.leafSPKI, caLeaf.spki) || !pc.notAfter.Equal(ca.cert.NotAfter) {
		t.Fatalf("CA pin with leaf: %v %+v", err, pc)
	}
	// A PEM anchor may be staged before the server presents it: no capture.
	if runner.calls != 0 {
		t.Fatalf("a PEM pin captured the chain (%d calls)", runner.calls)
	}
	pc, err = s.choosePin(t.Context(), Actor{}, row, PinInput{CertificatePEM: ca.pem, AllowLeafRotation: true})
	if err != nil || pc.leafSPKI != nil || !pc.notAfter.Equal(ca.cert.NotAfter) {
		t.Fatalf("CA pin with rotation: %v", err)
	}
	other := sha256.Sum256([]byte("other"))
	runner.chain = []*testCert{leaf}
	idx := 0
	for name, in := range map[string]PinInput{
		"nothing":               {},
		"both":                  {CertIndex: &idx, CertificatePEM: leaf.pem, LeafSPKI: b64(leaf.spki)},
		"leaf without spki":     {CertificatePEM: leaf.pem},
		"leaf wrong spki":       {CertificatePEM: leaf.pem, LeafSPKI: b64(other[:])},
		"leaf rotation":         {CertificatePEM: leaf.pem, LeafSPKI: b64(leaf.spki), AllowLeafRotation: true},
		"CA without spki":       {CertificatePEM: ca.pem},
		"intermediate":          {CertificatePEM: inter.pem, AllowLeafRotation: true},
		"expired":               {CertificatePEM: expired.pem, LeafSPKI: b64(expired.spki)},
		"two certs":             {CertificatePEM: leaf.pem + ca.pem, LeafSPKI: b64(leaf.spki)},
		"not pem":               {CertificatePEM: "hello", LeafSPKI: b64(leaf.spki)},
		"cert index, no spki":   {CertIndex: &idx},
		"spki garbage":          {CertificatePEM: leaf.pem, LeafSPKI: "!!"},
		"private key as anchor": {CertificatePEM: "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n", LeafSPKI: b64(leaf.spki)},
	} {
		if _, err := s.choosePin(t.Context(), Actor{}, row, in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestChoosePinChainCheck(t *testing.T) {
	noPublicRoots(t)
	s := testService(t)
	soon := time.Now().Add(48 * time.Hour)
	ca := newCert(t, "Root CA", true, nil, soon)
	inter := newCert(t, "Intermediate", true, ca, soon)
	leaf := newCert(t, "db.example.com", false, inter, soon)
	runner := &chainRunner{chain: []*testCert{leaf, inter, ca}}
	s.o.Runner = runner
	b64 := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

	// The root at index 2 verifies leaf -> intermediate -> root.
	idx := 2
	if pc, err := s.choosePin(t.Context(), Actor{}, pinRow(), PinInput{CertIndex: &idx, LeafSPKI: b64(leaf.spki)}); err != nil || pc.source != "captured" {
		t.Fatalf("root of a captured chain: %v", err)
	}
	// Another host name: the leaf is not valid for it.
	row := pinRow()
	row.Host = "other.example.com"
	var fe *FieldError
	if _, err := s.choosePin(t.Context(), Actor{}, row, PinInput{CertIndex: &idx, LeafSPKI: b64(leaf.spki)}); !errors.As(err, &fe) || !strings.Contains(fe.Message, "host name") {
		t.Fatalf("wrong host: %v", err)
	}
	// A self-signed root the server appended that did not issue the chain
	// (Code M9: the index alone proves nothing).
	stranger := newCert(t, "Stranger CA", true, nil, soon)
	runner.chain = []*testCert{leaf, inter, stranger}
	if _, err := s.choosePin(t.Context(), Actor{}, pinRow(), PinInput{CertIndex: &idx, LeafSPKI: b64(leaf.spki)}); !errors.As(err, &fe) || !strings.Contains(fe.Message, "does not verify") {
		t.Fatalf("stranger CA: %v", err)
	}
	// A chain without its intermediate cannot be verified against the root.
	runner.chain = []*testCert{leaf, ca}
	one := 1
	if _, err := s.choosePin(t.Context(), Actor{}, pinRow(), PinInput{CertIndex: &one, LeafSPKI: b64(leaf.spki)}); err == nil {
		t.Fatal("chain without the intermediate accepted")
	}
	// A self-signed leaf verifies as its own anchor.
	self := newCert(t, "db.example.com", false, nil, soon)
	runner.chain = []*testCert{self}
	zero := 0
	if _, err := s.choosePin(t.Context(), Actor{}, pinRow(), PinInput{CertIndex: &zero, LeafSPKI: b64(self.spki)}); err != nil {
		t.Fatalf("self-signed leaf: %v", err)
	}
}

func TestChoosePinRefusesPublicCA(t *testing.T) {
	s := testService(t)
	soon := time.Now().Add(48 * time.Hour)
	ca := newCert(t, "Pretend Public Root", true, nil, soon)
	leaf := newCert(t, "db.example.com", false, ca, soon)
	s.o.Runner = &chainRunner{chain: []*testCert{leaf, ca}}
	old := publicAnchor
	t.Cleanup(func() { publicAnchor = old })
	publicAnchor = func(c *x509.Certificate) bool { return c.Equal(ca.cert) }
	idx := 1
	in := PinInput{CertIndex: &idx, LeafSPKI: base64.StdEncoding.EncodeToString(leaf.spki), AllowLeafRotation: true}
	var fe *FieldError
	_, err := s.choosePin(t.Context(), Actor{}, pinRow(), in)
	if !errors.As(err, &fe) || !strings.Contains(fe.Message, "public CA") {
		t.Fatalf("public CA: %v", err)
	}
	// The real check: a private test CA is not public.
	publicAnchor = old
	if _, err := s.choosePin(t.Context(), Actor{}, pinRow(), in); err != nil {
		t.Fatalf("private CA refused: %v", err)
	}
	// An out-of-band PEM anchor is checked too.
	publicAnchor = func(c *x509.Certificate) bool { return c.Equal(ca.cert) }
	if _, err := s.choosePin(t.Context(), Actor{}, pinRow(), PinInput{CertificatePEM: ca.pem, AllowLeafRotation: true}); !errors.As(err, &fe) {
		t.Fatalf("public CA as PEM: %v", err)
	}
}

// --- request builder ------------------------------------------------------------

func ptr[T any](v T) *T { return &v }

func TestBuildTarget(t *testing.T) {
	leaf := newCert(t, "db.example.com", false, nil, time.Now().Add(time.Hour))
	ca := newCert(t, "Root CA", true, nil, time.Now().Add(time.Hour))
	row := &sqlc.Connection{
		Engine: sqlc.ConnectionEnginePostgres, VersionKey: "16", ImageKey: "17", Host: "db.example.com", Port: 5432,
		Username: "backup", TlsMode: sqlc.ConnectionTlsModePinned,
		TlsPinCertPem: &leaf.pem, TlsPinSpki: leaf.spki, TlsBackupPinCertPem: &ca.pem,
		ClientCertPem: ptr("CERT"),
		SshEnabled:    true, SshHost: ptr("bastion.example.com"), SshPort: ptr(int32(22)), SshUser: ptr("tunnel"),
		SshHostKeyType: ptr("ssh-ed25519"), SshHostKeyFingerprint: ptr("SHA256:x"), SshHostKeyAuthorizedKey: ptr("ssh-ed25519 AAAA"),
	}
	creds := &Credentials{Password: []byte("db-pw"), SSHPassword: []byte("ssh-pw"), ClientKey: []byte("KEY")}
	tg, err := buildTarget(row, creds)
	if err != nil {
		t.Fatal(err)
	}
	if tg.GetEngine() != runnerpb.Engine_ENGINE_POSTGRES || tg.GetVersionKey() != "17" || tg.GetTlsMode() != runnerpb.TlsMode_TLS_MODE_PINNED {
		t.Fatalf("target: %v", tg)
	}
	if len(tg.GetPins()) != 2 || !bytes.Equal(tg.GetPins()[0].GetAnchorDer(), leaf.cert.Raw) ||
		!bytes.Equal(tg.GetPins()[0].GetLeafSpkiSha256(), leaf.spki) || tg.GetPins()[1].GetLeafSpkiSha256() != nil {
		t.Fatalf("pins: %v", tg.GetPins())
	}
	if string(tg.GetPassword()) != "db-pw" || string(tg.GetSshTunnel().GetPassword()) != "ssh-pw" ||
		string(tg.GetClientCertificate().GetKeyPem()) != "KEY" || tg.GetSshTunnel().GetHostKeyPin().GetAuthorizedKey() != "ssh-ed25519 AAAA" {
		t.Fatalf("credentials: %v", tg)
	}
	// The request holds copies: zeroing the credentials leaves it intact,
	// and ZeroSecrets wipes the request.
	creds.Zero()
	if string(tg.GetPassword()) != "db-pw" {
		t.Fatal("the request aliases the credentials")
	}
	pw := tg.GetPassword()
	runnerpb.ZeroSecrets(tg)
	if strings.Trim(string(pw), "\x00") != "" {
		t.Fatal("ZeroSecrets left the password")
	}

	// Not ready: pinned without a pin, tunnel without host key or credential.
	noPin := *row
	noPin.TlsPinCertPem, noPin.TlsPinSpki = nil, nil
	var nr *NotReadyError
	if _, err := buildTarget(&noPin, &Credentials{SSHPassword: []byte("x")}); !errors.As(err, &nr) {
		t.Fatalf("no pin: %v", err)
	}
	noKey := *row
	noKey.SshHostKeyType, noKey.SshHostKeyFingerprint, noKey.SshHostKeyAuthorizedKey = nil, nil, nil
	if _, err := buildTarget(&noKey, &Credentials{SSHPassword: []byte("x")}); !errors.As(err, &nr) {
		t.Fatalf("no host key: %v", err)
	}
	if _, err := buildTarget(row, &Credentials{}); !errors.As(err, &nr) {
		t.Fatalf("no ssh credential: %v", err)
	}
	// Unverified: no pins are sent; disabled: no client certificate.
	plain := *row
	plain.TlsMode, plain.SshEnabled = sqlc.ConnectionTlsModeDisabled, false
	tg, err = buildTarget(&plain, &Credentials{ClientKey: []byte("KEY")})
	if err != nil || len(tg.GetPins()) != 0 || tg.GetClientCertificate() != nil || tg.GetSshTunnel() != nil {
		t.Fatalf("disabled: %v %v", err, tg)
	}
}

// --- runner errors ------------------------------------------------------------

func TestRunnerErrorMapping(t *testing.T) {
	s := testService(t)
	row := &sqlc.Connection{Name: "x"}
	for name, tc := range map[string]struct {
		err  error
		code string
		is   error
	}{
		"unreachable":  {runnerpb.ReasonUnreachable.Status(), string(runnerpb.ReasonUnreachable), nil},
		"auth":         {runnerpb.ReasonAuthFailed.Status(), string(runnerpb.ReasonAuthFailed), nil},
		"ssh auth":     {runnerpb.ReasonSSHAuthFailed.Status(), string(runnerpb.ReasonSSHAuthFailed), nil},
		"unsupported":  {runnerpb.ReasonSSHHostKeyUnsupported.Status(), string(runnerpb.ReasonSSHHostKeyUnsupported), nil},
		"not allowed":  {status.Error(codes.PermissionDenied, "x"), CodeTargetNotAllowed, nil},
		"invalid":      {status.Error(codes.InvalidArgument, "target.host: invalid"), CodeInvalidRequest, nil},
		"precondition": {status.Error(codes.FailedPrecondition, "x"), CodeRunnerFailed, nil},
		"deadline":     {status.Error(codes.DeadlineExceeded, "x"), string(runnerpb.ReasonTimeout), nil},
		"busy":         {status.Error(codes.ResourceExhausted, "x"), "", ErrBusy},
		"down":         {status.Error(codes.Unavailable, "connection refused"), "", ErrRunnerUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			err := s.runnerError(t.Context(), Actor{}, row, "test", tc.err)
			if tc.is != nil {
				if !errors.Is(err, tc.is) {
					t.Fatalf("got %v, want %v", err, tc.is)
				}
				return
			}
			var te *TargetError
			if !errors.As(err, &te) || te.Code != tc.code || te.Message == "" {
				t.Fatalf("got %v, want code %s", err, tc.code)
			}
			// Messages are fixed texts, never the runner's message.
			if strings.Contains(te.Message, "target.host") {
				t.Fatalf("message leaks the runner text: %q", te.Message)
			}
		})
	}
	if err := s.runnerError(t.Context(), Actor{}, row, "test", nil); err != nil {
		t.Fatal(err)
	}
}

func TestPinCheck(t *testing.T) {
	st := status.New(codes.FailedPrecondition, "target certificate does not match the pinned certificate")
	st, err := st.WithDetails(&errdetails.ErrorInfo{Reason: string(runnerpb.ReasonPinMismatch), Domain: runnerpb.ErrorDomain,
		Metadata: map[string]string{"check": "leaf_spki"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := pinCheck(st.Err()); got != "leaf_spki" {
		t.Fatalf("check = %q", got)
	}
	if runnerpb.ErrorReason(st.Err()) != runnerpb.ReasonPinMismatch {
		t.Fatal("reason")
	}
	if pinCheck(errors.New("x")) != "" {
		t.Fatal("plain error")
	}
}

func TestLogAlert(t *testing.T) {
	var b strings.Builder
	f := logAlert(slog.New(slog.NewTextHandler(&b, nil)))
	f(context.Background(), Alert{Kind: AlertTLSDowngrade, ConnectionName: "prod", Details: map[string]any{"to": "disabled"}})
	if !strings.Contains(b.String(), "kind=tls_downgrade") || !strings.Contains(b.String(), "to=disabled") {
		t.Fatalf("log: %s", b.String())
	}
}

// --- retargeting (Sec M3) -----------------------------------------------------

func TestRequireReentry(t *testing.T) {
	cur := func() *sqlc.Connection {
		return &sqlc.Connection{
			Host: "db.example.com", Port: 5432, HasPassword: true, SshEnabled: true,
			SshHost: ptr("bastion.example.com"), SshPort: ptr(int32(22)), SshUser: ptr("tunnel"),
			SshAuth: sqlc.ConnectionSshAuthPassword,
		}
	}
	next := func(c *sqlc.Connection) *state { return stateOf(c, &Credentials{}) }
	for name, tc := range map[string]struct {
		mut   func(*state)
		in    UpdateInput
		field string   // expected field error ("" = ok)
		want  []string // re-entered fields
	}{
		"name only":                {func(*state) {}, UpdateInput{}, "", nil},
		"db user only":             {func(st *state) { st.username = "other" }, UpdateInput{}, "", nil},
		"host without password":    {func(st *state) { st.host = "evil.example.com" }, UpdateInput{}, "password", nil},
		"port without password":    {func(st *state) { st.port = 6432 }, UpdateInput{}, "password", nil},
		"host with password":       {func(st *state) { st.host = "new.example.com" }, UpdateInput{Password: []byte("pw")}, "", []string{"password"}},
		"host with clear":          {func(st *state) { st.host = "new.example.com" }, UpdateInput{ClearPassword: true}, "", []string{"password"}},
		"tunnel removed":           {func(st *state) { st.ssh = false }, UpdateInput{}, "password", nil},
		"bastion moved, db pw":     {func(st *state) { st.sshHost = "b2.example.com" }, UpdateInput{SSH: &SSHInput{Password: []byte("s")}}, "password", nil},
		"bastion moved, ssh pw":    {func(st *state) { st.sshHost = "b2.example.com" }, UpdateInput{Password: []byte("p"), SSH: &SSHInput{}}, "ssh.password", nil},
		"bastion moved, both":      {func(st *state) { st.sshPort = 2222 }, UpdateInput{Password: []byte("p"), SSH: &SSHInput{Password: []byte("s")}}, "", []string{"password", "ssh_password"}},
		"bastion moved, key":       {func(st *state) { st.sshPort = 2222 }, UpdateInput{Password: []byte("p"), SSH: &SSHInput{PrivateKey: []byte("k")}}, "", []string{"password", "ssh_password"}},
		"ssh user without ssh pw":  {func(st *state) { st.sshUser = "root" }, UpdateInput{SSH: &SSHInput{}}, "ssh.password", nil},
		"ssh user, db pw not need": {func(st *state) { st.sshUser = "root" }, UpdateInput{SSH: &SSHInput{Password: []byte("s")}}, "", []string{"ssh_password"}},
	} {
		t.Run(name, func(t *testing.T) {
			c := cur()
			st := next(c)
			tc.mut(st)
			got, err := requireReentry(c, st, &tc.in)
			if tc.field != "" {
				var fe *FieldError
				if !errors.As(err, &fe) || fe.Field != tc.field {
					t.Fatalf("err = %v, want field %s", err, tc.field)
				}
				return
			}
			if err != nil || strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	// Without stored passwords nothing has to be re-entered; a private key
	// stays across a bastion move.
	c := cur()
	c.HasPassword, c.SshAuth = false, sqlc.ConnectionSshAuthPrivateKey
	st := next(c)
	st.host, st.sshHost = "new.example.com", "b2.example.com"
	if got, err := requireReentry(c, st, &UpdateInput{SSH: &SSHInput{}}); err != nil || got != nil {
		t.Fatalf("no stored passwords: %v %v", got, err)
	}
}
