package app_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/roigada/payment-gateway/internal/testsupport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cardInput struct {
	number      string
	cvv         string
	expiryMonth int
	expiryYear  int
}

const testIdempotencyClaimStuckAfter = 5 * time.Minute

var testNonExpiringStoreReadTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

func TestAuthorizePaymentCallsBankStoresAuthorizedPaymentAndReturnsPublicResult(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, bank, now)

	result, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	assert.Equal(t, app.BankAuthorizationRequest{
		OperationKey:    "bok_123",
		OrderID:         "order-1",
		CustomerID:      "customer-1",
		AmountCents:     1299,
		Currency:        "USD",
		CardNumber:      "4111111111111111",
		CardCVV:         "123",
		CardExpiryMonth: 12,
		CardExpiryYear:  2030,
	}, bank.request)
	assert.Equal(t, app.PaymentResult{
		ID:                     "pay_550e8400-e29b-41d4-a716-446655440000",
		OrderID:                "order-1",
		CustomerID:             "customer-1",
		AmountCents:            1299,
		Currency:               "USD",
		Status:                 "authorized",
		AuthorizationExpiresAt: defaultAuthorizationExpiresAt(),
		CreatedAt:              now,
		UpdatedAt:              now,
	}, result.Payment)
	assert.Equal(t, http.StatusCreated, result.HTTPStatus)

	saved, err := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"), testNonExpiringStoreReadTime)
	require.NoError(t, err)
	assert.Equal(t, "bank-auth-id-1", saved.BankAuthorizationID())
	assert.Equal(t, "bok_123", saved.AuthorizationBankOperationKey())
}

func TestAuthorizePaymentReplaysBeforeRetentionCutoffAndStartsNewCommandAfterCleanup(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	firstBank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	firstService := newPaymentService(repo, firstBank, now)

	first, err := firstService.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	removed, err := repo.CleanupCompletedIdempotencyRecords(context.Background(), now)
	require.NoError(t, err)
	assert.Zero(t, removed)
	replayed, err := firstService.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)
	assert.Equal(t, first, replayed)
	assert.Equal(t, 1, firstBank.calls)

	removed, err = repo.CleanupCompletedIdempotencyRecords(context.Background(), now.Add(time.Microsecond))
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	secondBank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-2"}}
	secondService := app.NewPaymentService(
		repo,
		testsupport.FixedPaymentIDGenerator{ID: domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440001")},
		testsupport.FixedBankOperationKeyGenerator{Key: "bok_124"},
		secondBank,
		&paymentOperationMetricsFake{},
		testsupport.FixedClock{Time: now.Add(time.Microsecond)},
		"fingerprint-secret",
		testIdempotencyClaimStuckAfter,
	)

	second, err := secondService.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)
	assert.Equal(t, "pay_550e8400-e29b-41d4-a716-446655440001", second.Payment.ID)
	assert.Equal(t, 1, secondBank.calls)
}

func TestAuthorizePaymentPersistsPendingPaymentBeforeCallingBank(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	bank.onAuthorize = func() {
		saved, err := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"), testNonExpiringStoreReadTime)
		require.NoError(t, err)
		assert.Equal(t, domain.PaymentStatusPending, saved.Status())
		assert.Equal(t, "bok_123", saved.AuthorizationBankOperationKey())
		assert.NotEmpty(t, saved.AuthorizationCardFingerprint())
	}
	service := newPaymentService(repo, bank, now)

	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())

	require.NoError(t, err)
	assert.Equal(t, 1, bank.calls)
}

func TestAuthorizePaymentStoresDeclinedPaymentAndReturnsPublicResult(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{DeclineReason: domain.DeclineReasonInsufficientFunds}}
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, bank, now)

	result, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	assert.Equal(t, app.PaymentResult{
		ID:            "pay_550e8400-e29b-41d4-a716-446655440000",
		OrderID:       "order-1",
		CustomerID:    "customer-1",
		AmountCents:   1299,
		Currency:      "USD",
		Status:        "declined",
		DeclineReason: "insufficient_funds",
		CreatedAt:     now,
		UpdatedAt:     now,
	}, result.Payment)
	assert.Equal(t, http.StatusCreated, result.HTTPStatus)

	saved, err := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"), testNonExpiringStoreReadTime)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusDeclined, saved.Status())
	assert.Equal(t, domain.DeclineReasonInsufficientFunds, saved.DeclineReason())
	assert.Empty(t, saved.BankAuthorizationID())
	assert.Equal(t, "bok_123", saved.AuthorizationBankOperationKey())
}

func TestAuthorizePaymentReturnsPendingPaymentForUnknownBankOutcome(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	bankErr := app.NewPaymentBankTimeoutError(context.DeadlineExceeded)
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, &bankAuthorizerFake{err: bankErr}, now)

	result, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())

	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, result.HTTPStatus)
	assert.Equal(t, app.PaymentResult{
		ID:          "pay_550e8400-e29b-41d4-a716-446655440000",
		OrderID:     "order-1",
		CustomerID:  "customer-1",
		AmountCents: 1299,
		Currency:    "USD",
		Status:      "pending",
		CreatedAt:   now,
		UpdatedAt:   now,
	}, result.Payment)

	replayed, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)
	assert.Equal(t, result, replayed)

	saved, err := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"), testNonExpiringStoreReadTime)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusPending, saved.Status())
	assert.Equal(t, "bok_123", saved.AuthorizationBankOperationKey())
	assert.NotEmpty(t, saved.AuthorizationCardFingerprint())
	assert.NotContains(t, saved.AuthorizationCardFingerprint(), "4111111111111111")
	assert.NotContains(t, saved.AuthorizationCardFingerprint(), "123")
}

func TestAuthorizePaymentStoresExpiredPaymentWhenBankAuthorizationAlreadyExpired(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{
		BankAuthorizationID:    "bank-auth-id-1",
		AuthorizationExpiresAt: now,
	}}
	service := newPaymentService(repo, bank, now)

	result, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	assert.Equal(t, "expired", result.Payment.Status)
	assert.Equal(t, now, result.Payment.AuthorizationExpiresAt)
	saved, err := repo.FindByID(context.Background(), domain.PaymentID(result.Payment.ID), testNonExpiringStoreReadTime)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusExpired, saved.Status())
}

func TestAuthorizePaymentStoresCardOnlyFingerprint(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	firstRepo := testsupport.NewPaymentStore()
	firstService := newPaymentService(firstRepo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailableError(errors.New("500"))}, now)
	_, err := firstService.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	secondRepo := testsupport.NewPaymentStore()
	secondService := newPaymentService(secondRepo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailableError(errors.New("500"))}, now)
	secondCommand, err := newAuthorizePaymentCommand("order-1", "customer-1", 2599, validCardDetails(), "public-key-1")
	require.NoError(t, err)
	_, err = secondService.AuthorizePayment(context.Background(), secondCommand)
	require.NoError(t, err)

	firstSaved, err := firstRepo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"), testNonExpiringStoreReadTime)
	require.NoError(t, err)
	secondSaved, err := secondRepo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"), testNonExpiringStoreReadTime)
	require.NoError(t, err)
	assert.Equal(t, firstSaved.AuthorizationCardFingerprint(), secondSaved.AuthorizationCardFingerprint())
}

func TestAuthorizePaymentRecoversStuckClaimUsingOriginalPendingPaymentAndReplays(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	crashingBank := &bankAuthorizerFake{}
	crashingBank.onAuthorize = func() {
		panic("process crashed after claim")
	}
	crashingService := newPaymentServiceWithBankOperationKeys(repo, crashingBank, now.Add(-10*time.Minute), &sequenceBankOperationKeyGenerator{keys: []string{"bok_original"}})
	assert.PanicsWithValue(t, "process crashed after claim", func() {
		_, _ = crashingService.AuthorizePayment(context.Background(), validAuthorizeCommand())
	})
	repo.AgeClaim(app.AuthorizePaymentOperation, "public-key-1", now.Add(-6*time.Minute))

	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	service := newPaymentServiceWithBankOperationKeys(repo, bank, now, &sequenceBankOperationKeyGenerator{keys: []string{"bok_new", "bok_newer"}})

	result, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	assert.Equal(t, "pay_550e8400-e29b-41d4-a716-446655440000", result.Payment.ID)
	assert.Equal(t, "authorized", result.Payment.Status)
	assert.Equal(t, http.StatusCreated, result.HTTPStatus)
	assert.Equal(t, "bok_original", bank.request.OperationKey)
	assert.Equal(t, 1, bank.calls)

	replayed, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)
	assert.Equal(t, result, replayed)
	assert.Equal(t, 1, bank.calls)
}

func TestRetryAuthorizationResolvesPendingPaymentToAuthorized(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailableError(errors.New("500"))}, now)
	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	service = newPaymentService(repo, bank, now.Add(time.Minute))
	retry := validRetryAuthorizationCommand("pay_550e8400-e29b-41d4-a716-446655440000")

	result, err := service.RetryAuthorization(context.Background(), retry)
	require.NoError(t, err)

	assert.Equal(t, "authorized", result.Payment.Status)
	assert.Equal(t, now.Add(time.Minute), result.Payment.UpdatedAt)
	assert.Equal(t, http.StatusOK, result.HTTPStatus)
	assert.Equal(t, app.BankAuthorizationRequest{
		OperationKey:    "bok_123",
		OrderID:         "order-1",
		CustomerID:      "customer-1",
		AmountCents:     1299,
		Currency:        "USD",
		CardNumber:      "4111111111111111",
		CardCVV:         "123",
		CardExpiryMonth: 12,
		CardExpiryYear:  2030,
	}, bank.request)
}

func TestRetryAuthorizationResolvesPendingPaymentToExpiredWhenApprovedAuthorizationAlreadyExpired(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailableError(errors.New("500"))}, now)
	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{
		BankAuthorizationID:    "bank-auth-id-1",
		AuthorizationExpiresAt: now.Add(time.Minute),
	}}
	service = newPaymentService(repo, bank, now.Add(time.Minute))

	result, err := service.RetryAuthorization(context.Background(), validRetryAuthorizationCommand("pay_550e8400-e29b-41d4-a716-446655440000"))
	require.NoError(t, err)

	assert.Equal(t, "expired", result.Payment.Status)
	assert.Equal(t, now.Add(time.Minute), result.Payment.AuthorizationExpiresAt)
}

func TestRetryAuthorizationResolvesPendingPaymentToDeclined(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailableError(errors.New("500"))}, now)
	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{DeclineReason: domain.DeclineReasonInvalidCard}}
	service = newPaymentService(repo, bank, now.Add(time.Minute))

	result, err := service.RetryAuthorization(context.Background(), validRetryAuthorizationCommand("pay_550e8400-e29b-41d4-a716-446655440000"))
	require.NoError(t, err)

	assert.Equal(t, "declined", result.Payment.Status)
	assert.Equal(t, "invalid_card", result.Payment.DeclineReason)
	assert.Equal(t, 1, bank.calls)
}

func TestRetryAuthorizationLeavesPendingPaymentPendingAndReleasesClaimForUnknownBankOutcome(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailableError(errors.New("500"))}, now)
	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	bank := &bankAuthorizerFake{err: app.NewPaymentBankTimeoutError(context.DeadlineExceeded)}
	service = newPaymentService(repo, bank, now.Add(time.Minute))

	_, err = service.RetryAuthorization(context.Background(), validRetryAuthorizationCommand("pay_550e8400-e29b-41d4-a716-446655440000"))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout))
	saved, findErr := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"), testNonExpiringStoreReadTime)
	require.NoError(t, findErr)
	assert.Equal(t, domain.PaymentStatusPending, saved.Status())
	assert.Equal(t, now, saved.UpdatedAt())
	assert.Equal(t, 1, bank.calls)
}

func TestRetryAuthorizationRejectsFingerprintMismatchWithoutCallingBank(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	service := newPaymentService(repo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailableError(errors.New("500"))}, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	service = newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 1, 0, 0, time.UTC))
	retryCard := validCardDetails()
	retryCard.number = "4000000000000002"
	retry, err := newRetryAuthorizationCommand("pay_550e8400-e29b-41d4-a716-446655440000", retryCard, "retry-key-1")
	require.NoError(t, err)

	_, err = service.RetryAuthorization(context.Background(), retry)

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorPaymentStatusConflict))
	assert.Zero(t, bank.calls)
}

