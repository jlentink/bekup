package audit

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db/auditchain"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/instancekeys"
)

// DefaultVerifyBatch is the number of rows Verify reads per query (memory
// stays bounded: details are at most 64 KiB per row).
const DefaultVerifyBatch = 500

// ErrTampered is returned (wrapped) by Report.Err when verification failed.
var ErrTampered = errors.New("audit log verification failed")

// BreakKind classifies the first broken link.
type BreakKind string

// Break kinds.
const (
	// BreakGenesis: the first row does not start from 32 zero bytes (rows
	// deleted at the start, or a forged first row).
	BreakGenesis BreakKind = "genesis"
	// BreakHash: the row's contents do not match its hash (row altered).
	BreakHash BreakKind = "hash_mismatch"
	// BreakLink: the row's prev_hash is not the previous row's hash (rows
	// deleted, inserted or rewritten before it).
	BreakLink BreakKind = "link_mismatch"
)

// Break is the first broken link found.
type Break struct {
	ID     int64     `json:"id"`
	TS     time.Time `json:"ts"`
	Kind   BreakKind `json:"kind"`
	Reason string    `json:"reason"`
}

// HeadCheck is the result of checking one signed chain head.
type HeadCheck struct {
	Source   string    `json:"source"` // "database" or "file"
	ChainID  int64     `json:"chain_id"`
	KeyID    string    `json:"key_id"`
	SignedAt time.Time `json:"signed_at"`
	OK       bool      `json:"ok"`
	Problem  string    `json:"problem,omitempty"`
}

// Report is the result of Verify.
type Report struct {
	// FromID is the requested start; Partial is set when rows before it
	// exist and were not verified (the first link is anchored on the hash
	// stored in the preceding row).
	FromID   int64  `json:"from_id"`
	Partial  bool   `json:"partial"`
	Rows     int64  `json:"rows"`
	FirstID  int64  `json:"first_id,omitempty"`
	LastID   int64  `json:"last_id,omitempty"`
	LastHash string `json:"last_hash,omitempty"`
	// IDGaps counts missing ids between consecutive rows. Gaps alone are
	// harmless (a rolled-back insert consumes an id); deleted rows show up
	// as a link_mismatch break.
	IDGaps int64       `json:"id_gaps"`
	Broken *Break      `json:"broken,omitempty"`
	Heads  []HeadCheck `json:"heads"`
	// NoSignedHead is set when the database holds no signed head (tail
	// truncation cannot be detected without one).
	NoSignedHead bool `json:"no_signed_head"`
	OK           bool `json:"ok"`
}

// Err returns nil when the report is OK and an error wrapping ErrTampered
// describing the first problem otherwise.
func (r *Report) Err() error {
	if r.OK {
		return nil
	}
	if r.Broken != nil {
		return fmt.Errorf("%w: %s at id %d (%s): %s", ErrTampered, r.Broken.Kind, r.Broken.ID,
			r.Broken.TS.UTC().Format(time.RFC3339Nano), r.Broken.Reason)
	}
	for _, h := range r.Heads {
		if !h.OK {
			return fmt.Errorf("%w: signed head (%s) at id %d: %s", ErrTampered, h.Source, h.ChainID, h.Problem)
		}
	}
	return ErrTampered
}

// VerifyOptions configures Verify.
type VerifyOptions struct {
	// FromID starts verification at the first row with id >= FromID
	// (0 or 1: the whole chain, genesis included).
	FromID int64
	// BatchSize is the rows per query (default DefaultVerifyBatch).
	BatchSize int32
	// ExternalHead is an exported head (e.g. from --head-file) checked in
	// addition to the newest head stored in the database. It is verified
	// with its own embedded public key, which must also match the stored
	// key with the same id.
	ExternalHead *SignedHead
}

