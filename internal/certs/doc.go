// Package certs manages the private CA and the per-component ed25519
// certificates used for runner mTLS (`bekup certs init|rotate|inspect`),
// exposes certificate expiry inspection (ExpiryInfo, for metrics/alerts) and
// builds the TLS 1.3 configurations for the runner (ServerTLSConfig) and its
// clients (ClientTLSConfig).
//
// Identity scheme: clients (serve, worker) are identified by exactly one URI
// SAN spiffe://bekup/<component> and carry only the clientAuth EKU; the
// runner's certificate carries DNS SANs (default "runner") and only the
// serverAuth EKU. The CA is name-constrained to the runner's names and the
// bekup URI trust domain, has path length 0, and its key is age-encrypted
// with a passphrase by default.
//
// Each component directory holds only that component's key, certificate and
// CA trust bundle; the CA key lives in <dir>/ca/ca.key and belongs offline.
// See docs/security/mtls-certs.md.
package certs
