package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPaymentCommandExecutorReturnsReplayWithoutInvokingBehavior(t *testing.T) {
	request, payment := executorTestClaim(t)
	replayed := PaymentCommandResult{Payment: PaymentResult{ID: string(payment.ID())}, HTTPStatus: 201}
	store := &executorStore{
		claim: NewReplayedPaymentCommand(request, replayed),
	}
	behaviorCalled := false

	execution, err := newExecutorForTest(store, nil).execute(context.Background(), request, func(context.Context, *domain.Payment) (PaymentCommandResult, error) {
		behaviorCalled = true
		return PaymentCommandResult{}, nil
	})

	require.NoError(t, err)
	assert.Equal(t, replayed, execution.result)
	assert.True(t, execution.replayed)
	assert.False(t, behaviorCalled)
	assert.Equal(t, []string{"claim"}, store.events)
}

func TestPaymentCommandExecutorReleasesClaimForBehaviorFailure(t *testing.T) {
	request, payment := executorTestClaim(t)
	store := &executorStore{claim: NewClaimedPaymentCommand(request, payment)}
	behaviorErr := NewPaymentBankUnavailableError(errors.New("bank unavailable"))

	_, err := newExecutorForTest(store, nil).execute(context.Background(), request, func(context.Context, *domain.Payment) (PaymentCommandResult, error) {
		store.events = append(store.events, "behavior")
		return PaymentCommandResult{}, behaviorErr
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorBankUnavailable))
	assert.Equal(t, []string{"claim", "behavior", "release"}, store.events)
}

func TestPaymentCommandExecutorPreservesClaimForCompletionFailure(t *testing.T) {
	request, payment := executorTestClaim(t)
	store := &executorStore{
		claim:       NewClaimedPaymentCommand(request, payment),
		completeErr: errors.New("completion failed"),
	}

	_, err := newExecutorForTest(store, nil).execute(context.Background(), request, func(context.Context, *domain.Payment) (PaymentCommandResult, error) {
		store.events = append(store.events, "behavior")
		return PaymentCommandResult{Payment: PaymentResult{ID: string(payment.ID())}}, nil
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorInternal))
	assert.Equal(t, []string{"claim", "behavior", "complete"}, store.events)
}

func TestPaymentCommandExecutorUsesRecoveredPaymentAndRecordsRecoveryAroundCompletion(t *testing.T) {
	request, preparedPayment := executorTestClaim(t)
	recoveredPayment, err := domain.NewPendingPayment(
		domain.PaymentID("pay_00000000-0000-4000-8000-000000000002"),
		"recovered-order",
		"recovered-customer",
		2599,
		"recovered-bank-key",
		"recovered-card-fingerprint",
		time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)
	store := &executorStore{claim: NewRecoveredPaymentCommand(request, recoveredPayment)}
	metrics := &executorMetrics{events: &store.events}

	execution, err := newExecutorForTest(store, metrics).execute(context.Background(), request, func(_ context.Context, payment *domain.Payment) (PaymentCommandResult, error) {
		store.events = append(store.events, "behavior:"+string(payment.ID()))
		assert.NotEqual(t, preparedPayment.ID(), payment.ID())
		return PaymentCommandResult{Payment: PaymentResult{ID: string(payment.ID())}}, nil
	})

	require.NoError(t, err)
	assert.Equal(t, string(recoveredPayment.ID()), execution.result.Payment.ID)
	assert.Equal(t, []string{
		"claim",
		"metric:attempted",
		"behavior:" + string(recoveredPayment.ID()),
		"complete",
		"metric:recovered",
	}, store.events)
}

func executorTestClaim(t *testing.T) (PaymentCommandClaimRequest, *domain.Payment) {
	t.Helper()
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	payment, err := domain.NewPendingPayment(
		domain.PaymentID("pay_00000000-0000-4000-8000-000000000001"),
		"order-1",
		"customer-1",
		1299,
		"bank-key",
		"card-fingerprint",
		now,
	)
	require.NoError(t, err)
	return NewAuthorizationStartClaim("key-1", "request-fingerprint", payment, now, time.Minute), payment
}

type executorStore struct {
	PaymentStore
	claim       PaymentCommandClaim
	claimErr    error
	completeErr error
	events      []string
}

func (s *executorStore) ClaimPaymentCommand(context.Context, PaymentCommandClaimRequest) (PaymentCommandClaim, error) {
	s.events = append(s.events, "claim")
	return s.claim, s.claimErr
}

func (s *executorStore) CompletePaymentCommand(context.Context, PaymentCommandClaim, PaymentCommandResult, time.Time) error {
	s.events = append(s.events, "complete")
	return s.completeErr
}

func (s *executorStore) ReleasePaymentCommand(context.Context, PaymentCommandClaim) error {
	s.events = append(s.events, "release")
	return nil
}

type executorMetrics struct {
	events *[]string
}

func (*executorMetrics) RecordPaymentOperation(string, string, time.Duration) {}

func (m *executorMetrics) RecordIdempotencyRecovery(_ string, result string) {
	if m.events != nil {
		*m.events = append(*m.events, "metric:"+result)
	}
}

type executorClock struct {
	now time.Time
}

func (c executorClock) Now() time.Time {
	return c.now
}

func newExecutorForTest(store PaymentStore, metrics PaymentOperationMetrics) paymentCommandExecutor {
	if metrics == nil {
		metrics = &executorMetrics{}
	}
	return paymentCommandExecutor{
		store:   store,
		metrics: metrics,
		clock:   executorClock{now: time.Date(2026, 7, 23, 10, 1, 0, 0, time.UTC)},
	}
}
