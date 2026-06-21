package domain_test

import (
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAuthorizedPaymentCreatesPaymentWithPrivateBankAuthorizationID(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	payment, err := domain.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		" order-1 ",
		" customer-1 ",
		1299,
		" bank-auth-id-1 ",
		" bok-1 ",
		" fingerprint-1 ",
		now,
	)
	require.NoError(t, err)

	assert.Equal(t, domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"), payment.ID())
	assert.Equal(t, "order-1", payment.OrderID())
	assert.Equal(t, "customer-1", payment.CustomerID())
	assert.Equal(t, int64(1299), payment.AmountCents())
	assert.Equal(t, domain.CurrencyUSD, payment.Currency())
	assert.Equal(t, domain.PaymentStatusAuthorized, payment.Status())
	assert.Empty(t, payment.DeclineReason())
	assert.Equal(t, "bank-auth-id-1", payment.BankAuthorizationID())
	assert.Equal(t, "bok-1", payment.AuthorizationBankOperationKey())
	assert.Equal(t, "fingerprint-1", payment.AuthorizationCardFingerprint())
	assert.Equal(t, now, payment.CreatedAt())
	assert.Equal(t, now, payment.UpdatedAt())
}

func TestNewPendingPaymentCreatesPaymentWithRetryPrivateFields(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	payment, err := domain.NewPendingPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		" order-1 ",
		" customer-1 ",
		1299,
		" bok-1 ",
		" fingerprint-1 ",
		now,
	)
	require.NoError(t, err)

	assert.Equal(t, domain.PaymentStatusPending, payment.Status())
	assert.Empty(t, payment.BankAuthorizationID())
	assert.Empty(t, payment.DeclineReason())
	assert.Equal(t, "bok-1", payment.AuthorizationBankOperationKey())
	assert.Equal(t, "fingerprint-1", payment.AuthorizationCardFingerprint())
	assert.Equal(t, now, payment.CreatedAt())
	assert.Equal(t, now, payment.UpdatedAt())
}

func TestNewDeclinedPaymentCreatesPaymentWithGatewayDeclineReason(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	payment, err := domain.NewDeclinedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		" order-1 ",
		" customer-1 ",
		1299,
		domain.DeclineReasonInsufficientFunds,
		" bok-1 ",
		" fingerprint-1 ",
		now,
	)
	require.NoError(t, err)

	assert.Equal(t, domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"), payment.ID())
	assert.Equal(t, "order-1", payment.OrderID())
	assert.Equal(t, "customer-1", payment.CustomerID())
	assert.Equal(t, int64(1299), payment.AmountCents())
	assert.Equal(t, domain.CurrencyUSD, payment.Currency())
	assert.Equal(t, domain.PaymentStatusDeclined, payment.Status())
	assert.Equal(t, domain.DeclineReasonInsufficientFunds, payment.DeclineReason())
	assert.Empty(t, payment.BankAuthorizationID())
	assert.Equal(t, "bok-1", payment.AuthorizationBankOperationKey())
	assert.Equal(t, "fingerprint-1", payment.AuthorizationCardFingerprint())
	assert.Equal(t, now, payment.CreatedAt())
	assert.Equal(t, now, payment.UpdatedAt())
}

