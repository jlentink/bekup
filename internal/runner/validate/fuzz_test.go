package validate

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// fuzzRequest decodes arbitrary bytes into a fresh message of seed's type
// and runs check on it. Invariants: no panic, every error is a gRPC status
// with an allowed code, and no error message contains a secret value of
// the request.
func fuzzRequest[M proto.Message](f *testing.F, seeds []M, newMsg func() M, check func(*Validator, M) error) {
	f.Helper()
	for _, s := range seeds {
		b, err := proto.Marshal(s)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte{})
	f.Add([]byte{0x0a, 0x00})
	v := newTestValidator(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		m := newMsg()
		if err := proto.Unmarshal(data, m); err != nil {
			return
		}
		err := check(v, m)
		if err == nil {
			return
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Fatalf("not a status error: %v", err)
		}
		switch st.Code() {
		case codes.InvalidArgument, codes.PermissionDenied, codes.FailedPrecondition:
		default:
			t.Fatalf("unexpected code %v: %v", st.Code(), err)
		}
		assertNoLeakMin(t, m, err, 24)
	})
}

func FuzzProbe(f *testing.F) {
	tg := validTarget(f)
	ssh := validTarget(f)
	ssh.SshTunnel = validSSH(f)
	fuzzRequest(f,
		[]*runnerpb.ProbeRequest{{Ctx: runCtx(), Target: tg}, {Ctx: runCtx(), Target: ssh}},
		func() *runnerpb.ProbeRequest { return &runnerpb.ProbeRequest{} },
		func(v *Validator, m *runnerpb.ProbeRequest) error {
			_, err := v.Probe(context.Background(), m)
			return err
		})
}

func FuzzDump(f *testing.F) {
	fuzzRequest(f,
		[]*runnerpb.DumpRequest{{Ctx: runCtx(), Target: validTarget(f), Database: "app"}},
		func() *runnerpb.DumpRequest { return &runnerpb.DumpRequest{} },
		func(v *Validator, m *runnerpb.DumpRequest) error {
			_, err := v.Dump(context.Background(), m)
			return err
		})
}

func FuzzDumpGlobals(f *testing.F) {
	fuzzRequest(f,
		[]*runnerpb.DumpGlobalsRequest{{Ctx: runCtx(), Target: validTarget(f)}},
		func() *runnerpb.DumpGlobalsRequest { return &runnerpb.DumpGlobalsRequest{} },
		func(v *Validator, m *runnerpb.DumpGlobalsRequest) error {
			_, err := v.DumpGlobals(context.Background(), m)
			return err
		})
}

func FuzzCreateDatabase(f *testing.F) {
	fuzzRequest(f,
		[]*runnerpb.CreateDatabaseRequest{{Ctx: runCtx(), Target: validTarget(f), Database: "new"}},
		func() *runnerpb.CreateDatabaseRequest { return &runnerpb.CreateDatabaseRequest{} },
		func(v *Validator, m *runnerpb.CreateDatabaseRequest) error {
			_, err := v.CreateDatabase(context.Background(), m)
			return err
		})
}

func FuzzRenameSwapDatabase(f *testing.F) {
	fuzzRequest(f,
		[]*runnerpb.RenameSwapDatabaseRequest{{Ctx: runCtx(), Target: validTarget(f), Database: "app", TempId: "r1"}},
		func() *runnerpb.RenameSwapDatabaseRequest { return &runnerpb.RenameSwapDatabaseRequest{} },
		func(v *Validator, m *runnerpb.RenameSwapDatabaseRequest) error {
			_, err := v.RenameSwapDatabase(context.Background(), m)
			return err
		})
}

func FuzzFetchServerCertificate(f *testing.F) {
	fuzzRequest(f,
		[]*runnerpb.FetchServerCertificateRequest{
			{Ctx: runCtx(), Engine: runnerpb.Engine_ENGINE_POSTGRES, Host: "db.example.com", Port: 5432},
			{Ctx: runCtx(), Engine: runnerpb.Engine_ENGINE_MYSQL, Host: "127.0.0.1", Port: 3306, SshTunnel: validSSH(f)},
		},
		func() *runnerpb.FetchServerCertificateRequest { return &runnerpb.FetchServerCertificateRequest{} },
		func(v *Validator, m *runnerpb.FetchServerCertificateRequest) error {
			_, err := v.FetchServerCertificate(context.Background(), m)
			return err
		})
}

func FuzzFetchHostKey(f *testing.F) {
	fuzzRequest(f,
		[]*runnerpb.FetchHostKeyRequest{{Ctx: runCtx(), Host: "bastion.example.com", Port: 22}},
		func() *runnerpb.FetchHostKeyRequest { return &runnerpb.FetchHostKeyRequest{} },
		func(v *Validator, m *runnerpb.FetchHostKeyRequest) error {
			_, err := v.FetchHostKey(context.Background(), m)
			return err
		})
}

func FuzzPrePull(f *testing.F) {
	fuzzRequest(f,
		[]*runnerpb.PrePullRequest{{Engine: runnerpb.Engine_ENGINE_POSTGRES, VersionKey: "17"}},
		func() *runnerpb.PrePullRequest { return &runnerpb.PrePullRequest{} },
		func(v *Validator, m *runnerpb.PrePullRequest) error { _, err := v.PrePull(m); return err })
}

// FuzzVerifyDump feeds a header followed by one fuzzed message.
func FuzzVerifyDump(f *testing.F) {
	fuzzRequest(f,
		[]*runnerpb.VerifyDumpRequest{verifyHeader(4), verifyChunk([]byte("abcd"))},
		func() *runnerpb.VerifyDumpRequest { return &runnerpb.VerifyDumpRequest{} },
		func(v *Validator, m *runnerpb.VerifyDumpRequest) error {
			if _, _, err := v.VerifyHeader(m); err == nil {
				return nil
			}
			_, s, err := v.VerifyHeader(verifyHeader(4))
			if err != nil {
				return err
			}
			if _, err := s.VerifyChunk(m); err != nil {
				return err
			}
			if s.Received() > 4 {
				panic("stream accepted more than total_bytes")
			}
			return nil
		})
}

// FuzzRestore feeds a header and, if it is not a valid header, uses it as
// the first chunk after a valid one.
func FuzzRestore(f *testing.F) {
	hdr := &runnerpb.RestoreRequest{Msg: &runnerpb.RestoreRequest_Header{Header: &runnerpb.RestoreHeader{
		Ctx: runCtx(), Target: validTarget(f), Database: "app", SourceVersionKey: "17",
		Kind: runnerpb.DumpKind_DUMP_KIND_DATABASE, TotalBytes: 4,
	}}}
	chunk := &runnerpb.RestoreRequest{Msg: &runnerpb.RestoreRequest_Chunk{Chunk: &runnerpb.DataChunk{Data: []byte("abcd")}}}
	fuzzRequest(f,
		[]*runnerpb.RestoreRequest{hdr, chunk},
		func() *runnerpb.RestoreRequest { return &runnerpb.RestoreRequest{} },
		func(v *Validator, m *runnerpb.RestoreRequest) error {
			if _, _, err := v.RestoreHeader(context.Background(), m); err == nil {
				return nil
			}
			_, s, err := v.RestoreHeader(context.Background(), hdr)
			if err != nil {
				return err
			}
			if _, err := s.RestoreChunk(m); err != nil {
				return err
			}
			if s.Received() > 4 {
				panic("stream accepted more than total_bytes")
			}
			return nil
		})
}