func TestRetryAuthorizationRejectsNonPendingPaymentWithoutCallingBank(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	service := newPaymentService(repo, &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	authorized, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-2"}}
	service = newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 1, 0, 0, time.UTC))

	_, err = service.RetryAuthorization(context.Background(), validRetryAuthorizationCommand(authorized.Payment.ID))

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorPaymentStatusConflict))
	assert.Zero(t, bank.calls)
}

func TestRetryAuthorizationRecoversStuckClaimUsingAuthorizationBankOperationKeyAndReplays(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentServiceWithBankOperationKeys(repo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailableError(errors.New("500"))}, now, &sequenceBankOperationKeyGenerator{keys: []string{"bok_original"}})
	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	crashingBank := &bankAuthorizerFake{}
	crashingBank.onAuthorize = func() {
		panic("process crashed after retry claim")
	}
	crashingService := newPaymentService(repo, crashingBank, now.Add(time.Minute))
	require.PanicsWithValue(t, "process crashed after retry claim", func() {
		_, _ = crashingService.RetryAuthorization(context.Background(), validRetryAuthorizationCommand("pay_550e8400-e29b-41d4-a716-446655440000"))
	})
	repo.AgeClaim(app.RetryAuthorizationOperation, "retry-key-1", now.Add(-6*time.Minute))

	metrics := &paymentOperationMetricsFake{}
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	recoveryService := newPaymentServiceWithBankOperationKeysAndMetrics(repo, bank, now, &sequenceBankOperationKeyGenerator{keys: []string{"bok_new"}}, metrics)
	result, err := recoveryService.RetryAuthorization(context.Background(), validRetryAuthorizationCommand("pay_550e8400-e29b-41d4-a716-446655440000"))
	require.NoError(t, err)

	assert.Equal(t, "authorized", result.Payment.Status)
	assert.Equal(t, "bok_original", bank.request.OperationKey)
	assert.Equal(t, []idempotencyRecoveryMetricRecord{
		{operation: app.RetryAuthorizationOperation, result: app.IdempotencyRecoveryAttempted},
		{operation: app.RetryAuthorizationOperation, result: app.IdempotencyRecoveryRecovered},
	}, metrics.recoveryRecords)

	replayed, err := recoveryService.RetryAuthorization(context.Background(), validRetryAuthorizationCommand("pay_550e8400-e29b-41d4-a716-446655440000"))
	require.NoError(t, err)
	assert.Equal(t, result, replayed)
	assert.Equal(t, 1, bank.calls)
}

func TestRetryAuthorizationRecoveredCardFingerprintMismatchIsIdempotencyConflict(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentServiceWithBankOperationKeys(repo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailableError(errors.New("500"))}, now, &sequenceBankOperationKeyGenerator{keys: []string{"bok_original"}})
	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	crashingBank := &bankAuthorizerFake{}
	crashingBank.onAuthorize = func() {
		panic("process crashed after retry claim")
	}
	crashingService := newPaymentService(repo, crashingBank, now.Add(time.Minute))
	require.Panics(t, func() {
		_, _ = crashingService.RetryAuthorization(context.Background(), validRetryAuthorizationCommand("pay_550e8400-e29b-41d4-a716-446655440000"))
	})
	repo.AgeClaim(app.RetryAuthorizationOperation, "retry-key-1", now.Add(-6*time.Minute))

	saved, err := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"), testNonExpiringStoreReadTime)
	require.NoError(t, err)
	corrupted, err := domain.LoadPayment(
		saved.ID(),
		saved.OrderID(),
		saved.CustomerID(),
		saved.AmountCents(),
		saved.Currency(),
		saved.Status(),
		saved.BankAuthorizationID(),
		saved.AuthorizationExpiresAt(),
		saved.AuthorizationBankOperationKey(),
		"different-card-fingerprint",
		saved.BankCaptureID(),
		saved.CaptureBankOperationKey(),
		saved.BankRefundID(),
		saved.RefundBankOperationKey(),
		saved.BankVoidID(),
		saved.VoidBankOperationKey(),
		saved.DeclineReason(),
		saved.CreatedAt(),
		saved.UpdatedAt(),
	)
	require.NoError(t, err)
	require.NoError(t, repo.ReplacePayment(corrupted))

	metrics := &paymentOperationMetricsFake{}
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	recoveryService := newPaymentServiceWithMetrics(repo, bank, now, metrics)
	_, err = recoveryService.RetryAuthorization(context.Background(), validRetryAuthorizationCommand("pay_550e8400-e29b-41d4-a716-446655440000"))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	assert.Zero(t, bank.calls)
	assert.Equal(t, []idempotencyRecoveryMetricRecord{
		{operation: app.RetryAuthorizationOperation, result: app.IdempotencyRecoveryAttempted},
		{operation: app.RetryAuthorizationOperation, result: app.IdempotencyRecoveryConflict},
	}, metrics.recoveryRecords)
}

func TestAuthorizePaymentReplaysDeclinedPaymentForSameIdempotencyKeyAndRequest(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{DeclineReason: domain.DeclineReasonInvalidCard}}
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, bank, now)

	first, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)
	bank.result = app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-2"}

	replayed, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	assert.Equal(t, first, replayed)
	assert.Equal(t, 1, bank.calls)
}

func TestPaymentOperationsRecordSuccessAndReplayOutcomes(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		outcome   string
		run       func(t *testing.T) (*paymentOperationMetricsFake, func())
	}{
		{
			name:      "authorize",
			operation: app.AuthorizePaymentOperation,
			outcome:   "authorized",
			run: func(t *testing.T) (*paymentOperationMetricsFake, func()) {
				t.Helper()
				repo := testsupport.NewPaymentStore()
				metrics := &paymentOperationMetricsFake{}
				service := newPaymentServiceWithMetrics(repo, &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC), metrics)
				return metrics, func() {
					_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
					require.NoError(t, err)
					_, err = service.AuthorizePayment(context.Background(), validAuthorizeCommand())
					require.NoError(t, err)
				}
			},
		},
		{
			name:      "retry authorization",
			operation: app.RetryAuthorizationOperation,
			outcome:   "declined",
			run: func(t *testing.T) (*paymentOperationMetricsFake, func()) {
				t.Helper()
				repo := testsupport.NewPaymentStore()
				now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
				initial := newPaymentService(repo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailableError(errors.New("500"))}, now)
				_, err := initial.AuthorizePayment(context.Background(), validAuthorizeCommand())
				require.NoError(t, err)
				metrics := &paymentOperationMetricsFake{}
				service := newPaymentServiceWithMetrics(repo, &bankAuthorizerFake{result: app.BankAuthorizationResult{DeclineReason: domain.DeclineReasonInvalidCard}}, now.Add(time.Minute), metrics)
				command := validRetryAuthorizationCommand("pay_550e8400-e29b-41d4-a716-446655440000")
				return metrics, func() {
					_, err := service.RetryAuthorization(context.Background(), command)
					require.NoError(t, err)
					_, err = service.RetryAuthorization(context.Background(), command)
					require.NoError(t, err)
				}
			},
		},
		{
			name:      "capture",
			operation: app.CapturePaymentOperation,
			outcome:   "captured",
			run: func(t *testing.T) (*paymentOperationMetricsFake, func()) {
				t.Helper()
				repo := testsupport.NewPaymentStore()
				payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
				require.NoError(t, repo.SeedPayment(context.Background(), payment))
				metrics := &paymentOperationMetricsFake{}
				service := newPaymentServiceWithMetrics(repo, &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC), metrics)
				command := mustCapturePaymentCommand(t, string(payment.ID()), "capture-key-1")
				return metrics, func() {
					_, err := service.CapturePayment(context.Background(), command)
					require.NoError(t, err)
					_, err = service.CapturePayment(context.Background(), command)
					require.NoError(t, err)
				}
			},
		},
		{
			name:      "void",
			operation: app.VoidPaymentOperation,
			outcome:   "voided",
			run: func(t *testing.T) (*paymentOperationMetricsFake, func()) {
				t.Helper()
				repo := testsupport.NewPaymentStore()
				payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
				require.NoError(t, repo.SeedPayment(context.Background(), payment))
				metrics := &paymentOperationMetricsFake{}
				service := newPaymentServiceWithMetrics(repo, &bankAuthorizerFake{voidResult: app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440002"}}, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC), metrics)
				command := mustVoidPaymentCommand(t, string(payment.ID()), "void-key-1")
				return metrics, func() {
					_, err := service.VoidPayment(context.Background(), command)
					require.NoError(t, err)
					_, err = service.VoidPayment(context.Background(), command)
					require.NoError(t, err)
				}
			},
		},
		{
			name:      "refund",
			operation: app.RefundPaymentOperation,
			outcome:   "refunded",
			run: func(t *testing.T) (*paymentOperationMetricsFake, func()) {
				t.Helper()
				repo := testsupport.NewPaymentStore()
				payment := newCapturedDomainPayment(t, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
				require.NoError(t, repo.SeedPayment(context.Background(), payment))
				metrics := &paymentOperationMetricsFake{}
				service := newPaymentServiceWithMetrics(repo, &bankFake{refundResult: app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440002"}}, time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC), metrics)
				command := mustRefundPaymentCommand(t, string(payment.ID()), "refund-key-1")
				return metrics, func() {
					_, err := service.RefundPayment(context.Background(), command)
					require.NoError(t, err)
					_, err = service.RefundPayment(context.Background(), command)
					require.NoError(t, err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics, run := tt.run(t)

			run()

			require.Len(t, metrics.records, 2)
			assertPaymentOperationMetric(t, metrics.records[0], tt.operation, tt.outcome)
			assertPaymentOperationMetric(t, metrics.records[1], tt.operation, "replayed")
		})
	}
}

func TestPaymentOperationsRecordExpectedAppErrorOutcomes(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		outcome   string
		run       func(t *testing.T) *paymentOperationMetricsFake
	}{
		{
			name:      "authorize pending",
			operation: app.AuthorizePaymentOperation,
			outcome:   "pending",
			run: func(t *testing.T) *paymentOperationMetricsFake {
				t.Helper()
				metrics := &paymentOperationMetricsFake{}
				service := newPaymentServiceWithMetrics(testsupport.NewPaymentStore(), &bankAuthorizerFake{err: app.NewPaymentBankTimeoutError(context.DeadlineExceeded)}, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC), metrics)
				_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
				require.NoError(t, err)
				return metrics
			},
		},
		{
			name:      "retry payment status conflict",
			operation: app.RetryAuthorizationOperation,
			outcome:   string(app.PaymentErrorPaymentStatusConflict),
			run: func(t *testing.T) *paymentOperationMetricsFake {
				t.Helper()
				repo := testsupport.NewPaymentStore()
				service := newPaymentService(repo, &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
				result, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
				require.NoError(t, err)
				metrics := &paymentOperationMetricsFake{}
				service = newPaymentServiceWithMetrics(repo, &bankAuthorizerFake{}, time.Date(2026, 6, 18, 12, 1, 0, 0, time.UTC), metrics)
				_, err = service.RetryAuthorization(context.Background(), validRetryAuthorizationCommand(result.Payment.ID))
				require.Error(t, err)
				return metrics
			},
		},
		{
			name:      "capture bank state conflict",
			operation: app.CapturePaymentOperation,
			outcome:   string(app.PaymentErrorBankStateConflict),
			run: func(t *testing.T) *paymentOperationMetricsFake {
				t.Helper()
				repo := testsupport.NewPaymentStore()
				payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
				require.NoError(t, repo.SeedPayment(context.Background(), payment))
				metrics := &paymentOperationMetricsFake{}
				service := newPaymentServiceWithMetrics(repo, &bankFake{captureErr: app.NewPaymentBankStateConflictError(errors.New("already captured"))}, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC), metrics)
				_, err := service.CapturePayment(context.Background(), mustCapturePaymentCommand(t, string(payment.ID()), "capture-key-1"))
				require.Error(t, err)
				return metrics
			},
		},
		{
			name:      "void bank unavailable",
			operation: app.VoidPaymentOperation,
			outcome:   string(app.PaymentErrorBankUnavailable),
			run: func(t *testing.T) *paymentOperationMetricsFake {
				t.Helper()
				repo := testsupport.NewPaymentStore()
				payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
				require.NoError(t, repo.SeedPayment(context.Background(), payment))
				metrics := &paymentOperationMetricsFake{}
				service := newPaymentServiceWithMetrics(repo, &bankAuthorizerFake{voidErr: app.NewPaymentBankUnavailableError(errors.New("connection refused"))}, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC), metrics)
				_, err := service.VoidPayment(context.Background(), mustVoidPaymentCommand(t, string(payment.ID()), "void-key-1"))
				require.Error(t, err)
				return metrics
			},
		},
		{
			name:      "refund bank timeout",
			operation: app.RefundPaymentOperation,
			outcome:   string(app.PaymentErrorBankTimeout),
			run: func(t *testing.T) *paymentOperationMetricsFake {
				t.Helper()
				repo := testsupport.NewPaymentStore()
				payment := newCapturedDomainPayment(t, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
				require.NoError(t, repo.SeedPayment(context.Background(), payment))
				metrics := &paymentOperationMetricsFake{}
				service := newPaymentServiceWithMetrics(repo, &bankFake{refundErr: app.NewPaymentBankTimeoutError(context.DeadlineExceeded)}, time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC), metrics)
				_, err := service.RefundPayment(context.Background(), mustRefundPaymentCommand(t, string(payment.ID()), "refund-key-1"))
				require.Error(t, err)
				return metrics
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := tt.run(t)

			require.Len(t, metrics.records, 1)
			assertPaymentOperationMetric(t, metrics.records[0], tt.operation, tt.outcome)
		})
	}
}

func TestAuthorizePaymentRecordsReleaseFailureAndPreservesBankError(t *testing.T) {
	metrics := &paymentOperationMetricsFake{}
	bankErr := app.NewPaymentBankStateConflictError(errors.New("bank state conflict"))
	service := newPaymentServiceWithMetrics(
		&failingReleasePaymentStore{
			PaymentStore: testsupport.NewPaymentStore(),
			err:          app.NewInternalPaymentError(errors.New("release failed")),
		},
		&bankAuthorizerFake{err: bankErr},
		time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC),
		metrics,
	)

	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankStateConflict))
	assert.Equal(t, []string{app.AuthorizePaymentOperation}, metrics.releaseFailureOperations)
}

