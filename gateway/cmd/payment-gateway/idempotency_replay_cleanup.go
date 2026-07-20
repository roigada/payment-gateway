package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
)

const (
	idempotencyReplayWindow          = 24 * time.Hour
	idempotencyReplayCleanupInterval = time.Hour
)

func runIdempotencyReplayCleanup(ctx context.Context, store app.PaymentStore, clock app.Clock, logger *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			removed, err := store.CleanupCompletedIdempotencyRecords(ctx, clock.Now().Add(-idempotencyReplayWindow))
			if err != nil {
				logger.Warn("idempotency replay cleanup failed", "error", err)
				continue
			}
			logger.Info("idempotency replay cleanup completed", "removed", removed)
		}
	}
}
