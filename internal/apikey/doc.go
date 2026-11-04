// Package apikey is the one implementation of the external API's token
// format and hash (D-220 item 8): bekup's side creates tokens with
// Generate (token management, internal/apiaccess) and `bekup external-api`
// verifies presented ones with Parse and Key.Hash (internal/externalapi).
//
// A token reads bkp_<id>_<secret>: the fixed prefix lets secret scanners
// recognize it, <id> is the token's uuid (16 bytes) and <secret> 256 bits
// from crypto/rand, both in unpadded lower-case RFC 4648 base32 (26 and 52
// characters, 83 in total). Only the canonical encoding parses (no upper
// case, no padding, no stray trailing bits), so one token has exactly one
// spelling. The database stores SHA-256 over "bekup-api-token-v1\x00" ‖ id
// (16 bytes) ‖ secret (32 bytes); no master key is involved, a 256-bit
// random secret needs no key stretching.
//
// It is a leaf package (standard library and uuid only).
package apikey
