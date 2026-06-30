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

func TestPaymentCommandRunnerReplaysCompletedClaim(t *testing.T) {
	store := &paymentCommandRunnerStoreFake{}
	runner := paymentCommandRunner{store: store}
	replayed := PaymentCommandResult{HTTPStatus: 200}
	handled := false

	result, err := runner.run(context.Background(), paymentCommandRun{
		operation:          CapturePaymentOperation,
		key:                "public-key-1",
		requestFingerprint: "fingerprint-1",
		expectedStatus:     domain.PaymentStatusAuthorized,
		claim: func(context.Context) (PaymentCommandClaim, error) {
			return PaymentCommandClaim{
				Record: IdempotencyRecord{
					Operation:          CapturePaymentOperation,
					Key:                "public-key-1",
					RequestFingerprint: "fingerprint-1",
					Result:             replayed,
				},
				Status: IdempotencyCompleted,
			}, nil
		},
		handle: func(context.Context, *domain.Payment) (paymentCommandOutcome, error) {
			handled = true
			return paymentCommandOutcome{}, nil
		},
	})

	require.NoError(t, err)
	assert.Equal(t, replayed, result)
	assert.False(t, handled)
	assert.False(t, store.completed)
}

func TestPaymentCommandRunnerRejectsFingerprintMismatch(t *testing.T) {
	store := &paymentCommandRunnerStoreFake{}
	runner := paymentCommandRunner{store: store}

	_, err := runner.run(context.Background(), paymentCommandRun{
		operation:          CapturePaymentOperation,
		key:                "public-key-1",
		requestFingerprint: "fingerprint-2",
		expectedStatus:     domain.PaymentStatusAuthorized,
		claim: func(context.Context) (PaymentCommandClaim, error) {
			return PaymentCommandClaim{
				Record: IdempotencyRecord{
					Operation:          CapturePaymentOperation,
					Key:                "public-key-1",
					RequestFingerprint: "fingerprint-1",
				},
				Status: IdempotencyCompleted,
			}, nil
		},
		handle: func(context.Context, *domain.Payment) (paymentCommandOutcome, error) {
			return paymentCommandOutcome{}, nil
		},
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorIdempotencyConflict))
	assert.False(t, store.completed)
}

func TestPaymentCommandRunnerRejectsInProgressClaim(t *testing.T) {
	store := &paymentCommandRunnerStoreFake{}
	runner := paymentCommandRunner{store: store}

	_, err := runner.run(context.Background(), paymentCommandRun{
		operation:          CapturePaymentOperation,
		key:                "public-key-1",
		requestFingerprint: "fingerprint-1",
		expectedStatus:     domain.PaymentStatusAuthorized,
		claim: func(context.Context) (PaymentCommandClaim, error) {
			return PaymentCommandClaim{
				Record: IdempotencyRecord{
					Operation:          CapturePaymentOperation,
					Key:                "public-key-1",
					RequestFingerprint: "fingerprint-1",
				},
				Status: IdempotencyInProgress,
			}, nil
		},
		handle: func(context.Context, *domain.Payment) (paymentCommandOutcome, error) {
			return paymentCommandOutcome{}, nil
		},
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorIdempotencyInProgress))
	assert.False(t, store.completed)
}

func TestPaymentCommandRunnerReleasesClaimOnNonCompleteError(t *testing.T) {
	store := &paymentCommandRunnerStoreFake{}
	runner := paymentCommandRunner{store: store}
	payment := mustRunnerAuthorizedPayment(t)
	bankErr := NewPaymentBankTimeoutError(context.DeadlineExceeded)

	_, err := runner.run(context.Background(), paymentCommandRun{
		operation:          CapturePaymentOperation,
		key:                "public-key-1",
		requestFingerprint: "fingerprint-1",
		expectedStatus:     domain.PaymentStatusAuthorized,
		claim: func(context.Context) (PaymentCommandClaim, error) {
			return PaymentCommandClaim{
				Record: IdempotencyRecord{
					Operation:          CapturePaymentOperation,
					Key:                "public-key-1",
					RequestFingerprint: "fingerprint-1",
				},
				Status:  IdempotencyClaimed,
				Payment: payment,
			}, nil
		},
		handle: func(context.Context, *domain.Payment) (paymentCommandOutcome, error) {
			return paymentCommandOutcome{}, bankErr
		},
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorBankTimeout))
	assert.False(t, store.completed)
	assert.Equal(t, CapturePaymentOperation, store.releasedOperation)
	assert.Equal(t, "public-key-1", store.releasedKey)
}

