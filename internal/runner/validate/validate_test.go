package validate

import (
	"bytes"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

func code(err error) codes.Code { return status.Code(err) }

func TestDumpValid(t *testing.T) {
	v := newTestValidator(t)
	req := &runnerpb.DumpRequest{Ctx: runCtx(), Target: validTarget(t), Database: "app_prod"}
	got, err := v.Dump(t.Context(), req)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if got.Kind != engine.KindPostgres || got.Entry.Key() != "17" || got.Database != "app_prod" {
		t.Errorf("got %+v", got)
	}
	if got.Direct == nil || got.Direct.Port != 5432 || len(got.Direct.Addrs) != 1 || got.SSH != nil {
		t.Errorf("endpoint %+v", got.Direct)
	}
	if got.Conn.TLS != engine.TLSPinned || got.Conn.ClientCert || len(got.Pins) != 1 {
		t.Errorf("conn %+v pins %d", got.Conn, len(got.Pins))
	}
	// The validated target builds an engine command.
	if _, err := got.Engine.Dump(got.Entry, got.Conn, got.Database); err != nil {
		t.Errorf("engine.Dump: %v", err)
	}
}

// mutation changes a valid DumpRequest.
type mutation struct {
	name string
	mut  func(*runnerpb.DumpRequest)
	code codes.Code
}

func TestDumpInvalid(t *testing.T) {
	f := getFixtures(t)
	tests := []mutation{
		{"no ctx", func(r *runnerpb.DumpRequest) { r.Ctx = nil }, codes.InvalidArgument},
		{"bad run id", func(r *runnerpb.DumpRequest) { r.Ctx.RunId = "x" }, codes.InvalidArgument},
		{"upper run id", func(r *runnerpb.DumpRequest) { r.Ctx.RunId = strings.ToUpper(testRunID) }, codes.InvalidArgument},
		{"no target", func(r *runnerpb.DumpRequest) { r.Target = nil }, codes.InvalidArgument},
		{"unspecified engine", func(r *runnerpb.DumpRequest) { r.Target.Engine = 0 }, codes.InvalidArgument},
		{"unknown engine", func(r *runnerpb.DumpRequest) { r.Target.Engine = 42 }, codes.InvalidArgument},
		{"unknown version", func(r *runnerpb.DumpRequest) { r.Target.VersionKey = "12" }, codes.InvalidArgument},
		{"cross-engine version", func(r *runnerpb.DumpRequest) { r.Target.VersionKey = "8.4" }, codes.InvalidArgument},
		{"empty version", func(r *runnerpb.DumpRequest) { r.Target.VersionKey = "" }, codes.InvalidArgument},
		{"long version", func(r *runnerpb.DumpRequest) { r.Target.VersionKey = strings.Repeat("1", 100) }, codes.InvalidArgument},
		{"bad host", func(r *runnerpb.DumpRequest) { r.Target.Host = "db example" }, codes.InvalidArgument},
		{"option host", func(r *runnerpb.DumpRequest) { r.Target.Host = "-h" }, codes.InvalidArgument},
		{"url host", func(r *runnerpb.DumpRequest) { r.Target.Host = "postgres://x" }, codes.InvalidArgument},
		{"zone host", func(r *runnerpb.DumpRequest) { r.Target.Host = "fe80::1%eth0" }, codes.InvalidArgument},
		{"port 0", func(r *runnerpb.DumpRequest) { r.Target.Port = 0 }, codes.InvalidArgument},
		{"port big", func(r *runnerpb.DumpRequest) { r.Target.Port = 70000 }, codes.InvalidArgument},
		{"bad user", func(r *runnerpb.DumpRequest) { r.Target.User = "-U" }, codes.InvalidArgument},
		{"user space", func(r *runnerpb.DumpRequest) { r.Target.User = "a b" }, codes.InvalidArgument},
		{"password newline", func(r *runnerpb.DumpRequest) { r.Target.Password = []byte("pass\nword") }, codes.InvalidArgument},
		{"password NUL", func(r *runnerpb.DumpRequest) { r.Target.Password = []byte("pa\x00ss") }, codes.InvalidArgument},
		{"password huge", func(r *runnerpb.DumpRequest) { r.Target.Password = bytes.Repeat([]byte("p"), MaxPasswordBytes+1) }, codes.InvalidArgument},
		{"tls unspecified", func(r *runnerpb.DumpRequest) { r.Target.TlsMode = 0 }, codes.InvalidArgument},
		{"tls unknown", func(r *runnerpb.DumpRequest) { r.Target.TlsMode = 9 }, codes.InvalidArgument},
		{"pinned without pin", func(r *runnerpb.DumpRequest) { r.Target.Pins = nil }, codes.InvalidArgument},
		{"three pins", func(r *runnerpb.DumpRequest) {
			p := r.Target.Pins[0]
			r.Target.Pins = []*runnerpb.CertificatePin{p, {AnchorDer: f.selfSignedLeaf, LeafSpkiSha256: f.selfSignedSPKI}, {AnchorDer: f.caDER}}
		}, codes.InvalidArgument},
		{"duplicate pins", func(r *runnerpb.DumpRequest) { r.Target.Pins = append(r.Target.Pins, r.Target.Pins[0]) }, codes.InvalidArgument},
		{"nil pin", func(r *runnerpb.DumpRequest) { r.Target.Pins = append(r.Target.Pins, nil) }, codes.InvalidArgument},
		{"garbage anchor", func(r *runnerpb.DumpRequest) { r.Target.Pins[0].AnchorDer = []byte("not der") }, codes.InvalidArgument},
		{"pem anchor", func(r *runnerpb.DumpRequest) { r.Target.Pins[0].AnchorDer = f.clientCertPEM }, codes.InvalidArgument},
		{"huge anchor", func(r *runnerpb.DumpRequest) { r.Target.Pins[0].AnchorDer = make([]byte, MaxAnchorBytes+1) }, codes.InvalidArgument},
		{"short spki", func(r *runnerpb.DumpRequest) { r.Target.Pins[0].LeafSpkiSha256 = []byte{1, 2, 3} }, codes.InvalidArgument},
		{"leaf anchor without spki", func(r *runnerpb.DumpRequest) {
			r.Target.Pins = []*runnerpb.CertificatePin{{AnchorDer: f.selfSignedLeaf}}
		}, codes.InvalidArgument},
		{"leaf anchor wrong spki", func(r *runnerpb.DumpRequest) {
			r.Target.Pins = []*runnerpb.CertificatePin{{AnchorDer: f.selfSignedLeaf, LeafSpkiSha256: f.leafSPKI}}
		}, codes.InvalidArgument},
		{"pins with unverified", func(r *runnerpb.DumpRequest) { r.Target.TlsMode = runnerpb.TlsMode_TLS_MODE_UNVERIFIED }, codes.InvalidArgument},
		{"pins with disabled", func(r *runnerpb.DumpRequest) { r.Target.TlsMode = runnerpb.TlsMode_TLS_MODE_DISABLED }, codes.InvalidArgument},
		{"client cert without tls", func(r *runnerpb.DumpRequest) {
			r.Target.TlsMode = runnerpb.TlsMode_TLS_MODE_DISABLED
			r.Target.Pins = nil
			r.Target.ClientCertificate = &runnerpb.ClientCertificate{CertPem: f.clientCertPEM, KeyPem: f.clientKeyPEM}
		}, codes.InvalidArgument},
		{"client cert empty", func(r *runnerpb.DumpRequest) { r.Target.ClientCertificate = &runnerpb.ClientCertificate{} }, codes.InvalidArgument},
		{"client cert no key", func(r *runnerpb.DumpRequest) {
			r.Target.ClientCertificate = &runnerpb.ClientCertificate{CertPem: f.clientCertPEM}
		}, codes.InvalidArgument},
		{"client cert is key", func(r *runnerpb.DumpRequest) {
			r.Target.ClientCertificate = &runnerpb.ClientCertificate{CertPem: f.clientKeyPEM, KeyPem: f.clientKeyPEM}
		}, codes.InvalidArgument},
		{"client key is cert", func(r *runnerpb.DumpRequest) {
			r.Target.ClientCertificate = &runnerpb.ClientCertificate{CertPem: f.clientCertPEM, KeyPem: f.clientCertPEM}
		}, codes.InvalidArgument},
		{"client key encrypted", func(r *runnerpb.DumpRequest) {
			r.Target.ClientCertificate = &runnerpb.ClientCertificate{CertPem: f.clientCertPEM, KeyPem: f.encryptedKeyPEM}
		}, codes.InvalidArgument},
		{"client key two blocks", func(r *runnerpb.DumpRequest) {
			r.Target.ClientCertificate = &runnerpb.ClientCertificate{CertPem: f.clientCertPEM, KeyPem: append(append([]byte{}, f.clientKeyPEM...), f.clientKeyPEM...)}
		}, codes.InvalidArgument},
		{"client cert trailing garbage", func(r *runnerpb.DumpRequest) {
			r.Target.ClientCertificate = &runnerpb.ClientCertificate{CertPem: append(append([]byte{}, f.clientCertPEM...), "junk"...), KeyPem: f.clientKeyPEM}
		}, codes.InvalidArgument},
		{"empty db", func(r *runnerpb.DumpRequest) { r.Database = "" }, codes.InvalidArgument},
		{"option db", func(r *runnerpb.DumpRequest) { r.Database = "-Fp" }, codes.InvalidArgument},
		{"conninfo db", func(r *runnerpb.DumpRequest) { r.Database = "x host=evil" }, codes.InvalidArgument},
		{"uri db", func(r *runnerpb.DumpRequest) { r.Database = "postgres://evil/x" }, codes.InvalidArgument},
		{"quote db", func(r *runnerpb.DumpRequest) { r.Database = "a'b" }, codes.InvalidArgument},
		{"long db", func(r *runnerpb.DumpRequest) { r.Database = strings.Repeat("a", 64) }, codes.InvalidArgument},
		{"host not allowed", func(r *runnerpb.DumpRequest) { r.Target.Host = "10.99.0.1" }, codes.PermissionDenied},
		{"host loopback literal", func(r *runnerpb.DumpRequest) { r.Target.Host = "127.0.0.1" }, codes.PermissionDenied},
		{"host loopback mapped", func(r *runnerpb.DumpRequest) { r.Target.Host = "::ffff:127.0.0.1" }, codes.PermissionDenied},
		{"allowed name to loopback", func(r *runnerpb.DumpRequest) { r.Target.Host = "loop.example.com" }, codes.PermissionDenied},
		{"name to metadata", func(r *runnerpb.DumpRequest) { r.Target.Host = "metadata.example.com" }, codes.PermissionDenied},
		{"metadata literal", func(r *runnerpb.DumpRequest) { r.Target.Host = "169.254.169.254" }, codes.PermissionDenied},
		{"deny cidr", func(r *runnerpb.DumpRequest) { r.Target.Host = "10.20.66.1" }, codes.PermissionDenied},
		{"deny host", func(r *runnerpb.DumpRequest) { r.Target.Host = "old.example.com" }, codes.PermissionDenied},
		{"unresolvable", func(r *runnerpb.DumpRequest) { r.Target.Host = "nx.example.com" }, codes.FailedPrecondition},
	}
	v := newTestValidator(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &runnerpb.DumpRequest{Ctx: runCtx(), Target: validTarget(t), Database: "app"}
			tt.mut(req)
			_, err := v.Dump(t.Context(), req)
			if code(err) != tt.code {
				t.Fatalf("err = %v, want %v", err, tt.code)
			}
			assertNoLeak(t, req, err)
		})
	}
}

