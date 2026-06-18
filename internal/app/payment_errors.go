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

type PaymentErrorKind string

const (
	PaymentErrorInvalidOrderID        PaymentErrorKind = "invalid_order_id"
	PaymentErrorInvalidCustomerID     PaymentErrorKind = "invalid_customer_id"
	PaymentErrorInvalidAmount         PaymentErrorKind = "invalid_amount"
	PaymentErrorInvalidCardDetails    PaymentErrorKind = "invalid_card_details"
	PaymentErrorMissingIdempotencyKey PaymentErrorKind = "missing_idempotency_key"
	PaymentErrorNotFound              PaymentErrorKind = "not_found"
	PaymentErrorUnknown               PaymentErrorKind = "unknown"
)

func ClassifyPaymentError(err error) PaymentErrorKind {
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
