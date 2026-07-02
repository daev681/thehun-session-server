package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS accounts (
			id            BIGSERIAL    PRIMARY KEY,
			account_id    TEXT         NOT NULL UNIQUE,
			password_hash TEXT         NOT NULL,
			display_name  TEXT         NOT NULL DEFAULT '',
			wins          INT          NOT NULL DEFAULT 0,
			losses        INT          NOT NULL DEFAULT 0,
			created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)
	`)
	return err
}
