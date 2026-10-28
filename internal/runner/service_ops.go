package runner

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// The task 2.7 RPCs. Each handler zeroes the request's secret fields when
// it returns (the interceptor does too) and delegates to operations.

var errOpsUnavailable = status.Error(codes.Unimplemented, "this RPC is not available")

// Probe tests a connection, detects the server version and lists the
// databases with the Go driver inside the runner.
func (s *service) Probe(ctx context.Context, req *runnerpb.ProbeRequest) (*runnerpb.ProbeResponse, error) {
	defer runnerpb.ZeroSecrets(req)
	if s.ops == nil {
		return nil, errOpsUnavailable
	}
	return s.ops.probe(ctx, req)
}

// Dump streams a database dump.
func (s *service) Dump(req *runnerpb.DumpRequest, stream grpc.ServerStreamingServer[runnerpb.DumpResponse]) error {
	defer runnerpb.ZeroSecrets(req)
	if s.ops == nil {
		return errOpsUnavailable
	}
	return s.ops.dump(stream.Context(), req, stream.Send)
}

// DumpGlobals streams the PostgreSQL globals.
func (s *service) DumpGlobals(req *runnerpb.DumpGlobalsRequest, stream grpc.ServerStreamingServer[runnerpb.DumpGlobalsResponse]) error {
	defer runnerpb.ZeroSecrets(req)
	if s.ops == nil {
		return errOpsUnavailable
	}
	return s.ops.dumpGlobals(stream.Context(), req, stream.Send)
}

// VerifyDump checks a streamed dump.
func (s *service) VerifyDump(stream grpc.ClientStreamingServer[runnerpb.VerifyDumpRequest, runnerpb.VerifyDumpResponse]) error {
	if s.ops == nil {
		return errOpsUnavailable
	}
	resp, err := s.ops.verifyDump(stream)
	if err != nil {
		return err
	}
	return stream.SendAndClose(resp)
}

// Restore feeds a streamed dump into a target.
func (s *service) Restore(stream grpc.BidiStreamingServer[runnerpb.RestoreRequest, runnerpb.RestoreResponse]) error {
	if s.ops == nil {
		return errOpsUnavailable
	}
	return s.ops.restore(stream)
}

// CreateDatabase creates an empty database.
func (s *service) CreateDatabase(ctx context.Context, req *runnerpb.CreateDatabaseRequest) (*runnerpb.CreateDatabaseResponse, error) {
	defer runnerpb.ZeroSecrets(req)
	if s.ops == nil {
		return nil, errOpsUnavailable
	}
	return s.ops.createDatabase(ctx, req)
}

// RenameSwapDatabase swaps a PostgreSQL database with its restored
// temporary database.
func (s *service) RenameSwapDatabase(ctx context.Context, req *runnerpb.RenameSwapDatabaseRequest) (*runnerpb.RenameSwapDatabaseResponse, error) {
	defer runnerpb.ZeroSecrets(req)
	if s.ops == nil {
		return nil, errOpsUnavailable
	}
	return s.ops.renameSwap(ctx, req)
}

// DropTempDatabase drops a bekup temporary database of a restore (the
// name is derived from temp_id).
func (s *service) DropTempDatabase(ctx context.Context, req *runnerpb.DropTempDatabaseRequest) (*runnerpb.DropTempDatabaseResponse, error) {
	defer runnerpb.ZeroSecrets(req)
	if s.ops == nil {
		return nil, errOpsUnavailable
	}
	return s.ops.dropTemp(ctx, req)
}

// PrePull pulls an allowlisted image by digest.
func (s *service) PrePull(ctx context.Context, req *runnerpb.PrePullRequest) (*runnerpb.PrePullResponse, error) {
	if s.ops == nil {
		return nil, errOpsUnavailable
	}
	return s.ops.prePull(ctx, req)
}

// FetchHostKey captures an SSH bastion's host key for TOFU confirmation.
func (s *service) FetchHostKey(ctx context.Context, req *runnerpb.FetchHostKeyRequest) (*runnerpb.FetchHostKeyResponse, error) {
	if s.ops == nil {
		return nil, errOpsUnavailable
	}
	return s.ops.fetchHostKey(ctx, req)
}

// ListImages returns the merged image allowlist.
func (s *service) ListImages(ctx context.Context, req *runnerpb.ListImagesRequest) (*runnerpb.ListImagesResponse, error) {
	if s.ops == nil {
		return nil, errOpsUnavailable
	}
	return s.ops.listImages(ctx, req)
}
