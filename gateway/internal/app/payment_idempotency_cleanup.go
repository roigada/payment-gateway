package app

import (
	"context"
	"time"
)

const idempotencyReplayWindow = 24 * time.Hour

func (s *PaymentService) CleanupCompletedIdempotencyReplays(ctx context.Context) (int, error) {
	return s.store.CleanupCompletedIdempotencyRecords(ctx, s.clock.Now().Add(-idempotencyReplayWindow))
}
