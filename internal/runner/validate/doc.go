// Package validate checks every runner RPC request before anything else
// happens (D-033, D-130). A validator either returns a typed, validated view
// of the request or a gRPC status error:
//
//   - codes.InvalidArgument for malformed requests (unknown fields, size
//     limits, unknown engine/version, bad names, malformed pins/keys);
//   - codes.PermissionDenied when the target allowlist denies a host;
//   - codes.FailedPrecondition when a host cannot be resolved.
//
// Messages are static and name the field only; they never echo request
// values (and never secrets). The validated views do not copy secret
// fields: the executor reads them from the request and zeroes the request
// afterwards (runnerpb.ZeroSecrets).
package validate
