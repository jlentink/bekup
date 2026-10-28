package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// TxBeginner starts transactions; *pgxpool.Pool and *pgx.Conn implement it.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Tx runs fn in a transaction. It commits when fn returns nil and rolls back
// when fn returns an error or panics (the panic is re-raised after the
// rollback).
func Tx(ctx context.Context, db TxBeginner, fn func(q *sqlc.Queries) error) error {
	return TxWith(ctx, db, func(_ pgx.Tx, q *sqlc.Queries) error { return fn(q) })
}

// TxWith is Tx for callers that also need the transaction itself, e.g. to
// insert River jobs atomically with their own writes (River's InsertTx).
// The pgx.Tx must not be committed or rolled back by fn.
func TxWith(ctx context.Context, db TxBeginner, fn func(tx pgx.Tx, q *sqlc.Queries) error) (err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		// Roll back with a context that survives cancellation of ctx.
		rbErr := tx.Rollback(context.WithoutCancel(ctx))
		if p := recover(); p != nil {
			panic(p)
		}
		if rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			err = errors.Join(err, fmt.Errorf("roll back transaction: %w", rbErr))
		}
	}()
	if err := fn(tx, sqlc.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}
