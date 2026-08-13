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
	ErrInvalidBankCaptureID                = errors.New("invalid bank capture id")
	ErrInvalidBankRefundID                 = errors.New("invalid bank refund id")
	ErrInvalidBankVoidID                   = errors.New("invalid bank void id")
	ErrInvalidBankOperationKey             = errors.New("invalid bank operation key")
	ErrInvalidAuthorizationCardFingerprint = errors.New("invalid authorization card fingerprint")
	ErrInvalidDeclineReason                = errors.New("invalid decline reason")
	ErrInvalidPaymentTimestamp             = errors.New("invalid payment timestamp")
	ErrInvalidAuthorizationExpirationTime  = errors.New("invalid authorization expiration time")
	ErrInvalidPaymentStatus                = errors.New("invalid payment status")
)

type PaymentID string

type PaymentStatus string

const (
	PaymentStatusPending    PaymentStatus = "pending"
	PaymentStatusAuthorized PaymentStatus = "authorized"
	PaymentStatusExpired    PaymentStatus = "expired"
	PaymentStatusDeclined   PaymentStatus = "declined"
	PaymentStatusCaptured   PaymentStatus = "captured"
	PaymentStatusVoided     PaymentStatus = "voided"
	PaymentStatusRefunded   PaymentStatus = "refunded"
)

func IsValidPaymentStatus(status PaymentStatus) bool {
	switch status {
	case PaymentStatusPending, PaymentStatusAuthorized, PaymentStatusExpired, PaymentStatusDeclined, PaymentStatusCaptured, PaymentStatusVoided, PaymentStatusRefunded:
		return true
	default:
		return false
	}
}

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
	authorizationExpiresAt        time.Time
	authorizationBankOperationKey string
	authorizationCardFingerprint  string
	bankCaptureID                 string
	captureBankOperationKey       string
	bankRefundID                  string
	refundBankOperationKey        string
	bankVoidID                    string
	voidBankOperationKey          string
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
	return newPayment(
		id,
		orderID,
		customerID,
		amountCents,
		PaymentStatusPending,
		"",
		time.Time{},
		authorizationBankOperationKey,
		authorizationCardFingerprint,
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		now,
	)
}

func NewAuthorizedPayment(
	id PaymentID,
	orderID string,
	customerID string,
	amountCents int64,
	bankAuthorizationID string,
	authorizationExpiresAt time.Time,
	authorizationBankOperationKey string,
	authorizationCardFingerprint string,
	now time.Time,
) (*Payment, error) {
	status := PaymentStatusAuthorized
	if !now.IsZero() && !authorizationExpiresAt.IsZero() && !now.Before(authorizationExpiresAt) {
		status = PaymentStatusExpired
	}
	return newPayment(
		id,
		orderID,
		customerID,
		amountCents,
		status,
		bankAuthorizationID,
		authorizationExpiresAt,
		authorizationBankOperationKey,
		authorizationCardFingerprint,
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		now,
	)
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
	if err := validateDeclineReason(declineReason); err != nil {
		return nil, err
	}
	return newPayment(
		id,
		orderID,
		customerID,
		amountCents,
		PaymentStatusDeclined,
		"",
		time.Time{},
		authorizationBankOperationKey,
		authorizationCardFingerprint,
		"",
		"",
		"",
		"",
		"",
		"",
		declineReason,
		now,
	)
}

