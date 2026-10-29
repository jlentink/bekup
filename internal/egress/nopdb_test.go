package egress

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// nopDB satisfies DB for tests that never reach the database.
type nopDB struct{}

var errNoDB = errors.New("no database in this test")

func (nopDB) Begin(context.Context) (pgx.Tx, error) { return nil, errNoDB }
func (nopDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errNoDB
}
func (nopDB) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, errNoDB }
func (nopDB) QueryRow(context.Context, string, ...any) pgx.Row        { return errRow{} }

type errRow struct{}

func (errRow) Scan(...any) error { return errNoDB }
