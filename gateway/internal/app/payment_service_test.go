package app_test

import (
	"context"
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

	saved, err := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"))
	require.NoError(t, err)
	assert.Equal(t, "bank-auth-id-1", saved.BankAuthorizationID())
	assert.Equal(t, "bok_123", saved.AuthorizationBankOperationKey())
}

func TestAuthorizePaymentPersistsPendingPaymentBeforeCallingBank(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	bank.onAuthorize = func() {
		saved, err := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"))
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

	saved, err := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"))
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusDeclined, saved.Status())
	assert.Equal(t, domain.DeclineReasonInsufficientFunds, saved.DeclineReason())
	assert.Empty(t, saved.BankAuthorizationID())
	assert.Equal(t, "bok_123", saved.AuthorizationBankOperationKey())
}

func TestAuthorizePaymentStoresPendingPaymentAndReleasesClaimForUnknownBankOutcome(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	bankErr := app.NewPaymentBankTimeoutError(context.DeadlineExceeded)
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, &bankAuthorizerFake{err: bankErr}, now)

	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout))

	saved, err := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"))
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
	saved, err := repo.FindByID(context.Background(), domain.PaymentID(result.Payment.ID))
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusExpired, saved.Status())
}

func TestAuthorizePaymentStoresCardOnlyFingerprint(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	firstRepo := testsupport.NewPaymentStore()
	firstService := newPaymentService(firstRepo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailableError(errors.New("500"))}, now)
	_, err := firstService.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.Error(t, err)

	secondRepo := testsupport.NewPaymentStore()
	secondService := newPaymentService(secondRepo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailableError(errors.New("500"))}, now)
	secondCommand, err := newAuthorizePaymentCommand("order-1", "customer-1", 2599, validCardDetails(), "public-key-1")
	require.NoError(t, err)
	_, err = secondService.AuthorizePayment(context.Background(), secondCommand)
	require.Error(t, err)

	firstSaved, err := firstRepo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"))
	require.NoError(t, err)
	secondSaved, err := secondRepo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"))
	require.NoError(t, err)
	assert.Equal(t, firstSaved.AuthorizationCardFingerprint(), secondSaved.AuthorizationCardFingerprint())
}

func TestRetryAuthorizationResolvesPendingPaymentToAuthorized(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailableError(errors.New("500"))}, now)
	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.Error(t, err)

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
	require.Error(t, err)

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
	require.Error(t, err)

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
	require.Error(t, err)

	bank := &bankAuthorizerFake{err: app.NewPaymentBankTimeoutError(context.DeadlineExceeded)}
	service = newPaymentService(repo, bank, now.Add(time.Minute))

	_, err = service.RetryAuthorization(context.Background(), validRetryAuthorizationCommand("pay_550e8400-e29b-41d4-a716-446655440000"))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout))
	saved, findErr := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"))
	require.NoError(t, findErr)
	assert.Equal(t, domain.PaymentStatusPending, saved.Status())
	assert.Equal(t, now, saved.UpdatedAt())
	assert.Equal(t, 1, bank.calls)
}

func TestRetryAuthorizationRejectsFingerprintMismatchWithoutCallingBank(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	service := newPaymentService(repo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailableError(errors.New("500"))}, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.Error(t, err)

	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	service = newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 1, 0, 0, time.UTC))
	retryCard := validCardDetails()
	retryCard.number = "4000000000000002"
	retry, err := newRetryAuthorizationCommand("pay_550e8400-e29b-41d4-a716-446655440000", retryCard, "retry-key-1")
	require.NoError(t, err)

	_, err = service.RetryAuthorization(context.Background(), retry)

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidStatusConflict))
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

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidStatusConflict))
	assert.Zero(t, bank.calls)
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
		testsupport.FixedClock{Time: time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)},
		"fingerprint-secret",
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
		testsupport.FixedClock{Time: time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)},
		"fingerprint-secret",
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
	saved, findErr := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"))
	require.NoError(t, findErr)
	assert.Equal(t, domain.PaymentStatusPending, saved.Status())
	assert.Equal(t, "bok_123", saved.AuthorizationBankOperationKey())
}

