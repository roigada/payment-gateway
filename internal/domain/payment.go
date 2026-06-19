package domain

import (
	"errors"
	"strings"
	"time"
)

const CurrencyUSD = "USD"

var (
	ErrInvalidPaymentID           = errors.New("invalid payment id")
	ErrInvalidOrderID             = errors.New("invalid order id")
	ErrInvalidCustomerID          = errors.New("invalid customer id")
	ErrInvalidAmount              = errors.New("invalid amount")
	ErrInvalidBankAuthorizationID = errors.New("invalid bank authorization id")
	ErrInvalidBankOperationKey    = errors.New("invalid bank operation key")
	ErrInvalidPaymentTimestamp    = errors.New("invalid payment timestamp")
)

type PaymentID string

type PaymentStatus string

const (
	PaymentStatusAuthorized PaymentStatus = "authorized"
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
	createdAt                     time.Time
	updatedAt                     time.Time
}

func NewAuthorizedPayment(
	id PaymentID,
	orderID string,
	customerID string,
	amountCents int64,
	bankAuthorizationID string,
	authorizationBankOperationKey string,
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
	bankAuthorizationID, err = normalizeRequired(bankAuthorizationID, ErrInvalidBankAuthorizationID)
	if err != nil {
		return nil, err
	}
	authorizationBankOperationKey, err = normalizeRequired(authorizationBankOperationKey, ErrInvalidBankOperationKey)
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
		status:                        PaymentStatusAuthorized,
		bankAuthorizationID:           bankAuthorizationID,
		authorizationBankOperationKey: authorizationBankOperationKey,
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
	createdAt time.Time,
	updatedAt time.Time,
) (*Payment, error) {
	if currency != CurrencyUSD {
		return nil, ErrInvalidAmount
	}
	if status != PaymentStatusAuthorized {
		return nil, ErrInvalidPaymentID
	}
	payment, err := NewAuthorizedPayment(id, orderID, customerID, amountCents, bankAuthorizationID, authorizationBankOperationKey, createdAt)
	if err != nil {
		return nil, err
	}
	if updatedAt.IsZero() {
		return nil, ErrInvalidPaymentTimestamp
	}
	payment.updatedAt = updatedAt
	return payment, nil
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

func (p *Payment) ID() PaymentID                         { return p.id }
func (p *Payment) OrderID() string                       { return p.orderID }
func (p *Payment) CustomerID() string                    { return p.customerID }
func (p *Payment) AmountCents() int64                    { return p.amountCents }
func (p *Payment) Currency() string                      { return p.currency }
func (p *Payment) Status() PaymentStatus                 { return p.status }
func (p *Payment) BankAuthorizationID() string           { return p.bankAuthorizationID }
func (p *Payment) AuthorizationBankOperationKey() string { return p.authorizationBankOperationKey }
func (p *Payment) CreatedAt() time.Time                  { return p.createdAt }
func (p *Payment) UpdatedAt() time.Time                  { return p.updatedAt }
