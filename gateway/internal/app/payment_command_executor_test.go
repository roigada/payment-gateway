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
	replay := PaymentCommandResult{Payment: PaymentResult{ID: "pay_550e8400-e29b-41d4-a716-446655440099"}, HTTPStatus: 201}
	request := executorClaimRequest(t)
	store := &executorStore{
		claim: NewReplayedPaymentCommand(request, replay),
	}
	executor := newTestPaymentCommandExecutor(store, &executorMetrics{})
	behaviorCalled := false

	execution, err := executor.execute(context.Background(), request, func(context.Context, *domain.Payment) claimDisposition {
		behaviorCalled = true
		return completeClaim(PaymentCommandResult{}, nil)
	})

	require.NoError(t, err)
	assert.Equal(t, replay, execution.result)
	assert.True(t, execution.replayed)
	assert.False(t, behaviorCalled)
	assert.Zero(t, store.completeCalls)
	assert.Zero(t, store.releaseCalls)
}

func TestPaymentCommandExecutorUsesRecoveredPaymentAndRecordsRecoveryAroundCompletion(t *testing.T) {
	request := executorClaimRequest(t)
	recoveredPayment := executorPayment(t, "pay_550e8400-e29b-41d4-a716-446655440098", "bok_recovered")
	events := []string{}
	store := &executorStore{
		claim: NewRecoveredPaymentCommand(request, recoveredPayment),
		onComplete: func() {
			events = append(events, "completed")
		},
	}
	metrics := &executorMetrics{onRecovery: func(result string) {
		events = append(events, result)
	}}
	executor := newTestPaymentCommandExecutor(store, metrics)

	execution, err := executor.execute(context.Background(), request, func(_ context.Context, payment *domain.Payment) claimDisposition {
		require.Same(t, recoveredPayment, payment)
		assert.Equal(t, "bok_recovered", payment.AuthorizationBankOperationKey())
		events = append(events, "behavior")
		return completeClaim(PaymentCommandResult{Payment: PaymentResult{ID: string(payment.ID())}, HTTPStatus: 201}, nil)
	})

	require.NoError(t, err)
	assert.Equal(t, "pay_550e8400-e29b-41d4-a716-446655440098", execution.result.Payment.ID)
	assert.Equal(t, []string{IdempotencyRecoveryAttempted, "behavior", "completed", IdempotencyRecoveryRecovered}, events)
}

func TestPaymentCommandExecutorReleasesBehaviorFailure(t *testing.T) {
	request := executorClaimRequest(t)
	store := &executorStore{claim: NewClaimedPaymentCommand(request, request.Payment())}
	executor := newTestPaymentCommandExecutor(store, &executorMetrics{})
	behaviorErr := NewPaymentBankUnavailableError(errors.New("bank unavailable"))

	_, err := executor.execute(context.Background(), request, func(context.Context, *domain.Payment) claimDisposition {
		return releaseClaim(behaviorErr)
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorBankUnavailable))
	assert.Equal(t, 1, store.releaseCalls)
	assert.Zero(t, store.completeCalls)
}

func TestPaymentCommandExecutorPreservesPrimaryFailureWhenClaimReleaseFails(t *testing.T) {
	request := executorClaimRequest(t)
	primaryErr := NewPaymentBankUnavailableError(errors.New("bank unavailable"))
	events := []string{}
	store := &executorStore{
		claim:      NewClaimedPaymentCommand(request, request.Payment()),
		releaseErr: NewInternalPaymentError(errors.New("release failed")),
		onRelease: func() {
			events = append(events, "released")
		},
	}
	executor := newTestPaymentCommandExecutor(store, &executorMetrics{})

	_, err := executor.execute(context.Background(), request, func(context.Context, *domain.Payment) claimDisposition {
		events = append(events, "behavior")
		return releaseClaim(primaryErr)
	})

	require.Error(t, err)
	assert.Same(t, primaryErr, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorBankUnavailable))
	assert.Equal(t, []string{"behavior", "released"}, events)
	assert.Equal(t, 1, store.releaseCalls)
	assert.Zero(t, store.completeCalls)
}

func TestPaymentCommandExecutorDoesNotInferClaimHandlingFromPaymentErrorKind(t *testing.T) {
	request := executorClaimRequest(t)
	store := &executorStore{claim: NewClaimedPaymentCommand(request, request.Payment())}
	executor := newTestPaymentCommandExecutor(store, &executorMetrics{})

	_, err := executor.execute(context.Background(), request, func(context.Context, *domain.Payment) claimDisposition {
		return releaseClaim(NewPaymentAuthorizationExpiredError(nil))
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorAuthorizationExpired))
	assert.Equal(t, 1, store.releaseCalls)
	assert.Zero(t, store.completeCalls)
}

func TestPaymentCommandExecutorRejectsMissingClaimDisposition(t *testing.T) {
	request := executorClaimRequest(t)
	store := &executorStore{claim: NewClaimedPaymentCommand(request, request.Payment())}
	executor := newTestPaymentCommandExecutor(store, &executorMetrics{})

	_, err := executor.execute(context.Background(), request, func(context.Context, *domain.Payment) claimDisposition {
		return claimDisposition{}
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorInternal))
	assert.Zero(t, store.releaseCalls)
	assert.Zero(t, store.completeCalls)
}