// Verify streams the audit log in id order, recomputes every hash
// (internal/db/auditchain), checks the prev_hash links and the genesis rule,
// and checks the newest signed chain head (and opts.ExternalHead). It stops
// at the first broken link. The error is only for operational failures;
// tampering is reported in the Report (see Report.Err).
func Verify(ctx context.Context, db sqlc.DBTX, opts VerifyOptions) (*Report, error) {
	q := sqlc.New(db)
	batch := opts.BatchSize
	if batch <= 0 {
		batch = DefaultVerifyBatch
	}
	rep := &Report{FromID: opts.FromID, Heads: []HeadCheck{}}
	prev := auditchain.GenesisPrevHash
	var after, prevID int64
	if opts.FromID > 1 {
		anchor, err := q.GetAuditLogChainAnchor(ctx, opts.FromID)
		switch {
		case err == nil:
			prev, after, prevID, rep.Partial = anchor.Hash, anchor.ID, anchor.ID, true
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("read verification anchor: %w", err)
		}
	}
	for rep.Broken == nil {
		rows, err := q.ListAuditLogCanonical(ctx, sqlc.ListAuditLogCanonicalParams{AfterID: after, MaxRows: batch})
		if err != nil {
			return nil, fmt.Errorf("read audit log after id %d: %w", after, err)
		}
		if len(rows) == 0 {
			break
		}
		for i := range rows {
			r := auditchain.FromCanonical(&rows[i])
			if b := checkRow(&r, rows[i].Hash, prev, prevID); b != nil {
				rep.Broken = b
				break
			}
			if rep.Rows == 0 {
				rep.FirstID = r.ID
			}
			if prevID != 0 && r.ID > prevID+1 {
				rep.IDGaps += r.ID - prevID - 1
			}
			rep.Rows++
			rep.LastID = r.ID
			rep.LastHash = hex.EncodeToString(rows[i].Hash)
			prev, prevID, after = rows[i].Hash, r.ID, r.ID
		}
	}

	latest, err := q.GetLatestAuditChainHead(ctx)
	switch {
	case err == nil:
		pub, perr := instancekeys.PublicKey(ctx, q, latest.KeyID)
		h := headFromRow(&latest, pub)
		hc := HeadCheck{Source: "database", ChainID: h.ChainID, KeyID: h.KeyID, SignedAt: h.SignedAt}
		if perr != nil {
			hc.Problem = "signing key not found in instance_keys"
		} else {
			hc.Problem = checkHead(ctx, q, &h)
		}
		hc.OK = hc.Problem == ""
		rep.Heads = append(rep.Heads, hc)
	case errors.Is(err, pgx.ErrNoRows):
		rep.NoSignedHead = true
	default:
		return nil, fmt.Errorf("read latest signed head: %w", err)
	}
	if h := opts.ExternalHead; h != nil {
		hc := HeadCheck{Source: "file", ChainID: h.ChainID, KeyID: h.KeyID, SignedAt: h.SignedAt}
		hc.Problem = checkExternalHead(ctx, q, h)
		if hc.Problem == "" {
			hc.Problem = checkHead(ctx, q, h)
		}
		hc.OK = hc.Problem == ""
		rep.Heads = append(rep.Heads, hc)
	}

	rep.OK = rep.Broken == nil
	for _, h := range rep.Heads {
		rep.OK = rep.OK && h.OK
	}
	return rep, nil
}

func checkRow(r *auditchain.Row, hash, prev []byte, prevID int64) *Break {
	if !bytes.Equal(auditchain.Hash(r), hash) {
		return &Break{ID: r.ID, TS: r.TS, Kind: BreakHash, Reason: "the row contents do not match its hash (row altered)"}
	}
	if bytes.Equal(r.PrevHash, prev) {
		return nil
	}
	if prevID == 0 {
		return &Break{ID: r.ID, TS: r.TS, Kind: BreakGenesis,
			Reason: "the first row does not start the chain (prev_hash is not 32 zero bytes): rows were deleted before it or it was forged"}
	}
	reason := fmt.Sprintf("prev_hash does not match the hash of row %d: rows were deleted, inserted or rewritten", prevID)
	if r.ID > prevID+1 {
		reason = fmt.Sprintf("prev_hash does not match the hash of row %d: row(s) between ids %d and %d are missing or rows were rewritten",
			prevID, prevID, r.ID)
	}
	return &Break{ID: r.ID, TS: r.TS, Kind: BreakLink, Reason: reason}
}

// checkHead verifies the signature (with h.PublicKey) and that the chain
// still holds the signed hash at the signed id. It returns "" when OK.
func checkHead(ctx context.Context, q *sqlc.Queries, h *SignedHead) string {
	if err := h.VerifySignature(ed25519.PublicKey(h.PublicKey)); err != nil {
		return "invalid signature: " + err.Error()
	}
	row, err := q.GetAuditLogCanonical(ctx, h.ChainID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "the signed row is missing from the audit log (chain truncated or rows deleted)"
	}
	if err != nil {
		return "cannot read the signed row: " + err.Error()
	}
	lr := sqlc.ListAuditLogCanonicalRow(row)
	r := auditchain.FromCanonical(&lr)
	if !bytes.Equal(auditchain.Hash(&r), row.Hash) {
		return "the signed row's contents do not match its hash (row altered)"
	}
	if !bytes.Equal(row.Hash, h.ChainHash) {
		return "the chain no longer contains the signed hash at this id (chain rewritten)"
	}
	return ""
}

// checkExternalHead requires the embedded public key to match the stored
// key with the same id (a replaced key in the database is reported).
func checkExternalHead(ctx context.Context, q *sqlc.Queries, h *SignedHead) string {
	pub, err := instancekeys.PublicKey(ctx, q, h.KeyID)
	if err != nil {
		return "signing key " + h.KeyID + " not found in instance_keys"
	}
	if !bytes.Equal(pub, h.PublicKey) {
		return "the stored public key differs from the exported one (instance key replaced)"
	}
	return ""
}
