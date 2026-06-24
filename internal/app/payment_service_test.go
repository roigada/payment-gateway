package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/roigada/payment-gateway/internal/testsupport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthorizePaymentCallsBankStoresAuthorizedPaymentAndReturnsPublicResult(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, bank, now)

	payment, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	assert.Equal(t, app.BankAuthorizationRequest{
		OperationKey: "bok_123",
		OrderID:      "order-1",
		CustomerID:   "customer-1",
		AmountCents:  1299,
		Currency:     "USD",
		Card: app.CardDetails{
			Number:      "4111111111111111",
			CVV:         "123",
			ExpiryMonth: 12,
			ExpiryYear:  2030,
		},
	}, bank.request)
	assert.Equal(t, app.PaymentResult{
		ID:             "pay_550e8400-e29b-41d4-a716-446655440000",
		OrderID:        "order-1",
		CustomerID:     "customer-1",
		AmountCents:    1299,
		Currency:       "USD",
		Status:         "authorized",
		CreatedAt:      now,
		UpdatedAt:      now,
		ResponseStatus: 201,
	}, payment)

	saved, err := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"))
	require.NoError(t, err)
	assert.Equal(t, "bank-auth-id-1", saved.BankAuthorizationID())
	assert.Equal(t, "bok_123", saved.AuthorizationBankOperationKey())
}

func TestAuthorizePaymentStoresDeclinedPaymentAndReturnsPublicResult(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{DeclineReason: domain.DeclineReasonInsufficientFunds}}
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, bank, now)

	payment, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	assert.Equal(t, app.PaymentResult{
		ID:             "pay_550e8400-e29b-41d4-a716-446655440000",
		OrderID:        "order-1",
		CustomerID:     "customer-1",
		AmountCents:    1299,
		Currency:       "USD",
		Status:         "declined",
		DeclineReason:  "insufficient_funds",
		CreatedAt:      now,
		UpdatedAt:      now,
		ResponseStatus: 201,
	}, payment)

	saved, err := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"))
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusDeclined, saved.Status())
	assert.Equal(t, domain.DeclineReasonInsufficientFunds, saved.DeclineReason())
	assert.Empty(t, saved.BankAuthorizationID())
	assert.Equal(t, "bok_123", saved.AuthorizationBankOperationKey())
}

func TestAuthorizePaymentStoresPendingPaymentForUnknownBankOutcome(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	bankErr := app.NewPaymentBankTimeout(context.DeadlineExceeded)
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, &bankAuthorizerFake{err: bankErr}, now)

	payment, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	assert.Equal(t, app.PaymentResult{
		ID:             "pay_550e8400-e29b-41d4-a716-446655440000",
		OrderID:        "order-1",
		CustomerID:     "customer-1",
		AmountCents:    1299,
		Currency:       "USD",
		Status:         "pending",
		CreatedAt:      now,
		UpdatedAt:      now,
		ResponseStatus: 201,
	}, payment)

	saved, err := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"))
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusPending, saved.Status())
	assert.Equal(t, "bok_123", saved.AuthorizationBankOperationKey())
	assert.NotEmpty(t, saved.AuthorizationCardFingerprint())
	assert.NotContains(t, saved.AuthorizationCardFingerprint(), "4111111111111111")
	assert.NotContains(t, saved.AuthorizationCardFingerprint(), "123")
}

func TestAuthorizePaymentStoresCardOnlyFingerprint(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	firstRepo := testsupport.NewPaymentRepository()
	firstService := newPaymentService(firstRepo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailable(errors.New("500"))}, now)
	first, err := firstService.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	secondRepo := testsupport.NewPaymentRepository()
	secondService := newPaymentService(secondRepo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailable(errors.New("500"))}, now)
	secondCommand := validAuthorizeCommand()
	secondCommand.AmountCents = 2599
	second, err := secondService.AuthorizePayment(context.Background(), secondCommand)
	require.NoError(t, err)

	firstSaved, err := firstRepo.FindByID(context.Background(), domain.PaymentID(first.ID))
	require.NoError(t, err)
	secondSaved, err := secondRepo.FindByID(context.Background(), domain.PaymentID(second.ID))
	require.NoError(t, err)
	assert.Equal(t, firstSaved.AuthorizationCardFingerprint(), secondSaved.AuthorizationCardFingerprint())
}

func TestRetryAuthorizationResolvesPendingPaymentToAuthorized(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailable(errors.New("500"))}, now)
	pending, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	service = newPaymentService(repo, bank, now.Add(time.Minute))
	retry := validRetryAuthorizationCommand(pending.ID)

	payment, err := service.RetryAuthorization(context.Background(), retry)
	require.NoError(t, err)

	assert.Equal(t, "authorized", payment.Status)
	assert.Equal(t, now.Add(time.Minute), payment.UpdatedAt)
	assert.Equal(t, app.BankAuthorizationRequest{
		OperationKey: "bok_123",
		OrderID:      "order-1",
		CustomerID:   "customer-1",
		AmountCents:  1299,
		Currency:     "USD",
		Card: app.CardDetails{
			Number:      "4111111111111111",
			CVV:         "123",
			ExpiryMonth: 12,
			ExpiryYear:  2030,
		},
	}, bank.request)
}

