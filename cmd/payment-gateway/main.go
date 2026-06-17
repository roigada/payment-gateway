package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/httpapi"
	"github.com/roigada/payment-gateway/internal/postgres"
	"github.com/roigada/payment-gateway/internal/uuidgen"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	if err := run(logger); err != nil {
		logger.Error("payment-gateway stopped", "error", err)
		os.Exit(1)
	}
}

type config struct {
	Addr                           string
	DatabaseURL                    string
	MockBankBaseURL                string
	AuthorizationFingerprintSecret string
}

func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		Addr:                           getenv("ADDR"),
		DatabaseURL:                    getenv("DATABASE_URL"),
		MockBankBaseURL:                getenv("MOCK_BANK_BASE_URL"),
		AuthorizationFingerprintSecret: getenv("AUTHORIZATION_FINGERPRINT_SECRET"),
	}
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
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

	return cfg, nil
}

func run(logger *slog.Logger) error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}

	db, err := postgres.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	taskRepository := postgres.NewTaskRepository(db)
	taskIDs := uuidgen.NewTaskIDGenerator()
	taskService := app.NewTaskService(taskRepository, taskIDs, app.NoopTaskNotifier{})
	readiness := postgres.NewReadinessChecker(db)
	server := httpapi.NewServer(taskService, readiness, logger)

	logger.Info("payment-gateway starting", "addr", cfg.Addr)
	return http.ListenAndServe(cfg.Addr, server)
}