func TestPaymentOperationsRecordExpiredOutcomeWhenCaptureOrVoidDurablyExpiresPayment(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		run       func(t *testing.T) *paymentOperationMetricsFake
	}{
		{
			name:      "capture expires before bank call",
			operation: app.CapturePaymentOperation,
			run: func(t *testing.T) *paymentOperationMetricsFake {
				t.Helper()
				repo := testsupport.NewPaymentStore()
				payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
				require.NoError(t, repo.SeedPayment(context.Background(), payment))
				metrics := &paymentOperationMetricsFake{}
				service := newPaymentServiceWithMetrics(repo, &bankFake{}, payment.AuthorizationExpiresAt(), metrics)
				_, err := service.CapturePayment(context.Background(), mustCapturePaymentCommand(t, string(payment.ID()), "capture-key-1"))
				require.Error(t, err)
				return metrics
			},
		},
		{
			name:      "capture bank reports expired",
			operation: app.CapturePaymentOperation,
			run: func(t *testing.T) *paymentOperationMetricsFake {
				t.Helper()
				repo := testsupport.NewPaymentStore()
				payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
				require.NoError(t, repo.SeedPayment(context.Background(), payment))
				metrics := &paymentOperationMetricsFake{}
				service := newPaymentServiceWithMetrics(repo, &bankFake{captureErr: app.NewPaymentAuthorizationExpiredError(nil)}, payment.AuthorizationExpiresAt().Add(-time.Minute), metrics)
				_, err := service.CapturePayment(context.Background(), mustCapturePaymentCommand(t, string(payment.ID()), "capture-key-1"))
				require.Error(t, err)
				return metrics
			},
		},
		{
			name:      "void expires before bank call",
			operation: app.VoidPaymentOperation,
			run: func(t *testing.T) *paymentOperationMetricsFake {
				t.Helper()
				repo := testsupport.NewPaymentStore()
				payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
				require.NoError(t, repo.SeedPayment(context.Background(), payment))
				metrics := &paymentOperationMetricsFake{}
				service := newPaymentServiceWithMetrics(repo, &bankAuthorizerFake{}, payment.AuthorizationExpiresAt(), metrics)
				_, err := service.VoidPayment(context.Background(), mustVoidPaymentCommand(t, string(payment.ID()), "void-key-1"))
				require.Error(t, err)
				return metrics
			},
		},
		{
			name:      "void bank reports expired",
			operation: app.VoidPaymentOperation,
			run: func(t *testing.T) *paymentOperationMetricsFake {
				t.Helper()
				repo := testsupport.NewPaymentStore()
				payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
				require.NoError(t, repo.SeedPayment(context.Background(), payment))
				metrics := &paymentOperationMetricsFake{}
				service := newPaymentServiceWithMetrics(repo, &bankAuthorizerFake{voidErr: app.NewPaymentAuthorizationExpiredError(nil)}, payment.AuthorizationExpiresAt().Add(-time.Minute), metrics)
				_, err := service.VoidPayment(context.Background(), mustVoidPaymentCommand(t, string(payment.ID()), "void-key-1"))
				require.Error(t, err)
				return metrics
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := tt.run(t)

			require.Len(t, metrics.records, 1)
			assertPaymentOperationMetric(t, metrics.records[0], tt.operation, "expired")
		})
	}
}

func TestAuthorizePaymentReplaysWhenOnlyCVVDiffers(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{DeclineReason: domain.DeclineReasonInvalidCard}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	first := validAuthorizeCommand()
	_, err := service.AuthorizePayment(context.Background(), first)
	require.NoError(t, err)

	secondCard := validCardDetails()
	secondCard.cvv = "999"
	second, err := newAuthorizePaymentCommand("order-1", "customer-1", 1299, secondCard, "public-key-1")
	require.NoError(t, err)
	replayed, err := service.AuthorizePayment(context.Background(), second)

	require.NoError(t, err)
	assert.Equal(t, "declined", replayed.Payment.Status)
	assert.Equal(t, 1, bank.calls)
}

func TestAuthorizePaymentRejectsReusedIdempotencyKeyWithDifferentRequest(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{DeclineReason: domain.DeclineReasonInvalidCard}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	first := validAuthorizeCommand()
	_, err := service.AuthorizePayment(context.Background(), first)
	require.NoError(t, err)

	second, err := newAuthorizePaymentCommand("order-1", "customer-1", 2599, validCardDetails(), "public-key-1")
	require.NoError(t, err)
	_, err = service.AuthorizePayment(context.Background(), second)

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	assert.Equal(t, 1, bank.calls)
}

func TestAuthorizePaymentRejectsInProgressIdempotencyKeyBeforeCallingBank(t *testing.T) {
	store := &alwaysInProgressPaymentStore{PaymentStore: testsupport.NewPaymentStore()}
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	service := app.NewPaymentService(
		store,
		testsupport.FixedPaymentIDGenerator{ID: domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000")},
		testsupport.FixedBankOperationKeyGenerator{Key: "bok_123"},
		bank,
		&paymentOperationMetricsFake{},
		testsupport.FixedClock{Time: time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)},
		"fingerprint-secret",
		testIdempotencyClaimStuckAfter,
	)

	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyInProgress))
	assert.Zero(t, bank.calls)
}

func TestAuthorizePaymentNormalizesRequestBeforeFingerprintBankCallAndStorage(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	command, err := newAuthorizePaymentCommand(" order-1 ", " customer-1 ", 1299, cardInput{
		number:      " 4111111111111111 ",
		cvv:         " 123 ",
		expiryMonth: 12,
		expiryYear:  2030,
	}, " public-key-1 ")
	require.NoError(t, err)

	result, err := service.AuthorizePayment(context.Background(), command)
	require.NoError(t, err)

	assert.Equal(t, "order-1", bank.request.OrderID)
	assert.Equal(t, "customer-1", bank.request.CustomerID)
	assert.Equal(t, "4111111111111111", bank.request.CardNumber)
	assert.Equal(t, "123", bank.request.CardCVV)
	assert.Equal(t, "order-1", result.Payment.OrderID)
	assert.Equal(t, "customer-1", result.Payment.CustomerID)

	replayed, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)
	assert.Equal(t, result, replayed)
	assert.Equal(t, 1, bank.calls)
}

func TestNewAuthorizePaymentCommandRequiresIdempotencyKey(t *testing.T) {
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}

	_, err := newAuthorizePaymentCommand("order-1", "customer-1", 1299, validCardDetails(), "")

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidInput))
	assert.Zero(t, bank.request)
}

func TestNewAuthorizePaymentCommandValidatesInput(t *testing.T) {
	tests := []struct {
		name           string
		orderID        string
		customerID     string
		amountCents    int64
		card           cardInput
		idempotencyKey string
	}{
		{name: "order id", orderID: "", customerID: "customer-1", amountCents: 1299, card: validCardDetails(), idempotencyKey: "public-key-1"},
		{name: "customer id", orderID: "order-1", customerID: "", amountCents: 1299, card: validCardDetails(), idempotencyKey: "public-key-1"},
		{name: "amount", orderID: "order-1", customerID: "customer-1", amountCents: 0, card: validCardDetails(), idempotencyKey: "public-key-1"},
		{name: "card number", orderID: "order-1", customerID: "customer-1", amountCents: 1299, card: cardInput{number: "4111x", cvv: "123", expiryMonth: 12, expiryYear: 2030}, idempotencyKey: "public-key-1"},
		{name: "cvv", orderID: "order-1", customerID: "customer-1", amountCents: 1299, card: cardInput{number: "4111111111111111", cvv: "12x", expiryMonth: 12, expiryYear: 2030}, idempotencyKey: "public-key-1"},
		{name: "expiry month", orderID: "order-1", customerID: "customer-1", amountCents: 1299, card: cardInput{number: "4111111111111111", cvv: "123", expiryMonth: 13, expiryYear: 2030}, idempotencyKey: "public-key-1"},
		{name: "expiry year", orderID: "order-1", customerID: "customer-1", amountCents: 1299, card: cardInput{number: "4111111111111111", cvv: "123", expiryMonth: 12, expiryYear: 0}, idempotencyKey: "public-key-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}

			_, err := newAuthorizePaymentCommand(tt.orderID, tt.customerID, tt.amountCents, tt.card, tt.idempotencyKey)

			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidInput))
			assert.Zero(t, bank.request)
		})
	}
}

func TestAuthorizePaymentDoesNotClaimIdempotencyForValidationFailure(t *testing.T) {
	store := testsupport.NewPaymentStore()
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	service := app.NewPaymentService(
		store,
		testsupport.FixedPaymentIDGenerator{ID: domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000")},
		testsupport.FixedBankOperationKeyGenerator{Key: "bok_123"},
		bank,
		&paymentOperationMetricsFake{},
		testsupport.FixedClock{Time: time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)},
		"fingerprint-secret",
		testIdempotencyClaimStuckAfter,
	)
	_, err := newAuthorizePaymentCommand("order-1", "customer-1", 0, validCardDetails(), "public-key-1")
	require.Error(t, err)

	_, err = service.AuthorizePayment(context.Background(), validAuthorizeCommand())

	require.NoError(t, err)
	assert.Equal(t, 1, bank.calls)
}

func TestAuthorizePaymentNormalizesBankErrorAfterStoringPendingPayment(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	bankErr := errors.New("bank unavailable")
	service := newPaymentService(repo, &bankAuthorizerFake{err: bankErr}, time.Now())

	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())

	assert.ErrorIs(t, err, bankErr)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInternal))
	saved, findErr := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"), testNonExpiringStoreReadTime)
	require.NoError(t, findErr)
	assert.Equal(t, domain.PaymentStatusPending, saved.Status())
	assert.Equal(t, "bok_123", saved.AuthorizationBankOperationKey())
}