func TestRetryAuthorizationResolvesPendingPaymentToDeclined(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailable(errors.New("500"))}, now)
	pending, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{DeclineReason: domain.DeclineReasonInvalidCard}}
	service = newPaymentService(repo, bank, now.Add(time.Minute))

	payment, err := service.RetryAuthorization(context.Background(), validRetryAuthorizationCommand(pending.ID))
	require.NoError(t, err)

	assert.Equal(t, "declined", payment.Status)
	assert.Equal(t, "invalid_card", payment.DeclineReason)
	assert.Equal(t, 1, bank.calls)
}

func TestRetryAuthorizationLeavesPendingPaymentPendingForUnknownBankOutcome(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailable(errors.New("500"))}, now)
	pending, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	bank := &bankAuthorizerFake{err: app.NewPaymentBankTimeout(context.DeadlineExceeded)}
	service = newPaymentService(repo, bank, now.Add(time.Minute))

	payment, err := service.RetryAuthorization(context.Background(), validRetryAuthorizationCommand(pending.ID))
	require.NoError(t, err)

	assert.Equal(t, "pending", payment.Status)
	assert.Equal(t, now.Add(time.Minute), payment.UpdatedAt)
	assert.Equal(t, 1, bank.calls)
}

func TestRetryAuthorizationRejectsFingerprintMismatchWithoutCallingBank(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	service := newPaymentService(repo, &bankAuthorizerFake{err: app.NewPaymentBankUnavailable(errors.New("500"))}, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	pending, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	service = newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 1, 0, 0, time.UTC))
	retry := validRetryAuthorizationCommand(pending.ID)
	retry.Card.Number = "4000000000000002"

	_, err = service.RetryAuthorization(context.Background(), retry)

	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorInvalidStatusConflict))
	assert.Zero(t, bank.calls)
}

func TestRetryAuthorizationRejectsNonPendingPaymentWithoutCallingBank(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	service := newPaymentService(repo, &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	authorized, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)

	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-2"}}
	service = newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 1, 0, 0, time.UTC))

	_, err = service.RetryAuthorization(context.Background(), validRetryAuthorizationCommand(authorized.ID))

	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorInvalidStatusConflict))
	assert.Zero(t, bank.calls)
}

func TestAuthorizePaymentReplaysDeclinedPaymentForSameIdempotencyKeyAndRequest(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
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
	repo := testsupport.NewPaymentRepository()
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{DeclineReason: domain.DeclineReasonInvalidCard}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	first := validAuthorizeCommand()
	_, err := service.AuthorizePayment(context.Background(), first)
	require.NoError(t, err)

	second := validAuthorizeCommand()
	second.Card.CVV = "999"
	replayed, err := service.AuthorizePayment(context.Background(), second)

	require.NoError(t, err)
	assert.Equal(t, "declined", replayed.Status)
	assert.Equal(t, 1, bank.calls)
}

func TestAuthorizePaymentRejectsReusedIdempotencyKeyWithDifferentRequest(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{DeclineReason: domain.DeclineReasonInvalidCard}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	first := validAuthorizeCommand()
	_, err := service.AuthorizePayment(context.Background(), first)
	require.NoError(t, err)

	second := validAuthorizeCommand()
	second.AmountCents = 2599
	_, err = service.AuthorizePayment(context.Background(), second)

	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	assert.Equal(t, 1, bank.calls)
}

func TestAuthorizePaymentRejectsInProgressIdempotencyKeyBeforeCallingBank(t *testing.T) {
	idempotency := &alwaysInProgressIdempotencyRepository{}
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	service := app.NewPaymentService(
		testsupport.NewPaymentRepository(),
		idempotency,
		testsupport.FixedPaymentIDGenerator{ID: domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000")},
		testsupport.FixedBankOperationKeyGenerator{Key: "bok_123"},
		bank,
		testsupport.FixedClock{Time: time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)},
		"fingerprint-secret",
	)

	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())

	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorIdempotencyInProgress))
	assert.Zero(t, bank.calls)
}

func TestAuthorizePaymentNormalizesRequestBeforeFingerprintBankCallAndStorage(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	command := validAuthorizeCommand()
	command.IdempotencyKey = " public-key-1 "
	command.OrderID = " order-1 "
	command.CustomerID = " customer-1 "
	command.Card.Number = " 4111111111111111 "
	command.Card.CVV = " 123 "

	payment, err := service.AuthorizePayment(context.Background(), command)
	require.NoError(t, err)

	assert.Equal(t, "order-1", bank.request.OrderID)
	assert.Equal(t, "customer-1", bank.request.CustomerID)
	assert.Equal(t, "4111111111111111", bank.request.Card.Number)
	assert.Equal(t, "123", bank.request.Card.CVV)
	assert.Equal(t, "order-1", payment.OrderID)
	assert.Equal(t, "customer-1", payment.CustomerID)

	replayed, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())
	require.NoError(t, err)
	assert.Equal(t, payment, replayed)
	assert.Equal(t, 1, bank.calls)
}

