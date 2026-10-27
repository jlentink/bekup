package audit

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/auditchain"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/instancekeys"
)

// Chain-head signatures (D-087, D-101).
const (
	// HeadPurpose is the signature purpose of a chain head (D-087 domain
	// separation: a manifest signature can never verify as a head).
	HeadPurpose = "audit-head"
	// HeadVersion prefixes the signed message and names the export format.
	HeadVersion = "bekup-audit-head-v1"
)

// Errors.
var (
	// ErrEmptyChain is returned by SignHead when the audit log is empty.
	ErrEmptyChain = errors.New("audit log is empty: nothing to sign")
	// ErrExportFailed wraps a HeadExporter failure; the head itself was
	// signed and stored.
	ErrExportFailed = errors.New("exporting the signed audit chain head failed")
)

// SignedHead is a signed (chain_id, chain_hash) pair: the audit_log row id
// and its hash at signing time. Its JSON form is the export format.
type SignedHead struct {
	Version   string    `json:"version"`
	ChainID   int64     `json:"chain_id"`
	ChainHash HexBytes  `json:"chain_hash"`
	SignedAt  time.Time `json:"signed_at"`
	KeyID     string    `json:"key_id"`
	PublicKey HexBytes  `json:"public_key"`
	Signature HexBytes  `json:"signature"`
}

// HexBytes marshals as lowercase hex in JSON.
type HexBytes []byte

