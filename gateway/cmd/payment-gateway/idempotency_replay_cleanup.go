package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
)

const (
	idempotencyReplayCleanupCompleted = "completed"
	idempotencyReplayCleanupEmpty     = "empty"
	idempotencyReplayCleanupFailed    = "failed"
)

type idempotencyReplayCleanupTicker interface {
	Chan() <-chan time.Time
	Stop()
}

type idempotencyReplayCleanupMetrics interface {
	RecordIdempotencyReplayCleanup(result string, removed int)
}

type timeTicker struct{ *time.Ticker }

func (t timeTicker) Chan() <-chan time.Time { return t.C }

func runIdempotencyReplayCleanup(ctx context.Context, store app.PaymentStore, clock app.Clock, logger *slog.Logger, metrics idempotencyReplayCleanupMetrics, replayWindow time.Duration, ticker idempotencyReplayCleanupTicker) {
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.Chan():
			removed, err := store.CleanupCompletedIdempotencyRecords(ctx, clock.Now().Add(-replayWindow))
			if err != nil {
				metrics.RecordIdempotencyReplayCleanup(idempotencyReplayCleanupFailed, 0)
				logger.Warn("idempotency replay cleanup failed", "result", idempotencyReplayCleanupFailed)
				continue
			}
			result := idempotencyReplayCleanupCompleted
			if removed == 0 {
				result = idempotencyReplayCleanupEmpty
			}
			metrics.RecordIdempotencyReplayCleanup(result, removed)
			logger.Info("idempotency replay cleanup completed", "result", result, "removed", removed)
		}
	}
}
