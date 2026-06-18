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
	PaymentErrorInvalidCommand        PaymentErrorKind = "invalid_command"
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
	case errors.Is(err, ErrInvalidCardDetails),
		errors.Is(err, domain.ErrInvalidPaymentID),
		errors.Is(err, domain.ErrInvalidOrderID),
		errors.Is(err, domain.ErrInvalidCustomerID),
		errors.Is(err, domain.ErrInvalidAmount),
		errors.Is(err, domain.ErrInvalidBankReference),
		errors.Is(err, domain.ErrInvalidBankOperationKey),
		errors.Is(err, domain.ErrInvalidPaymentTimestamp):
		return PaymentErrorInvalidCommand
	default:
		return PaymentErrorUnknown
	}
}