func TestTargetVariants(t *testing.T) {
	f := getFixtures(t)
	v := newTestValidator(t)
	tests := []struct {
		name string
		mut  func(*runnerpb.Target)
	}{
		{"ca pin without spki", func(tg *runnerpb.Target) { tg.Pins[0].LeafSpkiSha256 = nil }},
		{"self-signed leaf", func(tg *runnerpb.Target) {
			tg.Pins = []*runnerpb.CertificatePin{{AnchorDer: f.selfSignedLeaf, LeafSpkiSha256: f.selfSignedSPKI}}
		}},
		{"staged pin", func(tg *runnerpb.Target) {
			tg.Pins = append(tg.Pins, &runnerpb.CertificatePin{AnchorDer: f.selfSignedLeaf, LeafSpkiSha256: f.selfSignedSPKI})
		}},
		{"unverified", func(tg *runnerpb.Target) { tg.TlsMode = runnerpb.TlsMode_TLS_MODE_UNVERIFIED; tg.Pins = nil }},
		{"disabled", func(tg *runnerpb.Target) { tg.TlsMode = runnerpb.TlsMode_TLS_MODE_DISABLED; tg.Pins = nil }},
		{"client cert", func(tg *runnerpb.Target) {
			tg.ClientCertificate = &runnerpb.ClientCertificate{CertPem: f.clientCertPEM, KeyPem: append([]byte(nil), f.clientKeyPEM...)}
		}},
		{"empty password", func(tg *runnerpb.Target) { tg.Password = nil }},
		{"ip literal", func(tg *runnerpb.Target) { tg.Host = "10.20.3.4" }},
		{"allowed public name", func(tg *runnerpb.Target) { tg.Host = "public.example.com" }},
		{"mysql", func(tg *runnerpb.Target) {
			tg.Engine = runnerpb.Engine_ENGINE_MYSQL
			tg.VersionKey = "8.4"
			tg.Port = 3306
		}},
		{"mariadb", func(tg *runnerpb.Target) { tg.Engine = runnerpb.Engine_ENGINE_MARIADB; tg.VersionKey = "11.4" }},
		{"cloud user", func(tg *runnerpb.Target) { tg.User = "backup@server" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tg := validTarget(t)
			tt.mut(tg)
			req := &runnerpb.ProbeRequest{Ctx: runCtx(), Target: tg}
			got, err := v.Probe(t.Context(), req)
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if tg.ClientCertificate != nil && !got.Conn.ClientCert {
				t.Error("ClientCert not set")
			}
		})
	}
}