func TestCaptureAuthorizedPaymentStoresPrivateCaptureFields(t *testing.T) {
	authorizedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	payment, err := domain.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440000",
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		authorizedAt,
	)
	require.NoError(t, err)
	capturedAt := time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC)

	require.NoError(t, payment.Capture(
		" cap_550e8400-e29b-41d4-a716-446655440002 ",
		" bok_550e8400-e29b-41d4-a716-446655440003 ",
		capturedAt,
	))

	assert.Equal(t, domain.PaymentStatusCaptured, payment.Status())
	assert.Equal(t, "auth_550e8400-e29b-41d4-a716-446655440000", payment.BankAuthorizationID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440001", payment.AuthorizationBankOperationKey())
	assert.Equal(t, "cap_550e8400-e29b-41d4-a716-446655440002", payment.BankCaptureID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440003", payment.CaptureBankOperationKey())
	assert.Equal(t, authorizedAt, payment.CreatedAt())
	assert.Equal(t, capturedAt, payment.UpdatedAt())
}

func TestCapturePaymentRejectsInvalidValues(t *testing.T) {
	authorizedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name          string
		status        domain.PaymentStatus
		bankCaptureID string
		bok           string
		now           time.Time
		wantErr       error
	}{
		{name: "status", status: domain.PaymentStatusDeclined, bankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440002", bok: "bok_550e8400-e29b-41d4-a716-446655440003", now: authorizedAt, wantErr: domain.ErrInvalidPaymentStatus},
		{name: "capture id", status: domain.PaymentStatusAuthorized, bankCaptureID: " ", bok: "bok_550e8400-e29b-41d4-a716-446655440003", now: authorizedAt, wantErr: domain.ErrInvalidBankCaptureID},
		{name: "bank operation key", status: domain.PaymentStatusAuthorized, bankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440002", bok: " ", now: authorizedAt, wantErr: domain.ErrInvalidBankOperationKey},
		{name: "timestamp", status: domain.PaymentStatusAuthorized, bankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440002", bok: "bok_550e8400-e29b-41d4-a716-446655440003", now: time.Time{}, wantErr: domain.ErrInvalidPaymentTimestamp},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bankAuthorizationID := ""
			declineReason := domain.DeclineReasonUnknown
			if tt.status == domain.PaymentStatusAuthorized {
				bankAuthorizationID = "auth_550e8400-e29b-41d4-a716-446655440000"
				declineReason = ""
			}
			payment, err := domain.LoadPayment(
				domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
				"order-1",
				"customer-1",
				1299,
				domain.CurrencyUSD,
				tt.status,
				bankAuthorizationID,
				"bok_550e8400-e29b-41d4-a716-446655440001",
				"fingerprint-1",
				"",
				"",
				declineReason,
				authorizedAt,
				authorizedAt,
			)
			require.NoError(t, err)

			err = payment.Capture(tt.bankCaptureID, tt.bok, tt.now)

			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestNewAuthorizedPaymentRejectsInvalidValues(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name                string
		id                  domain.PaymentID
		orderID             string
		customer            string
		amount              int64
		bankAuthorizationID string
		bok                 string
		fingerprint         string
		now                 time.Time
		wantErr             error
	}{
		{name: "payment id without prefix", id: "550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidPaymentID},
		{name: "payment id without uuid", id: "pay_123", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidPaymentID},
		{name: "payment id with undashed uuid", id: "pay_550e8400e29b41d4a716446655440000", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidPaymentID},
		{name: "payment id with urn uuid", id: "pay_urn:uuid:550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidPaymentID},
		{name: "order id", id: "pay_550e8400-e29b-41d4-a716-446655440000", orderID: " ", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidOrderID},
		{name: "customer id", id: "pay_550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: " ", amount: 100, bankAuthorizationID: "bank-auth-id-1", bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidCustomerID},
		{name: "amount", id: "pay_550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: "customer-1", amount: 0, bankAuthorizationID: "bank-auth-id-1", bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidAmount},
		{name: "bank authorization id", id: "pay_550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: " ", bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidBankAuthorizationID},
		{name: "bank operation key", id: "pay_550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", bok: " ", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidBankOperationKey},
		{name: "authorization card fingerprint", id: "pay_550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", bok: "bok-1", fingerprint: " ", now: now, wantErr: domain.ErrInvalidAuthorizationCardFingerprint},
		{name: "timestamp", id: "pay_550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", bok: "bok-1", fingerprint: "fingerprint-1", now: time.Time{}, wantErr: domain.ErrInvalidPaymentTimestamp},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := domain.NewAuthorizedPayment(tt.id, tt.orderID, tt.customer, tt.amount, tt.bankAuthorizationID, tt.bok, tt.fingerprint, tt.now)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestNewDeclinedPaymentRejectsInvalidValues(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		reason      domain.DeclineReason
		bok         string
		fingerprint string
		now         time.Time
		wantErr     error
	}{
		{name: "decline reason", reason: domain.DeclineReason("raw_bank_code"), bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidDeclineReason},
		{name: "bank operation key", reason: domain.DeclineReasonInvalidCard, bok: " ", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidBankOperationKey},
		{name: "authorization card fingerprint", reason: domain.DeclineReasonInvalidCard, bok: "bok-1", fingerprint: " ", now: now, wantErr: domain.ErrInvalidAuthorizationCardFingerprint},
		{name: "timestamp", reason: domain.DeclineReasonExpiredCard, bok: "bok-1", fingerprint: "fingerprint-1", now: time.Time{}, wantErr: domain.ErrInvalidPaymentTimestamp},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := domain.NewDeclinedPayment(
				domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
				"order-1",
				"customer-1",
				100,
				tt.reason,
				tt.bok,
				tt.fingerprint,
				tt.now,
			)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}