func LoadPayment(
	id PaymentID,
	orderID string,
	customerID string,
	amountCents int64,
	currency string,
	status PaymentStatus,
	bankAuthorizationID string,
	authorizationExpiresAt time.Time,
	authorizationBankOperationKey string,
	authorizationCardFingerprint string,
	bankCaptureID string,
	captureBankOperationKey string,
	bankRefundID string,
	refundBankOperationKey string,
	bankVoidID string,
	voidBankOperationKey string,
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
		if strings.TrimSpace(bankAuthorizationID) != "" || strings.TrimSpace(bankCaptureID) != "" || strings.TrimSpace(captureBankOperationKey) != "" {
			return nil, ErrInvalidBankAuthorizationID
		}
		if !authorizationExpiresAt.IsZero() {
			return nil, ErrInvalidAuthorizationExpirationTime
		}
		if strings.TrimSpace(bankRefundID) != "" {
			return nil, ErrInvalidBankRefundID
		}
		if strings.TrimSpace(refundBankOperationKey) != "" {
			return nil, ErrInvalidBankOperationKey
		}
		if strings.TrimSpace(bankVoidID) != "" {
			return nil, ErrInvalidBankVoidID
		}
		if strings.TrimSpace(voidBankOperationKey) != "" {
			return nil, ErrInvalidBankOperationKey
		}
		if declineReason != "" {
			return nil, ErrInvalidDeclineReason
		}
		payment, err = NewPendingPayment(id, orderID, customerID, amountCents, authorizationBankOperationKey, authorizationCardFingerprint, createdAt)
	case PaymentStatusAuthorized:
		if declineReason != "" || strings.TrimSpace(bankCaptureID) != "" {
			return nil, ErrInvalidDeclineReason
		}
		if strings.TrimSpace(bankRefundID) != "" {
			return nil, ErrInvalidBankRefundID
		}
		if strings.TrimSpace(refundBankOperationKey) != "" {
			return nil, ErrInvalidBankOperationKey
		}
		if strings.TrimSpace(bankVoidID) != "" {
			return nil, ErrInvalidBankVoidID
		}
		payment, err = newPayment(id, orderID, customerID, amountCents, status, bankAuthorizationID, authorizationExpiresAt, authorizationBankOperationKey, authorizationCardFingerprint, "", captureBankOperationKey, "", "", "", voidBankOperationKey, "", createdAt)
	case PaymentStatusExpired:
		if declineReason != "" || strings.TrimSpace(bankCaptureID) != "" || strings.TrimSpace(captureBankOperationKey) != "" {
			return nil, ErrInvalidDeclineReason
		}
		if strings.TrimSpace(bankRefundID) != "" {
			return nil, ErrInvalidBankRefundID
		}
		if strings.TrimSpace(refundBankOperationKey) != "" {
			return nil, ErrInvalidBankOperationKey
		}
		if strings.TrimSpace(bankVoidID) != "" {
			return nil, ErrInvalidBankVoidID
		}
		if strings.TrimSpace(voidBankOperationKey) != "" {
			return nil, ErrInvalidBankOperationKey
		}
		payment, err = newPayment(id, orderID, customerID, amountCents, status, bankAuthorizationID, authorizationExpiresAt, authorizationBankOperationKey, authorizationCardFingerprint, "", "", "", "", "", "", "", createdAt)
	case PaymentStatusDeclined:
		if strings.TrimSpace(bankAuthorizationID) != "" || strings.TrimSpace(bankCaptureID) != "" || strings.TrimSpace(captureBankOperationKey) != "" {
			return nil, ErrInvalidBankAuthorizationID
		}
		if !authorizationExpiresAt.IsZero() {
			return nil, ErrInvalidAuthorizationExpirationTime
		}
		if strings.TrimSpace(bankRefundID) != "" {
			return nil, ErrInvalidBankRefundID
		}
		if strings.TrimSpace(refundBankOperationKey) != "" {
			return nil, ErrInvalidBankOperationKey
		}
		if strings.TrimSpace(bankVoidID) != "" {
			return nil, ErrInvalidBankVoidID
		}
		if strings.TrimSpace(voidBankOperationKey) != "" {
			return nil, ErrInvalidBankOperationKey
		}
		payment, err = NewDeclinedPayment(id, orderID, customerID, amountCents, declineReason, authorizationBankOperationKey, authorizationCardFingerprint, createdAt)
	case PaymentStatusCaptured:
		if declineReason != "" {
			return nil, ErrInvalidDeclineReason
		}
		if strings.TrimSpace(bankRefundID) != "" {
			return nil, ErrInvalidBankRefundID
		}
		if strings.TrimSpace(bankVoidID) != "" {
			return nil, ErrInvalidBankVoidID
		}
		if strings.TrimSpace(voidBankOperationKey) != "" {
			return nil, ErrInvalidBankOperationKey
		}
		payment, err = newPayment(id, orderID, customerID, amountCents, status, bankAuthorizationID, authorizationExpiresAt, authorizationBankOperationKey, authorizationCardFingerprint, bankCaptureID, captureBankOperationKey, "", refundBankOperationKey, "", "", "", createdAt)
	case PaymentStatusVoided:
		if declineReason != "" || strings.TrimSpace(bankCaptureID) != "" || strings.TrimSpace(captureBankOperationKey) != "" {
			return nil, ErrInvalidDeclineReason
		}
		if strings.TrimSpace(bankRefundID) != "" {
			return nil, ErrInvalidBankRefundID
		}
		if strings.TrimSpace(refundBankOperationKey) != "" {
			return nil, ErrInvalidBankOperationKey
		}
		payment, err = newPayment(id, orderID, customerID, amountCents, status, bankAuthorizationID, authorizationExpiresAt, authorizationBankOperationKey, authorizationCardFingerprint, "", "", "", "", bankVoidID, voidBankOperationKey, "", createdAt)
	case PaymentStatusRefunded:
		if declineReason != "" {
			return nil, ErrInvalidDeclineReason
		}
		if strings.TrimSpace(bankRefundID) == "" {
			return nil, ErrInvalidBankRefundID
		}
		if strings.TrimSpace(refundBankOperationKey) == "" {
			return nil, ErrInvalidBankOperationKey
		}
		if strings.TrimSpace(bankVoidID) != "" {
			return nil, ErrInvalidBankVoidID
		}
		if strings.TrimSpace(voidBankOperationKey) != "" {
			return nil, ErrInvalidBankOperationKey
		}
		payment, err = newPayment(id, orderID, customerID, amountCents, status, bankAuthorizationID, authorizationExpiresAt, authorizationBankOperationKey, authorizationCardFingerprint, bankCaptureID, captureBankOperationKey, bankRefundID, refundBankOperationKey, "", "", "", createdAt)
	default:
		return nil, ErrInvalidPaymentStatus
	}
	if err != nil {
		return nil, err
	}

	payment.updatedAt = updatedAt
	return payment, nil
}

