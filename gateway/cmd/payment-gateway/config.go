package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
)

const (
	defaultDatabaseMaxOpenConnections    = 10
	defaultDatabaseMaxIdleConnections    = 5
	defaultDatabaseConnectionMaxLifetime = 30 * time.Minute
	defaultIdempotencyClaimStuckAfter    = app.DefaultIdempotencyClaimStuckAfter
	defaultShutdownTimeout               = 30 * time.Second
)

type config struct {
	Addr                          string
	DatabaseURL                   string
	DatabaseMaxOpenConnections    int
	DatabaseMaxIdleConnections    int
	DatabaseConnectionMaxLifetime time.Duration
	MockBankBaseURL               string
	FingerprintSecret             string
	IdempotencyClaimStuckAfter    time.Duration
	ShutdownTimeout               time.Duration
}

func loadConfig() (config, error) {
	databaseMaxOpenConnections, err := envInt("DATABASE_MAX_OPEN_CONNECTIONS", defaultDatabaseMaxOpenConnections)
	if err != nil {
		return config{}, err
	}
	databaseMaxIdleConnections, err := envInt("DATABASE_MAX_IDLE_CONNECTIONS", defaultDatabaseMaxIdleConnections)
	if err != nil {
		return config{}, err
	}
	databaseConnectionMaxLifetime, err := envDuration("DATABASE_CONNECTION_MAX_LIFETIME", defaultDatabaseConnectionMaxLifetime)
	if err != nil {
		return config{}, err
	}
	idempotencyClaimStuckAfter, err := envDuration("IDEMPOTENCY_CLAIM_STUCK_AFTER", defaultIdempotencyClaimStuckAfter)
	if err != nil {
		return config{}, err
	}
	shutdownTimeout, err := envDuration("SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return config{}, err
	}

	cfg := config{
		Addr:                          os.Getenv("ADDR"),
		DatabaseURL:                   os.Getenv("DATABASE_URL"),
		DatabaseMaxOpenConnections:    databaseMaxOpenConnections,
		DatabaseMaxIdleConnections:    databaseMaxIdleConnections,
		DatabaseConnectionMaxLifetime: databaseConnectionMaxLifetime,
		MockBankBaseURL:               os.Getenv("MOCK_BANK_BASE_URL"),
		FingerprintSecret:             os.Getenv("FINGERPRINT_SECRET"),
		IdempotencyClaimStuckAfter:    idempotencyClaimStuckAfter,
		ShutdownTimeout:               shutdownTimeout,
	}
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}

	return cfg, nil
}

func (cfg config) validate() error {
	if cfg.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.DatabaseMaxOpenConnections <= 0 {
		return fmt.Errorf("DATABASE_MAX_OPEN_CONNECTIONS must be a positive integer")
	}
	if cfg.DatabaseMaxIdleConnections < 0 {
		return fmt.Errorf("DATABASE_MAX_IDLE_CONNECTIONS must be a non-negative integer")
	}
	if cfg.DatabaseMaxIdleConnections > cfg.DatabaseMaxOpenConnections {
		return fmt.Errorf("DATABASE_MAX_IDLE_CONNECTIONS must be less than or equal to DATABASE_MAX_OPEN_CONNECTIONS")
	}
	if cfg.DatabaseConnectionMaxLifetime <= 0 {
		return fmt.Errorf("DATABASE_CONNECTION_MAX_LIFETIME must be a positive duration")
	}
	if cfg.MockBankBaseURL == "" {
		return fmt.Errorf("MOCK_BANK_BASE_URL is required")
	}
	if cfg.FingerprintSecret == "" {
		return fmt.Errorf("FINGERPRINT_SECRET is required")
	}
	if cfg.IdempotencyClaimStuckAfter <= 0 {
		return fmt.Errorf("IDEMPOTENCY_CLAIM_STUCK_AFTER must be a positive duration")
	}
	if cfg.ShutdownTimeout <= 0 {
		return fmt.Errorf("SHUTDOWN_TIMEOUT must be a positive duration")
	}

	return nil
}

func envInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}

	return parsed, nil
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration", name)
	}

	return parsed, nil
}
