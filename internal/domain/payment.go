package domain

import (
	"errors"
	"strings"
	"time"
)

const CurrencyUSD = "USD"

var (
	ErrInvalidPaymentID                    = errors.New("invalid payment id")
	ErrInvalidOrderID                      = errors.New("invalid order id")
	ErrInvalidCustomerID                   = errors.New("invalid customer id")
	ErrInvalidAmount                       = errors.New("invalid amount")
	ErrInvalidBankAuthorizationID          = errors.New("invalid bank authorization id")
	ErrInvalidBankOperationKey             = errors.New("invalid bank operation key")
	ErrInvalidAuthorizationCardFingerprint = errors.New("invalid authorization card fingerprint")
	ErrInvalidDeclineReason                = errors.New("invalid decline reason")
	ErrInvalidPaymentTimestamp             = errors.New("invalid payment timestamp")
	ErrInvalidPaymentStatus                = errors.New("invalid payment status")
)

type PaymentID string

type PaymentStatus string

const (
	PaymentStatusPending    PaymentStatus = "pending"
	PaymentStatusAuthorized PaymentStatus = "authorized"
	PaymentStatusDeclined   PaymentStatus = "declined"
)

type DeclineReason string

const (
	DeclineReasonInsufficientFunds DeclineReason = "insufficient_funds"
	DeclineReasonInvalidCard       DeclineReason = "invalid_card"
	DeclineReasonExpiredCard       DeclineReason = "expired_card"
	DeclineReasonUnknown           DeclineReason = "unknown"
)

type Payment struct {
	id                            PaymentID
	orderID                       string
	customerID                    string
	amountCents                   int64
	currency                      string
	status                        PaymentStatus
	bankAuthorizationID           string
	authorizationBankOperationKey string
	authorizationCardFingerprint  string
	declineReason                 DeclineReason
	createdAt                     time.Time
	updatedAt                     time.Time
}

func NewPendingPayment(
	id PaymentID,
	orderID string,
	customerID string,
	amountCents int64,
	authorizationBankOperationKey string,
	authorizationCardFingerprint string,
	now time.Time,
) (*Payment, error) {
	payment, err := newBasePayment(
		id,
		orderID,
		customerID,
		amountCents,
		PaymentStatusPending,
		authorizationBankOperationKey,
		authorizationCardFingerprint,
		now,
	)
	if err != nil {
		return nil, err
	}
	return payment, nil
}

func NewAuthorizedPayment(
	id PaymentID,
	orderID string,
	customerID string,
	amountCents int64,
	bankAuthorizationID string,
	authorizationBankOperationKey string,
	authorizationCardFingerprint string,
	now time.Time,
) (*Payment, error) {
	payment, err := newBasePayment(
		id,
		orderID,
		customerID,
		amountCents,
		PaymentStatusAuthorized,
		authorizationBankOperationKey,
		authorizationCardFingerprint,
		now,
	)
	if err != nil {
		return nil, err
	}
	bankAuthorizationID, err = normalizeRequired(bankAuthorizationID, ErrInvalidBankAuthorizationID)
	if err != nil {
		return nil, err
	}
	payment.bankAuthorizationID = bankAuthorizationID
	return payment, nil
}

func NewDeclinedPayment(
	id PaymentID,
	orderID string,
	customerID string,
	amountCents int64,
	declineReason DeclineReason,
	authorizationBankOperationKey string,
	authorizationCardFingerprint string,
	now time.Time,
) (*Payment, error) {
	payment, err := newBasePayment(
		id,
		orderID,
		customerID,
		amountCents,
		PaymentStatusDeclined,
		authorizationBankOperationKey,
		authorizationCardFingerprint,
		now,
	)
	if err != nil {
		return nil, err
	}
	if err := validateDeclineReason(declineReason); err != nil {
		return nil, err
	}
	payment.declineReason = declineReason
	return payment, nil
}

func newBasePayment(
	id PaymentID,
	orderID string,
	customerID string,
	amountCents int64,
	status PaymentStatus,
	authorizationBankOperationKey string,
	authorizationCardFingerprint string,
	now time.Time,
) (*Payment, error) {
	if err := validatePaymentID(id); err != nil {
		return nil, err
	}
	orderID, err := normalizeRequired(orderID, ErrInvalidOrderID)
	if err != nil {
		return nil, err
	}
	customerID, err = normalizeRequired(customerID, ErrInvalidCustomerID)
	if err != nil {
		return nil, err
	}
	if amountCents <= 0 {
		return nil, ErrInvalidAmount
	}
	authorizationBankOperationKey, err = normalizeRequired(authorizationBankOperationKey, ErrInvalidBankOperationKey)
	if err != nil {
		return nil, err
	}
	authorizationCardFingerprint, err = normalizeRequired(authorizationCardFingerprint, ErrInvalidAuthorizationCardFingerprint)
	if err != nil {
		return nil, err
	}
	if now.IsZero() {
		return nil, ErrInvalidPaymentTimestamp
	}

	return &Payment{
		id:                            id,
		orderID:                       orderID,
		customerID:                    customerID,
		amountCents:                   amountCents,
		currency:                      CurrencyUSD,
		status:                        status,
		authorizationBankOperationKey: authorizationBankOperationKey,
		authorizationCardFingerprint:  authorizationCardFingerprint,
		createdAt:                     now,
		updatedAt:                     now,
	}, nil
}

