package app_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestClassifyPaymentError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		category app.PaymentErrorCategory
	}{
		{name: "missing idempotency key", err: app.ErrMissingIdempotencyKey, category: app.PaymentErrorMissingIdempotencyKey},
		{name: "payment not found", err: app.ErrPaymentNotFound, category: app.PaymentErrorNotFound},
		{name: "invalid order id", err: domain.ErrInvalidOrderID, category: app.PaymentErrorInvalidOrderID},
		{name: "invalid customer id", err: domain.ErrInvalidCustomerID, category: app.PaymentErrorInvalidCustomerID},
		{name: "invalid amount", err: domain.ErrInvalidAmount, category: app.PaymentErrorInvalidAmount},
		{name: "invalid card details", err: app.ErrInvalidCardDetails, category: app.PaymentErrorInvalidCardDetails},
		{name: "unknown", err: errors.New("bank unavailable"), category: app.PaymentErrorUnknown},
		{name: "wrapped", err: fmt.Errorf("validate payment: %w", domain.ErrInvalidAmount), category: app.PaymentErrorInvalidAmount},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.category, app.ClassifyPaymentError(tt.err))
		})
	}
}