func TestAuthorizePaymentRequiresIdempotencyKeyBeforeCallingBank(t *testing.T) {
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	service := newPaymentService(testsupport.NewPaymentRepository(), bank, time.Now())
	command := validAuthorizeCommand()
	command.IdempotencyKey = ""

	_, err := service.AuthorizePayment(context.Background(), command)

	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorInvalidInput))
	assert.Zero(t, bank.request)
}

func TestAuthorizePaymentValidatesCommandBeforeCallingBank(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*app.AuthorizePaymentCommand)
	}{
		{name: "order id", mutate: func(c *app.AuthorizePaymentCommand) { c.OrderID = "" }},
		{name: "customer id", mutate: func(c *app.AuthorizePaymentCommand) { c.CustomerID = "" }},
		{name: "amount", mutate: func(c *app.AuthorizePaymentCommand) { c.AmountCents = 0 }},
		{name: "card number", mutate: func(c *app.AuthorizePaymentCommand) { c.Card.Number = "4111x" }},
		{name: "cvv", mutate: func(c *app.AuthorizePaymentCommand) { c.Card.CVV = "12x" }},
		{name: "expiry month", mutate: func(c *app.AuthorizePaymentCommand) { c.Card.ExpiryMonth = 13 }},
		{name: "expiry year", mutate: func(c *app.AuthorizePaymentCommand) { c.Card.ExpiryYear = 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
			service := newPaymentService(testsupport.NewPaymentRepository(), bank, time.Now())
			command := validAuthorizeCommand()
			tt.mutate(&command)

			_, err := service.AuthorizePayment(context.Background(), command)

			assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorInvalidInput))
			assert.Zero(t, bank.request)
		})
	}
}

func TestAuthorizePaymentDoesNotClaimIdempotencyForValidationFailure(t *testing.T) {
	idempotency := testsupport.NewIdempotencyRepository()
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
	service := app.NewPaymentService(
		testsupport.NewPaymentRepository(),
		idempotency,
		testsupport.FixedPaymentIDGenerator{ID: domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000")},
		testsupport.FixedBankOperationKeyGenerator{Key: "bok_123"},
		bank,
		testsupport.FixedClock{Time: time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)},
		"fingerprint-secret",
	)
	invalid := validAuthorizeCommand()
	invalid.AmountCents = 0
	_, err := service.AuthorizePayment(context.Background(), invalid)
	require.Error(t, err)

	valid := validAuthorizeCommand()
	valid.AmountCents = 1299
	_, err = service.AuthorizePayment(context.Background(), valid)

	require.NoError(t, err)
	assert.Equal(t, 1, bank.calls)
}

func TestAuthorizePaymentNormalizesBankErrorWithoutStoringPayment(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	bankErr := errors.New("bank unavailable")
	service := newPaymentService(repo, &bankAuthorizerFake{err: bankErr}, time.Now())

	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())

	assert.ErrorIs(t, err, bankErr)
	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorInternal))
	_, findErr := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"))
	assert.True(t, app.IsPaymentErrorKind(findErr, app.PaymentErrorNotFound))
}

func TestGetPaymentReturnsPublicResult(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	payment, err := domain.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"bank-auth-id-1",
		"bok_123",
		"fingerprint-1",
		now,
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(context.Background(), payment))
	service := newPaymentService(repo, &bankAuthorizerFake{}, now)

	result, err := service.GetPayment(context.Background(), app.GetPaymentQuery{
		PaymentID: "pay_550e8400-e29b-41d4-a716-446655440000",
	})

	require.NoError(t, err)
	assert.Equal(t, app.PaymentResult{
		ID:          "pay_550e8400-e29b-41d4-a716-446655440000",
		OrderID:     "order-1",
		CustomerID:  "customer-1",
		AmountCents: 1299,
		Currency:    "USD",
		Status:      "authorized",
		CreatedAt:   now,
		UpdatedAt:   now,
	}, result)
}

