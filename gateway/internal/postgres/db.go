package postgres

import (
	"context"
	"database/sql"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Options struct {
	URL                   string
	MaxOpenConnections    int
	MaxIdleConnections    int
	ConnectionMaxLifetime time.Duration
	ConnectionMaxIdleTime time.Duration
}

func Open(ctx context.Context, options Options) (*sql.DB, error) {
	db, err := sql.Open("pgx", options.URL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(options.MaxOpenConnections)
	db.SetMaxIdleConns(options.MaxIdleConnections)
	db.SetConnMaxLifetime(options.ConnectionMaxLifetime)
	db.SetConnMaxIdleTime(options.ConnectionMaxIdleTime)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
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
