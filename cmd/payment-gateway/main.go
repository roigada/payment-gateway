package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/httpapi"
	"github.com/roigada/payment-gateway/internal/mockbank"
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
	Addr                           string
	DatabaseURL                    string
	MockBankBaseURL                string
	AuthorizationFingerprintSecret string
}

func loadConfig() config {
	cfg := config{
		Addr:                           os.Getenv("ADDR"),
		DatabaseURL:                    os.Getenv("DATABASE_URL"),
		MockBankBaseURL:                os.Getenv("MOCK_BANK_BASE_URL"),
		AuthorizationFingerprintSecret: os.Getenv("AUTHORIZATION_FINGERPRINT_SECRET"),
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
	if cfg.AuthorizationFingerprintSecret == "" {
		return fmt.Errorf("AUTHORIZATION_FINGERPRINT_SECRET is required")
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

	mockBank, err := mockbank.NewClient(cfg.MockBankBaseURL, http.DefaultClient)
	if err != nil {
		return err
	}

	paymentRepository := postgres.NewPaymentRepository(db)
	idempotencyRepository := postgres.NewIdempotencyRepository(db)
	paymentIDs := uuidgen.NewPaymentIDGenerator()
	bankOperationKeys := uuidgen.NewBankOperationKeyGenerator()
	paymentService := app.NewPaymentService(paymentRepository, idempotencyRepository, paymentIDs, bankOperationKeys, mockBank, app.SystemClock{})
	readiness := postgres.NewReadinessChecker(db)
	server := httpapi.NewServer(paymentService, readiness, logger)

	logger.Info("payment-gateway starting", "addr", cfg.Addr)
	return http.ListenAndServe(cfg.Addr, server)
}
