// Package crypto provides the credential keyring and AES-256-GCM encryption
// with AAD, key-check values, memory zeroing, ed25519 instance (manifest
// signing) keys with key IDs, generators for passphrases, age keypairs and
// ed25519 SSH keypairs, and thin, bounded age stream helpers.
//
//   - Keyring (keyring.go, keyfile.go): master keys loaded from
//     security.master_key_file in the "bekup-keyring v1" text format; the
//     primary key encrypts, every key decrypts (D-085).
//   - Seal/Open (aead.go): ciphertext v1 = version | key id | nonce |
//     ciphertext+tag, bound to AAD "table:row_id:field"; Rotate/RotateAll
//     re-encrypt under the primary key (D-086).
//   - Key-check value (kcv.go): stored in the DB settings and verified at
//     startup.
//   - Signing keys (signing.go): ed25519 with SHA-256-derived IDs and
//     domain-separated signatures (D-087).
//   - Generators and age helpers (generate.go, age.go): D-016, D-088.
//   - Secret and Zero (zero.go): redacting wrapper and buffer zeroing, with
//     Go's limitations documented on Zero.
//
// See docs/security/key-management.md.
package crypto
