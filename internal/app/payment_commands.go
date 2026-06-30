package app

import (
	"strings"

	"github.com/roigada/payment-gateway/internal/domain"
)

type AuthorizePaymentCommand struct {
	orderID        string
	customerID     string
	amountCents    int64
	card           cardDetails
	idempotencyKey string
}

type RetryAuthorizationCommand struct {
	paymentID      domain.PaymentID
	card           cardDetails
	idempotencyKey string
}

type CapturePaymentCommand struct {
	paymentID      domain.PaymentID
	idempotencyKey string
}

type VoidPaymentCommand struct {
	paymentID      domain.PaymentID
	idempotencyKey string
}

type RefundPaymentCommand struct {
	paymentID      domain.PaymentID
	idempotencyKey string
}

type cardDetails struct {
	number      string
	cvv         string
	expiryMonth int
	expiryYear  int
}

func NewAuthorizePaymentCommand(
	orderID string,
	customerID string,
	amountCents int64,
	cardNumber string,
	cardCVV string,
	cardExpiryMonth int,
	cardExpiryYear int,
	idempotencyKey string,
) (AuthorizePaymentCommand, error) {
	command := AuthorizePaymentCommand{
		orderID:        strings.TrimSpace(orderID),
		customerID:     strings.TrimSpace(customerID),
		amountCents:    amountCents,
		idempotencyKey: strings.TrimSpace(idempotencyKey),
	}
	if command.idempotencyKey == "" {
		return AuthorizePaymentCommand{}, NewInvalidPaymentInputError("idempotency key is required", nil)
	}
	if command.orderID == "" {
		return AuthorizePaymentCommand{}, NewInvalidPaymentInputError("order id is required", nil)
	}
	if command.customerID == "" {
		return AuthorizePaymentCommand{}, NewInvalidPaymentInputError("customer id is required", nil)
	}
	if command.amountCents <= 0 {
		return AuthorizePaymentCommand{}, NewInvalidPaymentInputError("amount must be greater than zero", nil)
	}
	card, err := newCardDetails(cardNumber, cardCVV, cardExpiryMonth, cardExpiryYear)
	if err != nil {
		return AuthorizePaymentCommand{}, ensurePaymentError(err)
	}
	command.card = card
	return command, nil
}

func NewRetryAuthorizationCommand(
	paymentID string,
	cardNumber string,
	cardCVV string,
	cardExpiryMonth int,
	cardExpiryYear int,
	idempotencyKey string,
) (RetryAuthorizationCommand, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return RetryAuthorizationCommand{}, NewInvalidPaymentInputError("idempotency key is required", nil)
	}
	parsedPaymentID, err := parsePaymentID(paymentID)
	if err != nil {
		return RetryAuthorizationCommand{}, ensurePaymentError(err)
	}
	card, err := newCardDetails(cardNumber, cardCVV, cardExpiryMonth, cardExpiryYear)
	if err != nil {
		return RetryAuthorizationCommand{}, ensurePaymentError(err)
	}
	return RetryAuthorizationCommand{paymentID: parsedPaymentID, card: card, idempotencyKey: idempotencyKey}, nil
}

func NewCapturePaymentCommand(paymentID string, idempotencyKey string) (CapturePaymentCommand, error) {
	id, key, err := parsePaymentOperationInput(paymentID, idempotencyKey)
	if err != nil {
		return CapturePaymentCommand{}, ensurePaymentError(err)
	}
	return CapturePaymentCommand{paymentID: id, idempotencyKey: key}, nil
}

func NewVoidPaymentCommand(paymentID string, idempotencyKey string) (VoidPaymentCommand, error) {
	id, key, err := parsePaymentOperationInput(paymentID, idempotencyKey)
	if err != nil {
		return VoidPaymentCommand{}, ensurePaymentError(err)
	}
	return VoidPaymentCommand{paymentID: id, idempotencyKey: key}, nil
}

func NewRefundPaymentCommand(paymentID string, idempotencyKey string) (RefundPaymentCommand, error) {
	id, key, err := parsePaymentOperationInput(paymentID, idempotencyKey)
	if err != nil {
		return RefundPaymentCommand{}, ensurePaymentError(err)
	}
	return RefundPaymentCommand{paymentID: id, idempotencyKey: key}, nil
}
