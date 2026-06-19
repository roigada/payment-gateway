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
		ID:          "pay_550e8400-e29b-41d4-a716-446655440000",
		OrderID:     "order-1",
		CustomerID:  "customer-1",
		AmountCents: 1299,
		Currency:    "USD",
		Status:      "authorized",
		CreatedAt:   now,
		UpdatedAt:   now,
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
		ID:            "pay_550e8400-e29b-41d4-a716-446655440000",
		OrderID:       "order-1",
		CustomerID:    "customer-1",
		AmountCents:   1299,
		Currency:      "USD",
		Status:        "declined",
		DeclineReason: "insufficient_funds",
		CreatedAt:     now,
		UpdatedAt:     now,
	}, payment)

	saved, err := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"))
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusDeclined, saved.Status())
	assert.Equal(t, domain.DeclineReasonInsufficientFunds, saved.DeclineReason())
	assert.Empty(t, saved.BankAuthorizationID())
	assert.Equal(t, "bok_123", saved.AuthorizationBankOperationKey())
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

	assert.ErrorIs(t, err, app.ErrIdempotencyConflict)
	assert.Equal(t, 1, bank.calls)
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

	assert.ErrorIs(t, err, app.ErrMissingIdempotencyKey)
	assert.Zero(t, bank.request)
}

func TestAuthorizePaymentValidatesCommandBeforeCallingBank(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*app.AuthorizePaymentCommand)
		category app.PaymentErrorCategory
	}{
		{name: "order id", mutate: func(c *app.AuthorizePaymentCommand) { c.OrderID = "" }, category: app.PaymentErrorInvalidOrderID},
		{name: "customer id", mutate: func(c *app.AuthorizePaymentCommand) { c.CustomerID = "" }, category: app.PaymentErrorInvalidCustomerID},
		{name: "amount", mutate: func(c *app.AuthorizePaymentCommand) { c.AmountCents = 0 }, category: app.PaymentErrorInvalidAmount},
		{name: "card number", mutate: func(c *app.AuthorizePaymentCommand) { c.Card.Number = "4111x" }, category: app.PaymentErrorInvalidCardDetails},
		{name: "cvv", mutate: func(c *app.AuthorizePaymentCommand) { c.Card.CVV = "12x" }, category: app.PaymentErrorInvalidCardDetails},
		{name: "expiry month", mutate: func(c *app.AuthorizePaymentCommand) { c.Card.ExpiryMonth = 13 }, category: app.PaymentErrorInvalidCardDetails},
		{name: "expiry year", mutate: func(c *app.AuthorizePaymentCommand) { c.Card.ExpiryYear = 0 }, category: app.PaymentErrorInvalidCardDetails},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
			service := newPaymentService(testsupport.NewPaymentRepository(), bank, time.Now())
			command := validAuthorizeCommand()
			tt.mutate(&command)

			_, err := service.AuthorizePayment(context.Background(), command)

			assert.Equal(t, tt.category, app.ClassifyPaymentError(err))
			assert.Zero(t, bank.request)
		})
	}
}

func TestAuthorizePaymentReturnsBankErrorWithoutStoringPayment(t *testing.T) {
	repo := testsupport.NewPaymentRepository()
	bankErr := errors.New("bank unavailable")
	service := newPaymentService(repo, &bankAuthorizerFake{err: bankErr}, time.Now())

	_, err := service.AuthorizePayment(context.Background(), validAuthorizeCommand())

	assert.ErrorIs(t, err, bankErr)
	_, findErr := repo.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"))
	assert.ErrorIs(t, findErr, app.ErrPaymentNotFound)
}

func TestNewPaymentServiceRequiresCollaborators(t *testing.T) {
	validPaymentRepository := testsupport.NewPaymentRepository()
	validIdempotency := testsupport.NewIdempotencyRepository()
	validPaymentIDs := testsupport.FixedPaymentIDGenerator{ID: domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000")}
	validBankOperationKeys := testsupport.FixedBankOperationKeyGenerator{Key: "bok_123"}
	validBank := &bankAuthorizerFake{result: app.BankAuthorizationResult{BankAuthorizationID: "bank-auth-id-1"}}
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
			reason: "authorization fingerprint secret is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.PanicsWithValue(t, tt.reason, tt.build)
		})
	}
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

func newPaymentService(repo app.PaymentRepository, bank app.BankAuthorizer, now time.Time) *app.PaymentService {
	return app.NewPaymentService(
		repo,
		testsupport.NewIdempotencyRepository(),
		testsupport.FixedPaymentIDGenerator{ID: domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000")},
		testsupport.FixedBankOperationKeyGenerator{Key: "bok_123"},
		bank,
		testsupport.FixedClock{Time: now},
		"fingerprint-secret",
	)
}

type bankAuthorizerFake struct {
	request app.BankAuthorizationRequest
	result  app.BankAuthorizationResult
	err     error
	calls   int
}

func (f *bankAuthorizerFake) AuthorizePayment(_ context.Context, request app.BankAuthorizationRequest) (app.BankAuthorizationResult, error) {
	f.request = request
	f.calls++
	return f.result, f.err
}
