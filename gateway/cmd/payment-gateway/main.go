package main

import (
	"log/slog"
	"os"
)

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

	logger := newLogger(cfg.Runtime.LogLevel)
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