func TestGetPaymentReturnsPublicResult(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	payment, err := testsupport.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"bank-auth-id-1",
		now.Add(time.Hour),
		"bok_123",
		"fingerprint-1",
		now,
	)
	require.NoError(t, err)
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	service := newPaymentService(repo, &bankAuthorizerFake{}, now)

	result, err := service.GetPayment(context.Background(), mustGetPaymentQuery(t, "pay_550e8400-e29b-41d4-a716-446655440000"))

	require.NoError(t, err)
	assert.Equal(t, app.PaymentResult{
		ID:                     "pay_550e8400-e29b-41d4-a716-446655440000",
		OrderID:                "order-1",
		CustomerID:             "customer-1",
		AmountCents:            1299,
		Currency:               "USD",
		Status:                 "authorized",
		AuthorizationExpiresAt: now.Add(time.Hour),
		CreatedAt:              now,
		UpdatedAt:              now,
	}, result)
}

func TestGetPaymentWrapsRawStoreErrorAsPaymentError(t *testing.T) {
	storeErr := errors.New("driver connection failed")
	service := newPaymentService(&failingFindPaymentStore{err: storeErr}, &bankAuthorizerFake{}, time.Now())

	_, err := service.GetPayment(context.Background(), mustGetPaymentQuery(t, "pay_550e8400-e29b-41d4-a716-446655440000"))

	require.Error(t, err)
	assert.ErrorIs(t, err, storeErr)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInternal))
}

func TestNewGetPaymentQueryRequiresPaymentID(t *testing.T) {
	_, err := app.NewGetPaymentQuery(" ")

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidInput))
}

func TestAppInputConstructorsReturnPaymentErrors(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "authorize command",
			run: func() error {
				_, err := newAuthorizePaymentCommand("order-1", "customer-1", 0, validCardDetails(), "public-key-1")
				return err
			},
		},
		{
			name: "retry authorization command",
			run: func() error {
				_, err := newRetryAuthorizationCommand("not-a-payment-id", validCardDetails(), "retry-key-1")
				return err
			},
		},
		{
			name: "capture command",
			run: func() error {
				_, err := app.NewCapturePaymentCommand("not-a-payment-id", "capture-key-1")
				return err
			},
		},
		{
			name: "void command",
			run: func() error {
				_, err := app.NewVoidPaymentCommand("not-a-payment-id", "void-key-1")
				return err
			},
		},
		{
			name: "refund command",
			run: func() error {
				_, err := app.NewRefundPaymentCommand("not-a-payment-id", "refund-key-1")
				return err
			},
		},
		{
			name: "get payment query",
			run: func() error {
				_, err := app.NewGetPaymentQuery("not-a-payment-id")
				return err
			},
		},
		{
			name: "search payments query",
			run: func() error {
				_, err := app.NewSearchPaymentsQuery("", "", "")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()

			require.Error(t, err)
			kind, ok := app.PaymentErrorKindOf(err)
			require.True(t, ok)
			assert.Equal(t, app.PaymentErrorInvalidInput, kind)
		})
	}
}

func TestGetPaymentPersistsExpiredStatusWhenAuthorizationExpires(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	authorizedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	payment := newAuthorizedDomainPayment(t, authorizedAt)
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	service := newPaymentService(repo, &bankAuthorizerFake{}, payment.AuthorizationExpiresAt())

	result, err := service.GetPayment(context.Background(), mustGetPaymentQuery(t, string(payment.ID())))

	require.NoError(t, err)
	assert.Equal(t, "expired", result.Status)
	assert.Equal(t, payment.AuthorizationExpiresAt(), result.AuthorizationExpiresAt)
	assert.Equal(t, payment.AuthorizationExpiresAt(), result.UpdatedAt)
	saved, err := repo.FindByID(context.Background(), payment.ID(), testNonExpiringStoreReadTime)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusExpired, saved.Status())
}

func TestVoidPaymentCallsBankStoresVoidedPaymentAndReturnsPublicResult(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	payment := mustAuthorizedPayment(t, "pay_550e8400-e29b-41d4-a716-446655440000", "order-1", "customer-1", now)
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	bank := &bankAuthorizerFake{voidResult: app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440002"}}
	service := newPaymentService(repo, bank, now.Add(time.Minute))

	result, err := service.VoidPayment(context.Background(), mustVoidPaymentCommand(t, string(payment.ID()), "void-key-1"))

	require.NoError(t, err)
	assert.Equal(t, app.BankVoidRequest{
		OperationKey:        "bok_123",
		BankAuthorizationID: "bank-auth-id-1",
	}, bank.voidRequest)
	assert.Equal(t, app.PaymentResult{
		ID:                     string(payment.ID()),
		OrderID:                "order-1",
		CustomerID:             "customer-1",
		AmountCents:            1299,
		Currency:               "USD",
		Status:                 "voided",
		AuthorizationExpiresAt: payment.AuthorizationExpiresAt(),
		CreatedAt:              now,
		UpdatedAt:              now.Add(time.Minute),
	}, result.Payment)
	assert.Equal(t, http.StatusOK, result.HTTPStatus)

	saved, err := repo.FindByID(context.Background(), payment.ID(), testNonExpiringStoreReadTime)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusVoided, saved.Status())
	assert.Equal(t, "void_550e8400-e29b-41d4-a716-446655440002", saved.BankVoidID())
	assert.Equal(t, "bok_123", saved.VoidBankOperationKey())
}

func TestVoidPaymentRejectsNonAuthorizedPayment(t *testing.T) {
	statuses := []domain.PaymentStatus{
		domain.PaymentStatusPending,
		domain.PaymentStatusDeclined,
		domain.PaymentStatusCaptured,
		domain.PaymentStatusVoided,
		domain.PaymentStatusRefunded,
	}

	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			repo := testsupport.NewPaymentStore()
			payment := loadDomainPaymentForStatus(t, status)
			require.NoError(t, repo.SeedPayment(context.Background(), payment))
			bank := &bankAuthorizerFake{voidResult: app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440002"}}
			service := newPaymentService(repo, bank, time.Now())

			_, err := service.VoidPayment(context.Background(), mustVoidPaymentCommand(t, string(payment.ID()), "void-key-1"))

			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorPaymentStatusConflict))
			assert.Zero(t, bank.voidCalls)
		})
	}
}

func TestNewVoidPaymentCommandRequiresIdempotencyKey(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newAuthorizedDomainPayment(t, time.Now())
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	bank := &bankAuthorizerFake{voidResult: app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440002"}}

	_, err := app.NewVoidPaymentCommand(string(payment.ID()), "")

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidInput))
	assert.Zero(t, bank.voidRequest)
}

func TestVoidPaymentLeavesPaymentStatusUnchangedWhenBankFails(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind app.PaymentErrorKind
	}{
		{name: "unavailable", err: app.NewPaymentBankUnavailableError(errors.New("connection refused")), kind: app.PaymentErrorBankUnavailable},
		{name: "timeout", err: app.NewPaymentBankTimeoutError(context.DeadlineExceeded), kind: app.PaymentErrorBankTimeout},
		{name: "bank state conflict", err: app.NewPaymentBankStateConflictError(errors.New("already voided")), kind: app.PaymentErrorBankStateConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := testsupport.NewPaymentStore()
			authorizedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
			payment := newAuthorizedDomainPayment(t, authorizedAt)
			require.NoError(t, repo.SeedPayment(context.Background(), payment))
			service := newPaymentService(repo, &bankAuthorizerFake{voidErr: tt.err}, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))

			_, err := service.VoidPayment(context.Background(), mustVoidPaymentCommand(t, string(payment.ID()), "void-key-1"))

			assert.True(t, app.HasPaymentErrorKind(err, tt.kind))
			saved, findErr := repo.FindByID(context.Background(), payment.ID(), testNonExpiringStoreReadTime)
			require.NoError(t, findErr)
			assert.Equal(t, domain.PaymentStatusAuthorized, saved.Status())
			assert.Equal(t, authorizedAt, saved.UpdatedAt())
			assert.Empty(t, saved.BankVoidID())
			assert.Equal(t, "bok_123", saved.VoidBankOperationKey())
		})
	}
}

