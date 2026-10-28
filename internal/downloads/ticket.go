package downloads

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// ticketBytes is the entropy of a ticket (256 bits).
const ticketBytes = 32

// ticketLen is the length of an encoded ticket (unpadded base64url).
const ticketLen = 43

var ticketEncoding = base64.RawURLEncoding.Strict()

// newTicket returns a fresh ticket value and the SHA-256 that is stored.
func newTicket() (value string, hash []byte) {
	var b [ticketBytes]byte
	// crypto/rand.Read never returns an error (it panics instead).
	_, _ = rand.Read(b[:])
	value = ticketEncoding.EncodeToString(b[:])
	sum := sha256.Sum256(b[:])
	clear(b[:])
	return value, sum[:]
}

// hashTicket validates the syntax of a presented ticket (exactly 43
// canonical base64url characters) and returns the SHA-256 of its bytes.
// Anything else yields ok = false without touching the database.
func hashTicket(value string) (hash []byte, ok bool) {
	if len(value) != ticketLen {
		return nil, false
	}
	var b [ticketBytes]byte
	n, err := ticketEncoding.Decode(b[:], []byte(value))
	if err != nil || n != ticketBytes {
		return nil, false
	}
	sum := sha256.Sum256(b[:])
	clear(b[:])
	return sum[:], true
}
