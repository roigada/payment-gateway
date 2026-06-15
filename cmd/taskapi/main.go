package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/roigada/template-go/internal/app"
	"github.com/roigada/template-go/internal/exampleapi"
	"github.com/roigada/template-go/internal/httpapi"
	"github.com/roigada/template-go/internal/postgres"
	"github.com/roigada/template-go/internal/uuidgen"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	if err := run(logger); err != nil {
		logger.Error("taskapi stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db, err := postgres.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	taskRepository := postgres.NewTaskRepository(db)
	taskIDs := uuidgen.NewTaskIDGenerator()
	taskNotifier, err := newTaskNotifier(os.Getenv("EXAMPLE_API_BASE_URL"), os.Getenv("EXAMPLE_API_TOKEN"))
	if err != nil {
		return err
	}
	taskService := app.NewTaskService(taskRepository, taskIDs, taskNotifier)
	server := httpapi.NewServer(taskService, logger)

	logger.Info("taskapi starting", "addr", addr)
	return http.ListenAndServe(addr, server)
}

func newTaskNotifier(baseURL string, token string) (app.TaskNotifier, error) {
	if baseURL == "" {
		return app.NoopTaskNotifier{}, nil
	}
	if token == "" {
		return nil, fmt.Errorf("EXAMPLE_API_TOKEN is required when EXAMPLE_API_BASE_URL is set")
	}

	return exampleapi.NewClient(baseURL, token, nil)
}