func TestPaymentCommandExecutorRejectsClaimReleaseWithoutError(t *testing.T) {
	request := executorClaimRequest(t)
	store := &executorStore{claim: NewClaimedPaymentCommand(request, request.Payment())}
	executor := newTestPaymentCommandExecutor(store, &executorMetrics{})

	_, err := executor.execute(context.Background(), request, func(context.Context, *domain.Payment) claimDisposition {
		return releaseClaim(nil)
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorInternal))
	assert.Zero(t, store.releaseCalls)
	assert.Zero(t, store.completeCalls)
}

func TestPaymentCommandExecutorRejectsClaimPreservationWithoutError(t *testing.T) {
	request := executorClaimRequest(t)
	store := &executorStore{claim: NewClaimedPaymentCommand(request, request.Payment())}
	executor := newTestPaymentCommandExecutor(store, &executorMetrics{})

	_, err := executor.execute(context.Background(), request, func(context.Context, *domain.Payment) claimDisposition {
		return preserveClaim(nil)
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorInternal))
	assert.Zero(t, store.releaseCalls)
	assert.Zero(t, store.completeCalls)
}

func TestPaymentCommandExecutorPreservesDefinitiveFailureWithoutReplaySnapshot(t *testing.T) {
	request := executorClaimRequest(t)
	store := &executorStore{claim: NewRecoveredPaymentCommand(request, request.Payment())}
	metrics := &executorMetrics{}
	executor := newTestPaymentCommandExecutor(store, metrics)

	_, err := executor.execute(context.Background(), request, func(context.Context, *domain.Payment) claimDisposition {
		return preserveClaim(errors.New("payment transition failed"))
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorInternal))
	assert.Zero(t, store.releaseCalls)
	assert.Zero(t, store.completeCalls)
	assert.Equal(t, []string{IdempotencyRecoveryAttempted}, metrics.recoveryResults)
}

func TestPaymentCommandExecutorCompletesAuthorizationExpirationBeforeReturningError(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	payment := executorAuthorizedPayment(t, now)
	request := NewCaptureClaim("public-key", "fingerprint", payment.ID(), "bok_capture", now, 5*time.Minute)
	store := &executorStore{claim: NewRecoveredPaymentCommand(request, payment)}
	metrics := &executorMetrics{}
	executor := newTestPaymentCommandExecutor(store, metrics)

	_, err := executor.execute(context.Background(), request, func(_ context.Context, payment *domain.Payment) claimDisposition {
		require.NoError(t, payment.MarkExpired(now))
		return completeClaim(
			PaymentCommandResult{Payment: newPaymentResult(payment), HTTPStatus: 409},
			NewPaymentAuthorizationExpiredError(errors.New("bank reports expired")),
		)
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorAuthorizationExpired))
	assert.Equal(t, domain.PaymentStatusExpired, payment.Status())
	assert.Equal(t, 1, store.completeCalls)
	assert.Equal(t, 409, store.completedResult.HTTPStatus)
	assert.Equal(t, string(domain.PaymentStatusExpired), store.completedResult.Payment.Status)
	assert.Zero(t, store.releaseCalls)
	assert.Equal(t, []string{IdempotencyRecoveryAttempted, IdempotencyRecoveryRecovered}, metrics.recoveryResults)
}

func TestPaymentCommandExecutorPreservesExpiredClaimWhenCompletionFails(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	payment := executorAuthorizedPayment(t, now)
	request := NewVoidClaim("public-key", "fingerprint", payment.ID(), "bok_void", now, 5*time.Minute)
	store := &executorStore{
		claim:       NewRecoveredPaymentCommand(request, payment),
		completeErr: NewInternalPaymentError(errors.New("completion failed")),
	}
	metrics := &executorMetrics{}
	executor := newTestPaymentCommandExecutor(store, metrics)

	_, err := executor.execute(context.Background(), request, func(_ context.Context, payment *domain.Payment) claimDisposition {
		require.NoError(t, payment.MarkExpired(now))
		return completeClaim(
			PaymentCommandResult{Payment: newPaymentResult(payment), HTTPStatus: 409},
			NewPaymentAuthorizationExpiredError(nil),
		)
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorInternal))
	assert.Equal(t, 1, store.completeCalls)
	assert.Zero(t, store.releaseCalls)
	assert.Equal(t, []string{IdempotencyRecoveryAttempted}, metrics.recoveryResults)
}

func TestPaymentCommandExecutorPreservesClaimWhenCompletionFails(t *testing.T) {
	request := executorClaimRequest(t)
	completionErr := NewInternalPaymentError(errors.New("completion failed"))
	store := &executorStore{
		claim:       NewRecoveredPaymentCommand(request, request.Payment()),
		completeErr: completionErr,
	}
	metrics := &executorMetrics{}
	executor := newTestPaymentCommandExecutor(store, metrics)

	_, err := executor.execute(context.Background(), request, func(context.Context, *domain.Payment) claimDisposition {
		return completeClaim(PaymentCommandResult{HTTPStatus: 201}, nil)
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorInternal))
	assert.Equal(t, 1, store.completeCalls)
	assert.Zero(t, store.releaseCalls)
	assert.Equal(t, []string{IdempotencyRecoveryAttempted}, metrics.recoveryResults)
}

