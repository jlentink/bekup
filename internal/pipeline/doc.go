// Package pipeline is the streaming backup pipeline (task 3.1).
//
// [Run] streams one dump: counter + SHA-256 of the raw bytes → zstd →
// age encryption (passphrase with a bounded scrypt work factor, or X25519
// recipients; "none" only for an admin opt-out) → SHA-256 of the stored
// bytes → fan-out to every [Target] through a bounded per-destination
// buffer. A destination whose Put fails or that lags beyond the threshold
// is dropped (its Put is canceled, so no partial object becomes visible)
// and the others continue; the run is then "partial".
//
// [Manifest] is the signed run description (manifest.json). It is
// serialized canonically ([Canonicalize]) and signed with the ed25519
// instance key for the "manifest" purpose (D-087). [Verify] checks the
// signature and binds the manifest to the caller's database records, which
// defeats replay. [OpenArtifact] reads an artifact back with bounded
// decryption and decompression. The format is documented in
// docs/reference/artifact-format.md.
package pipeline