func TestSSHTunnel(t *testing.T) {
	f := getFixtures(t)
	v := newTestValidator(t)
	ok := []struct {
		name string
		mut  func(*runnerpb.Target)
	}{
		{"key", func(*runnerpb.Target) {}},
		{"password", func(tg *runnerpb.Target) {
			tg.SshTunnel.PrivateKeyPem = nil
			tg.SshTunnel.Password = []byte(testSSHPass)
		}},
		{"fingerprint pin", func(tg *runnerpb.Target) {
			tg.SshTunnel.HostKeyPin = &runnerpb.HostKeyPin{Type: "ssh-ed25519", FingerprintSha256: f.hostKeyFP}
		}},
		{"db on bastion loopback", func(tg *runnerpb.Target) { tg.Host = "127.0.0.1" }},
		{"db name resolved by bastion", func(tg *runnerpb.Target) { tg.Host = "db.internal.lan" }},
		{"db private literal", func(tg *runnerpb.Target) { tg.Host = "192.168.1.5" }},
	}
	for _, tt := range ok {
		t.Run(tt.name, func(t *testing.T) {
			tg := validTarget(t)
			tg.SshTunnel = validSSH(t)
			tt.mut(tg)
			got, err := v.Probe(t.Context(), &runnerpb.ProbeRequest{Ctx: runCtx(), Target: tg})
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if got.SSH == nil || got.Direct != nil || got.SSH.Bastion.Port != 22 || len(got.SSH.Bastion.Addrs) != 1 {
				t.Errorf("ssh %+v direct %+v", got.SSH, got.Direct)
			}
		})
	}
	bad := []struct {
		name string
		mut  func(*runnerpb.Target)
		code codes.Code
	}{
		{"no pin", func(tg *runnerpb.Target) { tg.SshTunnel.HostKeyPin = nil }, codes.InvalidArgument},
		{"empty pin", func(tg *runnerpb.Target) { tg.SshTunnel.HostKeyPin = &runnerpb.HostKeyPin{} }, codes.InvalidArgument},
		{"bad pin", func(tg *runnerpb.Target) {
			tg.SshTunnel.HostKeyPin = &runnerpb.HostKeyPin{Type: "ssh-dss", FingerprintSha256: f.hostKeyFP}
		}, codes.InvalidArgument},
		{"huge pin", func(tg *runnerpb.Target) {
			tg.SshTunnel.HostKeyPin = &runnerpb.HostKeyPin{AuthorizedKey: strings.Repeat("a", MaxAuthorizedKeyBytes+1)}
		}, codes.InvalidArgument},
		{"both auths", func(tg *runnerpb.Target) { tg.SshTunnel.Password = []byte(testSSHPass) }, codes.InvalidArgument},
		{"no auth", func(tg *runnerpb.Target) { tg.SshTunnel.PrivateKeyPem = nil }, codes.InvalidArgument},
		{"passphrase with password", func(tg *runnerpb.Target) {
			tg.SshTunnel.PrivateKeyPem = nil
			tg.SshTunnel.Password = []byte(testSSHPass)
			tg.SshTunnel.Passphrase = []byte("phrase-SECRET")
		}, codes.InvalidArgument},
		{"key not pem", func(tg *runnerpb.Target) { tg.SshTunnel.PrivateKeyPem = []byte("ssh-SECRET-key-material") }, codes.InvalidArgument},
		{"key is certificate", func(tg *runnerpb.Target) { tg.SshTunnel.PrivateKeyPem = f.clientCertPEM }, codes.InvalidArgument},
		{"key huge", func(tg *runnerpb.Target) { tg.SshTunnel.PrivateKeyPem = make([]byte, MaxSSHKeyBytes+1) }, codes.InvalidArgument},
		{"password huge", func(tg *runnerpb.Target) {
			tg.SshTunnel.PrivateKeyPem = nil
			tg.SshTunnel.Password = make([]byte, MaxPasswordBytes+1)
		}, codes.InvalidArgument},
		{"user option", func(tg *runnerpb.Target) { tg.SshTunnel.User = "-oProxyCommand=x" }, codes.InvalidArgument},
		{"user empty", func(tg *runnerpb.Target) { tg.SshTunnel.User = "" }, codes.InvalidArgument},
		{"user space", func(tg *runnerpb.Target) { tg.SshTunnel.User = "a b" }, codes.InvalidArgument},
		{"port 0", func(tg *runnerpb.Target) { tg.SshTunnel.Port = 0 }, codes.InvalidArgument},
		{"bastion host bad", func(tg *runnerpb.Target) { tg.SshTunnel.Host = "a b" }, codes.InvalidArgument},
		{"bastion not allowed", func(tg *runnerpb.Target) { tg.SshTunnel.Host = "192.0.2.1" }, codes.PermissionDenied},
		{"bastion loopback", func(tg *runnerpb.Target) { tg.SshTunnel.Host = "127.0.0.1" }, codes.PermissionDenied},
		{"db metadata behind bastion", func(tg *runnerpb.Target) { tg.Host = "169.254.169.254" }, codes.PermissionDenied},
		{"db mapped metadata behind bastion", func(tg *runnerpb.Target) { tg.Host = "::ffff:169.254.169.254" }, codes.PermissionDenied},
		{"db deny host behind bastion", func(tg *runnerpb.Target) { tg.Host = "old.example.com" }, codes.PermissionDenied},
		{"db deny cidr behind bastion", func(tg *runnerpb.Target) { tg.Host = "10.20.66.9" }, codes.PermissionDenied},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			tg := validTarget(t)
			tg.SshTunnel = validSSH(t)
			tt.mut(tg)
			req := &runnerpb.ProbeRequest{Ctx: runCtx(), Target: tg}
			_, err := v.Probe(t.Context(), req)
			if code(err) != tt.code {
				t.Fatalf("err = %v, want %v", err, tt.code)
			}
			assertNoLeak(t, req, err)
		})
	}
}

