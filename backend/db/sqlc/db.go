package sqlc

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DBTX interface {
	QueryRow(ctx context.Context, sql string, args ...any) interface{ Scan(dest ...any) error }
	Query(ctx context.Context, sql string, args ...any) (interface{ Next() bool; Scan(dest ...any) error; Close() }, error)
	Exec(ctx context.Context, sql string, args ...any) (interface{ RowsAffected() int64 }, error)
}

type Queries struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Queries {
	return &Queries{pool: pool}
}

func NewWithDB(db DBTX) *Queries {
	// for sqlc compatibility, not used with pgxpool directly
	return &Queries{}
}
