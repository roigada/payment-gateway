package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanupCompletedIdempotencyReplaysDeletesRecordsOutsideReplayWindow(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	store := &paymentStoreFake{cleanupRemoved: 3}
	service := newPaymentService(store, &bankFake{}, now)

	removed, err := service.CleanupCompletedIdempotencyReplays(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 3, removed)
	assert.Equal(t, now.Add(-24*time.Hour), store.cleanupBefore)
}

func TestCleanupCompletedIdempotencyReplaysReturnsStoreError(t *testing.T) {
	storeErr := errors.New("database unavailable")
	store := &paymentStoreFake{cleanupErr: storeErr}
	service := newPaymentService(store, &bankFake{}, time.Time{})

	removed, err := service.CleanupCompletedIdempotencyReplays(context.Background())

	assert.Zero(t, removed)
	require.ErrorIs(t, err, storeErr)
}
