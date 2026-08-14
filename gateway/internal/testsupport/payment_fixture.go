package testsupport

import (
	"time"

	"github.com/roigada/payment-gateway/internal/domain"
)

// NewAuthorizedPayment creates a valid Authorized Payment fixture by applying
// the domain authorization transition to a new Pending Payment.
func NewAuthorizedPayment(
	id domain.PaymentID,
	orderID string,
	customerID string,
	amountCents int64,
	bankAuthorizationID string,
	authorizationExpiresAt time.Time,
	authorizationBankOperationKey string,
	authorizationCardFingerprint string,
	now time.Time,
) (*domain.Payment, error) {
	payment, err := domain.NewPendingPayment(id, orderID, customerID, amountCents, authorizationBankOperationKey, authorizationCardFingerprint, now)
	if err != nil {
		return nil, err
	}
	if err := payment.MarkAuthorized(bankAuthorizationID, authorizationExpiresAt, now); err != nil {
		return nil, err
	}
	return payment, nil
}

// NewDeclinedPayment creates a valid Declined Payment fixture by applying the
// domain decline transition to a new Pending Payment.
func NewDeclinedPayment(
	id domain.PaymentID,
	orderID string,
	customerID string,
	amountCents int64,
	declineReason domain.DeclineReason,
	authorizationBankOperationKey string,
	authorizationCardFingerprint string,
	now time.Time,
) (*domain.Payment, error) {
	payment, err := domain.NewPendingPayment(id, orderID, customerID, amountCents, authorizationBankOperationKey, authorizationCardFingerprint, now)
	if err != nil {
		return nil, err
	}
	if err := payment.MarkDeclined(declineReason, now); err != nil {
		return nil, err
	}
	return payment, nil
}