func newPayment(
	id PaymentID,
	orderID string,
	customerID string,
	amountCents int64,
	status PaymentStatus,
	bankAuthorizationID string,
	authorizationExpiresAt time.Time,
	authorizationBankOperationKey string,
	authorizationCardFingerprint string,
	bankCaptureID string,
	captureBankOperationKey string,
	bankRefundID string,
	refundBankOperationKey string,
	bankVoidID string,
	voidBankOperationKey string,
	declineReason DeclineReason,
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
	if bankAuthorizationID != "" {
		bankAuthorizationID, err = normalizeRequired(bankAuthorizationID, ErrInvalidBankAuthorizationID)
		if err != nil {
			return nil, err
		}
		if authorizationExpiresAt.IsZero() {
			return nil, ErrInvalidAuthorizationExpirationTime
		}
	} else if !authorizationExpiresAt.IsZero() {
		return nil, ErrInvalidAuthorizationExpirationTime
	}
	if bankCaptureID != "" {
		bankCaptureID, err = normalizeRequired(bankCaptureID, ErrInvalidBankCaptureID)
		if err != nil {
			return nil, err
		}
	}
	if captureBankOperationKey != "" {
		captureBankOperationKey, err = normalizeRequired(captureBankOperationKey, ErrInvalidBankOperationKey)
		if err != nil {
			return nil, err
		}
	}
	if bankRefundID != "" {
		bankRefundID, err = normalizeRequired(bankRefundID, ErrInvalidBankRefundID)
		if err != nil {
			return nil, err
		}
	}
	if refundBankOperationKey != "" {
		refundBankOperationKey, err = normalizeRequired(refundBankOperationKey, ErrInvalidBankOperationKey)
		if err != nil {
			return nil, err
		}
	}
	if bankVoidID != "" {
		bankVoidID, err = normalizeRequired(bankVoidID, ErrInvalidBankVoidID)
		if err != nil {
			return nil, err
		}
	}
	if voidBankOperationKey != "" {
		voidBankOperationKey, err = normalizeRequired(voidBankOperationKey, ErrInvalidBankOperationKey)
		if err != nil {
			return nil, err
		}
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
		bankAuthorizationID:           bankAuthorizationID,
		authorizationExpiresAt:        authorizationExpiresAt,
		authorizationBankOperationKey: authorizationBankOperationKey,
		authorizationCardFingerprint:  authorizationCardFingerprint,
		bankCaptureID:                 bankCaptureID,
		captureBankOperationKey:       captureBankOperationKey,
		bankRefundID:                  bankRefundID,
		refundBankOperationKey:        refundBankOperationKey,
		bankVoidID:                    bankVoidID,
		voidBankOperationKey:          voidBankOperationKey,
		declineReason:                 declineReason,
		createdAt:                     now,
		updatedAt:                     now,
	}, nil
}
func (p *Payment) MarkAuthorized(bankAuthorizationID string, authorizationExpiresAt time.Time, now time.Time) error {
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
	if authorizationExpiresAt.IsZero() {
		return ErrInvalidAuthorizationExpirationTime
	}
	if !now.Before(authorizationExpiresAt) {
		p.status = PaymentStatusExpired
	} else {
		p.status = PaymentStatusAuthorized
	}
	p.bankAuthorizationID = bankAuthorizationID
	p.authorizationExpiresAt = authorizationExpiresAt
	p.declineReason = ""
	p.updatedAt = now
	return nil
}

