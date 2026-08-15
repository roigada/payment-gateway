// Package idempotencycleanup runs the completed Idempotency Replay retention job.
package idempotencycleanup

import (
	"context"
	"log/slog"
	"time"
)

const (
	completed = "completed"
	empty     = "empty"
	failed    = "failed"
)

// Cleaner removes completed Idempotency Replays outside their retention window.
type Cleaner interface {
	CleanupCompletedIdempotencyReplays(context.Context) (int, error)
}

// Metrics records the outcome of an Idempotency Replay cleanup run.
type Metrics interface {
	RecordIdempotencyReplayCleanup(result string, removed int)
}

type ticker interface {
	Chan() <-chan time.Time
	Stop()
}

type timeTicker struct{ *time.Ticker }

func (t timeTicker) Chan() <-chan time.Time { return t.C }

// Runner periodically removes completed Idempotency Replays outside their retention window.
type Runner struct {
	cleaner   Cleaner
	logger    *slog.Logger
	metrics   Metrics
	interval  time.Duration
	newTicker func(time.Duration) ticker
}

// New constructs an Idempotency Replay cleanup runner.
func New(cleaner Cleaner, metrics Metrics, logger *slog.Logger, interval time.Duration) *Runner {
	return &Runner{
		cleaner:  cleaner,
		logger:   logger,
		metrics:  metrics,
		interval: interval,
		newTicker: func(interval time.Duration) ticker {
			return timeTicker{time.NewTicker(interval)}
		},
	}
}

// Run blocks until ctx is cancelled, cleaning only after each interval elapses.
func (r *Runner) Run(ctx context.Context) {
	ticker := r.newTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.Chan():
			removed, err := r.cleaner.CleanupCompletedIdempotencyReplays(ctx)
			if err != nil {
				r.metrics.RecordIdempotencyReplayCleanup(failed, 0)
				r.logger.Warn("idempotency replay cleanup failed", "result", failed)
				continue
			}
			result := completed
			if removed == 0 {
				result = empty
			}
			r.metrics.RecordIdempotencyReplayCleanup(result, removed)
			r.logger.Info("idempotency replay cleanup completed", "result", result, "removed", removed)
		}
	}
}