func TestUnknownFieldsAndSize(t *testing.T) {
	v := newTestValidator(t)
	req := &runnerpb.DumpRequest{Ctx: runCtx(), Target: validTarget(t), Database: "app"}
	// An unknown field nested in the target.
	req.Target.ProtoReflect().SetUnknown(protowire.AppendTag(nil, 99, protowire.VarintType))
	req.Target.ProtoReflect().SetUnknown(protowire.AppendVarint(req.Target.ProtoReflect().GetUnknown(), 1))
	if _, err := v.Dump(t.Context(), req); code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "unknown fields") {
		t.Fatalf("nested unknown field: %v", err)
	}
	// Unknown field in a list element (pin).
	req = &runnerpb.DumpRequest{Ctx: runCtx(), Target: validTarget(t), Database: "app"}
	req.Target.Pins[0].ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 77, protowire.VarintType), 5))
	if _, err := v.Dump(t.Context(), req); code(err) != codes.InvalidArgument {
		t.Fatalf("unknown field in pin: %v", err)
	}
	// Unknown field arriving over the wire.
	wire, err := proto.Marshal(&runnerpb.DumpRequest{Ctx: runCtx(), Target: validTarget(t), Database: "app"})
	if err != nil {
		t.Fatal(err)
	}
	wire = protowire.AppendBytes(protowire.AppendTag(wire, 1000, protowire.BytesType), []byte("x"))
	var decoded runnerpb.DumpRequest
	if err := proto.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Dump(t.Context(), &decoded); code(err) != codes.InvalidArgument {
		t.Fatalf("wire unknown field: %v", err)
	}
	// Total size.
	req = &runnerpb.DumpRequest{Ctx: runCtx(), Target: validTarget(t), Database: "app"}
	req.Target.Pins[0].AnchorDer = make([]byte, MaxRequestBytes)
	if _, err := v.Dump(t.Context(), req); code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized: %v", err)
	}
	// Nil request.
	if _, err := v.Dump(t.Context(), nil); code(err) != codes.InvalidArgument {
		t.Fatalf("nil: %v", err)
	}
}