func (p *Payment) MarkExpired(now time.Time) error {
	if p.status != PaymentStatusAuthorized {
		return ErrInvalidPaymentStatus
	}
	if now.IsZero() {
		return ErrInvalidPaymentTimestamp
	}
	p.status = PaymentStatusExpired
	p.captureBankOperationKey = ""
	p.voidBankOperationKey = ""
	p.updatedAt = now
	return nil
}

func (p *Payment) AuthorizationExpired(now time.Time) bool {
	return p.status == PaymentStatusAuthorized && !now.IsZero() && !now.Before(p.authorizationExpiresAt)
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
	p.authorizationExpiresAt = time.Time{}
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

func (p *Payment) MarkCaptured(bankCaptureID string, captureBankOperationKey string, now time.Time) error {
	if p.status != PaymentStatusAuthorized {
		return ErrInvalidPaymentStatus
	}
	bankCaptureID, err := normalizeRequired(bankCaptureID, ErrInvalidBankCaptureID)
	if err != nil {
		return err
	}
	captureBankOperationKey, err = normalizeRequired(captureBankOperationKey, ErrInvalidBankOperationKey)
	if err != nil {
		return err
	}
	if now.IsZero() {
		return ErrInvalidPaymentTimestamp
	}

	p.status = PaymentStatusCaptured
	p.bankCaptureID = bankCaptureID
	p.captureBankOperationKey = captureBankOperationKey
	p.voidBankOperationKey = ""
	p.updatedAt = now
	return nil
}

func (p *Payment) SetCaptureBankOperationKey(captureBankOperationKey string) error {
	if p.status != PaymentStatusAuthorized {
		return ErrInvalidPaymentStatus
	}
	captureBankOperationKey, err := normalizeRequired(captureBankOperationKey, ErrInvalidBankOperationKey)
	if err != nil {
		return err
	}
	p.captureBankOperationKey = captureBankOperationKey
	return nil
}

func (p *Payment) MarkVoided(bankVoidID string, voidBankOperationKey string, now time.Time) error {
	if p.status != PaymentStatusAuthorized {
		return ErrInvalidPaymentStatus
	}
	bankVoidID, err := normalizeRequired(bankVoidID, ErrInvalidBankVoidID)
	if err != nil {
		return err
	}
	voidBankOperationKey, err = normalizeRequired(voidBankOperationKey, ErrInvalidBankOperationKey)
	if err != nil {
		return err
	}
	if now.IsZero() {
		return ErrInvalidPaymentTimestamp
	}
	p.status = PaymentStatusVoided
	p.bankVoidID = bankVoidID
	p.voidBankOperationKey = voidBankOperationKey
	p.captureBankOperationKey = ""
	p.declineReason = ""
	p.updatedAt = now
	return nil
}

func (p *Payment) SetVoidBankOperationKey(voidBankOperationKey string) error {
	if p.status != PaymentStatusAuthorized {
		return ErrInvalidPaymentStatus
	}
	voidBankOperationKey, err := normalizeRequired(voidBankOperationKey, ErrInvalidBankOperationKey)
	if err != nil {
		return err
	}
	p.voidBankOperationKey = voidBankOperationKey
	return nil
}

func (p *Payment) MarkRefunded(bankRefundID string, refundBankOperationKey string, now time.Time) error {
	if p.status != PaymentStatusCaptured {
		return ErrInvalidPaymentStatus
	}
	bankRefundID, err := normalizeRequired(bankRefundID, ErrInvalidBankRefundID)
	if err != nil {
		return err
	}
	refundBankOperationKey, err = normalizeRequired(refundBankOperationKey, ErrInvalidBankOperationKey)
	if err != nil {
		return err
	}
	if now.IsZero() {
		return ErrInvalidPaymentTimestamp
	}

	p.status = PaymentStatusRefunded
	p.bankRefundID = bankRefundID
	p.refundBankOperationKey = refundBankOperationKey
	p.updatedAt = now
	return nil
}

func (p *Payment) SetRefundBankOperationKey(refundBankOperationKey string) error {
	if p.status != PaymentStatusCaptured {
		return ErrInvalidPaymentStatus
	}
	refundBankOperationKey, err := normalizeRequired(refundBankOperationKey, ErrInvalidBankOperationKey)
	if err != nil {
		return err
	}
	p.refundBankOperationKey = refundBankOperationKey
	return nil
}

func validatePaymentID(id PaymentID) error {
	uuidPart, ok := strings.CutPrefix(string(id), "pay_")
	if !ok || !isDashedUUID(uuidPart) {
		return ErrInvalidPaymentID
	}
	return nil
}

func ParsePaymentID(value string) (PaymentID, error) {
	id := PaymentID(value)
	if err := validatePaymentID(id); err != nil {
		return "", err
	}
	return id, nil
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
func (p *Payment) AuthorizationExpiresAt() time.Time     { return p.authorizationExpiresAt }
func (p *Payment) AuthorizationBankOperationKey() string { return p.authorizationBankOperationKey }
func (p *Payment) AuthorizationCardFingerprint() string  { return p.authorizationCardFingerprint }
func (p *Payment) BankCaptureID() string                 { return p.bankCaptureID }
func (p *Payment) CaptureBankOperationKey() string       { return p.captureBankOperationKey }
func (p *Payment) BankRefundID() string                  { return p.bankRefundID }
func (p *Payment) RefundBankOperationKey() string        { return p.refundBankOperationKey }
func (p *Payment) BankVoidID() string                    { return p.bankVoidID }
func (p *Payment) VoidBankOperationKey() string          { return p.voidBankOperationKey }
func (p *Payment) DeclineReason() DeclineReason          { return p.declineReason }
func (p *Payment) CreatedAt() time.Time                  { return p.createdAt }
func (p *Payment) UpdatedAt() time.Time                  { return p.updatedAt }
