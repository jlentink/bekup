// Package auditchain recomputes the audit_log hash chain in Go, byte for byte
// identical to the audit_log_chain trigger (migration 00004). It is used by
// `bekup audit verify` (task 1.3) and the integration tests.
//
// Canonical serialization v1 (docs/security/audit.md):
//
//	"bekup-audit-v1" || F(prev_hash) || F(id) || F(ts) || F(actor_user_id)
//	|| F(actor_kind) || F(action) || F(target_type) || F(target_id) || F(ip)
//	|| F(details)
//
// F(NULL) = 0x00; F(v) = 0x01 || uint32 big-endian len(v) || v. Values:
// prev_hash raw bytes; id in decimal; ts as decimal microseconds since the
// Unix epoch; actor_user_id as a lowercase hyphenated UUID; ip as Postgres
// host(ip); details as Postgres jsonb text output (details::text); text is
// UTF-8. hash = SHA-256(canonical). The first row's prev_hash is 32 zero
// bytes.
package auditchain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Domain is the version prefix of the canonical serialization.
const Domain = "bekup-audit-v1"

// HashSize is the size of prev_hash and hash.
const HashSize = sha256.Size

// GenesisPrevHash is the prev_hash of the first audit row.
var GenesisPrevHash = make([]byte, HashSize)

// ErrChainBroken reports a row whose prev_hash or hash does not match.
var ErrChainBroken = errors.New("audit chain broken")

// Row holds the hashed fields of an audit_log row in their canonical form.
type Row struct {
	ID          int64
	TS          time.Time
	ActorUserID *uuid.UUID
	ActorKind   string
	Action      string
	TargetType  *string
	TargetID    *string
	// IP is Postgres host(ip), nil when ip is NULL.
	IP *string
	// Details is the jsonb text output (details::text) exactly as returned by
	// Postgres; never re-encode it.
	Details  string
	PrevHash []byte
}

// FromCanonical converts a ListAuditLogCanonical row.
func FromCanonical(r *sqlc.ListAuditLogCanonicalRow) Row {
	row := Row{
		ID:          r.ID,
		TS:          r.Ts,
		ActorUserID: r.ActorUserID,
		ActorKind:   r.ActorKind,
		Action:      r.Action,
		TargetType:  r.TargetType,
		TargetID:    r.TargetID,
		Details:     r.DetailsText,
		PrevHash:    r.PrevHash,
	}
	if r.HasIp {
		ip := r.IpText
		row.IP = &ip
	}
	return row
}

// Canonical returns the canonical serialization of r.
func Canonical(r *Row) []byte {
	var b bytes.Buffer
	b.WriteString(Domain)
	field(&b, r.PrevHash, true)
	field(&b, []byte(strconv.FormatInt(r.ID, 10)), true)
	field(&b, []byte(strconv.FormatInt(r.TS.UnixMicro(), 10)), true)
	if r.ActorUserID != nil {
		field(&b, []byte(r.ActorUserID.String()), true)
	} else {
		field(&b, nil, false)
	}
	field(&b, []byte(r.ActorKind), true)
	field(&b, []byte(r.Action), true)
	optField(&b, r.TargetType)
	optField(&b, r.TargetID)
	optField(&b, r.IP)
	field(&b, []byte(r.Details), true)
	return b.Bytes()
}

// Hash returns SHA-256 of the canonical serialization.
func Hash(r *Row) []byte {
	sum := sha256.Sum256(Canonical(r))
	return sum[:]
}

// Verify checks one link: r.PrevHash must equal prev (GenesisPrevHash for
// the first row) and hash must equal Hash(r).
func Verify(r *Row, prev, hash []byte) error {
	if !bytes.Equal(r.PrevHash, prev) {
		return fmt.Errorf("%w at id %d: prev_hash does not match the previous row", ErrChainBroken, r.ID)
	}
	if !bytes.Equal(Hash(r), hash) {
		return fmt.Errorf("%w at id %d: hash does not match the row contents", ErrChainBroken, r.ID)
	}
	return nil
}

func optField(b *bytes.Buffer, s *string) {
	if s == nil {
		field(b, nil, false)
		return
	}
	field(b, []byte(*s), true)
}

func field(b *bytes.Buffer, v []byte, present bool) {
	if !present {
		b.WriteByte(0)
		return
	}
	b.WriteByte(1)
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(v))) //nolint:gosec // G115: values are far below 4 GiB (DB CHECK limits)
	b.Write(n[:])
	b.Write(v)
}
