package validate

import (
	"context"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Probe validates a ProbeRequest.
func (v *Validator) Probe(ctx context.Context, req *runnerpb.ProbeRequest) (*Target, error) {
	if err := checkEnvelope(req, MaxRequestBytes); err != nil {
		return nil, err
	}
	if err := checkRunContext(req.GetCtx()); err != nil {
		return nil, err
	}
	return v.target(ctx, "target.", req.GetTarget())
}

// CertFetch is a validated FetchServerCertificateRequest.
type CertFetch struct {
	Kind engine.Kind
	// Host/Port are the target (TLS server name = Host).
	Host string
	Port int
	// Direct is set without a tunnel (resolved, allowlist-checked).
	Direct *Endpoint
	SSH    *SSH
}

// FetchServerCertificate validates a FetchServerCertificateRequest.
func (v *Validator) FetchServerCertificate(ctx context.Context, req *runnerpb.FetchServerCertificateRequest) (*CertFetch, error) {
	if err := checkEnvelope(req, MaxRequestBytes); err != nil {
		return nil, err
	}
	if err := checkRunContext(req.GetCtx()); err != nil {
		return nil, err
	}
	k, ok := kindOf(req.GetEngine())
	if !ok {
		return nil, fieldError("engine", "unknown or unspecified engine")
	}
	if err := engine.ValidateHost(req.GetHost()); err != nil {
		return nil, fieldError("host", "invalid host")
	}
	pt, err := port("port", req.GetPort())
	if err != nil {
		return nil, err
	}
	out := &CertFetch{Kind: k, Host: req.GetHost(), Port: pt}
	if st := req.GetSshTunnel(); st != nil {
		s, err := v.sshTunnel(ctx, "ssh_tunnel.", st)
		if err != nil {
			return nil, err
		}
		if err := v.behindBastion("host", req.GetHost(), s); err != nil {
			return nil, err
		}
		out.SSH = s
		return out, nil
	}
	ep, err := v.endpoint(ctx, "", req.GetHost(), req.GetPort())
	if err != nil {
		return nil, err
	}
	out.Direct = &ep
	return out, nil
}

// FetchHostKey validates a FetchHostKeyRequest: the bastion must pass the
// target allowlist.
func (v *Validator) FetchHostKey(ctx context.Context, req *runnerpb.FetchHostKeyRequest) (*Endpoint, error) {
	if err := checkEnvelope(req, MaxRequestBytes); err != nil {
		return nil, err
	}
	if err := checkRunContext(req.GetCtx()); err != nil {
		return nil, err
	}
	ep, err := v.endpoint(ctx, "", req.GetHost(), req.GetPort())
	if err != nil {
		return nil, err
	}
	return &ep, nil
}

// DatabaseOp is a validated request that targets one database.
type DatabaseOp struct {
	*Target
	Database string
	// Stats is set for a Dump with statistics enabled (task 3.10).
	Stats *Stats
}

// database validates a database name; write operations additionally
// refuse system databases (the engine builders refuse the maintenance
// database too).
func database(t *Target, name string, write bool) error {
	if name == "" {
		return fieldError("database", "required")
	}
	if err := engine.ValidateDBName(name); err != nil {
		return fieldError("database", "invalid database name")
	}
	if write && t.Engine.IsSystemDatabase(name) {
		return fieldError("database", "system databases cannot be changed")
	}
	return nil
}

// Dump validates a DumpRequest. Whether the database exists in the
// server-reported list is checked by the executor through Probe (D-033).
func (v *Validator) Dump(ctx context.Context, req *runnerpb.DumpRequest) (*DatabaseOp, error) {
	if err := checkEnvelope(req, MaxRequestBytes); err != nil {
		return nil, err
	}
	if err := checkRunContext(req.GetCtx()); err != nil {
		return nil, err
	}
	t, err := v.target(ctx, "target.", req.GetTarget())
	if err != nil {
		return nil, err
	}
	if err := database(t, req.GetDatabase(), false); err != nil {
		return nil, err
	}
	st, err := StatsOptions(req.GetStats())
	if err != nil {
		return nil, err
	}
	return &DatabaseOp{Target: t, Database: req.GetDatabase(), Stats: st}, nil
}

// DumpGlobals validates a DumpGlobalsRequest (PostgreSQL only).
func (v *Validator) DumpGlobals(ctx context.Context, req *runnerpb.DumpGlobalsRequest) (*Target, error) {
	if err := checkEnvelope(req, MaxRequestBytes); err != nil {
		return nil, err
	}
	if err := checkRunContext(req.GetCtx()); err != nil {
		return nil, err
	}
	t, err := v.target(ctx, "target.", req.GetTarget())
	if err != nil {
		return nil, err
	}
	if t.Kind != engine.KindPostgres {
		return nil, fieldError("target.engine", "globals are PostgreSQL only")
	}
	return t, nil
}

// CreateDatabase validates a CreateDatabaseRequest.
func (v *Validator) CreateDatabase(ctx context.Context, req *runnerpb.CreateDatabaseRequest) (*DatabaseOp, error) {
	if err := checkEnvelope(req, MaxRequestBytes); err != nil {
		return nil, err
	}
	if err := checkRunContext(req.GetCtx()); err != nil {
		return nil, err
	}
	t, err := v.target(ctx, "target.", req.GetTarget())
	if err != nil {
		return nil, err
	}
	if err := database(t, req.GetDatabase(), true); err != nil {
		return nil, err
	}
	return &DatabaseOp{Target: t, Database: req.GetDatabase()}, nil
}

// Swap is a validated RenameSwapDatabaseRequest.
type Swap struct {
	*Target
	Database string
	TempID   string
}

// RenameSwapDatabase validates a RenameSwapDatabaseRequest (PostgreSQL
// only).
func (v *Validator) RenameSwapDatabase(ctx context.Context, req *runnerpb.RenameSwapDatabaseRequest) (*Swap, error) {
	if err := checkEnvelope(req, MaxRequestBytes); err != nil {
		return nil, err
	}
	if err := checkRunContext(req.GetCtx()); err != nil {
		return nil, err
	}
	t, err := v.target(ctx, "target.", req.GetTarget())
	if err != nil {
		return nil, err
	}
	if t.Kind != engine.KindPostgres {
		return nil, fieldError("target.engine", "rename swap is PostgreSQL only")
	}
	if err := database(t, req.GetDatabase(), true); err != nil {
		return nil, err
	}
	if engine.IsBekupTempDatabase(req.GetDatabase()) {
		return nil, fieldError("database", "must not be a bekup temporary database")
	}
	if err := engine.ValidateRestoreID(req.GetTempId()); err != nil {
		return nil, fieldError("temp_id", "must be [a-z0-9]{1,26}")
	}
	return &Swap{Target: t, Database: req.GetDatabase(), TempID: req.GetTempId()}, nil
}

// PrePull validates a PrePullRequest and returns the allowlisted entry.
func (v *Validator) PrePull(req *runnerpb.PrePullRequest) (engine.Entry, error) {
	if err := checkEnvelope(req, MaxRequestBytes); err != nil {
		return engine.Entry{}, err
	}
	_, _, ent, err := v.entry("", req.GetEngine(), req.GetVersionKey())
	return ent, err
}

// ListImages validates a ListImagesRequest (no fields; size and unknown
// fields are checked like every request).
func (v *Validator) ListImages(req *runnerpb.ListImagesRequest) error {
	return checkEnvelope(req, MaxRequestBytes)
}

// dumpKind maps the proto dump kind; globals exist for PostgreSQL only.
func dumpKind(field string, k engine.Kind, d runnerpb.DumpKind) error {
	switch d {
	case runnerpb.DumpKind_DUMP_KIND_DATABASE:
		return nil
	case runnerpb.DumpKind_DUMP_KIND_GLOBALS:
		if k != engine.KindPostgres {
			return fieldError(field, "globals are PostgreSQL only")
		}
		return nil
	default:
		return fieldError(field, "unknown or unspecified dump kind")
	}
}

func totalBytes(field string, n uint64) error {
	if n == 0 || n > MaxDumpBytes {
		return fieldError(field, "must be 1 byte to 64 TiB")
	}
	return nil
}

// Verify is a validated VerifyDumpHeader.
type Verify struct {
	Kind  engine.Kind
	Entry engine.Entry
	Dump  runnerpb.DumpKind
	// Total is the announced size (0 for a streamed dump).
	Total uint64
	// Streamed: the header announced no size; a trailer ends the stream.
	Streamed bool
}

// VerifyHeader validates the first message of a VerifyDump stream; it
// must be a header. total_bytes 0 announces a streamed dump whose chunks
// end with a VerifyDumpTrailer (at most MaxDumpBytes in total).
func (v *Validator) VerifyHeader(req *runnerpb.VerifyDumpRequest) (*Verify, *Stream, error) {
	if err := checkEnvelope(req, MaxRequestBytes); err != nil {
		return nil, nil, err
	}
	h := req.GetHeader()
	if h == nil {
		return nil, nil, fieldError("header", "the first message must be the header")
	}
	if err := checkRunContext(h.GetCtx()); err != nil {
		return nil, nil, err
	}
	k, _, ent, err := v.entry("header.", h.GetEngine(), h.GetVersionKey())
	if err != nil {
		return nil, nil, err
	}
	if err := dumpKind("header.kind", k, h.GetKind()); err != nil {
		return nil, nil, err
	}
	// total_bytes 0 announces a streamed dump (task 3.6): its size is
	// checked against the trailer that must end the stream.
	if h.GetTotalBytes() == 0 {
		return &Verify{Kind: k, Entry: ent, Dump: h.GetKind(), Streamed: true},
			&Stream{total: MaxDumpBytes, streamed: true}, nil
	}
	if err := totalBytes("header.total_bytes", h.GetTotalBytes()); err != nil {
		return nil, nil, err
	}
	return &Verify{Kind: k, Entry: ent, Dump: h.GetKind(), Total: h.GetTotalBytes()},
		&Stream{total: h.GetTotalBytes()}, nil
}

// Restore is a validated RestoreHeader.
type Restore struct {
	*Target
	// Database is empty for a globals restore.
	Database string
	// Source is the catalog entry the dump was taken with.
	Source  engine.Entry
	Dump    runnerpb.DumpKind
	NoOwner bool
	Total   uint64
}

// RestoreHeader validates the first message of a Restore stream; it must
// be a header.
func (v *Validator) RestoreHeader(ctx context.Context, req *runnerpb.RestoreRequest) (*Restore, *Stream, error) {
	if err := checkEnvelope(req, MaxRequestBytes); err != nil {
		return nil, nil, err
	}
	h := req.GetHeader()
	if h == nil {
		return nil, nil, fieldError("header", "the first message must be the header")
	}
	if err := checkRunContext(h.GetCtx()); err != nil {
		return nil, nil, err
	}
	t, err := v.target(ctx, "header.target.", h.GetTarget())
	if err != nil {
		return nil, nil, err
	}
	src, ok := v.cat.Lookup(t.Kind, h.GetSourceVersionKey())
	if !ok || len(h.GetSourceVersionKey()) > maxVersionKey {
		return nil, nil, fieldError("header.source_version_key", "not in the runner's image allowlist")
	}
	if err := dumpKind("header.kind", t.Kind, h.GetKind()); err != nil {
		return nil, nil, err
	}
	if h.GetNoOwner() && (t.Kind != engine.KindPostgres || h.GetKind() != runnerpb.DumpKind_DUMP_KIND_DATABASE) {
		return nil, nil, fieldError("header.no_owner", "only for PostgreSQL database restores")
	}
	switch h.GetKind() {
	case runnerpb.DumpKind_DUMP_KIND_GLOBALS:
		if h.GetDatabase() != "" {
			return nil, nil, fieldError("header.database", "must be empty for a globals restore")
		}
	default:
		if err := database(t, h.GetDatabase(), true); err != nil {
			return nil, nil, fieldError("header.database", "invalid, missing or a system database")
		}
	}
	if err := totalBytes("header.total_bytes", h.GetTotalBytes()); err != nil {
		return nil, nil, err
	}
	return &Restore{
		Target: t, Database: h.GetDatabase(), Source: src, Dump: h.GetKind(),
		NoOwner: h.GetNoOwner(), Total: h.GetTotalBytes(),
	}, &Stream{total: h.GetTotalBytes()}, nil
}
