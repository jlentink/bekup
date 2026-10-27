package destinations

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
)

func fieldOf(t *testing.T, err error) string {
	t.Helper()
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("want a FieldError, got %v", err)
	}
	return fe.Field
}

func TestNormalizeName(t *testing.T) {
	for in, ok := range map[string]bool{
		"Offsite S3": true, "  trimmed  ": true, "": false, "   ": false,
		strings.Repeat("x", 100): true, strings.Repeat("x", 101): false, "bad\x00name": false, "tab\tname": false,
	} {
		_, err := normalizeName(in)
		if (err == nil) != ok {
			t.Errorf("%q: err %v", in, err)
		}
	}
}

func TestValidateDisk(t *testing.T) {
	for in, want := range map[string]string{
		"backups":          "backups",
		"a/b/c":            "a/b/c",
		"/leading/slashes": "",
		"":                 "",
		"..":               "",
		"a/../b":           "",
		".hidden":          "",
		"a//b":             "",
		"with space":       "",
		"a\\b":             "",
	} {
		c, err := validateDisk(&DiskInput{Path: in})
		if want == "" {
			if err == nil {
				t.Errorf("%q accepted as %q", in, c.Path)
			}
			continue
		}
		if err != nil || c.Path != want {
			t.Errorf("%q: %q %v", in, c.Path, err)
		}
	}
}