// MarshalText implements encoding.TextMarshaler.
func (b HexBytes) MarshalText() ([]byte, error) { return []byte(hex.EncodeToString(b)), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (b *HexBytes) UnmarshalText(t []byte) error {
	v, err := hex.DecodeString(string(t))
	if err != nil {
		return fmt.Errorf("invalid hex: %w", err)
	}
	*b = v
	return nil
}

// HeadMessage returns the bytes signed for a chain head (before the D-087
// "bekup-sig-v1" 0x00 "audit-head" 0x00 prefix that crypto adds):
//
//	bekup-audit-head-v1\n
//	chain_id=<decimal>\n
//	chain_hash=<64 lowercase hex>\n
//	signed_at=<decimal microseconds since the Unix epoch>\n
func HeadMessage(chainID int64, chainHash []byte, signedAt time.Time) []byte {
	return []byte(HeadVersion + "\n" +
		"chain_id=" + strconv.FormatInt(chainID, 10) + "\n" +
		"chain_hash=" + hex.EncodeToString(chainHash) + "\n" +
		"signed_at=" + strconv.FormatInt(signedAt.UnixMicro(), 10) + "\n")
}

// VerifySignature checks h.Signature with pub, and that h.KeyID is pub's ID.
func (h *SignedHead) VerifySignature(pub ed25519.PublicKey) error {
	if h.Version != HeadVersion {
		return fmt.Errorf("unsupported head version %q", h.Version)
	}
	if len(h.ChainHash) != auditchain.HashSize || h.ChainID <= 0 {
		return errors.New("malformed chain head")
	}
	v := crypto.NewVerifier()
	id, err := v.Add(pub)
	if err != nil {
		return err
	}
	if id != h.KeyID {
		return fmt.Errorf("%w: key id %s does not match the public key (%s)", crypto.ErrSigningKey, h.KeyID, id)
	}
	return v.Verify(id, HeadPurpose, HeadMessage(h.ChainID, h.ChainHash, h.SignedAt), h.Signature)
}

// HeadExporter ships signed heads off the database host, so a rewrite of
// the database (chain and heads) is still detectable from the copy. Later
// tasks add destination and webhook exporters (tasks 3.x, 5.x).
type HeadExporter interface {
	ExportHead(ctx context.Context, h SignedHead) error
}

// NopExporter discards heads.
type NopExporter struct{}

// ExportHead implements HeadExporter.
func (NopExporter) ExportHead(context.Context, SignedHead) error { return nil }

// MultiExporter exports to every exporter and joins their errors.
type MultiExporter []HeadExporter

// ExportHead implements HeadExporter.
func (m MultiExporter) ExportHead(ctx context.Context, h SignedHead) error {
	errs := make([]error, 0, len(m))
	for _, e := range m {
		errs = append(errs, e.ExportHead(ctx, h))
	}
	return errors.Join(errs...)
}

// HeadSigner signs the audit chain head with the instance key.
type HeadSigner struct {
	db       sqlc.DBTX
	keyring  *crypto.Keyring
	auditor  *Auditor
	exporter HeadExporter
	now      func() time.Time
}

// NewHeadSigner returns a signer. auditor records the creation of the
// instance key when SignHead has to create one; exporter may be nil.
func NewHeadSigner(db sqlc.DBTX, kr *crypto.Keyring, auditor *Auditor, exporter HeadExporter) *HeadSigner {
	if exporter == nil {
		exporter = NopExporter{}
	}
	return &HeadSigner{db: db, keyring: kr, auditor: auditor, exporter: exporter, now: time.Now}
}

// SignHead signs the newest audit row's (id, hash) with the active instance
// key (creating and auditing the key first if none exists), stores the head
// in audit_chain_heads and exports it. On an export failure the stored head
// is returned with an error wrapping ErrExportFailed.
func (s *HeadSigner) SignHead(ctx context.Context) (SignedHead, error) {
	q := sqlc.New(s.db)
	key, created, err := instancekeys.Active(ctx, q, s.keyring)
	if err != nil {
		return SignedHead{}, err
	}
	defer key.Destroy()
	if created {
		if _, err := s.auditor.Record(ctx, Event{
			Actor: SystemActor(), Action: ActionInstanceKeyCreated,
			TargetType: "instance_key", TargetID: key.ID,
			Details: Details{"fingerprint": crypto.FingerprintBase64(key.Public), "purpose": HeadPurpose},
		}); err != nil {
			return SignedHead{}, err
		}
	}
	tip, err := q.GetAuditChainTip(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return SignedHead{}, ErrEmptyChain
	}
	if err != nil {
		return SignedHead{}, fmt.Errorf("read audit chain tip: %w", err)
	}
	signedAt := s.now().UTC().Truncate(time.Microsecond)
	sig, err := key.Sign(HeadPurpose, HeadMessage(tip.ID, tip.Hash, signedAt))
	if err != nil {
		return SignedHead{}, err
	}
	row, err := q.InsertAuditChainHead(ctx, sqlc.InsertAuditChainHeadParams{
		ChainID: tip.ID, ChainHash: tip.Hash, SignedAt: signedAt, KeyID: key.ID, Signature: sig,
	})
	if err != nil {
		return SignedHead{}, fmt.Errorf("store signed audit chain head: %w", err)
	}
	h := headFromRow(&row, key.Public)
	if err := s.exporter.ExportHead(ctx, h); err != nil {
		return h, fmt.Errorf("%w: %w", ErrExportFailed, err)
	}
	return h, nil
}

// SignIfChanged signs the head unless the newest stored head already covers
// the chain tip (or the chain is empty). signed reports whether it signed.
func (s *HeadSigner) SignIfChanged(ctx context.Context) (h SignedHead, signed bool, err error) {
	q := sqlc.New(s.db)
	tip, err := q.GetAuditChainTip(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return SignedHead{}, false, nil
	}
	if err != nil {
		return SignedHead{}, false, fmt.Errorf("read audit chain tip: %w", err)
	}
	last, err := q.GetLatestAuditChainHead(ctx)
	switch {
	case err == nil && last.ChainID == tip.ID:
		return SignedHead{}, false, nil
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return SignedHead{}, false, fmt.Errorf("read latest signed head: %w", err)
	}
	h, err = s.SignHead(ctx)
	return h, err == nil || errors.Is(err, ErrExportFailed), err
}

// Run signs the head every interval (when the chain changed) until ctx is
// done. Failures are logged and, for export failures, audited; they never
// stop the loop. `bekup serve` starts it (wiring in task 1.1+).
func (s *HeadSigner) Run(ctx context.Context, interval time.Duration, logger *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		h, signed, err := s.SignIfChanged(ctx)
		switch {
		case errors.Is(err, ErrExportFailed):
			logger.ErrorContext(ctx, "audit chain head export failed", slog.Int64("chain_id", h.ChainID), slog.Any("error", err))
			if _, aerr := s.auditor.Record(ctx, Event{
				Actor: SystemActor(), Action: ActionAuditHeadExportFailed,
				TargetType: "audit_chain_head", TargetID: strconv.FormatInt(h.ChainID, 10),
			}); aerr != nil {
				logger.ErrorContext(ctx, "recording the audit head export failure failed", slog.Any("error", aerr))
			}
		case err != nil:
			logger.ErrorContext(ctx, "signing the audit chain head failed", slog.Any("error", err))
		case signed:
			logger.InfoContext(ctx, "audit chain head signed", slog.Int64("chain_id", h.ChainID), slog.String("key_id", h.KeyID))
		}
	}
}

func headFromRow(r *sqlc.AuditChainHead, pub ed25519.PublicKey) SignedHead {
	return SignedHead{
		Version:   HeadVersion,
		ChainID:   r.ChainID,
		ChainHash: r.ChainHash,
		SignedAt:  r.SignedAt.UTC(),
		KeyID:     r.KeyID,
		PublicKey: HexBytes(pub),
		Signature: r.Signature,
	}
}