func TestGetPaymentReturnsPublicResult(t *testing.T) {
	repo := testsupport.NewPaymentStore()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	payment, err := domain.NewAuthorizedPayment(
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
	saved, err := repo.FindByID(context.Background(), payment.ID())
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

	saved, err := repo.FindByID(context.Background(), payment.ID())
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

			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidStatusConflict))
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
			saved, findErr := repo.FindByID(context.Background(), payment.ID())
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
	secondPayment, err := domain.NewAuthorizedPayment(
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

	saved, err := repo.FindByID(context.Background(), payment.ID())
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

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidStatusConflict))
	assert.Zero(t, bank.captureCalls)
	saved, findErr := repo.FindByID(context.Background(), payment.ID())
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

			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidStatusConflict))
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
			saved, findErr := repo.FindByID(context.Background(), payment.ID())
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

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidStatusConflict))
	saved, findErr := repo.FindByID(context.Background(), payment.ID())
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
	secondPayment, err := domain.NewAuthorizedPayment(
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

	saved, err := repo.FindByID(context.Background(), payment.ID())
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

			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidStatusConflict))
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
			saved, findErr := repo.FindByID(context.Background(), payment.ID())
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
	validClock := testsupport.FixedClock{Time: time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)}

	tests := []struct {
		name   string
		build  func()
		reason string
	}{
		{
			name: "payment store",
			build: func() {
				app.NewPaymentService(nil, validPaymentIDs, validBankOperationKeys, validBank, validClock, "secret")
			},
			reason: "payment store is required",
		},
		{
			name: "payment ID generator",
			build: func() {
				app.NewPaymentService(validStore, nil, validBankOperationKeys, validBank, validClock, "secret")
			},
			reason: "payment ID generator is required",
		},
		{
			name: "bank operation key generator",
			build: func() {
				app.NewPaymentService(validStore, validPaymentIDs, nil, validBank, validClock, "secret")
			},
			reason: "bank operation key generator is required",
		},
		{
			name: "bank authorizer",
			build: func() {
				app.NewPaymentService(validStore, validPaymentIDs, validBankOperationKeys, nil, validClock, "secret")
			},
			reason: "bank authorizer is required",
		},
		{
			name: "clock",
			build: func() {
				app.NewPaymentService(validStore, validPaymentIDs, validBankOperationKeys, validBank, nil, "secret")
			},
			reason: "clock is required",
		},
		{
			name: "fingerprint secret",
			build: func() {
				app.NewPaymentService(validStore, validPaymentIDs, validBankOperationKeys, validBank, validClock, " ")
			},
			reason: "fingerprint secret is required",
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

	payment, err := domain.NewAuthorizedPayment(
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

	payment, err := domain.NewDeclinedPayment(
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
	return newPaymentServiceWithBankOperationKeys(
		repo,
		bank,
		now,
		testsupport.FixedBankOperationKeyGenerator{Key: "bok_123"},
	)
}

func newPaymentServiceWithBankOperationKeys(repo app.PaymentStore, bank app.BankClient, now time.Time, bankOperationKeys app.BankOperationKeyGenerator) *app.PaymentService {
	return app.NewPaymentService(
		repo,
		testsupport.FixedPaymentIDGenerator{ID: domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000")},
		bankOperationKeys,
		bank,
		testsupport.FixedClock{Time: now},
		"fingerprint-secret",
	)
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
	voidRequest      app.BankVoidRequest
	voidResult       app.BankVoidResult
	voidErr          error
	voidCalls        int
	refundRequest    app.BankRefundRequest
	refundResult     app.BankRefundResult
	refundErr        error
	refundCalls      int
}

type alwaysInProgressPaymentStore struct {
	app.PaymentStore
}

type failingFindPaymentStore struct {
	app.PaymentStore
	err error
}

func (s *failingFindPaymentStore) FindByID(context.Context, domain.PaymentID) (*domain.Payment, error) {
	return nil, s.err
}

func (s *alwaysInProgressPaymentStore) ClaimPaymentCommand(context.Context, app.PaymentCommandClaimRequest) (app.PaymentCommandClaim, error) {
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
	return f.captureResult, f.captureErr
}

func (f *bankFake) VoidPayment(_ context.Context, request app.BankVoidRequest) (app.BankVoidResult, error) {
	f.voidRequest = request
	f.voidCalls++
	return f.voidResult, f.voidErr
}

func (f *bankFake) RefundPayment(_ context.Context, request app.BankRefundRequest) (app.BankRefundResult, error) {
	f.refundRequest = request
	f.refundCalls++
	return f.refundResult, f.refundErr
}

func newAuthorizedDomainPayment(t *testing.T, now time.Time) *domain.Payment {
	t.Helper()

	payment, err := domain.NewAuthorizedPayment(
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
	require.NoError(t, payment.Capture(
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