func TestVoidPaymentReusesStoredBankOperationKeyAfterProviderFailure(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	firstBank := &bankAuthorizerFake{voidErr: app.NewPaymentBankTimeoutError(context.DeadlineExceeded)}
	service := newPaymentServiceWithBankOperationKeys(repo, firstBank, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_first", "bok_second"}})

	_, err := service.VoidPayment(context.Background(), mustVoidPaymentCommand(t, string(payment.ID()), "void-key-1"))
	require.Error(t, err)

	secondBank := &bankAuthorizerFake{voidResult: app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440002"}}
	service = newPaymentServiceWithBankOperationKeys(repo, secondBank, time.Date(2026, 6, 18, 12, 31, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_second"}})
	result, err := service.VoidPayment(context.Background(), mustVoidPaymentCommand(t, string(payment.ID()), "void-key-1"))

	require.NoError(t, err)
	assert.Equal(t, "bok_first", secondBank.voidRequest.OperationKey)
	assert.Equal(t, "voided", result.Payment.Status)
}

func TestVoidPaymentRecoversStuckClaimUsingPersistedBankOperationKey(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	crashingBank := &bankFake{}
	crashingBank.onVoid = func() {
		panic("process crashed after void claim")
	}
	service := newPaymentServiceWithBankOperationKeys(repo, crashingBank, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_void_original"}})
	command := mustVoidPaymentCommand(t, string(payment.ID()), "void-key-1")
	require.PanicsWithValue(t, "process crashed after void claim", func() {
		_, _ = service.VoidPayment(context.Background(), command)
	})
	repo.AgeClaim(app.VoidPaymentOperation, "void-key-1", time.Date(2026, 6, 18, 12, 24, 0, 0, time.UTC))

	metrics := &paymentOperationMetricsFake{}
	bank := &bankFake{voidResult: app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440001"}}
	recoveryService := newPaymentServiceWithBankOperationKeysAndMetrics(repo, bank, time.Date(2026, 6, 18, 12, 31, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_void_new", "bok_void_replay"}}, metrics)
	result, err := recoveryService.VoidPayment(context.Background(), command)
	require.NoError(t, err)

	assert.Equal(t, "voided", result.Payment.Status)
	assert.Equal(t, "bok_void_original", bank.voidRequest.OperationKey)
	assert.Equal(t, []idempotencyRecoveryMetricRecord{
		{operation: app.VoidPaymentOperation, result: app.IdempotencyRecoveryAttempted},
		{operation: app.VoidPaymentOperation, result: app.IdempotencyRecoveryRecovered},
	}, metrics.recoveryRecords)

	replayed, err := recoveryService.VoidPayment(context.Background(), command)
	require.NoError(t, err)
	assert.Equal(t, result, replayed)
	assert.Equal(t, 1, bank.voidCalls)
}

func TestVoidPaymentRecoveredClaimCompletesExpiredReplayWhenBankReportsAuthorizationExpired(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	crashingBank := &bankFake{}
	crashingBank.onVoid = func() {
		panic("process crashed after void claim")
	}
	command := mustVoidPaymentCommand(t, string(payment.ID()), "void-key-1")
	service := newPaymentServiceWithBankOperationKeys(repo, crashingBank, payment.AuthorizationExpiresAt().Add(-time.Minute), &sequenceBankOperationKeyGenerator{keys: []string{"bok_void_original"}})
	require.Panics(t, func() {
		_, _ = service.VoidPayment(context.Background(), command)
	})
	repo.AgeClaim(app.VoidPaymentOperation, "void-key-1", payment.AuthorizationExpiresAt().Add(-6*time.Minute))

	metrics := &paymentOperationMetricsFake{}
	bank := &bankFake{voidErr: app.NewPaymentAuthorizationExpiredError(nil)}
	recoveryService := newPaymentServiceWithBankOperationKeysAndMetrics(repo, bank, payment.AuthorizationExpiresAt().Add(time.Minute), &sequenceBankOperationKeyGenerator{keys: []string{"bok_void_new", "bok_void_replay"}}, metrics)
	_, err := recoveryService.VoidPayment(context.Background(), command)
	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorAuthorizationExpired))

	replayed, err := recoveryService.VoidPayment(context.Background(), command)
	require.NoError(t, err)
	assert.Equal(t, "expired", replayed.Payment.Status)
	assert.Equal(t, http.StatusConflict, replayed.HTTPStatus)
	assert.Equal(t, "bok_void_original", bank.voidRequest.OperationKey)
	assert.Equal(t, []idempotencyRecoveryMetricRecord{
		{operation: app.VoidPaymentOperation, result: app.IdempotencyRecoveryAttempted},
		{operation: app.VoidPaymentOperation, result: app.IdempotencyRecoveryRecovered},
	}, metrics.recoveryRecords)
}

func TestVoidPaymentReplaysVoidedPaymentForSameIdempotencyKeyAndPayment(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	bank := &bankAuthorizerFake{voidResult: app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440001"}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
	command := mustVoidPaymentCommand(t, string(payment.ID()), "void-key-1")
	first, err := service.VoidPayment(context.Background(), command)
	require.NoError(t, err)
	bank.voidResult = app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440002"}

	replayed, err := service.VoidPayment(context.Background(), command)
	require.NoError(t, err)

	assert.Equal(t, first, replayed)
	assert.Equal(t, 1, bank.voidCalls)
}

func TestVoidPaymentRejectsReusedIdempotencyKeyWithDifferentPayment(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	secondPayment, err := testsupport.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440001"),
		"order-2",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440002",
		payment.AuthorizationExpiresAt(),
		"bok_550e8400-e29b-41d4-a716-446655440003",
		payment.AuthorizationCardFingerprint(),
		payment.CreatedAt(),
	)
	require.NoError(t, err)
	require.NoError(t, repo.SeedPayment(context.Background(), secondPayment))
	bank := &bankAuthorizerFake{voidResult: app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440001"}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
	_, err = service.VoidPayment(context.Background(), mustVoidPaymentCommand(t, string(payment.ID()), "void-key-1"))
	require.NoError(t, err)

	_, err = service.VoidPayment(context.Background(), mustVoidPaymentCommand(t, string(secondPayment.ID()), "void-key-1"))

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	assert.Equal(t, 1, bank.voidCalls)
}

func TestSearchPaymentsNormalizesFiltersAndReturnsPublicResults(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	newer := mustAuthorizedPayment(t, "pay_550e8400-e29b-41d4-a716-446655440001", "order-1", "customer-1", time.Date(2026, 6, 18, 12, 1, 0, 0, time.UTC))
	older := mustDeclinedPayment(t, "pay_550e8400-e29b-41d4-a716-446655440000", "order-1", "customer-1", time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), older))
	require.NoError(t, repo.SeedPayment(context.Background(), newer))
	service := newPaymentService(repo, &bankAuthorizerFake{}, newer.CreatedAt())

	results, err := service.SearchPayments(context.Background(), mustSearchPaymentsQuery(t, " order-1 ", " customer-1 ", " authorized "))

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, newer.ID(), domain.PaymentID(results[0].ID))
	assert.Equal(t, "authorized", results[0].Status)
}

func TestSearchPaymentsRefreshesExpiredAuthorizationsBeforeFiltering(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	authorizedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	payment := mustAuthorizedPayment(t, "pay_550e8400-e29b-41d4-a716-446655440000", "order-1", "customer-1", authorizedAt)
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	service := newPaymentService(repo, &bankAuthorizerFake{}, payment.AuthorizationExpiresAt())

	authorized, err := service.SearchPayments(context.Background(), mustSearchPaymentsQuery(t, "order-1", "", "authorized"))
	require.NoError(t, err)
	assert.Empty(t, authorized)

	expired, err := service.SearchPayments(context.Background(), mustSearchPaymentsQuery(t, "order-1", "", "expired"))
	require.NoError(t, err)
	require.Len(t, expired, 1)
	assert.Equal(t, "expired", expired[0].Status)
}

func TestNewSearchPaymentsQueryRejectsInvalidFilters(t *testing.T) {
	tests := []struct {
		name       string
		orderID    string
		customerID string
		status     string
	}{
		{name: "unfiltered"},
		{name: "status only", status: "authorized"},
		{name: "invalid status", orderID: "order-1", status: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := app.NewSearchPaymentsQuery(tt.orderID, tt.customerID, tt.status)

			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidInput))
		})
	}
}

func TestCapturePaymentCallsBankStoresCapturedPaymentAndReturnsPublicResult(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	authorizedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	payment := newAuthorizedDomainPayment(t, authorizedAt)
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	bank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}
	capturedAt := time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC)
	service := newPaymentService(repo, bank, capturedAt)

	captured, err := service.CapturePayment(context.Background(), mustCapturePaymentCommand(t, string(payment.ID()), "public-capture-key-1"))
	require.NoError(t, err)

	assert.Equal(t, app.BankCaptureRequest{
		OperationKey:        "bok_123",
		BankAuthorizationID: "auth_550e8400-e29b-41d4-a716-446655440000",
		AmountCents:         1299,
		Currency:            "USD",
	}, bank.captureRequest)
	assert.Equal(t, app.PaymentResult{
		ID:                     string(payment.ID()),
		OrderID:                "order-1",
		CustomerID:             "customer-1",
		AmountCents:            1299,
		Currency:               "USD",
		Status:                 "captured",
		AuthorizationExpiresAt: payment.AuthorizationExpiresAt(),
		CreatedAt:              authorizedAt,
		UpdatedAt:              capturedAt,
	}, captured.Payment)
	assert.Equal(t, http.StatusOK, captured.HTTPStatus)

	saved, err := repo.FindByID(context.Background(), payment.ID(), testNonExpiringStoreReadTime)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusCaptured, saved.Status())
	assert.Equal(t, "cap_550e8400-e29b-41d4-a716-446655440001", saved.BankCaptureID())
	assert.Equal(t, "bok_123", saved.CaptureBankOperationKey())
}

func TestNewCapturePaymentCommandRequiresIdempotencyKey(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newAuthorizedDomainPayment(t, time.Now())
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	bank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}

	_, err := app.NewCapturePaymentCommand(string(payment.ID()), "")

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidInput))
	assert.Zero(t, bank.captureRequest)
}

func TestCapturePaymentExpiresPaymentBeforeNewBankCallWhenAuthorizationExpired(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	bank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}
	service := newPaymentService(repo, bank, payment.AuthorizationExpiresAt())

	_, err := service.CapturePayment(context.Background(), mustCapturePaymentCommand(t, string(payment.ID()), "public-capture-key-1"))

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorAuthorizationExpired))
	assert.Zero(t, bank.captureCalls)
	saved, findErr := repo.FindByID(context.Background(), payment.ID(), testNonExpiringStoreReadTime)
	require.NoError(t, findErr)
	assert.Equal(t, domain.PaymentStatusExpired, saved.Status())
	assert.Equal(t, payment.AuthorizationExpiresAt(), saved.UpdatedAt())
}

func TestCapturePaymentRejectsNonAuthorizedStatusesWithoutCallingBank(t *testing.T) {
	statuses := []domain.PaymentStatus{
		domain.PaymentStatusPending,
		domain.PaymentStatusDeclined,
		domain.PaymentStatusCaptured,
		domain.PaymentStatusVoided,
		domain.PaymentStatusRefunded,
	}

	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			repo := testsupport.NewPaymentStore()
			payment := loadDomainPaymentForStatus(t, status)
			require.NoError(t, repo.SeedPayment(context.Background(), payment))
			bank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}
			service := newPaymentService(repo, bank, time.Now())

			_, err := service.CapturePayment(context.Background(), mustCapturePaymentCommand(t, string(payment.ID()), "public-capture-key-1"))

			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorPaymentStatusConflict))
			assert.Zero(t, bank.captureRequest)
		})
	}
}

func TestCapturePaymentLeavesPaymentStatusUnchangedWhenBankFails(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind app.PaymentErrorKind
	}{
		{name: "unavailable", err: app.NewPaymentBankUnavailableError(errors.New("connection refused")), kind: app.PaymentErrorBankUnavailable},
		{name: "timeout", err: app.NewPaymentBankTimeoutError(context.DeadlineExceeded), kind: app.PaymentErrorBankTimeout},
		{name: "bank state conflict", err: app.NewPaymentBankStateConflictError(errors.New("already captured")), kind: app.PaymentErrorBankStateConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := testsupport.NewPaymentStore()
			authorizedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
			payment := newAuthorizedDomainPayment(t, authorizedAt)
			require.NoError(t, repo.SeedPayment(context.Background(), payment))
			service := newPaymentService(repo, &bankFake{captureErr: tt.err}, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))

			_, err := service.CapturePayment(context.Background(), mustCapturePaymentCommand(t, string(payment.ID()), "public-capture-key-1"))

			assert.True(t, app.HasPaymentErrorKind(err, tt.kind))
			saved, findErr := repo.FindByID(context.Background(), payment.ID(), testNonExpiringStoreReadTime)
			require.NoError(t, findErr)
			assert.Equal(t, domain.PaymentStatusAuthorized, saved.Status())
			assert.Equal(t, authorizedAt, saved.UpdatedAt())
			assert.Empty(t, saved.BankCaptureID())
			assert.Equal(t, "bok_123", saved.CaptureBankOperationKey())
		})
	}
}

func TestCapturePaymentPersistsExpiredStatusWhenBankReportsAuthorizationExpired(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	now := payment.AuthorizationExpiresAt().Add(-time.Minute)
	bank := &bankFake{captureErr: app.NewPaymentAuthorizationExpiredError(nil)}
	service := newPaymentService(repo, bank, now)
	command := mustCapturePaymentCommand(t, string(payment.ID()), "public-capture-key-1")

	_, err := service.CapturePayment(context.Background(), command)

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorAuthorizationExpired))
	saved, findErr := repo.FindByID(context.Background(), payment.ID(), testNonExpiringStoreReadTime)
	require.NoError(t, findErr)
	assert.Equal(t, domain.PaymentStatusExpired, saved.Status())
	assert.Equal(t, now, saved.UpdatedAt())

	replayed, err := service.CapturePayment(context.Background(), command)
	require.NoError(t, err)
	assert.Equal(t, "expired", replayed.Payment.Status)
	assert.Equal(t, http.StatusConflict, replayed.HTTPStatus)
	assert.Equal(t, 1, bank.captureCalls)
}

