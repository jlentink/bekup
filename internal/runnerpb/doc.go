// Package runnerpb holds the Go code generated from proto/runner/v1 (`just
// gen`, buf + protoc-gen-go + protoc-gen-go-grpc; never edit *.pb.go) plus a
// few hand-written helpers shared by the runner and its clients: the stream
// limits and ZeroSecrets, which zeroes every field annotated with
// [(runner.v1.secret) = true] (D-126).
package runnerpb