func TestDumpGlobals(t *testing.T) {
	v := newTestValidator(t)
	if _, err := v.DumpGlobals(t.Context(), &runnerpb.DumpGlobalsRequest{Ctx: runCtx(), Target: validTarget(t)}); err != nil {
		t.Fatalf("pg: %v", err)
	}
	tg := validTarget(t)
	tg.Engine, tg.VersionKey = runnerpb.Engine_ENGINE_MYSQL, "8.4"
	if _, err := v.DumpGlobals(t.Context(), &runnerpb.DumpGlobalsRequest{Ctx: runCtx(), Target: tg}); code(err) != codes.InvalidArgument {
		t.Fatalf("mysql: %v", err)
	}
	if _, err := v.DumpGlobals(t.Context(), &runnerpb.DumpGlobalsRequest{Target: validTarget(t)}); code(err) != codes.InvalidArgument {
		t.Fatalf("no ctx: %v", err)
	}
}

func TestCreateDatabase(t *testing.T) {
	v := newTestValidator(t)
	if _, err := v.CreateDatabase(t.Context(), &runnerpb.CreateDatabaseRequest{Ctx: runCtx(), Target: validTarget(t), Database: "new_db"}); err != nil {
		t.Fatal(err)
	}
	for _, db := range []string{"", "template1", "template0", "rdsadmin", "-x", "a b"} {
		if _, err := v.CreateDatabase(t.Context(), &runnerpb.CreateDatabaseRequest{Ctx: runCtx(), Target: validTarget(t), Database: db}); code(err) != codes.InvalidArgument {
			t.Errorf("%q: %v", db, err)
		}
	}
	tg := validTarget(t)
	tg.Engine, tg.VersionKey = runnerpb.Engine_ENGINE_MYSQL, "8.4"
	for _, db := range []string{"mysql", "sys", "information_schema", "performance_schema"} {
		if _, err := v.CreateDatabase(t.Context(), &runnerpb.CreateDatabaseRequest{Ctx: runCtx(), Target: tg, Database: db}); code(err) != codes.InvalidArgument {
			t.Errorf("mysql %q: %v", db, err)
		}
	}
}

func TestRenameSwap(t *testing.T) {
	v := newTestValidator(t)
	ok := &runnerpb.RenameSwapDatabaseRequest{Ctx: runCtx(), Target: validTarget(t), Database: "app", TempId: "r1a2b3"}
	got, err := v.RenameSwapDatabase(t.Context(), ok)
	if err != nil || got.TempID != "r1a2b3" || got.Database != "app" {
		t.Fatalf("%+v %v", got, err)
	}
	bad := []func(*runnerpb.RenameSwapDatabaseRequest){
		func(r *runnerpb.RenameSwapDatabaseRequest) { r.TempId = "" },
		func(r *runnerpb.RenameSwapDatabaseRequest) { r.TempId = "UPPER" },
		func(r *runnerpb.RenameSwapDatabaseRequest) { r.TempId = strings.Repeat("a", 27) },
		func(r *runnerpb.RenameSwapDatabaseRequest) { r.Database = "template1" },
		func(r *runnerpb.RenameSwapDatabaseRequest) { r.Database = "app__bekup_old_x1" },
		func(r *runnerpb.RenameSwapDatabaseRequest) {
			r.Target.Engine, r.Target.VersionKey = runnerpb.Engine_ENGINE_MARIADB, "11.4"
		},
	}
	for i, mut := range bad {
		req := &runnerpb.RenameSwapDatabaseRequest{Ctx: runCtx(), Target: validTarget(t), Database: "app", TempId: "r1"}
		mut(req)
		if _, err := v.RenameSwapDatabase(t.Context(), req); code(err) != codes.InvalidArgument {
			t.Errorf("case %d: %v", i, err)
		}
	}
}