func TestVoidPaymentCallsBankStoresVoidedPaymentAndReturnsPublicResult(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	payment := mustAuthorizedPayment(t, "pay_550e8400-e29b-41d4-a716-446655440000", "order-1", "customer-1", now)
	require.NoError(t, repo.Create(context.Background(), payment))
	bank := &bankAuthorizerFake{voidResult: app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440002"}}
	service := newPaymentService(repo, bank, now.Add(time.Minute))

	result, err := service.VoidPayment(context.Background(), app.VoidPaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "void-key-1",
	})

	require.NoError(t, err)
	assert.Equal(t, app.BankVoidRequest{
		OperationKey:        "bok_123",
		BankAuthorizationID: "bank-auth-id-1",
	}, bank.voidRequest)
	assert.Equal(t, app.PaymentResult{
		ID:             string(payment.ID()),
		OrderID:        "order-1",
		CustomerID:     "customer-1",
		AmountCents:    1299,
		Currency:       "USD",
		Status:         "voided",
		CreatedAt:      now,
		UpdatedAt:      now.Add(time.Minute),
		ResponseStatus: 200,
	}, result)

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
			repo := testsupport.NewPaymentRepository()
			payment := loadDomainPaymentForStatus(t, status)
			require.NoError(t, repo.Create(context.Background(), payment))
			bank := &bankAuthorizerFake{voidResult: app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440002"}}
			service := newPaymentService(repo, bank, time.Now())

			_, err := service.VoidPayment(context.Background(), app.VoidPaymentCommand{
				PaymentID:      string(payment.ID()),
				IdempotencyKey: "void-key-1",
			})

			assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorInvalidStatusConflict))
			assert.Zero(t, bank.voidCalls)
		})
	}
}

func TestVoidPaymentRejectsMissingIdempotencyKeyBeforeCallingBank(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	payment := newAuthorizedDomainPayment(t, time.Now())
	require.NoError(t, repo.Create(context.Background(), payment))
	bank := &bankAuthorizerFake{voidResult: app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440002"}}
	service := newPaymentService(repo, bank, time.Now())

	_, err := service.VoidPayment(context.Background(), app.VoidPaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "",
	})

	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorInvalidInput))
	assert.Zero(t, bank.voidRequest)
}

func TestVoidPaymentLeavesPaymentStatusUnchangedWhenBankFails(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind app.PaymentErrorKind
	}{
		{name: "unavailable", err: app.NewPaymentBankUnavailable(errors.New("connection refused")), kind: app.PaymentErrorBankUnavailable},
		{name: "timeout", err: app.NewPaymentBankTimeout(context.DeadlineExceeded), kind: app.PaymentErrorBankTimeout},
		{name: "bank state conflict", err: app.NewPaymentBankStateConflict(errors.New("already voided")), kind: app.PaymentErrorBankStateConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := testsupport.NewPaymentRepository()
			authorizedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
			payment := newAuthorizedDomainPayment(t, authorizedAt)
			require.NoError(t, repo.Create(context.Background(), payment))
			service := newPaymentService(repo, &bankAuthorizerFake{voidErr: tt.err}, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))

			_, err := service.VoidPayment(context.Background(), app.VoidPaymentCommand{
				PaymentID:      string(payment.ID()),
				IdempotencyKey: "void-key-1",
			})

			assert.True(t, app.IsPaymentErrorKind(err, tt.kind))
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
	repo := testsupport.NewPaymentRepository()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.Create(context.Background(), payment))
	firstBank := &bankAuthorizerFake{voidErr: app.NewPaymentBankTimeout(context.DeadlineExceeded)}
	service := newPaymentServiceWithBankOperationKeys(repo, firstBank, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_first", "bok_second"}})

	_, err := service.VoidPayment(context.Background(), app.VoidPaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "void-key-1",
	})
	require.Error(t, err)

	secondBank := &bankAuthorizerFake{voidResult: app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440002"}}
	service = newPaymentServiceWithBankOperationKeys(repo, secondBank, time.Date(2026, 6, 18, 12, 31, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_second"}})
	result, err := service.VoidPayment(context.Background(), app.VoidPaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "void-key-1",
	})

	require.NoError(t, err)
	assert.Equal(t, "bok_first", secondBank.voidRequest.OperationKey)
	assert.Equal(t, "voided", result.Status)
}

func TestVoidPaymentReplaysVoidedPaymentForSameIdempotencyKeyAndPayment(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.Create(context.Background(), payment))
	bank := &bankAuthorizerFake{voidResult: app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440001"}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
	command := app.VoidPaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "void-key-1",
	}
	first, err := service.VoidPayment(context.Background(), command)
	require.NoError(t, err)
	bank.voidResult = app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440002"}

	replayed, err := service.VoidPayment(context.Background(), command)
	require.NoError(t, err)

	assert.Equal(t, first, replayed)
	assert.Equal(t, 1, bank.voidCalls)
}

func TestVoidPaymentRejectsReusedIdempotencyKeyWithDifferentPayment(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.Create(context.Background(), payment))
	secondPayment, err := domain.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440001"),
		"order-2",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440002",
		"bok_550e8400-e29b-41d4-a716-446655440003",
		payment.AuthorizationCardFingerprint(),
		payment.CreatedAt(),
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(context.Background(), secondPayment))
	bank := &bankAuthorizerFake{voidResult: app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440001"}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
	_, err = service.VoidPayment(context.Background(), app.VoidPaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "void-key-1",
	})
	require.NoError(t, err)

	_, err = service.VoidPayment(context.Background(), app.VoidPaymentCommand{
		PaymentID:      string(secondPayment.ID()),
		IdempotencyKey: "void-key-1",
	})

	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	assert.Equal(t, 1, bank.voidCalls)
}

