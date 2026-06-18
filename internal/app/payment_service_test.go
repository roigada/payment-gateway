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
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{AuthorizationReference: "bank-auth-1"}}
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
		ID:          "pay_123",
		OrderID:     "order-1",
		CustomerID:  "customer-1",
		AmountCents: 1299,
		Currency:    "USD",
		Status:      "authorized",
		CreatedAt:   now,
		UpdatedAt:   now,
	}, payment)

	saved, err := repo.FindByID(context.Background(), domain.PaymentID("pay_123"))
	require.NoError(t, err)
	assert.Equal(t, "bank-auth-1", saved.AuthorizationBankReference())
	assert.Equal(t, "bok_123", saved.AuthorizationBankOperationKey())
}

func TestAuthorizePaymentRequiresIdempotencyKeyBeforeCallingBank(t *testing.T) {
	bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{AuthorizationReference: "bank-auth-1"}}
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
			bank := &bankAuthorizerFake{result: app.BankAuthorizationResult{AuthorizationReference: "bank-auth-1"}}
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
	_, findErr := repo.FindByID(context.Background(), domain.PaymentID("pay_123"))
	assert.ErrorIs(t, findErr, app.ErrPaymentNotFound)
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
		testsupport.FixedPaymentIDGenerator{ID: domain.PaymentID("pay_123")},
		testsupport.FixedBankOperationKeyGenerator{Key: "bok_123"},
		bank,
		testsupport.FixedClock{Time: now},
	)
}

type bankAuthorizerFake struct {
	request app.BankAuthorizationRequest
	result  app.BankAuthorizationResult
	err     error
}

func (f *bankAuthorizerFake) AuthorizePayment(_ context.Context, request app.BankAuthorizationRequest) (app.BankAuthorizationResult, error) {
	f.request = request
	return f.result, f.err
}
