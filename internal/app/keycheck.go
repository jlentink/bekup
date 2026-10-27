package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// KeyCheckSetting is the settings key of the stored key-check values
// (D-086, D-119): one KCV per master key ID the database has seen.
const KeyCheckSetting = "security.key_check_values"

// ErrKeyringMismatch means the configured master keyring is not the one
// this database was set up with.
var ErrKeyringMismatch = errors.New("the master key does not match this database")

// keyCheckValues is the JSON value of KeyCheckSetting.
type keyCheckValues struct {
	KCVs []string `json:"kcvs"`
}

// errKCVRace: another instance stored the first KCVs concurrently.
var errKCVRace = errors.New("app: key-check values stored concurrently")

// checkKeyring verifies the master keyring against the key-check values
// stored in settings (D-119), fail closed:
//
//   - none stored (first start): the KCV of every key in the ring is
//     stored, audited as keys.check_value_stored;
//   - stored: every stored KCV whose key is in the ring must verify, and at
//     least one must (a key missing from the ring is tolerated: retired
//     after re-encryption); otherwise serve refuses with
//     ErrKeyringMismatch;
//   - keys in the ring without a stored KCV (after `bekup keys rotate`)
//     are added and recorded as keys.rotated.
func (a *App) checkKeyring(ctx context.Context) error {
	err := a.checkKeyringOnce(ctx)
	if errors.Is(err, errKCVRace) {
		err = a.checkKeyringOnce(ctx)
	}
	return err
}

func (a *App) checkKeyringOnce(ctx context.Context) error {
	ring := a.keyring
	var rotated []string
	err := db.Tx(ctx, a.pool, func(q *sqlc.Queries) error {
		row, err := q.GetSettingForUpdate(ctx, KeyCheckSetting)
		if errors.Is(err, pgx.ErrNoRows) {
			return a.storeFirstKCVs(ctx, q, ring)
		}
		if err != nil {
			return fmt.Errorf("load key-check values: %w", err)
		}
		var stored keyCheckValues
		if err := json.Unmarshal(row.Value, &stored); err != nil {
			return fmt.Errorf("%w: unreadable %s setting: %w", ErrKeyringMismatch, KeyCheckSetting, err)
		}
		known, err := verifyKCVs(ctx, ring, stored.KCVs, a.opts.Logger)
		if err != nil {
			return err
		}
		next := stored
		for _, id := range ring.IDs() {
			if known[id] {
				continue
			}
			kcv, err := ring.KeyCheckValueFor(id)
			if err != nil {
				return fmt.Errorf("key-check value: %w", err)
			}
			next.KCVs = append(next.KCVs, kcv)
			rotated = append(rotated, id.String())
		}
		if len(rotated) == 0 {
			return nil
		}
		value, err := json.Marshal(next)
		if err != nil {
			return err
		}
		if _, err := q.PutSetting(ctx, sqlc.PutSettingParams{Key: KeyCheckSetting, Value: value}); err != nil {
			return fmt.Errorf("store key-check values: %w", err)
		}
		_, err = a.auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.SystemActor(), Action: audit.ActionKeysRotated, TargetType: "keyring",
			Details: audit.Details{"primary_key_id": ring.PrimaryID().String(), "added_key_ids": rotated},
		})
		return err
	})
	if err != nil {
		return err
	}
	if len(rotated) > 0 {
		a.opts.Logger.InfoContext(ctx, "new master keys recorded", slog.Any("key_ids", rotated),
			slog.String("primary_key_id", ring.PrimaryID().String()))
	}
	return nil
}

// storeFirstKCVs stores the KCVs of every key of ring as the first
// key-check values of the database.
func (a *App) storeFirstKCVs(ctx context.Context, q *sqlc.Queries, ring *crypto.Keyring) error {
	var vals keyCheckValues
	ids := make([]string, 0, len(ring.IDs()))
	for _, id := range ring.IDs() {
		kcv, err := ring.KeyCheckValueFor(id)
		if err != nil {
			return fmt.Errorf("key-check value: %w", err)
		}
		vals.KCVs = append(vals.KCVs, kcv)
		ids = append(ids, id.String())
	}
	value, err := json.Marshal(vals)
	if err != nil {
		return err
	}
	n, err := q.InsertSettingIfAbsent(ctx, sqlc.InsertSettingIfAbsentParams{Key: KeyCheckSetting, Value: value})
	if err != nil {
		return fmt.Errorf("store key-check values: %w", err)
	}
	if n == 0 {
		return errKCVRace
	}
	_, err = a.auditor.RecordTx(ctx, q, audit.Event{
		Actor: audit.SystemActor(), Action: audit.ActionKeysCheckValueStored, TargetType: "keyring",
		Details: audit.Details{"primary_key_id": ring.PrimaryID().String(), "key_ids": ids},
	})
	return err
}

// verifyKCVs checks the stored KCVs against ring and returns the key IDs
// they cover. A mismatch, a malformed value or no verifying value at all
// is ErrKeyringMismatch.
func verifyKCVs(ctx context.Context, ring *crypto.Keyring, kcvs []string, logger *slog.Logger) (map[crypto.KeyID]bool, error) {
	known := map[crypto.KeyID]bool{}
	for _, v := range kcvs {
		err := ring.VerifyKeyCheckValue(v)
		switch {
		case err == nil:
			id, perr := kcvKeyID(v)
			if perr != nil {
				return nil, fmt.Errorf("%w: %w", ErrKeyringMismatch, perr)
			}
			known[id] = true
		case errors.Is(err, crypto.ErrUnknownKeyID):
			logger.WarnContext(ctx, "a master key known to this database is not in the keyring (retired?)")
		default:
			return nil, fmt.Errorf("%w: %w", ErrKeyringMismatch, err)
		}
	}
	if len(known) == 0 {
		return nil, fmt.Errorf("%w: the keyring contains none of the keys this database was set up with", ErrKeyringMismatch)
	}
	return known, nil
}

// kcvKeyID extracts the key ID of a well-formed KCV.
func kcvKeyID(kcv string) (crypto.KeyID, error) {
	rest, ok := strings.CutPrefix(kcv, "kcv1:")
	if !ok {
		return crypto.KeyID{}, crypto.ErrMalformedKCV
	}
	id, _, ok := strings.Cut(rest, ":")
	if !ok {
		return crypto.KeyID{}, crypto.ErrMalformedKCV
	}
	return crypto.ParseKeyID(id)
}
