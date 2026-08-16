package domain_test

import (
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAuthorizedPayment(
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

func newDeclinedPayment(
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

// Verifies that is valid payment status.
func TestIsValidPaymentStatus(t *testing.T) {
	validStatuses := []domain.PaymentStatus{
		domain.PaymentStatusPending,
		domain.PaymentStatusAuthorized,
		domain.PaymentStatusExpired,
		domain.PaymentStatusDeclined,
		domain.PaymentStatusCaptured,
		domain.PaymentStatusVoided,
		domain.PaymentStatusRefunded,
	}

	for _, status := range validStatuses {
		// Verifies the table-defined scenario for this case.
		t.Run(string(status), func(t *testing.T) {
			assert.True(t, domain.IsValidPaymentStatus(status))
		})
	}

	assert.False(t, domain.IsValidPaymentStatus("unknown"))
}

// Verifies that payment authorization stores private bank authorization id.
func TestPaymentAuthorizationStoresPrivateBankAuthorizationID(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(time.Hour)

	payment, err := newAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		" order-1 ",
		" customer-1 ",
		1299,
		" bank-auth-id-1 ",
		expiresAt,
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
	assert.Equal(t, expiresAt, payment.AuthorizationExpiresAt())
	assert.Equal(t, "bok-1", payment.AuthorizationBankOperationKey())
	assert.Equal(t, "fingerprint-1", payment.AuthorizationCardFingerprint())
	assert.Equal(t, now, payment.CreatedAt())
	assert.Equal(t, now, payment.UpdatedAt())
}

// Verifies that new pending payment creates payment with retry private fields.
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

// Verifies that payment decline stores gateway decline reason.
func TestPaymentDeclineStoresGatewayDeclineReason(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	payment, err := newDeclinedPayment(
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

// Verifies that capture authorized payment stores private capture fields.
func TestCaptureAuthorizedPaymentStoresPrivateCaptureFields(t *testing.T) {
	authorizedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	expiresAt := authorizedAt.Add(time.Hour)
	payment, err := newAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440000",
		expiresAt,
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		authorizedAt,
	)
	require.NoError(t, err)
	capturedAt := time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC)

	require.NoError(t, payment.MarkCaptured(
		" cap_550e8400-e29b-41d4-a716-446655440002 ",
		" bok_550e8400-e29b-41d4-a716-446655440003 ",
		capturedAt,
	))

	assert.Equal(t, domain.PaymentStatusCaptured, payment.Status())
	assert.Equal(t, "auth_550e8400-e29b-41d4-a716-446655440000", payment.BankAuthorizationID())
	assert.Equal(t, expiresAt, payment.AuthorizationExpiresAt())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440001", payment.AuthorizationBankOperationKey())
	assert.Equal(t, "cap_550e8400-e29b-41d4-a716-446655440002", payment.BankCaptureID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440003", payment.CaptureBankOperationKey())
	assert.Equal(t, authorizedAt, payment.CreatedAt())
	assert.Equal(t, capturedAt, payment.UpdatedAt())
}

// Verifies that capture payment rejects invalid values.
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
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			bankAuthorizationID := ""
			declineReason := domain.DeclineReasonUnknown
			if tt.status == domain.PaymentStatusAuthorized {
				bankAuthorizationID = "auth_550e8400-e29b-41d4-a716-446655440000"
				declineReason = ""
			}
			authorizationExpiresAt := time.Time{}
			if bankAuthorizationID != "" {
				authorizationExpiresAt = authorizedAt.Add(time.Hour)
			}
			payment, err := domain.LoadPayment(
				domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
				"order-1",
				"customer-1",
				1299,
				domain.CurrencyUSD,
				tt.status,
				bankAuthorizationID,
				authorizationExpiresAt,
				"bok_550e8400-e29b-41d4-a716-446655440001",
				"fingerprint-1",
				"",
				"",
				"",
				"",
				"",
				"",
				declineReason,
				authorizedAt,
				authorizedAt,
			)
			require.NoError(t, err)

			err = payment.MarkCaptured(tt.bankCaptureID, tt.bok, tt.now)

			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

// Verifies that refund captured payment stores private refund fields.
func TestRefundCapturedPaymentStoresPrivateRefundFields(t *testing.T) {
	createdAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	expiresAt := createdAt.Add(time.Hour)
	payment, err := newAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440000",
		expiresAt,
		"bok-auth",
		"fingerprint-1",
		createdAt,
	)
	require.NoError(t, err)
	capturedAt := time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC)
	require.NoError(t, payment.MarkCaptured(
		"cap_550e8400-e29b-41d4-a716-446655440001",
		"bok-capture",
		capturedAt,
	))
	refundedAt := time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC)

	require.NoError(t, payment.MarkRefunded(
		" ref_550e8400-e29b-41d4-a716-446655440002 ",
		" bok-refund ",
		refundedAt,
	))

	assert.Equal(t, domain.PaymentStatusRefunded, payment.Status())
	assert.Equal(t, "ref_550e8400-e29b-41d4-a716-446655440002", payment.BankRefundID())
	assert.Equal(t, "bok-refund", payment.RefundBankOperationKey())
	assert.Equal(t, createdAt, payment.CreatedAt())
	assert.Equal(t, refundedAt, payment.UpdatedAt())
}

// Verifies that refund payment rejects invalid values.
func TestRefundPaymentRejectsInvalidValues(t *testing.T) {
	capturedAt := time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC)
	tests := []struct {
		name         string
		status       domain.PaymentStatus
		bankRefundID string
		bok          string
		now          time.Time
		wantErr      error
	}{
		{name: "status", status: domain.PaymentStatusAuthorized, bankRefundID: "ref_550e8400-e29b-41d4-a716-446655440002", bok: "bok-refund", now: capturedAt, wantErr: domain.ErrInvalidPaymentStatus},
		{name: "refund id", status: domain.PaymentStatusCaptured, bankRefundID: " ", bok: "bok-refund", now: capturedAt, wantErr: domain.ErrInvalidBankRefundID},
		{name: "bank operation key", status: domain.PaymentStatusCaptured, bankRefundID: "ref_550e8400-e29b-41d4-a716-446655440002", bok: " ", now: capturedAt, wantErr: domain.ErrInvalidBankOperationKey},
		{name: "timestamp", status: domain.PaymentStatusCaptured, bankRefundID: "ref_550e8400-e29b-41d4-a716-446655440002", bok: "bok-refund", now: time.Time{}, wantErr: domain.ErrInvalidPaymentTimestamp},
	}

	for _, tt := range tests {
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			var (
				payment *domain.Payment
				err     error
			)
			if tt.status == domain.PaymentStatusCaptured {
				payment, err = domain.LoadPayment(
					domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
					"order-1",
					"customer-1",
					1299,
					domain.CurrencyUSD,
					tt.status,
					"auth_550e8400-e29b-41d4-a716-446655440000",
					capturedAt.Add(time.Hour),
					"bok-auth",
					"fingerprint-1",
					"cap_550e8400-e29b-41d4-a716-446655440001",
					"bok-capture",
					"",
					"",
					"",
					"",
					"",
					capturedAt,
					capturedAt,
				)
			} else {
				payment, err = newAuthorizedPayment(
					domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
					"order-1",
					"customer-1",
					1299,
					"auth_550e8400-e29b-41d4-a716-446655440000",
					capturedAt.Add(time.Hour),
					"bok-auth",
					"fingerprint-1",
					capturedAt,
				)
			}
			require.NoError(t, err)

			err = payment.MarkRefunded(tt.bankRefundID, tt.bok, tt.now)

			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

// Verifies that payment void moves authorized payment to voided.
func TestPaymentVoidMovesAuthorizedPaymentToVoided(t *testing.T) {
	createdAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	expiresAt := createdAt.Add(2 * time.Hour)
	payment, err := newAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440000",
		expiresAt,
		"bok-auth",
		"fingerprint-1",
		createdAt,
	)
	require.NoError(t, err)
	voidedAt := time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC)

	err = payment.MarkVoided(" void_550e8400-e29b-41d4-a716-446655440002 ", " bok-void ", voidedAt)
	require.NoError(t, err)

	assert.Equal(t, domain.PaymentStatusVoided, payment.Status())
	assert.Equal(t, "void_550e8400-e29b-41d4-a716-446655440002", payment.BankVoidID())
	assert.Equal(t, "bok-void", payment.VoidBankOperationKey())
	assert.Equal(t, createdAt, payment.CreatedAt())
	assert.Equal(t, voidedAt, payment.UpdatedAt())
}

