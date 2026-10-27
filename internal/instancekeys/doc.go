// Package instancekeys loads and creates the ed25519 instance signing key
// stored in the instance_keys table (D-038, D-087, D-101).
//
// The table holds one active key (retired_at IS NULL, enforced by a unique
// partial index) and every retired key, whose public key stays available so
// old signatures (audit chain heads, later backup manifests) keep verifying.
// The private seed is sealed with the master keyring under AAD
// "instance_keys:<id>:private" (crypto.SigningKey.SealPrivate).
//
// Minimal by design (task 1.3): Active loads or lazily creates the active
// key and Verifier loads every public key. Key rotation (retire + create)
// and re-sealing after a master key rotation are left to later tasks.
package instancekeys
