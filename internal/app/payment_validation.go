package app

import (
	"strings"

	"github.com/roigada/payment-gateway/internal/domain"
)

func newCardDetails(number string, cvv string, expiryMonth int, expiryYear int) (cardDetails, error) {
	card := cardDetails{
		number:      strings.TrimSpace(number),
		cvv:         strings.TrimSpace(cvv),
		expiryMonth: expiryMonth,
		expiryYear:  expiryYear,
	}
	if !allDigits(card.number) || len(card.number) < 12 || len(card.number) > 19 {
		return cardDetails{}, NewInvalidPaymentInputError("card details are invalid", nil)
	}
	if !allDigits(card.cvv) || len(card.cvv) < 3 || len(card.cvv) > 4 {
		return cardDetails{}, NewInvalidPaymentInputError("card details are invalid", nil)
	}
	if card.expiryMonth < 1 || card.expiryMonth > 12 {
		return cardDetails{}, NewInvalidPaymentInputError("card details are invalid", nil)
	}
	if card.expiryYear <= 0 {
		return cardDetails{}, NewInvalidPaymentInputError("card details are invalid", nil)
	}
	return card, nil
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parsePaymentOperationInput(paymentID string, idempotencyKey string) (domain.PaymentID, string, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return "", "", NewInvalidPaymentInputError("idempotency key is required", nil)
	}
	parsedPaymentID, err := parsePaymentID(paymentID)
	if err != nil {
		return "", "", err
	}
	return parsedPaymentID, idempotencyKey, nil
}

func parsePaymentID(value string) (domain.PaymentID, error) {
	paymentID, err := domain.ParsePaymentID(strings.TrimSpace(value))
	if err != nil {
		return "", NewInvalidPaymentInputError("payment id is invalid", err)
	}
	return paymentID, nil
}
