package audit

import (
	"errors"
	"testing"
	"time"
)

func TestSaveVerificationRejectsInvalid(t *testing.T) {
	now := time.Now()
	for name, v := range map[string]Verification{
		"no time":            {OK: true},
		"ok with finding":    {CheckedAt: now, OK: true, Finding: "hash_mismatch:1"},
		"failed no finding":  {CheckedAt: now},
		"free-text finding":  {CheckedAt: now, Finding: "row 12 was <b>edited</b>"},
		"negative id":        {CheckedAt: now, OK: true, VerifiedThroughID: -1},
		"finding without id": {CheckedAt: now, Finding: "hash_mismatch"},
	} {
		// The checks run before any database access (nil queries).
		if err := SaveVerification(t.Context(), nil, v); !errors.Is(err, ErrInvalidEvent) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	for _, f := range []string{"hash_mismatch:12", "signed_head_database:0", "anchor_rewritten:9"} {
		if !findingRe.MatchString(f) {
			t.Errorf("finding %q rejected", f)
		}
	}
}
