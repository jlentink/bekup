package instancekeys

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// ErrNoActiveKey is returned by Load when no active key exists.
var ErrNoActiveKey = errors.New("no active instance signing key")

// Load unseals the active instance key. The caller must Destroy it.
func Load(ctx context.Context, q *sqlc.Queries, kr *crypto.Keyring) (*crypto.SigningKey, error) {
	row, err := q.GetActiveInstanceKey(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoActiveKey
	}
	if err != nil {
		return nil, fmt.Errorf("load instance key: %w", err)
	}
	k, err := crypto.OpenSigningKey(kr, row.ID, ed25519.PublicKey(row.PublicKey), row.SealedPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("open instance key %s: %w", row.ID, err)
	}
	return k, nil
}

// Active loads the active instance key, creating it first when none exists.
// created reports whether this call created it (callers audit that).
// Concurrent callers converge on one key: the insert does nothing when
// another active key won the race, and the winner is loaded instead.
func Active(ctx context.Context, q *sqlc.Queries, kr *crypto.Keyring) (key *crypto.SigningKey, created bool, err error) {
	k, err := Load(ctx, q, kr)
	if !errors.Is(err, ErrNoActiveKey) {
		return k, false, err
	}
	nk, err := crypto.GenerateSigningKey()
	if err != nil {
		return nil, false, err
	}
	sealed, err := nk.SealPrivate(kr)
	if err != nil {
		nk.Destroy()
		return nil, false, fmt.Errorf("seal instance key: %w", err)
	}
	n, err := q.InsertInstanceKey(ctx, sqlc.InsertInstanceKeyParams{ID: nk.ID, PublicKey: nk.Public, SealedPrivateKey: sealed})
	if err != nil {
		nk.Destroy()
		return nil, false, fmt.Errorf("store instance key: %w", err)
	}
	if n == 1 {
		return nk, true, nil
	}
	nk.Destroy()
	k, err = Load(ctx, q, kr)
	return k, false, err
}

// Verifier returns a verifier holding every stored public key (active and
// retired). A row whose id does not match its public key is an error.
func Verifier(ctx context.Context, q *sqlc.Queries) (*crypto.Verifier, error) {
	rows, err := q.ListInstancePublicKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list instance keys: %w", err)
	}
	v := crypto.NewVerifier()
	for _, r := range rows {
		id, err := v.Add(ed25519.PublicKey(r.PublicKey))
		if err != nil {
			return nil, fmt.Errorf("instance key %s: %w", r.ID, err)
		}
		if id != r.ID {
			return nil, fmt.Errorf("instance key %s: %w: id does not match public key", r.ID, crypto.ErrSigningKey)
		}
	}
	return v, nil
}

// PublicKey returns the stored public key with the given id.
func PublicKey(ctx context.Context, q *sqlc.Queries, id string) (ed25519.PublicKey, error) {
	row, err := q.GetInstanceKey(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load instance key %s: %w", id, err)
	}
	return ed25519.PublicKey(row.PublicKey), nil
}
