// Package runnerclient is the mTLS gRPC client used by serve and the workers
// to call the bekup runner (D-127): TLS 1.3 against the private CA only, the
// runner's DNS SAN, no proxy, no transparent retries (Health alone is retried
// on Unavailable), keepalive within the runner's enforcement policy, default
// deadlines for unary calls and mandatory deadlines for streams.
package runnerclient