func TestPaymentCommandExecutorRecordsUnrecoverableClaimFailureInOrder(t *testing.T) {
	request := executorClaimRequest(t)
	store := &executorStore{
		claimErr: NewIdempotencyRecoveryError(
			IdempotencyRecoveryUnrecoverable,
			NewInternalPaymentError(errors.New("recovery facts are incomplete")),
		),
	}
	metrics := &executorMetrics{}
	executor := newTestPaymentCommandExecutor(store, metrics)

	_, err := executor.execute(context.Background(), request, func(context.Context, *domain.Payment) claimDisposition {
		require.FailNow(t, "behavior must not run after unrecoverable claim failure")
		return completeClaim(PaymentCommandResult{}, nil)
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorInternal))
	assert.Equal(t, []string{IdempotencyRecoveryAttempted, IdempotencyRecoveryUnrecoverable}, metrics.recoveryResults)
	assert.Zero(t, store.completeCalls)
	assert.Zero(t, store.releaseCalls)
}

func TestPaymentCommandExecutorRecordsRecoveryClaimFailureInOrder(t *testing.T) {
	request := executorClaimRequest(t)
	store := &executorStore{
		claimErr: NewIdempotencyRecoveryError(
			IdempotencyRecoveryConflict,
			NewPaymentIdempotencyConflictError(nil),
		),
	}
	metrics := &executorMetrics{}
	executor := newTestPaymentCommandExecutor(store, metrics)

	_, err := executor.execute(context.Background(), request, func(context.Context, *domain.Payment) claimDisposition {
		require.FailNow(t, "behavior must not run after claim failure")
		return completeClaim(PaymentCommandResult{}, nil)
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorIdempotencyConflict))
	assert.Equal(t, []string{IdempotencyRecoveryAttempted, IdempotencyRecoveryConflict}, metrics.recoveryResults)
	assert.Zero(t, store.completeCalls)
	assert.Zero(t, store.releaseCalls)
}

func newTestPaymentCommandExecutor(store paymentCommandStore, metrics idempotencyRecoveryRecorder) paymentCommandExecutor {
	return paymentCommandExecutor{
		store:   store,
		metrics: metrics,
		clock:   executorClock{now: time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)},
	}
}

func executorClaimRequest(t *testing.T) PaymentCommandClaimRequest {
	t.Helper()
	now := time.Date(2026, 7, 23, 11, 0, 0, 0, time.UTC)
	return NewAuthorizationStartClaim("public-key", "fingerprint", executorPayment(t, "pay_550e8400-e29b-41d4-a716-446655440097", "bok_new"), now, 5*time.Minute)
}

func executorPayment(t *testing.T, id domain.PaymentID, operationKey string) *domain.Payment {
	t.Helper()
	payment, err := domain.NewPendingPayment(id, "order-1", "customer-1", 1299, operationKey, "card-fingerprint", time.Date(2026, 7, 23, 11, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	return payment
}

func executorAuthorizedPayment(t *testing.T, now time.Time) *domain.Payment {
	t.Helper()
	payment, err := domain.NewAuthorizedPayment(
		"pay_550e8400-e29b-41d4-a716-446655440096",
		"order-1",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440096",
		now.Add(time.Hour),
		"bok_authorize",
		"card-fingerprint",
		now.Add(-time.Hour),
	)
	require.NoError(t, err)
	return payment
}

type executorStore struct {
	claim           PaymentCommandClaim
	claimErr        error
	completeErr     error
	releaseErr      error
	completeCalls   int
	releaseCalls    int
	onComplete      func()
	onRelease       func()
	completedResult PaymentCommandResult
}

func (s *executorStore) ClaimPaymentCommand(context.Context, PaymentCommandClaimRequest) (PaymentCommandClaim, error) {
	return s.claim, s.claimErr
}

func (s *executorStore) CompletePaymentCommand(_ context.Context, _ PaymentCommandClaim, result PaymentCommandResult, _ time.Time) error {
	s.completeCalls++
	s.completedResult = result
	if s.onComplete != nil {
		s.onComplete()
	}
	return s.completeErr
}

func (s *executorStore) ReleasePaymentCommand(context.Context, PaymentCommandClaim) error {
	s.releaseCalls++
	if s.onRelease != nil {
		s.onRelease()
	}
	return s.releaseErr
}

type executorMetrics struct {
	recoveryResults []string
	onRecovery      func(string)
}

func (m *executorMetrics) RecordIdempotencyRecovery(_ string, result string) {
	m.recoveryResults = append(m.recoveryResults, result)
	if m.onRecovery != nil {
		m.onRecovery(result)
	}
}

type executorClock struct {
	now time.Time
}

func (c executorClock) Now() time.Time {
	return c.now
}
