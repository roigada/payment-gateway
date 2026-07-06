package postgres

import (
	"context"
	"database/sql"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func Connect(ctx context.Context, databaseURL string) (*sql.DB, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

func Open(databaseURL string) (*sql.DB, error) {
	return sql.Open("pgx", databaseURL)
}

type ReadinessChecker struct {
	db *sql.DB
}

func NewReadinessChecker(db *sql.DB) ReadinessChecker {
	return ReadinessChecker{db: db}
}

func (c ReadinessChecker) CheckReady(ctx context.Context) error {
	return c.db.PingContext(ctx)
}