func TestSearchPaymentsNormalizesFiltersAndReturnsPublicResults(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	newer := mustAuthorizedPayment(t, "pay_550e8400-e29b-41d4-a716-446655440001", "order-1", "customer-1", time.Date(2026, 6, 18, 12, 1, 0, 0, time.UTC))
	older := mustDeclinedPayment(t, "pay_550e8400-e29b-41d4-a716-446655440000", "order-1", "customer-1", time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.Create(context.Background(), older))
	require.NoError(t, repo.Create(context.Background(), newer))
	service := newPaymentService(repo, &bankAuthorizerFake{}, newer.CreatedAt())

	results, err := service.SearchPayments(context.Background(), app.SearchPaymentsQuery{
		OrderID:    " order-1 ",
		CustomerID: " customer-1 ",
		Status:     " authorized ",
	})

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, newer.ID(), domain.PaymentID(results[0].ID))
	assert.Equal(t, "authorized", results[0].Status)
}

func TestSearchPaymentsRejectsInvalidFilters(t *testing.T) {
	tests := []struct {
		name  string
		query app.SearchPaymentsQuery
	}{
		{name: "unfiltered", query: app.SearchPaymentsQuery{}},
		{name: "status only", query: app.SearchPaymentsQuery{Status: "authorized"}},
		{name: "invalid status", query: app.SearchPaymentsQuery{OrderID: "order-1", Status: "unknown"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newPaymentService(testsupport.NewPaymentRepository(), &bankAuthorizerFake{}, time.Now())

			_, err := service.SearchPayments(context.Background(), tt.query)

			assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorInvalidInput))
		})
	}
}

func TestCapturePaymentCallsBankStoresCapturedPaymentAndReturnsPublicResult(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	authorizedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	payment := newAuthorizedDomainPayment(t, authorizedAt)
	require.NoError(t, repo.Create(context.Background(), payment))
	bank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}
	capturedAt := time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC)
	service := newPaymentService(repo, bank, capturedAt)

	captured, err := service.CapturePayment(context.Background(), app.CapturePaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "public-capture-key-1",
	})
	require.NoError(t, err)

	assert.Equal(t, app.BankCaptureRequest{
		OperationKey:        "bok_123",
		BankAuthorizationID: "auth_550e8400-e29b-41d4-a716-446655440000",
		AmountCents:         1299,
		Currency:            "USD",
	}, bank.captureRequest)
	assert.Equal(t, app.PaymentResult{
		ID:             string(payment.ID()),
		OrderID:        "order-1",
		CustomerID:     "customer-1",
		AmountCents:    1299,
		Currency:       "USD",
		Status:         "captured",
		CreatedAt:      authorizedAt,
		UpdatedAt:      capturedAt,
		ResponseStatus: 200,
	}, captured)

	saved, err := repo.FindByID(context.Background(), payment.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusCaptured, saved.Status())
	assert.Equal(t, "cap_550e8400-e29b-41d4-a716-446655440001", saved.BankCaptureID())
	assert.Equal(t, "bok_123", saved.CaptureBankOperationKey())
}

func TestCapturePaymentRejectsMissingIdempotencyKeyBeforeCallingBank(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	payment := newAuthorizedDomainPayment(t, time.Now())
	require.NoError(t, repo.Create(context.Background(), payment))
	bank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}
	service := newPaymentService(repo, bank, time.Now())

	_, err := service.CapturePayment(context.Background(), app.CapturePaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "",
	})

	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorInvalidInput))
	assert.Zero(t, bank.captureRequest)
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
			repo := testsupport.NewPaymentRepository()
			payment := loadDomainPaymentForStatus(t, status)
			require.NoError(t, repo.Create(context.Background(), payment))
			bank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}
			service := newPaymentService(repo, bank, time.Now())

			_, err := service.CapturePayment(context.Background(), app.CapturePaymentCommand{
				PaymentID:      string(payment.ID()),
				IdempotencyKey: "public-capture-key-1",
			})

			assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorInvalidStatusConflict))
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
		{name: "unavailable", err: app.NewPaymentBankUnavailable(errors.New("connection refused")), kind: app.PaymentErrorBankUnavailable},
		{name: "timeout", err: app.NewPaymentBankTimeout(context.DeadlineExceeded), kind: app.PaymentErrorBankTimeout},
		{name: "bank state conflict", err: app.NewPaymentBankStateConflict(errors.New("already captured")), kind: app.PaymentErrorBankStateConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := testsupport.NewPaymentRepository()
			authorizedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
			payment := newAuthorizedDomainPayment(t, authorizedAt)
			require.NoError(t, repo.Create(context.Background(), payment))
			service := newPaymentService(repo, &bankFake{captureErr: tt.err}, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))

			_, err := service.CapturePayment(context.Background(), app.CapturePaymentCommand{
				PaymentID:      string(payment.ID()),
				IdempotencyKey: "public-capture-key-1",
			})

			assert.True(t, app.IsPaymentErrorKind(err, tt.kind))
			saved, findErr := repo.FindByID(context.Background(), payment.ID())
			require.NoError(t, findErr)
			assert.Equal(t, domain.PaymentStatusAuthorized, saved.Status())
			assert.Equal(t, authorizedAt, saved.UpdatedAt())
			assert.Empty(t, saved.BankCaptureID())
			assert.Equal(t, "bok_123", saved.CaptureBankOperationKey())
		})
	}
}