func testCA(t *testing.T) string {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestCABundle(t *testing.T) {
	ca := testCA(t)
	if _, err := s3ConfigOf(&S3Input{CABundlePEM: ca}); err != nil {
		t.Fatalf("valid bundle: %v", err)
	}
	for name, b := range map[string]string{
		"garbage":     "not pem",
		"private key": ca + "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n",
		"trailing":    ca + "junk",
		"bad cert":    "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n",
	} {
		if _, err := s3ConfigOf(&S3Input{CABundlePEM: b}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSFTPInputConflicts(t *testing.T) {
	for name, in := range map[string]SFTPInput{
		"both":            {Password: []byte("a"), PrivateKey: []byte("b")},
		"passphrase only": {Passphrase: []byte("p")},
	} {
		if _, err := sftpConfigOf(&in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	c, err := sftpConfigOf(&SFTPInput{Host: " sftp.example.com ", User: "u", BasePath: "/up", Dir: "/a/"})
	if err != nil || c.Port != 22 || c.Host != "sftp.example.com" || c.Dir != "a" {
		t.Fatalf("defaults: %+v %v", c, err)
	}
}

func pemOf(t *testing.T, key any, passphrase []byte) []byte {
	t.Helper()
	var (
		b   *pem.Block
		err error
	)
	if passphrase != nil {
		b, err = ssh.MarshalPrivateKeyWithPassphrase(key, "", passphrase)
	} else {
		b, err = ssh.MarshalPrivateKey(key, "")
	}
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(b)
}

func TestCheckPrivateKey(t *testing.T) {
	kp, err := crypto.GenerateSSHKeypair("c")
	if err != nil {
		t.Fatal(err)
	}
	defer kp.PrivateKeyPEM.Destroy()
	line, err := checkPrivateKey(kp.PrivateKeyPEM.Bytes(), nil)
	if err != nil || !strings.HasPrefix(kp.AuthorizedKey, line) {
		t.Fatalf("ed25519: %q %v", line, err)
	}
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enc := pemOf(t, ec, []byte("pass phrase"))
	if _, err := checkPrivateKey(enc, nil); fieldOf(t, err) != "sftp.privateKeyPassphrase" {
		t.Fatalf("missing passphrase: %v", err)
	}
	if _, err := checkPrivateKey(enc, []byte("wrong")); fieldOf(t, err) != "sftp.privateKeyPassphrase" {
		t.Fatalf("wrong passphrase: %v", err)
	}
	if _, err := checkPrivateKey(enc, []byte("pass phrase")); err != nil {
		t.Fatalf("ecdsa with passphrase: %v", err)
	}
	small, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // G403: the test proves short keys are rejected
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkPrivateKey(pemOf(t, small, nil), nil); err == nil || !strings.Contains(err.Error(), "2048") {
		t.Fatalf("rsa 1024: %v", err)
	}
	if _, err := checkPrivateKey([]byte("garbage"), nil); fieldOf(t, err) != "sftp.privateKey" {
		t.Fatalf("garbage: %v", err)
	}
}

// testService is a service without a database for the validation paths.
func testService(t *testing.T, allowHTTP bool) *Service {
	t.Helper()
	g, err := netguard.New(netguard.Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	return &Service{o: Options{Guard: g, AllowHTTP: allowHTTP, Keyring: testKeyring(t)}}
}

func validS3() *S3Input {
	return &S3Input{
		Endpoint: "https://127.0.0.1:9000", Region: "us-east-1", Bucket: "backups", AccessKeyID: "AKIAEXAMPLE",
		SecretAccessKey: []byte("secret-access-key"), PathStyle: true,
	}
}

func TestApplyS3(t *testing.T) {
	ctx := context.Background()
	s := testService(t, false)
	newState := func() *state { return &state{typ: TypeS3, creds: &Credentials{}} }

	st := newState()
	if err := s.applyS3(ctx, st, validS3()); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if string(st.creds.SecretAccessKey) != "secret-access-key" || st.s3.Checksum != "when_supported" || !st.s3.ConditionalWrites {
		t.Fatalf("state: %+v %+v", st.s3, st.creds)
	}
	if len(st.credsChanged) != 1 || st.credsChanged[0] != "secret_access_key" {
		t.Fatalf("creds changed: %v", st.credsChanged)
	}

	cases := map[string]func(*S3Input){
		"http outside dev":    func(in *S3Input) { in.Endpoint = "http://127.0.0.1:9000" },
		"private endpoint":    func(in *S3Input) { in.Endpoint = "https://10.1.2.3" },
		"metadata endpoint":   func(in *S3Input) { in.Endpoint = "https://169.254.169.254" },
		"endpoint with path":  func(in *S3Input) { in.Endpoint = "https://127.0.0.1/bucket" },
		"no secret":           func(in *S3Input) { in.SecretAccessKey = nil },
		"empty secret":        func(in *S3Input) { in.SecretAccessKey = []byte{} },
		"bad bucket":          func(in *S3Input) { in.Bucket = "Bad_Bucket" },
		"bad region":          func(in *S3Input) { in.Region = "" },
		"lock without days":   func(in *S3Input) { in.ObjectLockMode = storage.LockCompliance },
		"days without lock":   func(in *S3Input) { in.ObjectLockRetentionDays = 3 },
		"unknown lock":        func(in *S3Input) { in.ObjectLockMode = "forever" },
		"bad prefix":          func(in *S3Input) { in.Prefix = "../x" },
		"kms id without kms":  func(in *S3Input) { in.KMSKeyID = "alias/x" },
		"archive class":       func(in *S3Input) { in.StorageClass = "GLACIER" },
		"user info":           func(in *S3Input) { in.Endpoint = "https://u:p@127.0.0.1" },
		"unknown sse":         func(in *S3Input) { in.SSE = "rot13" },
		"unknown checksum":    func(in *S3Input) { in.Checksum = "sometimes" },
		"bad access key":      func(in *S3Input) { in.AccessKeyID = "a b" },
		"oversized CA bundle": func(in *S3Input) { in.CABundlePEM = strings.Repeat("x", maxCABundleBytes+1) },
	}
	for name, mut := range cases {
		in := validS3()
		mut(in)
		if err := s.applyS3(ctx, newState(), in); err == nil {
			t.Errorf("%s accepted", name)
		} else {
			fieldOf(t, err)
		}
	}

	// Locked + valid.
	in := validS3()
	in.ObjectLockMode, in.ObjectLockRetentionDays = storage.LockGovernance, 30
	st = newState()
	if err := s.applyS3(ctx, st, in); err != nil || st.lockMode != storage.LockGovernance || st.lockDays != 30 {
		t.Fatalf("lock: %v %+v", err, st)
	}
	// http in dev mode.
	in = validS3()
	in.Endpoint = "http://127.0.0.1:9000"
	if err := testService(t, true).applyS3(ctx, newState(), in); err != nil {
		t.Fatalf("http in dev: %v", err)
	}
	// Keeping the stored secret: no new value, existing credentials.
	st = newState()
	st.creds.SecretAccessKey = []byte("stored")
	in = validS3()
	in.SecretAccessKey = nil
	if err := s.applyS3(ctx, st, in); err != nil || string(st.creds.SecretAccessKey) != "stored" || len(st.credsChanged) != 0 {
		t.Fatalf("keep secret: %v %+v", err, st)
	}
}

func TestApplySFTP(t *testing.T) {
	ctx := context.Background()
	s := testService(t, false)
	kp, err := crypto.GenerateSSHKeypair("k")
	if err != nil {
		t.Fatal(err)
	}
	defer kp.PrivateKeyPEM.Destroy()
	base := func() *SFTPInput {
		return &SFTPInput{Host: "127.0.0.1", Port: 2222, User: "bekup", BasePath: "/upload"}
	}
	// No credentials yet: allowed (keypair generation comes later).
	st := &state{typ: TypeSFTP, creds: &Credentials{}}
	if err := s.applySFTP(ctx, st, nil, base()); err != nil || st.sftp.AuthMethod != AuthNone {
		t.Fatalf("no creds: %v %+v", err, st.sftp)
	}
	// Password, then a key replaces it.
	in := base()
	in.Password = []byte("pw")
	if err := s.applySFTP(ctx, st, nil, in); err != nil || st.sftp.AuthMethod != AuthPassword || string(st.creds.Password) != "pw" {
		t.Fatalf("password: %v", err)
	}
	prev := *st.sftp
	in = base()
	in.PrivateKey = append([]byte(nil), kp.PrivateKeyPEM.Bytes()...)
	st.credsChanged = nil
	if err := s.applySFTP(ctx, st, &prev, in); err != nil {
		t.Fatal(err)
	}
	if st.creds.Password != nil || st.sftp.AuthMethod != AuthPrivateKey || !strings.HasPrefix(kp.AuthorizedKey, st.sftp.PublicKey) {
		t.Fatalf("key replaces password: %+v", st.sftp)
	}
	if strings.Join(st.credsChanged, ",") != "password,private_key" {
		t.Fatalf("creds changed: %v", st.credsChanged)
	}
	if st.clearHostKey {
		t.Fatal("same host clears the pin")
	}
	// A host change clears the host key pin.
	prev = *st.sftp
	in = base()
	in.Port = 2223
	if err := s.applySFTP(ctx, st, &prev, in); err != nil || !st.clearHostKey || st.sftp.AuthMethod != AuthPrivateKey {
		t.Fatalf("host change: %v %+v", err, st)
	}
	if len(st.reentered) != 0 {
		t.Fatalf("a private key needs no re-entry: %v", st.reentered)
	}

	// D-140 (Sec M3): a stored password is never sent to a new endpoint.
	pwState := func() (*state, *SFTPConfig) {
		st := &state{typ: TypeSFTP, creds: &Credentials{}}
		in := base()
		in.Password = []byte("stored-pw")
		if err := s.applySFTP(ctx, st, nil, in); err != nil {
			t.Fatal(err)
		}
		prev := *st.sftp
		st.credsChanged = nil
		return st, &prev
	}
	for name, mut := range map[string]func(*SFTPInput){
		"host":      func(in *SFTPInput) { in.Host = "127.0.0.2" },
		"port":      func(in *SFTPInput) { in.Port = 2200 },
		"user":      func(in *SFTPInput) { in.User = "other" },
		"base path": func(in *SFTPInput) { in.BasePath = "/elsewhere" },
	} {
		st, prev := pwState()
		in := base()
		mut(in)
		if err := s.applySFTP(ctx, st, prev, in); fieldOf(t, err) != "sftp.password" {
			t.Errorf("%s change without password: %v", name, err)
		}
		st, prev = pwState()
		in = base()
		mut(in)
		in.Password = []byte("new-pw")
		if err := s.applySFTP(ctx, st, prev, in); err != nil || strings.Join(st.reentered, ",") != "password" {
			t.Errorf("%s change with password: %v %v", name, err, st.reentered)
		}
	}
	st2, prev2 := pwState()
	in = base()
	in.Dir = "sub"
	if err := s.applySFTP(ctx, st2, prev2, in); err != nil || len(st2.reentered) != 0 {
		t.Fatalf("a subdirectory change needs no re-entry: %v %v", err, st2.reentered)
	}
	for name, mut := range map[string]func(*SFTPInput){
		"private host":  func(in *SFTPInput) { in.Host = "192.168.1.10" },
		"relative base": func(in *SFTPInput) { in.BasePath = "upload" },
		"bad user":      func(in *SFTPInput) { in.User = "a b" },
		"bad port":      func(in *SFTPInput) { in.Port = 70000 },
		"traversal dir": func(in *SFTPInput) { in.Dir = "../x" },
		"empty pw":      func(in *SFTPInput) { in.Password = []byte{} },
		"bad key":       func(in *SFTPInput) { in.PrivateKey = []byte("nope") },
	} {
		in := base()
		mut(in)
		if err := s.applySFTP(ctx, &state{typ: TypeSFTP, creds: &Credentials{}}, nil, in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestReadinessAndView(t *testing.T) {
	sealed := []byte{1}
	now := time.Now()
	typ, fp, line := "ssh-ed25519", "SHA256:"+strings.Repeat("A", 43), "ssh-ed25519 AAAA"
	for name, tc := range map[string]struct {
		row   sqlc.Destination
		ready bool
	}{
		"disk":             {sqlc.Destination{Type: sqlc.DestinationTypeDisk, Config: []byte(`{"path":"a"}`)}, true},
		"s3 without key":   {sqlc.Destination{Type: sqlc.DestinationTypeS3, Config: []byte(`{}`)}, false},
		"s3":               {sqlc.Destination{Type: sqlc.DestinationTypeS3, Config: []byte(`{}`), SecretEnc: sealed}, true},
		"sftp no pin":      {sqlc.Destination{Type: sqlc.DestinationTypeSftp, Config: []byte(`{"auth_method":"password"}`), SecretEnc: sealed}, false},
		"sftp no creds":    {sqlc.Destination{Type: sqlc.DestinationTypeSftp, Config: []byte(`{}`), HostKeyType: &typ, HostKeyFingerprint: &fp, HostKeyAuthorizedKey: &line, HostKeyConfirmedAt: &now}, false},
		"sftp ready (key)": {sqlc.Destination{Type: sqlc.DestinationTypeSftp, Config: []byte(`{"auth_method":"private_key"}`), SecretEnc: sealed, HostKeyType: &typ, HostKeyFingerprint: &fp, HostKeyAuthorizedKey: &line, HostKeyConfirmedAt: &now}, true},
	} {
		tc.row.ID = uuid.New()
		r, err := decode(tc.row)
		if err != nil {
			t.Fatal(err)
		}
		v := r.view()
		if v.Ready != tc.ready || (v.Ready == (v.NotReadyReason != "")) {
			t.Errorf("%s: ready %v reason %q", name, v.Ready, v.NotReadyReason)
		}
	}
}

func TestDiffListsSecretsByNameOnly(t *testing.T) {
	cur := &record{row: sqlc.Destination{Name: "a", Type: sqlc.DestinationTypeS3}, s3: &S3Config{Bucket: "one", CABundlePEM: "x"}}
	st := &state{typ: TypeS3, name: "b", s3: &S3Config{Bucket: "two", CABundlePEM: "y"}, credsChanged: []string{"secret_access_key"}}
	changed, values := diff(cur, st)
	if strings.Join(changed, ",") != "name,bucket,ca_bundle_pem,secret_access_key" {
		t.Fatalf("changed: %v", changed)
	}
	if _, ok := values["ca_bundle_pem"]; ok {
		t.Fatal("CA bundle values recorded")
	}
	if _, ok := values["secret_access_key"]; ok {
		t.Fatal("secret recorded")
	}
	if v, ok := values["bucket"].(map[string]any); !ok || v["old"] != "one" || v["new"] != "two" {
		t.Fatalf("bucket: %v", values["bucket"])
	}
}

func TestOnlyInputFor(t *testing.T) {
	if err := onlyInputFor(TypeS3, false, true, false, true); err != nil {
		t.Fatal(err)
	}
	if err := onlyInputFor(TypeS3, true, false, false, false); err == nil {
		t.Fatal("disk input on S3 accepted")
	}
	if err := onlyInputFor(TypeSFTP, false, false, false, true); err == nil {
		t.Fatal("missing input accepted on create")
	}
	if err := onlyInputFor(TypeSFTP, false, false, false, false); err != nil {
		t.Fatal(err)
	}
}