func TestCapturePaymentReusesStoredBankOperationKeyAfterProviderFailure(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	firstBank := &bankFake{captureErr: app.NewPaymentBankTimeoutError(context.DeadlineExceeded)}
	service := newPaymentServiceWithBankOperationKeys(repo, firstBank, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_first", "bok_second"}})

	_, err := service.CapturePayment(context.Background(), mustCapturePaymentCommand(t, string(payment.ID()), "public-capture-key-1"))
	require.Error(t, err)

	secondBank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}
	service = newPaymentServiceWithBankOperationKeys(repo, secondBank, time.Date(2026, 6, 18, 12, 31, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_second"}})
	result, err := service.CapturePayment(context.Background(), mustCapturePaymentCommand(t, string(payment.ID()), "public-capture-key-1"))

	require.NoError(t, err)
	assert.Equal(t, "bok_first", secondBank.captureRequest.OperationKey)
	assert.Equal(t, "captured", result.Payment.Status)
}

func TestCapturePaymentReusesStoredBankOperationKeyAfterExpiration(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	firstBank := &bankFake{captureErr: app.NewPaymentBankTimeoutError(context.DeadlineExceeded)}
	service := newPaymentServiceWithBankOperationKeys(repo, firstBank, payment.AuthorizationExpiresAt().Add(-time.Minute), &sequenceBankOperationKeyGenerator{keys: []string{"bok_first", "bok_second"}})
	_, err := service.CapturePayment(context.Background(), mustCapturePaymentCommand(t, string(payment.ID()), "public-capture-key-1"))
	require.Error(t, err)

	secondBank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}
	service = newPaymentServiceWithBankOperationKeys(repo, secondBank, payment.AuthorizationExpiresAt().Add(time.Minute), &sequenceBankOperationKeyGenerator{keys: []string{"bok_second"}})
	_, err = service.GetPayment(context.Background(), mustGetPaymentQuery(t, string(payment.ID())))
	require.NoError(t, err)

	result, err := service.CapturePayment(context.Background(), mustCapturePaymentCommand(t, string(payment.ID()), "public-capture-key-1"))

	require.NoError(t, err)
	assert.Equal(t, "bok_first", secondBank.captureRequest.OperationKey)
	assert.Equal(t, "captured", result.Payment.Status)
}

func TestCapturePaymentRecoversStuckClaimUsingPersistedBankOperationKey(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	crashingBank := &bankFake{}
	crashingBank.onCapture = func() {
		panic("process crashed after capture claim")
	}
	service := newPaymentServiceWithBankOperationKeys(repo, crashingBank, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_capture_original"}})
	command := mustCapturePaymentCommand(t, string(payment.ID()), "public-capture-key-1")
	require.PanicsWithValue(t, "process crashed after capture claim", func() {
		_, _ = service.CapturePayment(context.Background(), command)
	})
	repo.AgeClaim(app.CapturePaymentOperation, "public-capture-key-1", time.Date(2026, 6, 18, 12, 24, 0, 0, time.UTC))

	metrics := &paymentOperationMetricsFake{}
	bank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}
	recoveryService := newPaymentServiceWithBankOperationKeysAndMetrics(repo, bank, time.Date(2026, 6, 18, 12, 31, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_capture_new", "bok_capture_replay"}}, metrics)
	result, err := recoveryService.CapturePayment(context.Background(), command)
	require.NoError(t, err)

	assert.Equal(t, "captured", result.Payment.Status)
	assert.Equal(t, "bok_capture_original", bank.captureRequest.OperationKey)
	assert.Equal(t, []idempotencyRecoveryMetricRecord{
		{operation: app.CapturePaymentOperation, result: app.IdempotencyRecoveryAttempted},
		{operation: app.CapturePaymentOperation, result: app.IdempotencyRecoveryRecovered},
	}, metrics.recoveryRecords)

	replayed, err := recoveryService.CapturePayment(context.Background(), command)
	require.NoError(t, err)
	assert.Equal(t, result, replayed)
	assert.Equal(t, 1, bank.captureCalls)
}

func TestCapturePaymentRecoveredClaimCompletesExpiredReplayWhenBankReportsAuthorizationExpired(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	crashingBank := &bankFake{}
	crashingBank.onCapture = func() {
		panic("process crashed after capture claim")
	}
	command := mustCapturePaymentCommand(t, string(payment.ID()), "public-capture-key-1")
	service := newPaymentServiceWithBankOperationKeys(repo, crashingBank, payment.AuthorizationExpiresAt().Add(-time.Minute), &sequenceBankOperationKeyGenerator{keys: []string{"bok_capture_original"}})
	require.Panics(t, func() {
		_, _ = service.CapturePayment(context.Background(), command)
	})
	repo.AgeClaim(app.CapturePaymentOperation, "public-capture-key-1", payment.AuthorizationExpiresAt().Add(-6*time.Minute))

	metrics := &paymentOperationMetricsFake{}
	bank := &bankFake{captureErr: app.NewPaymentAuthorizationExpiredError(nil)}
	recoveryService := newPaymentServiceWithBankOperationKeysAndMetrics(repo, bank, payment.AuthorizationExpiresAt().Add(time.Minute), &sequenceBankOperationKeyGenerator{keys: []string{"bok_capture_new", "bok_capture_replay"}}, metrics)
	_, err := recoveryService.CapturePayment(context.Background(), command)
	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorAuthorizationExpired))

	replayed, err := recoveryService.CapturePayment(context.Background(), command)
	require.NoError(t, err)
	assert.Equal(t, "expired", replayed.Payment.Status)
	assert.Equal(t, http.StatusConflict, replayed.HTTPStatus)
	assert.Equal(t, "bok_capture_original", bank.captureRequest.OperationKey)
	assert.Equal(t, []idempotencyRecoveryMetricRecord{
		{operation: app.CapturePaymentOperation, result: app.IdempotencyRecoveryAttempted},
		{operation: app.CapturePaymentOperation, result: app.IdempotencyRecoveryRecovered},
	}, metrics.recoveryRecords)
}

func TestCapturePaymentRecoveredClaimMissingBankOperationKeyRecordsUnrecoverable(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	now := time.Date(2026, 6, 18, 12, 31, 0, 0, time.UTC)
	repo.SeedAuthorizationClaim(app.CapturePaymentOperation, "public-capture-key-1", capturePaymentRequestFingerprintForTest(t, string(payment.ID())), payment.ID(), now.Add(-6*time.Minute))

	metrics := &paymentOperationMetricsFake{}
	bank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}
	service := newPaymentServiceWithBankOperationKeysAndMetrics(repo, bank, now, &sequenceBankOperationKeyGenerator{keys: []string{"bok_capture_new"}}, metrics)
	_, err := service.CapturePayment(context.Background(), mustCapturePaymentCommand(t, string(payment.ID()), "public-capture-key-1"))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInternal))
	assert.Zero(t, bank.captureCalls)
	assert.Equal(t, []idempotencyRecoveryMetricRecord{
		{operation: app.CapturePaymentOperation, result: app.IdempotencyRecoveryAttempted},
		{operation: app.CapturePaymentOperation, result: app.IdempotencyRecoveryUnrecoverable},
	}, metrics.recoveryRecords)
}

func TestCapturePaymentRecoveredClaimPaymentStatusConflictRecordsConflict(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newCapturedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	now := time.Date(2026, 6, 18, 12, 31, 0, 0, time.UTC)
	repo.SeedAuthorizationClaim(app.CapturePaymentOperation, "public-capture-key-1", capturePaymentRequestFingerprintForTest(t, string(payment.ID())), payment.ID(), now.Add(-6*time.Minute))

	metrics := &paymentOperationMetricsFake{}
	bank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440002"}}
	service := newPaymentServiceWithBankOperationKeysAndMetrics(repo, bank, now, &sequenceBankOperationKeyGenerator{keys: []string{"bok_capture_new"}}, metrics)
	_, err := service.CapturePayment(context.Background(), mustCapturePaymentCommand(t, string(payment.ID()), "public-capture-key-1"))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorPaymentStatusConflict))
	assert.Zero(t, bank.captureCalls)
	assert.Equal(t, []idempotencyRecoveryMetricRecord{
		{operation: app.CapturePaymentOperation, result: app.IdempotencyRecoveryAttempted},
		{operation: app.CapturePaymentOperation, result: app.IdempotencyRecoveryConflict},
	}, metrics.recoveryRecords)
}

func TestSearchPaymentsRefreshesAllMatchingExpiredAuthorizationsBeforeFiltering(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	base := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	var expiresAt time.Time
	for i := 0; i < 105; i++ {
		payment := mustAuthorizedPayment(t, fmt.Sprintf("pay_00000000-0000-4000-8000-%012d", i), "order-expired", "customer-1", base.Add(time.Duration(i)*time.Minute))
		expiresAt = payment.AuthorizationExpiresAt()
		require.NoError(t, repo.SeedPayment(context.Background(), payment))
	}
	service := newPaymentService(repo, &bankAuthorizerFake{}, expiresAt)

	authorized, err := service.SearchPayments(context.Background(), mustSearchPaymentsQuery(t, "order-expired", "", "authorized"))
	require.NoError(t, err)
	assert.Empty(t, authorized)

	expired, err := service.SearchPayments(context.Background(), mustSearchPaymentsQuery(t, "order-expired", "", "expired"))
	require.NoError(t, err)
	require.Len(t, expired, 100)
	assert.Equal(t, "expired", expired[0].Status)
}

func TestCapturePaymentReplaysCapturedPaymentForSameIdempotencyKeyAndPayment(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	bank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
	command := mustCapturePaymentCommand(t, string(payment.ID()), "public-capture-key-1")
	first, err := service.CapturePayment(context.Background(), command)
	require.NoError(t, err)
	bank.captureResult = app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440002"}

	replayed, err := service.CapturePayment(context.Background(), command)
	require.NoError(t, err)

	assert.Equal(t, first, replayed)
	assert.Equal(t, 1, bank.captureCalls)
}

func TestCapturePaymentRejectsReusedIdempotencyKeyWithDifferentPayment(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	secondPayment, err := testsupport.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440001"),
		"order-2",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440002",
		payment.AuthorizationExpiresAt(),
		"bok_550e8400-e29b-41d4-a716-446655440003",
		payment.AuthorizationCardFingerprint(),
		payment.CreatedAt(),
	)
	require.NoError(t, err)
	require.NoError(t, repo.SeedPayment(context.Background(), secondPayment))
	bank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
	_, err = service.CapturePayment(context.Background(), mustCapturePaymentCommand(t, string(payment.ID()), "public-capture-key-1"))
	require.NoError(t, err)

	_, err = service.CapturePayment(context.Background(), mustCapturePaymentCommand(t, string(secondPayment.ID()), "public-capture-key-1"))

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	assert.Equal(t, 1, bank.captureCalls)
}

func TestRefundPaymentCallsBankStoresRefundedPaymentAndReturnsPublicResult(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	capturedAt := time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC)
	payment := newCapturedDomainPayment(t, capturedAt)
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	bank := &bankFake{refundResult: app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440002"}}
	refundedAt := time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, bank, refundedAt)

	refunded, err := service.RefundPayment(context.Background(), mustRefundPaymentCommand(t, string(payment.ID()), "public-refund-key-1"))
	require.NoError(t, err)

	assert.Equal(t, app.BankRefundRequest{
		OperationKey:  "bok_123",
		BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001",
		AmountCents:   1299,
		Currency:      "USD",
	}, bank.refundRequest)
	assert.Equal(t, app.PaymentResult{
		ID:                     string(payment.ID()),
		OrderID:                "order-1",
		CustomerID:             "customer-1",
		AmountCents:            1299,
		Currency:               "USD",
		Status:                 "refunded",
		AuthorizationExpiresAt: payment.AuthorizationExpiresAt(),
		CreatedAt:              capturedAt,
		UpdatedAt:              refundedAt,
	}, refunded.Payment)
	assert.Equal(t, http.StatusOK, refunded.HTTPStatus)

	saved, err := repo.FindByID(context.Background(), payment.ID(), testNonExpiringStoreReadTime)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusRefunded, saved.Status())
	assert.Equal(t, "ref_550e8400-e29b-41d4-a716-446655440002", saved.BankRefundID())
	assert.Equal(t, "bok_123", saved.RefundBankOperationKey())
}

func TestNewRefundPaymentCommandRequiresIdempotencyKey(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newCapturedDomainPayment(t, time.Now())
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	bank := &bankFake{refundResult: app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440002"}}

	_, err := app.NewRefundPaymentCommand(string(payment.ID()), "")

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidInput))
	assert.Zero(t, bank.refundRequest)
}

func TestRefundPaymentRejectsNonCapturedStatusesWithoutCallingBank(t *testing.T) {
	statuses := []domain.PaymentStatus{
		domain.PaymentStatusPending,
		domain.PaymentStatusAuthorized,
		domain.PaymentStatusDeclined,
		domain.PaymentStatusVoided,
		domain.PaymentStatusRefunded,
	}

	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			repo := testsupport.NewPaymentStore()
			payment := loadDomainPaymentForStatus(t, status)
			require.NoError(t, repo.SeedPayment(context.Background(), payment))
			bank := &bankFake{refundResult: app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440002"}}
			service := newPaymentService(repo, bank, time.Now())

			_, err := service.RefundPayment(context.Background(), mustRefundPaymentCommand(t, string(payment.ID()), "public-refund-key-1"))

			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorPaymentStatusConflict))
			assert.Zero(t, bank.refundRequest)
		})
	}
}