func TestCapturePaymentReusesStoredBankOperationKeyAfterProviderFailure(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.Create(context.Background(), payment))
	firstBank := &bankFake{captureErr: app.NewPaymentBankTimeout(context.DeadlineExceeded)}
	service := newPaymentServiceWithBankOperationKeys(repo, firstBank, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_first", "bok_second"}})

	_, err := service.CapturePayment(context.Background(), app.CapturePaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "public-capture-key-1",
	})
	require.Error(t, err)

	secondBank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}
	service = newPaymentServiceWithBankOperationKeys(repo, secondBank, time.Date(2026, 6, 18, 12, 31, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_second"}})
	result, err := service.CapturePayment(context.Background(), app.CapturePaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "public-capture-key-1",
	})

	require.NoError(t, err)
	assert.Equal(t, "bok_first", secondBank.captureRequest.OperationKey)
	assert.Equal(t, "captured", result.Status)
}

func TestCapturePaymentReplaysCapturedPaymentForSameIdempotencyKeyAndPayment(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.Create(context.Background(), payment))
	bank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
	command := app.CapturePaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "public-capture-key-1",
	}
	first, err := service.CapturePayment(context.Background(), command)
	require.NoError(t, err)
	bank.captureResult = app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440002"}

	replayed, err := service.CapturePayment(context.Background(), command)
	require.NoError(t, err)

	assert.Equal(t, first, replayed)
	assert.Equal(t, 1, bank.captureCalls)
}

func TestCapturePaymentRejectsReusedIdempotencyKeyWithDifferentPayment(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	payment := newAuthorizedDomainPayment(t, time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC))
	require.NoError(t, repo.Create(context.Background(), payment))
	secondPayment, err := domain.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440001"),
		"order-2",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440002",
		"bok_550e8400-e29b-41d4-a716-446655440003",
		payment.AuthorizationCardFingerprint(),
		payment.CreatedAt(),
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(context.Background(), secondPayment))
	bank := &bankFake{captureResult: app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
	_, err = service.CapturePayment(context.Background(), app.CapturePaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "public-capture-key-1",
	})
	require.NoError(t, err)

	_, err = service.CapturePayment(context.Background(), app.CapturePaymentCommand{
		PaymentID:      string(secondPayment.ID()),
		IdempotencyKey: "public-capture-key-1",
	})

	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	assert.Equal(t, 1, bank.captureCalls)
}

func TestRefundPaymentCallsBankStoresRefundedPaymentAndReturnsPublicResult(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	capturedAt := time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC)
	payment := newCapturedDomainPayment(t, capturedAt)
	require.NoError(t, repo.Create(context.Background(), payment))
	bank := &bankFake{refundResult: app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440002"}}
	refundedAt := time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC)
	service := newPaymentService(repo, bank, refundedAt)

	refunded, err := service.RefundPayment(context.Background(), app.RefundPaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "public-refund-key-1",
	})
	require.NoError(t, err)

	assert.Equal(t, app.BankRefundRequest{
		OperationKey:  "bok_123",
		BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001",
		AmountCents:   1299,
		Currency:      "USD",
	}, bank.refundRequest)
	assert.Equal(t, app.PaymentResult{
		ID:             string(payment.ID()),
		OrderID:        "order-1",
		CustomerID:     "customer-1",
		AmountCents:    1299,
		Currency:       "USD",
		Status:         "refunded",
		CreatedAt:      capturedAt,
		UpdatedAt:      refundedAt,
		ResponseStatus: 200,
	}, refunded)

	saved, err := repo.FindByID(context.Background(), payment.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusRefunded, saved.Status())
	assert.Equal(t, "ref_550e8400-e29b-41d4-a716-446655440002", saved.BankRefundID())
	assert.Equal(t, "bok_123", saved.RefundBankOperationKey())
}

