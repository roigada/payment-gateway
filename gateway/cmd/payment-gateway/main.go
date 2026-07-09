package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/httpapi"
	"github.com/roigada/payment-gateway/internal/mockbank"
	"github.com/roigada/payment-gateway/internal/observability"
	"github.com/roigada/payment-gateway/internal/postgres"
	"github.com/roigada/payment-gateway/internal/uuidgen"
)

const (
	postgresStartupTimeout               = 5 * time.Second
	defaultDatabaseMaxOpenConnections    = 10
	defaultDatabaseMaxIdleConnections    = 5
	defaultDatabaseConnectionMaxLifetime = 30 * time.Minute
	defaultIdempotencyClaimStuckAfter    = app.DefaultIdempotencyClaimStuckAfter
)

func main() {
	logger := newLogger()

	if err := run(logger); err != nil {
		logger.Error("payment-gateway stopped", "error", err)
		os.Exit(1)
	}
}

func newLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

type config struct {
	Addr                          string
	DatabaseURL                   string
	DatabaseMaxOpenConnections    int
	DatabaseMaxIdleConnections    int
	DatabaseConnectionMaxLifetime time.Duration
	MockBankBaseURL               string
	FingerprintSecret             string
	IdempotencyClaimStuckAfter    time.Duration
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

	cfg := config{
		Addr:                          os.Getenv("ADDR"),
		DatabaseURL:                   os.Getenv("DATABASE_URL"),
		DatabaseMaxOpenConnections:    databaseMaxOpenConnections,
		DatabaseMaxIdleConnections:    databaseMaxIdleConnections,
		DatabaseConnectionMaxLifetime: databaseConnectionMaxLifetime,
		MockBankBaseURL:               os.Getenv("MOCK_BANK_BASE_URL"),
		FingerprintSecret:             os.Getenv("FINGERPRINT_SECRET"),
		IdempotencyClaimStuckAfter:    idempotencyClaimStuckAfter,
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

func configureDatabasePool(db interface {
	SetMaxOpenConns(int)
	SetMaxIdleConns(int)
	SetConnMaxLifetime(time.Duration)
}, cfg config) {
	db.SetMaxOpenConns(cfg.DatabaseMaxOpenConnections)
	db.SetMaxIdleConns(cfg.DatabaseMaxIdleConnections)
	db.SetConnMaxLifetime(cfg.DatabaseConnectionMaxLifetime)
}

func run(logger *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := cfg.validate(); err != nil {
		return err
	}

	dbCtx, cancel := context.WithTimeout(context.Background(), postgresStartupTimeout)
	defer cancel()

	db, err := postgres.Connect(dbCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	configureDatabasePool(db, cfg)

	metricsRegistry := observability.NewRegistry()
	httpMetrics, err := observability.NewHTTPMetrics(metricsRegistry)
	if err != nil {
		return err
	}
	mockBankMetrics, err := observability.NewMockBankMetrics(metricsRegistry)
	if err != nil {
		return err
	}
	paymentOperationMetrics, err := observability.NewPaymentOperationMetrics(metricsRegistry)
	if err != nil {
		return err
	}
	postgresPoolCollector, err := observability.NewPostgresPoolCollector(db)
	if err != nil {
		return err
	}
	if err := metricsRegistry.Register(postgresPoolCollector); err != nil {
		return err
	}

	mockBank, err := mockbank.NewClient(cfg.MockBankBaseURL, http.DefaultClient, mockBankMetrics)
	if err != nil {
		return err
	}

	paymentStore := postgres.NewPaymentStore(db)
	paymentIDs := uuidgen.NewPaymentIDGenerator()
	bankOperationKeys := uuidgen.NewBankOperationKeyGenerator()
	paymentService := app.NewPaymentService(paymentStore, paymentIDs, bankOperationKeys, mockBank, paymentOperationMetrics, app.SystemClock{}, cfg.FingerprintSecret, cfg.IdempotencyClaimStuckAfter)
	readiness := postgres.NewReadinessChecker(db)
	metricsHandler := promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{})
	server := httpapi.NewServer(paymentService, readiness, logger, httpMetrics, metricsHandler)

	logger.Info("payment-gateway starting", "addr", cfg.Addr)
	return http.ListenAndServe(cfg.Addr, server)
}
