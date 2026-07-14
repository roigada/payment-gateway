package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/httpapi"
	"github.com/roigada/payment-gateway/internal/mockbank"
	"github.com/roigada/payment-gateway/internal/observability"
	"github.com/roigada/payment-gateway/internal/postgres"
	"github.com/roigada/payment-gateway/internal/uuidgen"
)

const postgresStartupTimeout = 5 * time.Second

func main() {
	cfg, err := loadConfig()
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stdout, nil)).Error("payment-gateway stopped", "error", err)
		os.Exit(1)
	}
	if err := cfg.validate(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stdout, nil)).Error("payment-gateway stopped", "error", err)
		os.Exit(1)
	}
	logger := newLogger(cfg.LogLevel)
	if err := run(cfg, logger); err != nil {
		logger.Error("payment-gateway stopped", "error", err)
		os.Exit(1)
	}
}

func newLogger(level string) *slog.Logger {
	var slogLevel slog.Level
	_ = slogLevel.UnmarshalText([]byte(level))
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slogLevel,
	}))
}

func run(cfg config, logger *slog.Logger) error {
	dbCtx, cancel := context.WithTimeout(context.Background(), cfg.DatabaseStartupTimeout)
	defer cancel()

	db, err := connectDatabase(dbCtx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	handler, readiness, err := newHandler(cfg, db, logger)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	shutdownSignals := make(chan os.Signal, 2)
	signal.Notify(shutdownSignals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(shutdownSignals)

	logger.Info("payment-gateway starting", "addr", cfg.Addr)
	return runHTTPServerWithConfig(listener, handler, readiness, cfg, shutdownSignals, logger)
}

func connectDatabase(ctx context.Context, cfg config) (*sql.DB, error) {
	db, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	configureDatabasePool(db, cfg)
	return db, nil
}

func configureDatabasePool(db interface {
	SetMaxOpenConns(int)
	SetMaxIdleConns(int)
	SetConnMaxLifetime(time.Duration)
	SetConnMaxIdleTime(time.Duration)
}, cfg config) {
	db.SetMaxOpenConns(cfg.DatabaseMaxOpenConnections)
	db.SetMaxIdleConns(cfg.DatabaseMaxIdleConnections)
	db.SetConnMaxLifetime(cfg.DatabaseConnectionMaxLifetime)
	db.SetConnMaxIdleTime(cfg.DatabaseConnectionMaxIdleTime)
}

func newHandler(cfg config, db *sql.DB, logger *slog.Logger) (http.Handler, *shutdownReadiness, error) {
	metricsRegistry := observability.NewRegistry()
	paymentStore := postgres.NewPaymentStore(db)
	httpMetrics, err := observability.NewHTTPMetrics(metricsRegistry)
	if err != nil {
		return nil, nil, err
	}
	mockBankMetrics, err := observability.NewMockBankMetrics(metricsRegistry)
	if err != nil {
		return nil, nil, err
	}
	paymentOperationMetrics, err := observability.NewPaymentOperationMetrics(metricsRegistry)
	if err != nil {
		return nil, nil, err
	}
	postgresPoolCollector, err := observability.NewPostgresPoolCollector(db)
	if err != nil {
		return nil, nil, err
	}
	if err := metricsRegistry.Register(postgresPoolCollector); err != nil {
		return nil, nil, err
	}
	pendingPaymentCollector, err := observability.NewPendingPaymentCollector(paymentStore)
	if err != nil {
		return nil, nil, err
	}
	if err := metricsRegistry.Register(pendingPaymentCollector); err != nil {
		return nil, nil, err
	}

	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: cfg.MockBankConnectTimeout}).DialContext,
		TLSHandshakeTimeout:   cfg.MockBankTLSHandshakeTimeout,
		ResponseHeaderTimeout: cfg.MockBankResponseHeaderTimeout,
		IdleConnTimeout:       cfg.MockBankIdleConnectionTimeout,
	}
	mockBank, err := mockbank.NewClientWithTimeout(cfg.MockBankBaseURL, &http.Client{Transport: transport}, mockBankMetrics, cfg.MockBankTimeout)
	if err != nil {
		return nil, nil, err
	}

	paymentIDs := uuidgen.NewPaymentIDGenerator()
	bankOperationKeys := uuidgen.NewBankOperationKeyGenerator()
	paymentService := app.NewPaymentService(paymentStore, paymentIDs, bankOperationKeys, mockBank, paymentOperationMetrics, app.SystemClock{}, cfg.FingerprintSecret, cfg.IdempotencyClaimStuckAfter)
	readiness := newShutdownReadiness(postgres.NewReadinessChecker(db))
	metricsHandler := promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{})
	return httpapi.NewServer(paymentService, readiness, logger, httpMetrics, metricsHandler, httpapi.ServerOptions{
		PaymentCommandTimeout: cfg.PaymentCommandTimeout,
		PaymentReadTimeout:    cfg.PaymentReadTimeout,
		ReadinessTimeout:      defaultReadyTimeout,
		MaxRequestBodyBytes:   cfg.HTTPMaxRequestBodyBytes,
	}), readiness, nil
}
