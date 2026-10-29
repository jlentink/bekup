package audit

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Audit chain status (D-196): the latest result of the periodic chain
// verification (the security check of internal/monitor, D-190) is kept in
// settings under VerificationSetting, so admins see when the chain was
// last verified and with what outcome. The row is informational: anyone
// who can write the app database can also forge it, so the proof stays
// `bekup audit verify` against an exported signed head (D-101).

// VerificationSetting is the settings key of the latest verification.
const VerificationSetting = "audit.chain_verification"

// findingRe bounds the stored finding to the fixed "<problem>:<row id>"
// form the checker writes.
var findingRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}:\d{1,19}$`)

// Verification is the outcome of one completed chain verification.
type Verification struct {
	// CheckedAt is when it ran.
	CheckedAt time.Time `json:"checked_at"`
	// OK is false when a finding was recorded (audit.verify.failed).
	OK bool `json:"ok"`
	// Full is set when the whole chain was verified (otherwise only the
	// rows after the last verified one, anchored on its hash).
	Full bool `json:"full"`
	// Finding is "<problem>:<row id>" when OK is false.
	Finding string `json:"finding,omitempty"`
	// VerifiedThroughID is the newest row known to verify (0: none yet).
	VerifiedThroughID int64 `json:"verified_through_id"`
}

type verificationJSON struct {
	Version int `json:"version"`
	Verification
}

// SaveVerification stores v as the latest verification (system write, no
// actor).
func SaveVerification(ctx context.Context, q *sqlc.Queries, v Verification) error {
	if v.CheckedAt.IsZero() || v.VerifiedThroughID < 0 || (v.Finding != "" && !findingRe.MatchString(v.Finding)) || v.OK != (v.Finding == "") {
		return fmt.Errorf("%w: verification result", ErrInvalidEvent)
	}
	v.CheckedAt = v.CheckedAt.UTC()
	raw, err := json.Marshal(verificationJSON{Version: 1, Verification: v})
	if err != nil {
		return fmt.Errorf("audit: encode verification: %w", err)
	}
	if _, err := q.PutSetting(ctx, sqlc.PutSettingParams{Key: VerificationSetting, Value: raw}); err != nil {
		return fmt.Errorf("audit: store verification: %w", err)
	}
	return nil
}

// ChainStatus is what Query.auditChainStatus reports.
type ChainStatus struct {
	// LastVerification is the latest stored verification (nil: none ran
	// yet or the stored value is unreadable).
	LastVerification *Verification
	// HeadID, HeadHash (hex) and HeadAt describe the newest audit row
	// (zero when the log is empty).
	HeadID   int64
	HeadHash string
	HeadAt   time.Time
	// SignedHead is the newest signed chain head (nil: none yet).
	SignedHead *SignedHeadInfo
}

// SignedHeadInfo describes a signed chain head.
type SignedHeadInfo struct {
	ChainID  int64
	SignedAt time.Time
	KeyID    string
}

// LoadChainStatus reads the chain status.
func LoadChainStatus(ctx context.Context, db sqlc.DBTX) (ChainStatus, error) {
	q := sqlc.New(db)
	var st ChainStatus
	row, err := q.GetSetting(ctx, VerificationSetting)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return ChainStatus{}, fmt.Errorf("audit: read verification: %w", err)
	default:
		var j verificationJSON
		if json.Unmarshal(row.Value, &j) == nil && j.Version == 1 && !j.CheckedAt.IsZero() &&
			(j.Finding == "" || findingRe.MatchString(j.Finding)) {
			v := j.Verification
			st.LastVerification = &v
		}
	}
	head, err := q.GetLatestAuditLog(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return ChainStatus{}, fmt.Errorf("audit: read chain head: %w", err)
	default:
		st.HeadID, st.HeadHash, st.HeadAt = head.ID, hex.EncodeToString(head.Hash), head.Ts
	}
	signed, err := q.GetLatestAuditChainHead(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return ChainStatus{}, fmt.Errorf("audit: read signed head: %w", err)
	default:
		st.SignedHead = &SignedHeadInfo{ChainID: signed.ChainID, SignedAt: signed.SignedAt, KeyID: signed.KeyID}
	}
	return st, nil
}

// ChainStatus reads the chain status through the Auditor's database.
func (a *Auditor) ChainStatus(ctx context.Context) (ChainStatus, error) {
	return LoadChainStatus(ctx, a.db)
}
