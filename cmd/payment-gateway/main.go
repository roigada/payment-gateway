package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
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
	Addr              string
	DatabaseURL       string
	MockBankBaseURL   string
	FingerprintSecret string
}

func loadConfig() config {
	cfg := config{
		Addr:              os.Getenv("ADDR"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		MockBankBaseURL:   os.Getenv("MOCK_BANK_BASE_URL"),
		FingerprintSecret: os.Getenv("FINGERPRINT_SECRET"),
	}
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}

	return cfg
}

func (cfg config) validate() error {
	if cfg.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.MockBankBaseURL == "" {
		return fmt.Errorf("MOCK_BANK_BASE_URL is required")
	}
	if cfg.FingerprintSecret == "" {
		return fmt.Errorf("FINGERPRINT_SECRET is required")
	}

	return nil
}

func run(logger *slog.Logger) error {
	cfg := loadConfig()
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

	metricsRegistry := observability.NewRegistry()
	httpMetrics, err := observability.NewHTTPMetrics(metricsRegistry)
	if err != nil {
		return err
	}
	mockBankMetrics, err := observability.NewMockBankMetrics(metricsRegistry)
	if err != nil {
		return err
	}

	mockBank, err := mockbank.NewClient(cfg.MockBankBaseURL, http.DefaultClient, mockBankMetrics)
	if err != nil {
		return err
	}

	paymentStore := postgres.NewPaymentStore(db)
	paymentIDs := uuidgen.NewPaymentIDGenerator()
	bankOperationKeys := uuidgen.NewBankOperationKeyGenerator()
	paymentService := app.NewPaymentService(paymentStore, paymentIDs, bankOperationKeys, mockBank, app.SystemClock{}, cfg.FingerprintSecret)
	readiness := postgres.NewReadinessChecker(db)
	metricsHandler := promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{})
	server := httpapi.NewServer(paymentService, readiness, logger, httpMetrics, metricsHandler)

	logger.Info("payment-gateway starting", "addr", cfg.Addr)
	return http.ListenAndServe(cfg.Addr, server)
}