func LoadPayment(
	id PaymentID,
	orderID string,
	customerID string,
	amountCents int64,
	currency string,
	status PaymentStatus,
	bankAuthorizationID string,
	authorizationBankOperationKey string,
	authorizationCardFingerprint string,
	declineReason DeclineReason,
	createdAt time.Time,
	updatedAt time.Time,
) (*Payment, error) {
	if currency != CurrencyUSD {
		return nil, ErrInvalidAmount
	}
	if updatedAt.IsZero() {
		return nil, ErrInvalidPaymentTimestamp
	}

	var (
		payment *Payment
		err     error
	)
	switch status {
	case PaymentStatusPending:
		if strings.TrimSpace(bankAuthorizationID) != "" {
			return nil, ErrInvalidBankAuthorizationID
		}
		if declineReason != "" {
			return nil, ErrInvalidDeclineReason
		}
		payment, err = NewPendingPayment(id, orderID, customerID, amountCents, authorizationBankOperationKey, authorizationCardFingerprint, createdAt)
	case PaymentStatusAuthorized:
		if declineReason != "" {
			return nil, ErrInvalidDeclineReason
		}
		payment, err = NewAuthorizedPayment(id, orderID, customerID, amountCents, bankAuthorizationID, authorizationBankOperationKey, authorizationCardFingerprint, createdAt)
	case PaymentStatusDeclined:
		if strings.TrimSpace(bankAuthorizationID) != "" {
			return nil, ErrInvalidBankAuthorizationID
		}
		payment, err = NewDeclinedPayment(id, orderID, customerID, amountCents, declineReason, authorizationBankOperationKey, authorizationCardFingerprint, createdAt)
	default:
		return nil, ErrInvalidPaymentStatus
	}
	if err != nil {
		return nil, err
	}

	payment.updatedAt = updatedAt
	return payment, nil
}

func (p *Payment) MarkAuthorized(bankAuthorizationID string, now time.Time) error {
	if p.status != PaymentStatusPending {
		return ErrInvalidPaymentStatus
	}
	bankAuthorizationID, err := normalizeRequired(bankAuthorizationID, ErrInvalidBankAuthorizationID)
	if err != nil {
		return err
	}
	if now.IsZero() {
		return ErrInvalidPaymentTimestamp
	}
	p.status = PaymentStatusAuthorized
	p.bankAuthorizationID = bankAuthorizationID
	p.declineReason = ""
	p.updatedAt = now
	return nil
}

func (p *Payment) MarkDeclined(declineReason DeclineReason, now time.Time) error {
	if p.status != PaymentStatusPending {
		return ErrInvalidPaymentStatus
	}
	if err := validateDeclineReason(declineReason); err != nil {
		return err
	}
	if now.IsZero() {
		return ErrInvalidPaymentTimestamp
	}
	p.status = PaymentStatusDeclined
	p.bankAuthorizationID = ""
	p.declineReason = declineReason
	p.updatedAt = now
	return nil
}

func (p *Payment) MarkPending(now time.Time) error {
	if p.status != PaymentStatusPending {
		return ErrInvalidPaymentStatus
	}
	if now.IsZero() {
		return ErrInvalidPaymentTimestamp
	}
	p.updatedAt = now
	return nil
}

func validatePaymentID(id PaymentID) error {
	uuidPart, ok := strings.CutPrefix(string(id), "pay_")
	if !ok || !isDashedUUID(uuidPart) {
		return ErrInvalidPaymentID
	}
	return nil
}

func isDashedUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !isHexDigit(r) {
				return false
			}
		}
	}
	return true
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') ||
		(r >= 'a' && r <= 'f') ||
		(r >= 'A' && r <= 'F')
}

func normalizeRequired(value string, err error) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", err
	}
	return value, nil
}

func validateDeclineReason(reason DeclineReason) error {
	switch reason {
	case DeclineReasonInsufficientFunds, DeclineReasonInvalidCard, DeclineReasonExpiredCard, DeclineReasonUnknown:
		return nil
	default:
		return ErrInvalidDeclineReason
	}
}

func (p *Payment) ID() PaymentID                         { return p.id }
func (p *Payment) OrderID() string                       { return p.orderID }
func (p *Payment) CustomerID() string                    { return p.customerID }
func (p *Payment) AmountCents() int64                    { return p.amountCents }
func (p *Payment) Currency() string                      { return p.currency }
func (p *Payment) Status() PaymentStatus                 { return p.status }
func (p *Payment) BankAuthorizationID() string           { return p.bankAuthorizationID }
func (p *Payment) AuthorizationBankOperationKey() string { return p.authorizationBankOperationKey }
func (p *Payment) AuthorizationCardFingerprint() string  { return p.authorizationCardFingerprint }
func (p *Payment) DeclineReason() DeclineReason          { return p.declineReason }
func (p *Payment) CreatedAt() time.Time                  { return p.createdAt }
func (p *Payment) UpdatedAt() time.Time                  { return p.updatedAt }
