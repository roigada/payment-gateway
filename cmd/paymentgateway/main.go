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
	"github.com/roigada/payment-gateway/internal/postgres"
	"github.com/roigada/payment-gateway/internal/uuidgen"
)

type config struct {
	DatabaseURL                    string
	MockBankBaseURL                string
	AuthorizationFingerprintSecret string
	Addr                           string
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	if err := run(logger); err != nil {
		logger.Error("paymentgateway stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	taskRepository := postgres.NewTaskRepository(db)
	taskIDs := uuidgen.NewTaskIDGenerator()
	taskService := app.NewTaskService(taskRepository, taskIDs, app.NoopTaskNotifier{})
	server := httpapi.NewServerWithReadiness(taskService, logger, db)

	logger.Info("paymentgateway starting", "addr", cfg.Addr)
	return http.ListenAndServe(cfg.Addr, server)
}

func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		DatabaseURL:                    getenv("DATABASE_URL"),
		MockBankBaseURL:                getenv("MOCK_BANK_BASE_URL"),
		AuthorizationFingerprintSecret: getenv("AUTHORIZATION_FINGERPRINT_SECRET"),
		Addr:                           getenv("ADDR"),
	}

	if cfg.DatabaseURL == "" {
		return config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.MockBankBaseURL == "" {
		return config{}, fmt.Errorf("MOCK_BANK_BASE_URL is required")
	}
	if cfg.AuthorizationFingerprintSecret == "" {
		return config{}, fmt.Errorf("AUTHORIZATION_FINGERPRINT_SECRET is required")
	}
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}

	return cfg, nil
}