func TestFetchServerCertificate(t *testing.T) {
	v := newTestValidator(t)
	req := &runnerpb.FetchServerCertificateRequest{Ctx: runCtx(), Engine: runnerpb.Engine_ENGINE_MYSQL, Host: "db.example.com", Port: 3306}
	got, err := v.FetchServerCertificate(t.Context(), req)
	if err != nil || got.Direct == nil || got.Kind != engine.KindMySQL {
		t.Fatalf("%+v %v", got, err)
	}
	req.SshTunnel = validSSH(t)
	req.Host = "127.0.0.1"
	if got, err := v.FetchServerCertificate(t.Context(), req); err != nil || got.SSH == nil {
		t.Fatalf("ssh: %+v %v", got, err)
	}
	cases := []struct {
		mut  func(*runnerpb.FetchServerCertificateRequest)
		code codes.Code
	}{
		{func(r *runnerpb.FetchServerCertificateRequest) { r.Engine = 0 }, codes.InvalidArgument},
		{func(r *runnerpb.FetchServerCertificateRequest) { r.Host = "" }, codes.InvalidArgument},
		{func(r *runnerpb.FetchServerCertificateRequest) { r.Port = 0 }, codes.InvalidArgument},
		{func(r *runnerpb.FetchServerCertificateRequest) { r.Ctx = nil }, codes.InvalidArgument},
		{func(r *runnerpb.FetchServerCertificateRequest) { r.Host = "127.0.0.1" }, codes.PermissionDenied},
		{func(r *runnerpb.FetchServerCertificateRequest) { r.Host = "nx.example.com" }, codes.FailedPrecondition},
		{func(r *runnerpb.FetchServerCertificateRequest) {
			r.SshTunnel = validSSH(t)
			r.SshTunnel.HostKeyPin = nil
		}, codes.InvalidArgument},
		{func(r *runnerpb.FetchServerCertificateRequest) {
			r.SshTunnel = validSSH(t)
			r.Host = "169.254.169.254"
		}, codes.PermissionDenied},
	}
	for i, tc := range cases {
		r := &runnerpb.FetchServerCertificateRequest{Ctx: runCtx(), Engine: runnerpb.Engine_ENGINE_POSTGRES, Host: "db.example.com", Port: 5432}
		tc.mut(r)
		_, err := v.FetchServerCertificate(t.Context(), r)
		if code(err) != tc.code {
			t.Errorf("case %d: %v, want %v", i, err, tc.code)
		}
		assertNoLeak(t, r, err)
	}
}

func TestFetchHostKey(t *testing.T) {
	v := newTestValidator(t)
	got, err := v.FetchHostKey(t.Context(), &runnerpb.FetchHostKeyRequest{Ctx: runCtx(), Host: "bastion.example.com", Port: 22})
	if err != nil || got.Port != 22 {
		t.Fatalf("%+v %v", got, err)
	}
	for _, r := range []*runnerpb.FetchHostKeyRequest{
		{Host: "bastion.example.com", Port: 22},
		{Ctx: runCtx(), Host: "bastion.example.com"},
		{Ctx: runCtx(), Host: "", Port: 22},
	} {
		if _, err := v.FetchHostKey(t.Context(), r); code(err) != codes.InvalidArgument {
			t.Errorf("%v: %v", r, err)
		}
	}
	if _, err := v.FetchHostKey(t.Context(), &runnerpb.FetchHostKeyRequest{Ctx: runCtx(), Host: "169.254.169.254", Port: 22}); code(err) != codes.PermissionDenied {
		t.Errorf("metadata: %v", err)
	}
}

func TestPrePull(t *testing.T) {
	v := newTestValidator(t)
	e, err := v.PrePull(&runnerpb.PrePullRequest{Engine: runnerpb.Engine_ENGINE_MARIADB, VersionKey: "12.3"})
	if err != nil || e.Kind() != engine.KindMariaDB {
		t.Fatalf("%v %v", e, err)
	}
	for _, r := range []*runnerpb.PrePullRequest{
		{Engine: runnerpb.Engine_ENGINE_MARIADB, VersionKey: "8.4"},
		{VersionKey: "17"},
		{Engine: runnerpb.Engine_ENGINE_POSTGRES},
		{Engine: runnerpb.Engine_ENGINE_POSTGRES, VersionKey: "postgres:17@sha256:abc"},
	} {
		if _, err := v.PrePull(r); code(err) != codes.InvalidArgument {
			t.Errorf("%v: %v", r, err)
		}
	}
}

func verifyHeader(total uint64) *runnerpb.VerifyDumpRequest {
	return &runnerpb.VerifyDumpRequest{Msg: &runnerpb.VerifyDumpRequest_Header{Header: &runnerpb.VerifyDumpHeader{
		Ctx: runCtx(), Engine: runnerpb.Engine_ENGINE_POSTGRES, VersionKey: "18",
		Kind: runnerpb.DumpKind_DUMP_KIND_DATABASE, TotalBytes: total,
	}}}
}

func verifyChunk(d []byte) *runnerpb.VerifyDumpRequest {
	return &runnerpb.VerifyDumpRequest{Msg: &runnerpb.VerifyDumpRequest_Chunk{Chunk: &runnerpb.DataChunk{Data: d}}}
}

func verifyTrailer(total uint64) *runnerpb.VerifyDumpRequest {
	return &runnerpb.VerifyDumpRequest{Msg: &runnerpb.VerifyDumpRequest_Trailer{Trailer: &runnerpb.VerifyDumpTrailer{TotalBytes: total}}}
}