func TestRefundPaymentRejectsMissingIdempotencyKeyBeforeCallingBank(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	payment := newCapturedDomainPayment(t, time.Now())
	require.NoError(t, repo.Create(context.Background(), payment))
	bank := &bankFake{refundResult: app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440002"}}
	service := newPaymentService(repo, bank, time.Now())

	_, err := service.RefundPayment(context.Background(), app.RefundPaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "",
	})

	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorInvalidInput))
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
			repo := testsupport.NewPaymentRepository()
			payment := loadDomainPaymentForStatus(t, status)
			require.NoError(t, repo.Create(context.Background(), payment))
			bank := &bankFake{refundResult: app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440002"}}
			service := newPaymentService(repo, bank, time.Now())

			_, err := service.RefundPayment(context.Background(), app.RefundPaymentCommand{
				PaymentID:      string(payment.ID()),
				IdempotencyKey: "public-refund-key-1",
			})

			assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorInvalidStatusConflict))
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
		{name: "unavailable", err: app.NewPaymentBankUnavailable(errors.New("connection refused")), kind: app.PaymentErrorBankUnavailable},
		{name: "timeout", err: app.NewPaymentBankTimeout(context.DeadlineExceeded), kind: app.PaymentErrorBankTimeout},
		{name: "bank state conflict", err: app.NewPaymentBankStateConflict(errors.New("already refunded")), kind: app.PaymentErrorBankStateConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := testsupport.NewPaymentRepository()
			capturedAt := time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC)
			payment := newCapturedDomainPayment(t, capturedAt)
			require.NoError(t, repo.Create(context.Background(), payment))
			service := newPaymentService(repo, &bankFake{refundErr: tt.err}, time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC))

			_, err := service.RefundPayment(context.Background(), app.RefundPaymentCommand{
				PaymentID:      string(payment.ID()),
				IdempotencyKey: "public-refund-key-1",
			})

			assert.True(t, app.IsPaymentErrorKind(err, tt.kind))
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
	repo := testsupport.NewPaymentRepository()
	payment := newCapturedDomainPayment(t, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
	require.NoError(t, repo.Create(context.Background(), payment))
	firstBank := &bankFake{refundErr: app.NewPaymentBankTimeout(context.DeadlineExceeded)}
	service := newPaymentServiceWithBankOperationKeys(repo, firstBank, time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_first", "bok_second"}})

	_, err := service.RefundPayment(context.Background(), app.RefundPaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "public-refund-key-1",
	})
	require.Error(t, err)

	secondBank := &bankFake{refundResult: app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440002"}}
	service = newPaymentServiceWithBankOperationKeys(repo, secondBank, time.Date(2026, 6, 18, 13, 1, 0, 0, time.UTC), &sequenceBankOperationKeyGenerator{keys: []string{"bok_second"}})
	result, err := service.RefundPayment(context.Background(), app.RefundPaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "public-refund-key-1",
	})

	require.NoError(t, err)
	assert.Equal(t, "bok_first", secondBank.refundRequest.OperationKey)
	assert.Equal(t, "refunded", result.Status)
}

func TestRefundPaymentReplaysRefundedPaymentForSameIdempotencyKeyAndPayment(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	payment := newCapturedDomainPayment(t, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
	require.NoError(t, repo.Create(context.Background(), payment))
	bank := &bankFake{refundResult: app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440001"}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC))
	command := app.RefundPaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "public-refund-key-1",
	}
	first, err := service.RefundPayment(context.Background(), command)
	require.NoError(t, err)
	bank.refundResult = app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440002"}

	replayed, err := service.RefundPayment(context.Background(), command)
	require.NoError(t, err)

	assert.Equal(t, first, replayed)
	assert.Equal(t, 1, bank.refundCalls)
}

func TestRefundPaymentRejectsReusedIdempotencyKeyWithDifferentPayment(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	payment := newCapturedDomainPayment(t, time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC))
	require.NoError(t, repo.Create(context.Background(), payment))
	secondPayment := loadDomainPaymentForStatus(t, domain.PaymentStatusCaptured)
	secondPayment, err := domain.LoadPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440001"),
		"order-2",
		"customer-1",
		1299,
		domain.CurrencyUSD,
		domain.PaymentStatusCaptured,
		"auth_550e8400-e29b-41d4-a716-446655440004",
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
	require.NoError(t, repo.Create(context.Background(), secondPayment))
	bank := &bankFake{refundResult: app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440001"}}
	service := newPaymentService(repo, bank, time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC))
	_, err = service.RefundPayment(context.Background(), app.RefundPaymentCommand{
		PaymentID:      string(payment.ID()),
		IdempotencyKey: "public-refund-key-1",
	})
	require.NoError(t, err)

	_, err = service.RefundPayment(context.Background(), app.RefundPaymentCommand{
		PaymentID:      string(secondPayment.ID()),
		IdempotencyKey: "public-refund-key-1",
	})

	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	assert.Equal(t, 1, bank.refundCalls)
}