func TestRefundPaymentLeavesPaymentStatusUnchangedWhenBankFails(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind app.PaymentErrorKind
	}{
		{name: "unavailable", err: app.NewPaymentBankUnavailableError(errors.New("connection refused")), kind: app.PaymentErrorBankUnavailable},
		{name: "timeout", err: app.NewPaymentBankTimeoutError(context.DeadlineExceeded), kind: app.PaymentErrorBankTimeout},
		{name: "bank state conflict", err: app.NewPaymentBankStateConflictError(errors.New("already refunded")), kind: app.PaymentErrorBankStateConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := testsupport.NewPaymentStore()
			capturedAt := time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC)
			payment := newCapturedDomainPayment(t, capturedAt)
			require.NoError(t, repo.SeedPayment(context.Background(), payment))
			service := newPaymentService(repo, &bankFake{refundErr: tt.err}, time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC))

			_, err := service.RefundPayment(context.Background(), mustRefundPaymentCommand(t, string(payment.ID()), "public-refund-key-1"))

			assert.True(t, app.HasPaymentErrorKind(err, tt.kind))
			saved, findErr := repo.FindByID(context.Background(), payment.ID(), testNonExpiringStoreReadTime)
			require.NoError(t, findErr)
			assert.Equal(t, domain.PaymentStatusCaptured, saved.Status())
			assert.Equal(t, capturedAt, saved.UpdatedAt())
			assert.Empty(t, saved.BankRefundID())
			assert.Equal(t, "bok_123", saved.RefundBankOperationKey())
		})
	}
}

