package app_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPaymentErrorConstructorsExposeKindAndSafeMessage(t *testing.T) {
	cause := errors.New("driver: connection refused")
	tests := []struct {
		name    string
		err     error
		kind    app.PaymentErrorKind
		message string
	}{
		{
			name:    "invalid input",
			err:     app.NewInvalidPaymentInput("amount must be greater than zero", cause),
			kind:    app.PaymentErrorInvalidInput,
			message: "amount must be greater than zero",
		},
		{
			name:    "not found",
			err:     app.NewPaymentNotFound("pay_123", cause),
			kind:    app.PaymentErrorNotFound,
			message: "payment pay_123 was not found",
		},
		{
			name:    "idempotency conflict",
			err:     app.NewPaymentIdempotencyConflict(cause),
			kind:    app.PaymentErrorIdempotencyConflict,
			message: "idempotency key was already used with a different request",
		},
		{
			name:    "idempotency in progress",
			err:     app.NewPaymentIdempotencyInProgress(cause),
			kind:    app.PaymentErrorIdempotencyInProgress,
			message: "idempotency key is already in progress",
		},
		{
			name:    "invalid status conflict",
			err:     app.NewPaymentInvalidStatusConflict(cause),
			kind:    app.PaymentErrorInvalidStatusConflict,
			message: "payment status does not allow this operation",
		},
		{
			name:    "bank state conflict",
			err:     app.NewPaymentBankStateConflict(cause),
			kind:    app.PaymentErrorBankStateConflict,
			message: "bank state conflicts with local payment state",
		},
		{
			name:    "bank unavailable",
			err:     app.NewPaymentBankUnavailable(cause),
			kind:    app.PaymentErrorBankUnavailable,
			message: "bank is unavailable",
		},
		{
			name:    "bank timeout",
			err:     app.NewPaymentBankTimeout(cause),
			kind:    app.PaymentErrorBankTimeout,
			message: "bank request timed out",
		},
		{
			name:    "internal",
			err:     app.NewInternalPaymentError(cause),
			kind:    app.PaymentErrorInternal,
			message: "internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, ok := app.PaymentErrorKindOf(tt.err)

			require.True(t, ok)
			assert.Equal(t, tt.kind, kind)
			assert.True(t, app.IsPaymentErrorKind(tt.err, tt.kind))
			assert.Equal(t, tt.message, tt.err.Error())
			assert.ErrorIs(t, tt.err, cause)
			assert.NotContains(t, tt.err.Error(), cause.Error())
		})
	}
}

func TestPaymentErrorKindOfFindsWrappedPaymentError(t *testing.T) {
	err := fmt.Errorf("save payment: %w", app.NewPaymentBankUnavailable(errors.New("connection refused")))

	kind, ok := app.PaymentErrorKindOf(err)

	require.True(t, ok)
	assert.Equal(t, app.PaymentErrorBankUnavailable, kind)
}

func TestPaymentErrorKindOfReturnsFalseForNilAndRawErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "nil", err: nil},
		{name: "raw", err: errors.New("raw failure")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, ok := app.PaymentErrorKindOf(tt.err)

			assert.False(t, ok)
			assert.Empty(t, kind)
		})
	}
}
