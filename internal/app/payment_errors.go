package app

import (
	"errors"

	"github.com/roigada/payment-gateway/internal/domain"
)

var (
	ErrInvalidCardDetails    = errors.New("invalid card details")
	ErrMissingIdempotencyKey = errors.New("missing idempotency key")
	ErrPaymentNotFound       = errors.New("payment not found")
)

type PaymentErrorCategory string

const (
	PaymentErrorInvalidOrderID        PaymentErrorCategory = "invalid_order_id"
	PaymentErrorInvalidCustomerID     PaymentErrorCategory = "invalid_customer_id"
	PaymentErrorInvalidAmount         PaymentErrorCategory = "invalid_amount"
	PaymentErrorInvalidCardDetails    PaymentErrorCategory = "invalid_card_details"
	PaymentErrorMissingIdempotencyKey PaymentErrorCategory = "missing_idempotency_key"
	PaymentErrorNotFound              PaymentErrorCategory = "not_found"
	PaymentErrorUnknown               PaymentErrorCategory = "unknown"
)

func ClassifyPaymentError(err error) PaymentErrorCategory {
	switch {
	case errors.Is(err, ErrMissingIdempotencyKey):
		return PaymentErrorMissingIdempotencyKey
	case errors.Is(err, ErrPaymentNotFound):
		return PaymentErrorNotFound
	case errors.Is(err, domain.ErrInvalidOrderID):
		return PaymentErrorInvalidOrderID
	case errors.Is(err, domain.ErrInvalidCustomerID):
		return PaymentErrorInvalidCustomerID
	case errors.Is(err, domain.ErrInvalidAmount):
		return PaymentErrorInvalidAmount
	case errors.Is(err, ErrInvalidCardDetails):
		return PaymentErrorInvalidCardDetails
	default:
		return PaymentErrorUnknown
	}
}