func TestPaymentCommandRunnerCompletesSuccessfulOutcome(t *testing.T) {
	store := &paymentCommandRunnerStoreFake{}
	runner := paymentCommandRunner{store: store}
	payment := mustRunnerAuthorizedPayment(t)

	result, err := runner.run(context.Background(), paymentCommandRun{
		operation:          CapturePaymentOperation,
		key:                "public-key-1",
		requestFingerprint: "fingerprint-1",
		expectedStatus:     domain.PaymentStatusAuthorized,
		claim: func(context.Context) (PaymentCommandClaim, error) {
			return PaymentCommandClaim{
				Record: IdempotencyRecord{
					Operation:          CapturePaymentOperation,
					Key:                "public-key-1",
					RequestFingerprint: "fingerprint-1",
				},
				Status:  IdempotencyClaimed,
				Payment: payment,
			}, nil
		},
		handle: func(context.Context, *domain.Payment) (paymentCommandOutcome, error) {
			require.NoError(t, payment.Capture("cap_1", "bok_capture", time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC)))
			return paymentCommandOutcome{httpStatus: 200}, nil
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "captured", result.Payment.Status)
	assert.Equal(t, 200, result.HTTPStatus)
	require.True(t, store.completed)
	assert.Equal(t, CapturePaymentOperation, store.completedRecord.Operation)
	assert.Equal(t, "public-key-1", store.completedRecord.Key)
	assert.Equal(t, "fingerprint-1", store.completedRecord.RequestFingerprint)
	assert.Equal(t, result, store.completedRecord.Result)
	assert.Equal(t, domain.PaymentStatusAuthorized, store.completedExpectedStatus)
	assert.Empty(t, store.releasedOperation)
}

func TestPaymentCommandRunnerCompletesBeforeReturningDefinitiveCallerError(t *testing.T) {
	store := &paymentCommandRunnerStoreFake{}
	runner := paymentCommandRunner{store: store}
	payment := mustRunnerAuthorizedPayment(t)
	now := time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC)

	_, err := runner.run(context.Background(), paymentCommandRun{
		operation:          CapturePaymentOperation,
		key:                "public-key-1",
		requestFingerprint: "fingerprint-1",
		expectedStatus:     domain.PaymentStatusAuthorized,
		claim: func(context.Context) (PaymentCommandClaim, error) {
			return PaymentCommandClaim{
				Record: IdempotencyRecord{
					Operation:          CapturePaymentOperation,
					Key:                "public-key-1",
					RequestFingerprint: "fingerprint-1",
				},
				Status:  IdempotencyClaimed,
				Payment: payment,
			}, nil
		},
		handle: func(context.Context, *domain.Payment) (paymentCommandOutcome, error) {
			require.NoError(t, payment.MarkExpired(now))
			return paymentCommandOutcome{
				httpStatus:       409,
				returnAfterError: NewPaymentInvalidStatusConflictError(nil),
			}, nil
		},
	})

	require.Error(t, err)
	assert.True(t, HasPaymentErrorKind(err, PaymentErrorInvalidStatusConflict))
	require.True(t, store.completed)
	assert.Equal(t, "expired", store.completedRecord.Result.Payment.Status)
	assert.Equal(t, 409, store.completedRecord.Result.HTTPStatus)
	assert.Empty(t, store.releasedOperation)
}

type paymentCommandRunnerStoreFake struct {
	completed               bool
	completedRecord         IdempotencyRecord
	completedExpectedStatus domain.PaymentStatus
	releasedOperation       string
	releasedKey             string
}

func (s *paymentCommandRunnerStoreFake) FindByID(context.Context, domain.PaymentID) (*domain.Payment, error) {
	return nil, errors.New("unexpected FindByID call")
}

func (s *paymentCommandRunnerStoreFake) ExpireAuthorization(context.Context, *domain.Payment, domain.PaymentStatus) error {
	return errors.New("unexpected ExpireAuthorization call")
}

func (s *paymentCommandRunnerStoreFake) RefreshExpiredAuthorizations(context.Context, SearchPaymentsQuery, time.Time) error {
	return errors.New("unexpected RefreshExpiredAuthorizations call")
}

func (s *paymentCommandRunnerStoreFake) Search(context.Context, SearchPaymentsQuery) ([]*domain.Payment, error) {
	return nil, errors.New("unexpected Search call")
}

func (s *paymentCommandRunnerStoreFake) ClaimAuthorizationStart(context.Context, ClaimAuthorizationStartInput) (PaymentCommandClaim, error) {
	return PaymentCommandClaim{}, errors.New("unexpected ClaimAuthorizationStart call")
}

func (s *paymentCommandRunnerStoreFake) ClaimAuthorizationRetry(context.Context, ClaimAuthorizationRetryInput) (PaymentCommandClaim, error) {
	return PaymentCommandClaim{}, errors.New("unexpected ClaimAuthorizationRetry call")
}

func (s *paymentCommandRunnerStoreFake) ClaimCapture(context.Context, ClaimCaptureInput) (PaymentCommandClaim, error) {
	return PaymentCommandClaim{}, errors.New("unexpected ClaimCapture call")
}

func (s *paymentCommandRunnerStoreFake) ClaimVoid(context.Context, ClaimVoidInput) (PaymentCommandClaim, error) {
	return PaymentCommandClaim{}, errors.New("unexpected ClaimVoid call")
}

func (s *paymentCommandRunnerStoreFake) ClaimRefund(context.Context, ClaimRefundInput) (PaymentCommandClaim, error) {
	return PaymentCommandClaim{}, errors.New("unexpected ClaimRefund call")
}

func (s *paymentCommandRunnerStoreFake) CompletePaymentCommand(_ context.Context, record IdempotencyRecord, _ *domain.Payment, expectedStatus domain.PaymentStatus) error {
	s.completed = true
	s.completedRecord = record
	s.completedExpectedStatus = expectedStatus
	return nil
}

func (s *paymentCommandRunnerStoreFake) ReleasePaymentCommand(_ context.Context, operation string, key string) error {
	s.releasedOperation = operation
	s.releasedKey = key
	return nil
}

func mustRunnerAuthorizedPayment(t *testing.T) *domain.Payment {
	t.Helper()
	payment, err := domain.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"auth_1",
		time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC),
		"bok_authorize",
		"fingerprint-1",
		time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)
	return payment
}