// TestVerifyStreamedTrailer covers the streamed mode of task 3.6: header
// total_bytes 0, chunks, then exactly one trailer with the exact total.
func TestVerifyStreamedTrailer(t *testing.T) {
	v := newTestValidator(t)
	hdr, s, err := v.VerifyHeader(verifyHeader(0))
	if err != nil || !hdr.Streamed || hdr.Total != 0 {
		t.Fatalf("%+v %v", hdr, err)
	}
	if err := s.Complete(); code(err) != codes.InvalidArgument {
		t.Fatalf("complete without trailer: %v", err)
	}
	for _, d := range []string{"abc", "de"} {
		if got, err := s.VerifyChunk(verifyChunk([]byte(d))); err != nil || string(got) != d {
			t.Fatalf("chunk %q: %q %v", d, got, err)
		}
	}
	if err := s.Complete(); code(err) != codes.InvalidArgument {
		t.Fatalf("complete before trailer: %v", err)
	}
	if _, err := s.VerifyChunk(verifyTrailer(4)); code(err) != codes.InvalidArgument {
		t.Fatalf("wrong trailer total: %v", err)
	}
	if d, err := s.VerifyChunk(verifyTrailer(5)); err != nil || d != nil {
		t.Fatalf("trailer: %q %v", d, err)
	}
	if err := s.Complete(); err != nil || s.Received() != 5 {
		t.Fatalf("complete: %v (%d)", err, s.Received())
	}
	for _, m := range []*runnerpb.VerifyDumpRequest{verifyChunk([]byte("x")), verifyTrailer(5)} {
		if _, err := s.VerifyChunk(m); code(err) != codes.InvalidArgument {
			t.Fatalf("after trailer: %v", err)
		}
	}

	// A sized stream refuses a trailer.
	_, sized, err := v.VerifyHeader(verifyHeader(3))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sized.VerifyChunk(verifyChunk([]byte("abc"))); err != nil {
		t.Fatal(err)
	}
	if _, err := sized.VerifyChunk(verifyTrailer(3)); code(err) != codes.InvalidArgument {
		t.Fatalf("trailer on a sized stream: %v", err)
	}
	// Restore streams have no trailer and still need a size.
	if _, _, err := v.VerifyHeader(verifyHeader(MaxDumpBytes)); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyStream(t *testing.T) {
	v := newTestValidator(t)
	hdr, s, err := v.VerifyHeader(verifyHeader(5))
	if err != nil || hdr.Total != 5 || hdr.Entry.Key() != "18" {
		t.Fatalf("%+v %v", hdr, err)
	}
	if err := s.Complete(); code(err) != codes.InvalidArgument {
		t.Errorf("complete before data: %v", err)
	}
	if _, err := s.VerifyChunk(verifyChunk([]byte("abc"))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyChunk(verifyChunk([]byte("def"))); code(err) != codes.InvalidArgument {
		t.Errorf("overflow: %v", err)
	}
	if _, err := s.VerifyChunk(verifyChunk(nil)); code(err) != codes.InvalidArgument {
		t.Errorf("empty chunk: %v", err)
	}
	if _, err := s.VerifyChunk(verifyHeader(5)); code(err) != codes.InvalidArgument {
		t.Errorf("second header: %v", err)
	}
	if _, err := s.VerifyChunk(&runnerpb.VerifyDumpRequest{}); code(err) != codes.InvalidArgument {
		t.Errorf("empty msg: %v", err)
	}
	if d, err := s.VerifyChunk(verifyChunk([]byte("de"))); err != nil || string(d) != "de" {
		t.Fatal(err)
	}
	if err := s.Complete(); err != nil || s.Received() != 5 {
		t.Errorf("complete: %v", err)
	}

	// Chunk limit.
	_, s, err = v.VerifyHeader(verifyHeader(MaxDumpBytes))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyChunk(verifyChunk(make([]byte, runnerpb.MaxChunkBytes+1))); code(err) != codes.InvalidArgument {
		t.Errorf("big chunk: %v", err)
	}
	if _, err := s.VerifyChunk(verifyChunk(make([]byte, runnerpb.MaxChunkBytes))); err != nil {
		t.Errorf("max chunk: %v", err)
	}

	bad := []func(*runnerpb.VerifyDumpHeader){
		func(h *runnerpb.VerifyDumpHeader) { h.TotalBytes = MaxDumpBytes + 1 },
		func(h *runnerpb.VerifyDumpHeader) { h.Kind = 0 },
		func(h *runnerpb.VerifyDumpHeader) {
			h.Engine = runnerpb.Engine_ENGINE_MYSQL
			h.VersionKey = "8.4"
			h.Kind = runnerpb.DumpKind_DUMP_KIND_GLOBALS
		},
		func(h *runnerpb.VerifyDumpHeader) { h.VersionKey = "99" },
		func(h *runnerpb.VerifyDumpHeader) { h.Ctx = nil },
	}
	for i, mut := range bad {
		r := verifyHeader(10)
		mut(r.GetHeader())
		if _, _, err := v.VerifyHeader(r); code(err) != codes.InvalidArgument {
			t.Errorf("case %d: %v", i, err)
		}
	}
	if _, _, err := v.VerifyHeader(verifyChunk([]byte("x"))); code(err) != codes.InvalidArgument {
		t.Errorf("chunk first: %v", err)
	}
}

func restoreHeader(t *testing.T) *runnerpb.RestoreRequest {
	return &runnerpb.RestoreRequest{Msg: &runnerpb.RestoreRequest_Header{Header: &runnerpb.RestoreHeader{
		Ctx: runCtx(), Target: validTarget(t), Database: "app__bekup_restore_r1",
		SourceVersionKey: "16", Kind: runnerpb.DumpKind_DUMP_KIND_DATABASE, NoOwner: true, TotalBytes: 3,
	}}}
}

func TestRestoreStream(t *testing.T) {
	v := newTestValidator(t)
	got, s, err := v.RestoreHeader(t.Context(), restoreHeader(t))
	if err != nil || got.Source.Key() != "16" || !got.NoOwner || got.Database != "app__bekup_restore_r1" {
		t.Fatalf("%+v %v", got, err)
	}
	chunk := &runnerpb.RestoreRequest{Msg: &runnerpb.RestoreRequest_Chunk{Chunk: &runnerpb.DataChunk{Data: []byte("abc")}}}
	if _, err := s.RestoreChunk(chunk); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreChunk(chunk); code(err) != codes.InvalidArgument {
		t.Errorf("overflow: %v", err)
	}
	if _, err := s.RestoreChunk(restoreHeader(t)); code(err) != codes.InvalidArgument {
		t.Errorf("second header: %v", err)
	}
	if err := s.Complete(); err != nil {
		t.Error(err)
	}

	globals := restoreHeader(t)
	globals.GetHeader().Kind = runnerpb.DumpKind_DUMP_KIND_GLOBALS
	globals.GetHeader().Database = ""
	globals.GetHeader().NoOwner = false
	if _, _, err := v.RestoreHeader(t.Context(), globals); err != nil {
		t.Fatalf("globals: %v", err)
	}
	// --no-privileges only into a test-restore scratch database.
	scratch := restoreHeader(t)
	scratch.GetHeader().Database, scratch.GetHeader().NoPrivileges = "bekup_test_0123456789ab", true
	if got, _, err := v.RestoreHeader(t.Context(), scratch); err != nil || !got.NoPrivileges {
		t.Fatalf("scratch no_privileges: %+v %v", got, err)
	}
	for _, db := range []string{"app", "app__bekup_restore_r1", "bekup_test_short"} {
		h := restoreHeader(t)
		h.GetHeader().Database, h.GetHeader().NoPrivileges = db, true
		if _, _, err := v.RestoreHeader(t.Context(), h); code(err) != codes.InvalidArgument {
			t.Errorf("no_privileges into %q: %v", db, err)
		}
	}

	bad := []func(*runnerpb.RestoreHeader){
		func(h *runnerpb.RestoreHeader) { h.SourceVersionKey = "8.4" },
		func(h *runnerpb.RestoreHeader) { h.SourceVersionKey = "" },
		func(h *runnerpb.RestoreHeader) { h.Kind = 0 },
		func(h *runnerpb.RestoreHeader) { h.Database = "" },
		func(h *runnerpb.RestoreHeader) { h.Database = "template1" },
		func(h *runnerpb.RestoreHeader) { h.Kind = runnerpb.DumpKind_DUMP_KIND_GLOBALS; h.NoOwner = false }, // database set
		func(h *runnerpb.RestoreHeader) { h.Kind = runnerpb.DumpKind_DUMP_KIND_GLOBALS; h.Database = "" },   // no_owner set
		func(h *runnerpb.RestoreHeader) { h.TotalBytes = 0 },
		func(h *runnerpb.RestoreHeader) { h.Target = nil },
		func(h *runnerpb.RestoreHeader) { h.Ctx = nil },
		func(h *runnerpb.RestoreHeader) {
			h.Target.Engine, h.Target.VersionKey, h.SourceVersionKey = runnerpb.Engine_ENGINE_MYSQL, "8.4", "8.4"
		}, // no_owner on mysql
		func(h *runnerpb.RestoreHeader) { h.Target.Host = "127.0.0.1" },
	}
	for i, mut := range bad {
		r := restoreHeader(t)
		mut(r.GetHeader())
		_, _, err := v.RestoreHeader(t.Context(), r)
		if err == nil {
			t.Errorf("case %d accepted", i)
		}
		assertNoLeak(t, r, err)
	}
	chunkFirst := &runnerpb.RestoreRequest{Msg: &runnerpb.RestoreRequest_Chunk{Chunk: &runnerpb.DataChunk{Data: []byte("x")}}}
	if _, _, err := v.RestoreHeader(t.Context(), chunkFirst); code(err) != codes.InvalidArgument {
		t.Errorf("chunk first: %v", err)
	}
}

func TestNewAndLoadCatalog(t *testing.T) {
	if _, err := New(nil, nil); err == nil {
		t.Error("New(nil) succeeded")
	}
	cfg := config.DefaultRunner()
	cat, err := LoadCatalog(&cfg)
	if err != nil || len(cat.Entries()) != 14 {
		t.Fatalf("LoadCatalog: %v", err)
	}
	cfg.Images.ExtraFile = t.TempDir() + "/missing.toml"
	if _, err := LoadCatalog(&cfg); err == nil {
		t.Error("missing extra file accepted")
	}
	v := newTestValidator(t)
	if v.Catalog() == nil || v.Guard() == nil {
		t.Error("accessors")
	}
}