func TestRefundPaymentReusesStoredBankOperationKeyAfterProviderFailure(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newCapturedDomainPayment(t, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	firstBank := &bankFake{refundErr: app.NewPaymentBankTimeoutError(context.DeadlineExceeded)}
	service := newPaymentServiceWithBankOperationKeys(repo, firstBank, time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_first", "bok_second"}})

	_, err := service.RefundPayment(context.Background(), mustRefundPaymentCommand(t, string(payment.ID()), "public-refund-key-1"))
	require.Error(t, err)

	secondBank := &bankFake{refundResult: app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440002"}}
	service = newPaymentServiceWithBankOperationKeys(repo, secondBank, time.Date(2026, 6, 18, 13, 1, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_second"}})
	result, err := service.RefundPayment(context.Background(), mustRefundPaymentCommand(t, string(payment.ID()), "public-refund-key-1"))

	require.NoError(t, err)
	assert.Equal(t, "bok_first", secondBank.refundRequest.OperationKey)
	assert.Equal(t, "refunded", result.Payment.Status)
}

func TestRefundPaymentRecoversStuckClaimUsingPersistedBankOperationKey(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newCapturedDomainPayment(t, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	crashingBank := &bankFake{}
	crashingBank.onRefund = func() {
		panic("process crashed after refund claim")
	}
	service := newPaymentServiceWithBankOperationKeys(repo, crashingBank, time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_refund_original"}})
	command := mustRefundPaymentCommand(t, string(payment.ID()), "public-refund-key-1")
	require.PanicsWithValue(t, "process crashed after refund claim", func() {
		_, _ = service.RefundPayment(context.Background(), command)
	})
	repo.AgeClaim(app.RefundPaymentOperation, "public-refund-key-1", time.Date(2026, 6, 18, 12, 54, 0, 0, time.UTC))

	metrics := &paymentOperationMetricsFake{}
	bank := &bankFake{refundResult: app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440002"}}
	recoveryService := newPaymentServiceWithBankOperationKeysAndMetrics(repo, bank, time.Date(2026, 6, 18, 13, 1, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_refund_new", "bok_refund_replay"}}, metrics)
	result, err := recoveryService.RefundPayment(context.Background(), command)
	require.NoError(t, err)

	assert.Equal(t, "refunded", result.Payment.Status)
	assert.Equal(t, "bok_refund_original", bank.refundRequest.OperationKey)
	assert.Equal(t, []idempotencyRecoveryMetricRecord{
		{operation: app.RefundPaymentOperation, result: app.IdempotencyRecoveryAttempted},
		{operation: app.RefundPaymentOperation, result: app.IdempotencyRecoveryRecovered},
	}, metrics.recoveryRecords)

	replayed, err := recoveryService.RefundPayment(context.Background(), command)
	require.NoError(t, err)
	assert.Equal(t, result, replayed)
	assert.Equal(t, 1, bank.refundCalls)
}

func TestRefundPaymentReplaysRefundedPaymentForSameIdempotencyKeyAndPayment(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newCapturedDomainPayment(t, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	bank := &bankFake{refundResult: app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440001"}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC))
	command := mustRefundPaymentCommand(t, string(payment.ID()), "public-refund-key-1")
	first, err := service.RefundPayment(context.Background(), command)
	require.NoError(t, err)
	bank.refundResult = app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440002"}

	replayed, err := service.RefundPayment(context.Background(), command)
	require.NoError(t, err)

	assert.Equal(t, first, replayed)
	assert.Equal(t, 1, bank.refundCalls)
}

func TestRefundPaymentRejectsReusedIdempotencyKeyWithDifferentPayment(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	payment := newCapturedDomainPayment(t, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
	require.NoError(t, repo.SeedPayment(context.Background(), payment))
	secondPayment := loadDomainPaymentForStatus(t, domain.PaymentStatusCaptured)
	secondPayment, err := domain.LoadPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440001"),
		"order-2",
		"customer-1",
		1299,
		domain.CurrencyUSD,
		domain.PaymentStatusCaptured,
		"auth_550e8400-e29b-41d4-a716-446655440004",
		payment.AuthorizationExpiresAt(),
		"bok_550e8400-e29b-41d4-a716-446655440005",
		"fingerprint-1",
		"cap_550e8400-e29b-41d4-a716-446655440006",
		"bok_550e8400-e29b-41d4-a716-446655440007",
		"",
		"",
		"",
		"",
		"",
		payment.CreatedAt(),
		payment.CreatedAt(),
	)
	require.NoError(t, err)
	require.NoError(t, repo.SeedPayment(context.Background(), secondPayment))
	bank := &bankFake{refundResult: app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440001"}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC))
	_, err = service.RefundPayment(context.Background(), mustRefundPaymentCommand(t, string(payment.ID()), "public-refund-key-1"))
	require.NoError(t, err)

	_, err = service.RefundPayment(context.Background(), mustRefundPaymentCommand(t, string(secondPayment.ID()), "public-refund-key-1"))

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	assert.Equal(t, 1, bank.refundCalls)
}

func TestNewPaymentServiceRequiresCollaborators(t *testing.T) {
	validStore := testsupport.NewPaymentStore()
	validPaymentIDs := testsupport.FixedPaymentIDGenerator{ID: domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000")}
	validBankOperationKeys := testsupport.FixedBankOperationKeyGenerator{Key: "bok_123"}
	validBank := &bankFake{authorizeResult: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	validOperationMetrics := &paymentOperationMetricsFake{}
	validClock := testsupport.FixedClock{Time: time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)}

	tests := []struct {
		name   string
		build  func()
		reason string
	}{
		{
			name: "payment store",
			build: func() {
				app.NewPaymentService(nil, validPaymentIDs, validBankOperationKeys, validBank, validOperationMetrics, validClock, "secret", testIdempotencyClaimStuckAfter)
			},
			reason: "payment store is required",
		},
		{
			name: "payment ID generator",
			build: func() {
				app.NewPaymentService(validStore, nil, validBankOperationKeys, validBank, validOperationMetrics, validClock, "secret", testIdempotencyClaimStuckAfter)
			},
			reason: "payment ID generator is required",
		},
		{
			name: "bank operation key generator",
			build: func() {
				app.NewPaymentService(validStore, validPaymentIDs, nil, validBank, validOperationMetrics, validClock, "secret", testIdempotencyClaimStuckAfter)
			},
			reason: "bank operation key generator is required",
		},
		{
			name: "bank authorizer",
			build: func() {
				app.NewPaymentService(validStore, validPaymentIDs, validBankOperationKeys, nil, validOperationMetrics, validClock, "secret", testIdempotencyClaimStuckAfter)
			},
			reason: "bank authorizer is required",
		},
		{
			name: "payment operation metrics",
			build: func() {
				app.NewPaymentService(validStore, validPaymentIDs, validBankOperationKeys, validBank, nil, validClock, "secret", testIdempotencyClaimStuckAfter)
			},
			reason: "payment operation metrics is required",
		},
		{
			name: "clock",
			build: func() {
				app.NewPaymentService(validStore, validPaymentIDs, validBankOperationKeys, validBank, validOperationMetrics, nil, "secret", testIdempotencyClaimStuckAfter)
			},
			reason: "clock is required",
		},
		{
			name: "fingerprint secret",
			build: func() {
				app.NewPaymentService(validStore, validPaymentIDs, validBankOperationKeys, validBank, validOperationMetrics, validClock, " ", testIdempotencyClaimStuckAfter)
			},
			reason: "fingerprint secret is required",
		},
		{
			name: "idempotency claim stuck-after",
			build: func() {
				app.NewPaymentService(validStore, validPaymentIDs, validBankOperationKeys, validBank, validOperationMetrics, validClock, "secret", 0)
			},
			reason: "idempotency claim stuck-after must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.PanicsWithValue(t, tt.reason, tt.build)
		})
	}
}

func mustAuthorizedPayment(t *testing.T, id string, orderID string, customerID string, now time.Time) *domain.Payment {
	t.Helper()

	payment, err := testsupport.NewAuthorizedPayment(
		domain.PaymentID(id),
		orderID,
		customerID,
		1299,
		"bank-auth-id-1",
		now.Add(time.Hour),
		"bok_123",
		"fingerprint-1",
		now,
	)
	require.NoError(t, err)
	return payment
}

func mustDeclinedPayment(t *testing.T, id string, orderID string, customerID string, now time.Time) *domain.Payment {
	t.Helper()

	payment, err := testsupport.NewDeclinedPayment(
		domain.PaymentID(id),
		orderID,
		customerID,
		1299,
		domain.DeclineReasonInvalidCard,
		"bok_123",
		"fingerprint-1",
		now,
	)
	require.NoError(t, err)
	return payment
}

func validAuthorizeCommand() app.AuthorizePaymentCommand {
	command, err := newAuthorizePaymentCommand("order-1", "customer-1", 1299, validCardDetails(), "public-key-1")
	if err != nil {
		panic(err)
	}
	return command
}

func validRetryAuthorizationCommand(paymentID string) app.RetryAuthorizationCommand {
	command, err := newRetryAuthorizationCommand(paymentID, validCardDetails(), "retry-key-1")
	if err != nil {
		panic(err)
	}
	return command
}

func newAuthorizePaymentCommand(orderID string, customerID string, amountCents int64, card cardInput, idempotencyKey string) (app.AuthorizePaymentCommand, error) {
	return app.NewAuthorizePaymentCommand(
		orderID,
		customerID,
		amountCents,
		card.number,
		card.cvv,
		card.expiryMonth,
		card.expiryYear,
		idempotencyKey,
	)
}

func newRetryAuthorizationCommand(paymentID string, card cardInput, idempotencyKey string) (app.RetryAuthorizationCommand, error) {
	return app.NewRetryAuthorizationCommand(
		paymentID,
		card.number,
		card.cvv,
		card.expiryMonth,
		card.expiryYear,
		idempotencyKey,
	)
}

func validCardDetails() cardInput {
	return cardInput{
		number:      "4111111111111111",
		cvv:         "123",
		expiryMonth: 12,
		expiryYear:  2030,
	}
}

func mustGetPaymentQuery(t *testing.T, paymentID string) app.GetPaymentQuery {
	t.Helper()
	query, err := app.NewGetPaymentQuery(paymentID)
	require.NoError(t, err)
	return query
}

func mustSearchPaymentsQuery(t *testing.T, orderID string, customerID string, status string) app.SearchPaymentsQuery {
	t.Helper()
	query, err := app.NewSearchPaymentsQuery(orderID, customerID, status)
	require.NoError(t, err)
	return query
}

func mustCapturePaymentCommand(t *testing.T, paymentID string, idempotencyKey string) app.CapturePaymentCommand {
	t.Helper()
	command, err := app.NewCapturePaymentCommand(paymentID, idempotencyKey)
	require.NoError(t, err)
	return command
}

func capturePaymentRequestFingerprintForTest(t *testing.T, paymentID string) string {
	t.Helper()
	_, err := app.NewCapturePaymentCommand(paymentID, "public-capture-key-1")
	require.NoError(t, err)

	hash := hmac.New(sha256.New, []byte("fingerprint-secret"))
	_, _ = fmt.Fprintf(hash, "%s\n%s", app.CapturePaymentOperation, paymentID)
	return hex.EncodeToString(hash.Sum(nil))
}

func mustVoidPaymentCommand(t *testing.T, paymentID string, idempotencyKey string) app.VoidPaymentCommand {
	t.Helper()
	command, err := app.NewVoidPaymentCommand(paymentID, idempotencyKey)
	require.NoError(t, err)
	return command
}

func mustRefundPaymentCommand(t *testing.T, paymentID string, idempotencyKey string) app.RefundPaymentCommand {
	t.Helper()
	command, err := app.NewRefundPaymentCommand(paymentID, idempotencyKey)
	require.NoError(t, err)
	return command
}

func newPaymentService(repo app.PaymentStore, bank app.BankClient, now time.Time) *app.PaymentService {
	return newPaymentServiceWithMetrics(repo, bank, now, &paymentOperationMetricsFake{})
}

func newPaymentServiceWithMetrics(repo app.PaymentStore, bank app.BankClient, now time.Time, metrics app.PaymentOperationMetrics) *app.PaymentService {
	return newPaymentServiceWithBankOperationKeysAndMetrics(
		repo,
		bank,
		now,
		testsupport.FixedBankOperationKeyGenerator{Key: "bok_123"},
		metrics,
	)
}

func newPaymentServiceWithBankOperationKeys(repo app.PaymentStore, bank app.BankClient, now time.Time, bankOperationKeys app.BankOperationKeyGenerator) *app.PaymentService {
	return newPaymentServiceWithBankOperationKeysAndMetrics(repo, bank, now, bankOperationKeys, &paymentOperationMetricsFake{})
}

func newPaymentServiceWithBankOperationKeysAndMetrics(repo app.PaymentStore, bank app.BankClient, now time.Time, bankOperationKeys app.BankOperationKeyGenerator, metrics app.PaymentOperationMetrics) *app.PaymentService {
	return app.NewPaymentService(
		repo,
		testsupport.FixedPaymentIDGenerator{ID: domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000")},
		bankOperationKeys,
		bank,
		metrics,
		testsupport.FixedClock{Time: now},
		"fingerprint-secret",
		testIdempotencyClaimStuckAfter,
	)
}

func assertPaymentOperationMetric(t *testing.T, record paymentOperationMetricRecord, operation string, outcome string) {
	t.Helper()

	assert.Equal(t, operation, record.operation)
	assert.Equal(t, outcome, record.outcome)
	assert.GreaterOrEqual(t, record.duration, time.Duration(0))
}

type paymentOperationMetricRecord struct {
	operation string
	outcome   string
	duration  time.Duration
}

type idempotencyRecoveryMetricRecord struct {
	operation string
	result    string
}

type paymentOperationMetricsFake struct {
	records                  []paymentOperationMetricRecord
	recoveryRecords          []idempotencyRecoveryMetricRecord
	releaseFailureOperations []string
}

func (m *paymentOperationMetricsFake) RecordPaymentOperation(operation string, outcome string, duration time.Duration) {
	m.records = append(m.records, paymentOperationMetricRecord{
		operation: operation,
		outcome:   outcome,
		duration:  duration,
	})
}

func (m *paymentOperationMetricsFake) RecordIdempotencyRecovery(operation string, result string) {
	m.recoveryRecords = append(m.recoveryRecords, idempotencyRecoveryMetricRecord{
		operation: operation,
		result:    result,
	})
}

func (m *paymentOperationMetricsFake) RecordPaymentCommandReleaseFailure(operation string) {
	m.releaseFailureOperations = append(m.releaseFailureOperations, operation)
}

type sequenceBankOperationKeyGenerator struct {
	keys []string
	next int
}

func (g *sequenceBankOperationKeyGenerator) NewBankOperationKey() string {
	key := g.keys[g.next]
	g.next++
	return key
}

type bankAuthorizerFake struct {
	request     app.BankAuthorizationRequest
	result      app.BankAuthorizationResult
	err         error
	calls       int
	onAuthorize func()
	voidRequest app.BankVoidRequest
	voidResult  app.BankVoidResult
	voidErr     error
	voidCalls   int
}

func (f *bankAuthorizerFake) AuthorizePayment(_ context.Context, request app.BankAuthorizationRequest) (app.BankAuthorizationResult, error) {
	f.request = request
	f.calls++
	if f.onAuthorize != nil {
		f.onAuthorize()
	}
	if f.result.BankAuthorizationID != "" && f.result.AuthorizationExpiresAt.IsZero() {
		f.result.AuthorizationExpiresAt = defaultAuthorizationExpiresAt()
	}
	return f.result, f.err
}

func (f *bankAuthorizerFake) CapturePayment(context.Context, app.BankCaptureRequest) (app.BankCaptureResult, error) {
	return app.BankCaptureResult{}, errors.New("unexpected capture call")
}

func (f *bankAuthorizerFake) VoidPayment(_ context.Context, request app.BankVoidRequest) (app.BankVoidResult, error) {
	f.voidRequest = request
	f.voidCalls++
	return f.voidResult, f.voidErr
}

func (f *bankAuthorizerFake) RefundPayment(context.Context, app.BankRefundRequest) (app.BankRefundResult, error) {
	return app.BankRefundResult{}, errors.New("unexpected refund call")
}

type bankFake struct {
	authorizeRequest app.BankAuthorizationRequest
	authorizeResult  app.BankAuthorizationResult
	authorizeErr     error
	authorizeCalls   int
	captureRequest   app.BankCaptureRequest
	captureResult    app.BankCaptureResult
	captureErr       error
	captureCalls     int
	onCapture        func()
	voidRequest      app.BankVoidRequest
	voidResult       app.BankVoidResult
	voidErr          error
	voidCalls        int
	onVoid           func()
	refundRequest    app.BankRefundRequest
	refundResult     app.BankRefundResult
	refundErr        error
	refundCalls      int
	onRefund         func()
}

type alwaysInProgressPaymentStore struct {
	app.PaymentStore
}

type failingFindPaymentStore struct {
	app.PaymentStore
	err error
}

type failingReleasePaymentStore struct {
	app.PaymentStore
	err error
}

func (s *failingReleasePaymentStore) ReleasePaymentCommand(context.Context, app.PaymentCommandClaim) error {
	return s.err
}

func (s *failingFindPaymentStore) FindByID(context.Context, domain.PaymentID, time.Time) (*domain.Payment, error) {
	return nil, s.err
}

func (s *alwaysInProgressPaymentStore) ClaimAuthorizationStart(context.Context, app.AuthorizationStartClaimRequest) (app.PaymentCommandClaim, error) {
	return app.PaymentCommandClaim{}, app.NewPaymentIdempotencyInProgressError(nil)
}

func (s *alwaysInProgressPaymentStore) ClaimExistingPaymentCommand(context.Context, app.ExistingPaymentCommandClaimRequest) (app.PaymentCommandClaim, error) {
	return app.PaymentCommandClaim{}, app.NewPaymentIdempotencyInProgressError(nil)
}

func (f *bankFake) AuthorizePayment(_ context.Context, request app.BankAuthorizationRequest) (app.BankAuthorizationResult, error) {
	f.authorizeRequest = request
	f.authorizeCalls++
	if f.authorizeResult.BankAuthorizationID != "" && f.authorizeResult.AuthorizationExpiresAt.IsZero() {
		f.authorizeResult.AuthorizationExpiresAt = defaultAuthorizationExpiresAt()
	}
	return f.authorizeResult, f.authorizeErr
}

func defaultAuthorizationExpiresAt() time.Time {
	return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
}

func (f *bankFake) CapturePayment(_ context.Context, request app.BankCaptureRequest) (app.BankCaptureResult, error) {
	f.captureRequest = request
	f.captureCalls++
	if f.onCapture != nil {
		f.onCapture()
	}
	return f.captureResult, f.captureErr
}

func (f *bankFake) VoidPayment(_ context.Context, request app.BankVoidRequest) (app.BankVoidResult, error) {
	f.voidRequest = request
	f.voidCalls++
	if f.onVoid != nil {
		f.onVoid()
	}
	return f.voidResult, f.voidErr
}

func (f *bankFake) RefundPayment(_ context.Context, request app.BankRefundRequest) (app.BankRefundResult, error) {
	f.refundRequest = request
	f.refundCalls++
	if f.onRefund != nil {
		f.onRefund()
	}
	return f.refundResult, f.refundErr
}

func newAuthorizedDomainPayment(t *testing.T, now time.Time) *domain.Payment {
	t.Helper()

	payment, err := testsupport.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440000",
		now.Add(time.Hour),
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		now,
	)
	require.NoError(t, err)
	return payment
}

func newCapturedDomainPayment(t *testing.T, now time.Time) *domain.Payment {
	t.Helper()

	payment := newAuthorizedDomainPayment(t, now)
	require.NoError(t, payment.MarkCaptured(
		"cap_550e8400-e29b-41d4-a716-446655440001",
		"bok_550e8400-e29b-41d4-a716-446655440002",
		now,
	))
	return payment
}

func loadDomainPaymentForStatus(t *testing.T, status domain.PaymentStatus) *domain.Payment {
	t.Helper()

	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	var (
		bankAuthorizationID     string
		authorizationExpiresAt  time.Time
		captureBankOperationKey string
		bankCaptureID           string
		refundBankOperationKey  string
		bankRefundID            string
		voidBankOperationKey    string
		bankVoidID              string
		declineReason           domain.DeclineReason
	)
	switch status {
	case domain.PaymentStatusPending:
		// Pending payments keep the authorization operation key but have no definitive bank result yet.
	case domain.PaymentStatusDeclined:
		declineReason = domain.DeclineReasonUnknown
	case domain.PaymentStatusAuthorized:
		bankAuthorizationID = "auth_550e8400-e29b-41d4-a716-446655440000"
		authorizationExpiresAt = now.Add(time.Hour)
	case domain.PaymentStatusCaptured:
		bankAuthorizationID = "auth_550e8400-e29b-41d4-a716-446655440000"
		authorizationExpiresAt = now.Add(time.Hour)
		bankCaptureID = "cap_550e8400-e29b-41d4-a716-446655440002"
		captureBankOperationKey = "bok_550e8400-e29b-41d4-a716-446655440003"
	case domain.PaymentStatusVoided:
		bankAuthorizationID = "auth_550e8400-e29b-41d4-a716-446655440000"
		authorizationExpiresAt = now.Add(time.Hour)
		bankVoidID = "void_550e8400-e29b-41d4-a716-446655440002"
		voidBankOperationKey = "bok_550e8400-e29b-41d4-a716-446655440003"
	case domain.PaymentStatusRefunded:
		bankAuthorizationID = "auth_550e8400-e29b-41d4-a716-446655440000"
		authorizationExpiresAt = now.Add(time.Hour)
		bankCaptureID = "cap_550e8400-e29b-41d4-a716-446655440002"
		captureBankOperationKey = "bok_550e8400-e29b-41d4-a716-446655440003"
		bankRefundID = "ref_550e8400-e29b-41d4-a716-446655440004"
		refundBankOperationKey = "bok_550e8400-e29b-41d4-a716-446655440005"
	}
	payment, err := domain.LoadPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		domain.CurrencyUSD,
		status,
		bankAuthorizationID,
		authorizationExpiresAt,
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		bankCaptureID,
		captureBankOperationKey,
		bankRefundID,
		refundBankOperationKey,
		bankVoidID,
		voidBankOperationKey,
		declineReason,
		now,
		now,
	)
	require.NoError(t, err)
	return payment
}
