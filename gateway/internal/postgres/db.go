package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Config struct {
	URL                   string
	MaxOpenConnections    int
	MaxIdleConnections    int
	ConnectionMaxLifetime time.Duration
	ConnectionMaxIdleTime time.Duration
}

func Open(ctx context.Context, config Config) (*sql.DB, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", config.URL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(config.MaxOpenConnections)
	db.SetMaxIdleConns(config.MaxIdleConnections)
	db.SetConnMaxLifetime(config.ConnectionMaxLifetime)
	db.SetConnMaxIdleTime(config.ConnectionMaxIdleTime)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

func (config Config) validate() error {
	if strings.TrimSpace(config.URL) == "" {
		return errors.New("postgres URL is required")
	}
	if config.MaxOpenConnections <= 0 {
		return errors.New("postgres max open connections must be positive")
	}
	if config.MaxIdleConnections < 0 {
		return errors.New("postgres max idle connections must be non-negative")
	}
	if config.MaxIdleConnections > config.MaxOpenConnections {
		return errors.New("postgres max idle connections must not exceed max open connections")
	}
	if config.ConnectionMaxLifetime <= 0 {
		return errors.New("postgres connection max lifetime must be positive")
	}
	if config.ConnectionMaxIdleTime <= 0 {
		return errors.New("postgres connection max idle time must be positive")
	}
	return nil
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