func TestNewPaymentServiceRequiresCollaborators(t *testing.T) {
	validPaymentRepository := testsupport.NewPaymentRepository()
	validIdempotency := testsupport.NewIdempotencyRepository()
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
			name: "payment repository",
			build: func() {
				app.NewPaymentService(nil, validIdempotency, validPaymentIDs, validBankOperationKeys, validBank, validClock, "secret")
			},
			reason: "payment repository is required",
		},
		{
			name: "idempotency repository",
			build: func() {
				app.NewPaymentService(validPaymentRepository, nil, validPaymentIDs, validBankOperationKeys, validBank, validClock, "secret")
			},
			reason: "idempotency repository is required",
		},
		{
			name: "payment ID generator",
			build: func() {
				app.NewPaymentService(validPaymentRepository, validIdempotency, nil, validBankOperationKeys, validBank, validClock, "secret")
			},
			reason: "payment ID generator is required",
		},
		{
			name: "bank operation key generator",
			build: func() {
				app.NewPaymentService(validPaymentRepository, validIdempotency, validPaymentIDs, nil, validBank, validClock, "secret")
			},
			reason: "bank operation key generator is required",
		},
		{
			name: "bank authorizer",
			build: func() {
				app.NewPaymentService(validPaymentRepository, validIdempotency, validPaymentIDs, validBankOperationKeys, nil, validClock, "secret")
			},
			reason: "bank authorizer is required",
		},
		{
			name: "clock",
			build: func() {
				app.NewPaymentService(validPaymentRepository, validIdempotency, validPaymentIDs, validBankOperationKeys, validBank, nil, "secret")
			},
			reason: "clock is required",
		},
		{
			name: "fingerprint secret",
			build: func() {
				app.NewPaymentService(validPaymentRepository, validIdempotency, validPaymentIDs, validBankOperationKeys, validBank, validClock, " ")
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
	return app.AuthorizePaymentCommand{
		OrderID:        "order-1",
		CustomerID:     "customer-1",
		AmountCents:    1299,
		IdempotencyKey: "public-key-1",
		Card: app.CardDetails{
			Number:      "4111111111111111",
			CVV:         "123",
			ExpiryMonth: 12,
			ExpiryYear:  2030,
		},
	}
}

func validRetryAuthorizationCommand(paymentID string) app.RetryAuthorizationCommand {
	return app.RetryAuthorizationCommand{
		PaymentID:      paymentID,
		IdempotencyKey: "retry-key-1",
		Card:           validAuthorizeCommand().Card,
	}
}

func newPaymentService(repo app.PaymentRepository, bank app.BankClient, now time.Time) *app.PaymentService {
	return newPaymentServiceWithBankOperationKeys(
		repo,
		bank,
		now,
		testsupport.FixedBankOperationKeyGenerator{Key: "bok_123"},
	)
}

func newPaymentServiceWithBankOperationKeys(repo app.PaymentRepository, bank app.BankClient, now time.Time, bankOperationKeys app.BankOperationKeyGenerator) *app.PaymentService {
	return app.NewPaymentService(
		repo,
		testsupport.NewIdempotencyRepository(),
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
	voidRequest app.BankVoidRequest
	voidResult  app.BankVoidResult
	voidErr     error
	voidCalls   int
}

func (f *bankAuthorizerFake) AuthorizePayment(_ context.Context, request app.BankAuthorizationRequest) (app.BankAuthorizationResult, error) {
	f.request = request
	f.calls++
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

type alwaysInProgressIdempotencyRepository struct{}

func (r *alwaysInProgressIdempotencyRepository) Claim(_ context.Context, operation string, key string, requestFingerprint string) (app.IdempotencyRecord, app.IdempotencyClaimStatus, error) {
	return app.IdempotencyRecord{
		Operation:          operation,
		Key:                key,
		RequestFingerprint: requestFingerprint,
	}, app.IdempotencyInProgress, nil
}

func (r *alwaysInProgressIdempotencyRepository) Complete(context.Context, app.IdempotencyRecord) error {
	return nil
}

func (r *alwaysInProgressIdempotencyRepository) Release(context.Context, string, string) error {
	return nil
}

func (f *bankFake) AuthorizePayment(_ context.Context, request app.BankAuthorizationRequest) (app.BankAuthorizationResult, error) {
	f.authorizeRequest = request
	f.authorizeCalls++
	return f.authorizeResult, f.authorizeErr
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
	case domain.PaymentStatusCaptured:
		bankAuthorizationID = "auth_550e8400-e29b-41d4-a716-446655440000"
		bankCaptureID = "cap_550e8400-e29b-41d4-a716-446655440002"
		captureBankOperationKey = "bok_550e8400-e29b-41d4-a716-446655440003"
	case domain.PaymentStatusVoided:
		bankAuthorizationID = "auth_550e8400-e29b-41d4-a716-446655440000"
		bankVoidID = "void_550e8400-e29b-41d4-a716-446655440002"
		voidBankOperationKey = "bok_550e8400-e29b-41d4-a716-446655440003"
	case domain.PaymentStatusRefunded:
		bankAuthorizationID = "auth_550e8400-e29b-41d4-a716-446655440000"
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
