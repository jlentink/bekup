// Package offline implements the recovery tooling that works without the
// bekup database (task 4.7, D-164): verifying a run's signed manifest and
// its artifacts against escrowed instance public keys, decrypting and
// decompressing an artifact to a ready-to-use dump with a passphrase (or a
// passphrase export) or an age identity, and re-indexing a destination:
// listing its runs, verifying them and, with database access, importing
// them as runs of an existing job.
//
// Everything here reads untrusted bytes: manifests are parsed strictly and
// only trusted after the ed25519 signature verified; artifact digests are
// computed while streaming with size bounds; age decryption uses the
// bounded helpers of internal/crypto and zstd the window cap of
// internal/pipeline. Text from manifests is escaped before it reaches a
// terminal (SafeText).
//
// The pieces are reusable: CheckArtifact / Decrypt work on any io.Reader,
// and Source abstracts a run directory (a local directory or a
// storage.Destination).
package offline