// Verifies that payment void rejects invalid transition.
func TestPaymentVoidRejectsInvalidTransition(t *testing.T) {
	payment, err := newDeclinedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		domain.DeclineReasonInvalidCard,
		"bok-auth",
		"fingerprint-1",
		time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)

	err = payment.MarkVoided("void_550e8400-e29b-41d4-a716-446655440002", "bok-void", time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC))

	assert.ErrorIs(t, err, domain.ErrInvalidPaymentStatus)
}

// Verifies that load payment rejects incomplete completed bank operations.
func TestLoadPaymentRejectsIncompleteCompletedBankOperations(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name                    string
		status                  domain.PaymentStatus
		bankCaptureID           string
		captureBankOperationKey string
		bankRefundID            string
		refundBankOperationKey  string
		bankVoidID              string
		voidBankOperationKey    string
		wantErr                 error
	}{
		{name: "captured without capture id", status: domain.PaymentStatusCaptured, captureBankOperationKey: "bok-capture", wantErr: domain.ErrInvalidBankCaptureID},
		{name: "captured without capture operation key", status: domain.PaymentStatusCaptured, bankCaptureID: "cap-1", wantErr: domain.ErrInvalidBankOperationKey},
		{name: "voided without void id", status: domain.PaymentStatusVoided, voidBankOperationKey: "bok-void", wantErr: domain.ErrInvalidBankVoidID},
		{name: "voided without void operation key", status: domain.PaymentStatusVoided, bankVoidID: "void-1", wantErr: domain.ErrInvalidBankOperationKey},
		{name: "refunded without capture id", status: domain.PaymentStatusRefunded, captureBankOperationKey: "bok-capture", bankRefundID: "ref-1", refundBankOperationKey: "bok-refund", wantErr: domain.ErrInvalidBankCaptureID},
		{name: "refunded without capture operation key", status: domain.PaymentStatusRefunded, bankCaptureID: "cap-1", bankRefundID: "ref-1", refundBankOperationKey: "bok-refund", wantErr: domain.ErrInvalidBankOperationKey},
	}

	for _, tt := range tests {
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			_, err := domain.LoadPayment(
				domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
				"order-1",
				"customer-1",
				1299,
				domain.CurrencyUSD,
				tt.status,
				"auth-1",
				now.Add(time.Hour),
				"bok-auth",
				"fingerprint-1",
				tt.bankCaptureID,
				tt.captureBankOperationKey,
				tt.bankRefundID,
				tt.refundBankOperationKey,
				tt.bankVoidID,
				tt.voidBankOperationKey,
				"",
				now,
				now,
			)

			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

// Verifies that load payment reports the invalid private field.
func TestLoadPaymentReportsTheInvalidPrivateField(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name                    string
		status                  domain.PaymentStatus
		bankCaptureID           string
		captureBankOperationKey string
		wantErr                 error
	}{
		{name: "pending capture id", status: domain.PaymentStatusPending, bankCaptureID: "cap-1", wantErr: domain.ErrInvalidBankCaptureID},
		{name: "expired capture operation key", status: domain.PaymentStatusExpired, captureBankOperationKey: "bok-capture", wantErr: domain.ErrInvalidBankOperationKey},
		{name: "voided capture id", status: domain.PaymentStatusVoided, bankCaptureID: "cap-1", wantErr: domain.ErrInvalidBankCaptureID},
	}

	for _, tt := range tests {
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			bankAuthorizationID := ""
			authorizationExpiresAt := time.Time{}
			bankVoidID := ""
			voidBankOperationKey := ""
			if tt.status != domain.PaymentStatusPending {
				bankAuthorizationID = "auth-1"
				authorizationExpiresAt = now.Add(time.Hour)
			}
			if tt.status == domain.PaymentStatusVoided {
				bankVoidID = "void-1"
				voidBankOperationKey = "bok-void"
			}

			_, err := domain.LoadPayment(
				domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
				"order-1",
				"customer-1",
				1299,
				domain.CurrencyUSD,
				tt.status,
				bankAuthorizationID,
				authorizationExpiresAt,
				"bok-auth",
				"fingerprint-1",
				tt.bankCaptureID,
				tt.captureBankOperationKey,
				"",
				"",
				bankVoidID,
				voidBankOperationKey,
				"",
				now,
				now,
			)

			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

// Verifies that payment authorization rejects invalid values.
func TestPaymentAuthorizationRejectsInvalidValues(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(time.Hour)

	tests := []struct {
		name                string
		id                  domain.PaymentID
		orderID             string
		customer            string
		amount              int64
		bankAuthorizationID string
		expiresAt           time.Time
		bok                 string
		fingerprint         string
		now                 time.Time
		wantErr             error
	}{
		{name: "payment id without prefix", id: "550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", expiresAt: expiresAt, bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidPaymentID},
		{name: "payment id without uuid", id: "pay_123", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", expiresAt: expiresAt, bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidPaymentID},
		{name: "payment id with undashed uuid", id: "pay_550e8400e29b41d4a716446655440000", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", expiresAt: expiresAt, bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidPaymentID},
		{name: "payment id with urn uuid", id: "pay_urn:uuid:550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", expiresAt: expiresAt, bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidPaymentID},
		{name: "order id", id: "pay_550e8400-e29b-41d4-a716-446655440000", orderID: " ", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", expiresAt: expiresAt, bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidOrderID},
		{name: "customer id", id: "pay_550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: " ", amount: 100, bankAuthorizationID: "bank-auth-id-1", expiresAt: expiresAt, bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidCustomerID},
		{name: "amount", id: "pay_550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: "customer-1", amount: 0, bankAuthorizationID: "bank-auth-id-1", expiresAt: expiresAt, bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidAmount},
		{name: "bank authorization id", id: "pay_550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: " ", expiresAt: expiresAt, bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidBankAuthorizationID},
		{name: "authorization expiration", id: "pay_550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", expiresAt: time.Time{}, bok: "bok-1", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidAuthorizationExpirationTime},
		{name: "bank operation key", id: "pay_550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", expiresAt: expiresAt, bok: " ", fingerprint: "fingerprint-1", now: now, wantErr: domain.ErrInvalidBankOperationKey},
		{name: "authorization card fingerprint", id: "pay_550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", expiresAt: expiresAt, bok: "bok-1", fingerprint: " ", now: now, wantErr: domain.ErrInvalidAuthorizationCardFingerprint},
		{name: "timestamp", id: "pay_550e8400-e29b-41d4-a716-446655440000", orderID: "order-1", customer: "customer-1", amount: 100, bankAuthorizationID: "bank-auth-id-1", expiresAt: expiresAt, bok: "bok-1", fingerprint: "fingerprint-1", now: time.Time{}, wantErr: domain.ErrInvalidPaymentTimestamp},
	}

	for _, tt := range tests {
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			_, err := newAuthorizedPayment(tt.id, tt.orderID, tt.customer, tt.amount, tt.bankAuthorizationID, tt.expiresAt, tt.bok, tt.fingerprint, tt.now)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

// Verifies that payment decline rejects invalid values.
func TestPaymentDeclineRejectsInvalidValues(t *testing.T) {
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
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			_, err := newDeclinedPayment(
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
