package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/testsupport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanupCompletedIdempotencyReplaysDeletesRecordsOutsideReplayWindow(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	store := &cleanupPaymentStore{PaymentStore: testsupport.NewPaymentStore(), removed: 3}
	service := newPaymentService(store, &bankAuthorizerFake{}, now)

	removed, err := service.CleanupCompletedIdempotencyReplays(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 3, removed)
	assert.Equal(t, now.Add(-24*time.Hour), store.completedBefore)
}

func TestCleanupCompletedIdempotencyReplaysReturnsStoreError(t *testing.T) {
	storeErr := errors.New("database unavailable")
	store := &cleanupPaymentStore{PaymentStore: testsupport.NewPaymentStore(), err: storeErr}
	service := newPaymentService(store, &bankAuthorizerFake{}, time.Time{})

	removed, err := service.CleanupCompletedIdempotencyReplays(context.Background())

	assert.Zero(t, removed)
	require.ErrorIs(t, err, storeErr)
}

type cleanupPaymentStore struct {
	app.PaymentStore
	completedBefore time.Time
	removed         int
	err             error
}

func (s *cleanupPaymentStore) CleanupCompletedIdempotencyRecords(_ context.Context, completedBefore time.Time) (int, error) {
	s.completedBefore = completedBefore
	return s.removed, s.err
}
