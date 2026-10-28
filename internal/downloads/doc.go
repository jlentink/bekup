// Package downloads implements secure backup downloads (task 3.9, D-150):
// single-use download tickets created by the createDownloadTicket mutation
// and redeemed by GET /api/download/{ticket}.
//
// Who: admins and the backup owners (role user) of the artifact's job,
// never maintainers (D-012, D-067). Creating a ticket needs a fresh
// step-up and is audited; redeeming it re-checks role and ownership.
//
// Tickets are 256 random bits (crypto/rand, unpadded base64url); only
// their SHA-256 is stored (download_tickets). A ticket lives 60 seconds,
// serves exactly once and only for the session that created it: any other
// session (or none) presenting it burns it. For plain downloads of
// recipient-encrypted artifacts, the age identity sent with the mutation
// is sealed into the ticket row with the keyring (AAD
// download_tickets:<id>:identity) and cleared when the ticket is redeemed
// or burned; rows are deleted shortly after expiry.
//
// Modes: raw streams the stored object unchanged; plain decrypts (bounded
// scrypt work factor, X25519 identities only) and decompresses (bounded
// zstd window) into a file ready for pg_restore (.dump) or mysql (.sql).
// Before any byte is sent, the run's signed manifest on the same
// destination is verified against the database records. While streaming,
// the stored bytes (and in plain mode the dump) are hashed; the last
// 64 KiB are held back until the digests match, so a mismatch aborts the
// connection and the client never receives a complete file. From ticket
// redemption to the end of the stream the artifact is held
// (runstate.AcquireHoldTx, renewed while streaming), so retention cannot
// delete it meanwhile (D-155).
package downloads
