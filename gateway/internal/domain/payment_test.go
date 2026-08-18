package domain_test

import (
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	validPaymentID = domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000")
	createdAt      = time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
)

func newPendingPayment(now time.Time) (*domain.Payment, error) {
	return domain.NewPendingPayment(validPaymentID, "order-1", "customer-1", 1299, "bok-auth", "fingerprint-1", now)
}

func newAuthorizedPayment(now time.Time) (*domain.Payment, error) {
	payment, err := newPendingPayment(now)
	if err != nil {
		return nil, err
	}
	if err := payment.MarkAuthorized("auth-1", now.Add(time.Hour), now); err != nil {
		return nil, err
	}
	return payment, nil
}

func newCapturedPayment(now time.Time) (*domain.Payment, error) {
	payment, err := newAuthorizedPayment(now)
	if err != nil {
		return nil, err
	}
	if err := payment.SetCaptureBankOperationKey("bok-capture"); err != nil {
		return nil, err
	}
	if err := payment.MarkCaptured("cap-1", now); err != nil {
		return nil, err
	}
	return payment, nil
}

type paymentSnapshot struct {
	status                  domain.PaymentStatus
	bankAuthorizationID     string
	authorizationExpiresAt  time.Time
	captureBankOperationKey string
	bankCaptureID           string
	voidBankOperationKey    string
	bankVoidID              string
	refundBankOperationKey  string
	bankRefundID            string
	declineReason           domain.DeclineReason
	updatedAt               time.Time
}

func snapshot(payment *domain.Payment) paymentSnapshot {
	return paymentSnapshot{
		status:                  payment.Status(),
		bankAuthorizationID:     payment.BankAuthorizationID(),
		authorizationExpiresAt:  payment.AuthorizationExpiresAt(),
		captureBankOperationKey: payment.CaptureBankOperationKey(),
		bankCaptureID:           payment.BankCaptureID(),
		voidBankOperationKey:    payment.VoidBankOperationKey(),
		bankVoidID:              payment.BankVoidID(),
		refundBankOperationKey:  payment.RefundBankOperationKey(),
		bankRefundID:            payment.BankRefundID(),
		declineReason:           payment.DeclineReason(),
		updatedAt:               payment.UpdatedAt(),
	}
}

func assertUnchanged(t *testing.T, payment *domain.Payment, before paymentSnapshot) {
	t.Helper()
	assert.Equal(t, before, snapshot(payment))
}

// Payment lifecycle validation recognizes every declared status and rejects values that cannot
// represent a persisted Payment state.
func TestIsValidPaymentStatus(t *testing.T) {
	for _, status := range []domain.PaymentStatus{
		domain.PaymentStatusPending,
		domain.PaymentStatusAuthorized,
		domain.PaymentStatusExpired,
		domain.PaymentStatusDeclined,
		domain.PaymentStatusCaptured,
		domain.PaymentStatusVoided,
		domain.PaymentStatusRefunded,
	} {
		t.Run(string(status), func(t *testing.T) {
			assert.True(t, domain.IsValidPaymentStatus(status))
		})
	}
	assert.False(t, domain.IsValidPaymentStatus("unknown"))
}

// Creating a Pending Payment normalizes customer-facing identifiers and records the retry
// metadata needed to safely authorize the Payment later.
func TestNewPendingPaymentNormalizesAndInitializesState(t *testing.T) {
	payment, err := domain.NewPendingPayment(validPaymentID, " order-1 ", " customer-1 ", 1299, " bok-auth ", " fingerprint-1 ", createdAt)
	require.NoError(t, err)

	assert.Equal(t, "order-1", payment.OrderID())
	assert.Equal(t, "customer-1", payment.CustomerID())
	assert.Equal(t, domain.PaymentStatusPending, payment.Status())
	assert.Equal(t, "bok-auth", payment.AuthorizationBankOperationKey())
	assert.Equal(t, "fingerprint-1", payment.AuthorizationCardFingerprint())
	assert.Equal(t, createdAt, payment.CreatedAt())
	assert.Equal(t, createdAt, payment.UpdatedAt())
}

// Each valid lifecycle transition records its normalized bank result, clears operation data that
// no longer applies, and preserves the original creation time.
func TestPaymentLifecycleTransitions(t *testing.T) {
	transitionedAt := createdAt.Add(30 * time.Minute)
	tests := []struct {
		name       string
		newPayment func() (*domain.Payment, error)
		transition func(*domain.Payment) error
		assert     func(*testing.T, *domain.Payment)
	}{
		{
			name:       "authorize pending payment",
			newPayment: func() (*domain.Payment, error) { return newPendingPayment(createdAt) },
			transition: func(p *domain.Payment) error {
				return p.MarkAuthorized(" auth-1 ", createdAt.Add(time.Hour), transitionedAt)
			},
			assert: func(t *testing.T, p *domain.Payment) {
				assert.Equal(t, domain.PaymentStatusAuthorized, p.Status())
				assert.Equal(t, "auth-1", p.BankAuthorizationID())
				assert.Equal(t, createdAt.Add(time.Hour), p.AuthorizationExpiresAt())
			},
		},
		{
			name:       "decline pending payment",
			newPayment: func() (*domain.Payment, error) { return newPendingPayment(createdAt) },
			transition: func(p *domain.Payment) error {
				return p.MarkDeclined(domain.DeclineReasonInsufficientFunds, transitionedAt)
			},
			assert: func(t *testing.T, p *domain.Payment) {
				assert.Equal(t, domain.PaymentStatusDeclined, p.Status())
				assert.Equal(t, domain.DeclineReasonInsufficientFunds, p.DeclineReason())
				assert.Empty(t, p.BankAuthorizationID())
				assert.True(t, p.AuthorizationExpiresAt().IsZero())
			},
		},
		{
			name: "expire authorized payment",
			newPayment: func() (*domain.Payment, error) {
				p, err := newAuthorizedPayment(createdAt)
				if err != nil {
					return nil, err
				}
				if err := p.SetCaptureBankOperationKey("bok-capture"); err != nil {
					return nil, err
				}
				return p, p.SetVoidBankOperationKey("bok-void")
			},
			transition: func(p *domain.Payment) error { return p.MarkExpired(transitionedAt) },
			assert: func(t *testing.T, p *domain.Payment) {
				assert.Equal(t, domain.PaymentStatusExpired, p.Status())
				assert.Empty(t, p.CaptureBankOperationKey())
				assert.Empty(t, p.VoidBankOperationKey())
			},
		},
		{
			name: "capture authorized payment",
			newPayment: func() (*domain.Payment, error) {
				p, err := newAuthorizedPayment(createdAt)
				if err != nil {
					return nil, err
				}
				if err := p.SetCaptureBankOperationKey("bok-capture"); err != nil {
					return nil, err
				}
				return p, p.SetVoidBankOperationKey("bok-void")
			},
			transition: func(p *domain.Payment) error { return p.MarkCaptured(" cap-1 ", transitionedAt) },
			assert: func(t *testing.T, p *domain.Payment) {
				assert.Equal(t, domain.PaymentStatusCaptured, p.Status())
				assert.Equal(t, "cap-1", p.BankCaptureID())
				assert.Equal(t, "bok-capture", p.CaptureBankOperationKey())
				assert.Empty(t, p.VoidBankOperationKey())
			},
		},
		{
			name: "void authorized payment",
			newPayment: func() (*domain.Payment, error) {
				p, err := newAuthorizedPayment(createdAt)
				if err != nil {
					return nil, err
				}
				if err := p.SetCaptureBankOperationKey("bok-capture"); err != nil {
					return nil, err
				}
				return p, p.SetVoidBankOperationKey("bok-void")
			},
			transition: func(p *domain.Payment) error { return p.MarkVoided(" void-1 ", transitionedAt) },
			assert: func(t *testing.T, p *domain.Payment) {
				assert.Equal(t, domain.PaymentStatusVoided, p.Status())
				assert.Equal(t, "void-1", p.BankVoidID())
				assert.Equal(t, "bok-void", p.VoidBankOperationKey())
				assert.Empty(t, p.CaptureBankOperationKey())
			},
		},
		{
			name: "refund captured payment",
			newPayment: func() (*domain.Payment, error) {
				p, err := newCapturedPayment(createdAt)
				if err != nil {
					return nil, err
				}
				return p, p.SetRefundBankOperationKey("bok-refund")
			},
			transition: func(p *domain.Payment) error { return p.MarkRefunded(" ref-1 ", transitionedAt) },
			assert: func(t *testing.T, p *domain.Payment) {
				assert.Equal(t, domain.PaymentStatusRefunded, p.Status())
				assert.Equal(t, "ref-1", p.BankRefundID())
				assert.Equal(t, "bok-refund", p.RefundBankOperationKey())
				assert.Equal(t, "cap-1", p.BankCaptureID())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payment, err := tt.newPayment()
			require.NoError(t, err)
			require.NoError(t, tt.transition(payment))
			tt.assert(t, payment)
			assert.Equal(t, createdAt, payment.CreatedAt())
			assert.Equal(t, transitionedAt, payment.UpdatedAt())
		})
	}
}

// Invalid lifecycle transitions and malformed bank data return precise domain errors without
// partially mutating the Payment, so callers can retry or report failures safely.
func TestPaymentTransitionsRejectInvalidValuesWithoutMutation(t *testing.T) {
	tests := []struct {
		name       string
		newPayment func() (*domain.Payment, error)
		transition func(*domain.Payment) error
		wantErr    error
	}{
		{"authorize from authorized", func() (*domain.Payment, error) { return newAuthorizedPayment(createdAt) }, func(p *domain.Payment) error { return p.MarkAuthorized("auth-2", createdAt.Add(time.Hour), createdAt) }, domain.ErrInvalidPaymentStatus},
		{"authorize with blank id", func() (*domain.Payment, error) { return newPendingPayment(createdAt) }, func(p *domain.Payment) error { return p.MarkAuthorized(" ", createdAt.Add(time.Hour), createdAt) }, domain.ErrInvalidBankAuthorizationID},
		{"authorize with zero expiration", func() (*domain.Payment, error) { return newPendingPayment(createdAt) }, func(p *domain.Payment) error { return p.MarkAuthorized("auth-1", time.Time{}, createdAt) }, domain.ErrInvalidAuthorizationExpirationTime},
		{"authorize with zero timestamp", func() (*domain.Payment, error) { return newPendingPayment(createdAt) }, func(p *domain.Payment) error {
			return p.MarkAuthorized("auth-1", createdAt.Add(time.Hour), time.Time{})
		}, domain.ErrInvalidPaymentTimestamp},
		{"decline from authorized", func() (*domain.Payment, error) { return newAuthorizedPayment(createdAt) }, func(p *domain.Payment) error { return p.MarkDeclined(domain.DeclineReasonInvalidCard, createdAt) }, domain.ErrInvalidPaymentStatus},
		{"decline with unknown reason", func() (*domain.Payment, error) { return newPendingPayment(createdAt) }, func(p *domain.Payment) error { return p.MarkDeclined("raw-bank-code", createdAt) }, domain.ErrInvalidDeclineReason},
		{"decline with zero timestamp", func() (*domain.Payment, error) { return newPendingPayment(createdAt) }, func(p *domain.Payment) error { return p.MarkDeclined(domain.DeclineReasonInvalidCard, time.Time{}) }, domain.ErrInvalidPaymentTimestamp},
		{"expire from pending", func() (*domain.Payment, error) { return newPendingPayment(createdAt) }, func(p *domain.Payment) error { return p.MarkExpired(createdAt) }, domain.ErrInvalidPaymentStatus},
		{"expire with zero timestamp", func() (*domain.Payment, error) { return newAuthorizedPayment(createdAt) }, func(p *domain.Payment) error { return p.MarkExpired(time.Time{}) }, domain.ErrInvalidPaymentTimestamp},
		{"capture from declined", func() (*domain.Payment, error) {
			p, err := newPendingPayment(createdAt)
			if err != nil {
				return nil, err
			}
			return p, p.MarkDeclined(domain.DeclineReasonInvalidCard, createdAt)
		}, func(p *domain.Payment) error { return p.MarkCaptured("cap-1", createdAt) }, domain.ErrInvalidPaymentStatus},
		{"capture with blank id", func() (*domain.Payment, error) { return newAuthorizedPayment(createdAt) }, func(p *domain.Payment) error { return p.MarkCaptured(" ", createdAt) }, domain.ErrInvalidBankCaptureID},
		{"capture with zero timestamp", func() (*domain.Payment, error) { return newAuthorizedPayment(createdAt) }, func(p *domain.Payment) error { return p.MarkCaptured("cap-1", time.Time{}) }, domain.ErrInvalidPaymentTimestamp},
		{"void from declined", func() (*domain.Payment, error) {
			p, err := newPendingPayment(createdAt)
			if err != nil {
				return nil, err
			}
			return p, p.MarkDeclined(domain.DeclineReasonInvalidCard, createdAt)
		}, func(p *domain.Payment) error { return p.MarkVoided("void-1", createdAt) }, domain.ErrInvalidPaymentStatus},
		{"void with blank id", func() (*domain.Payment, error) { return newAuthorizedPayment(createdAt) }, func(p *domain.Payment) error { return p.MarkVoided(" ", createdAt) }, domain.ErrInvalidBankVoidID},
		{"void with zero timestamp", func() (*domain.Payment, error) { return newAuthorizedPayment(createdAt) }, func(p *domain.Payment) error { return p.MarkVoided("void-1", time.Time{}) }, domain.ErrInvalidPaymentTimestamp},
		{"refund from authorized", func() (*domain.Payment, error) { return newAuthorizedPayment(createdAt) }, func(p *domain.Payment) error { return p.MarkRefunded("ref-1", createdAt) }, domain.ErrInvalidPaymentStatus},
		{"refund with blank id", func() (*domain.Payment, error) { return newCapturedPayment(createdAt) }, func(p *domain.Payment) error { return p.MarkRefunded(" ", createdAt) }, domain.ErrInvalidBankRefundID},
		{"refund with zero timestamp", func() (*domain.Payment, error) { return newCapturedPayment(createdAt) }, func(p *domain.Payment) error { return p.MarkRefunded("ref-1", time.Time{}) }, domain.ErrInvalidPaymentTimestamp},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payment, err := tt.newPayment()
			require.NoError(t, err)
			before := snapshot(payment)

			assert.ErrorIs(t, tt.transition(payment), tt.wantErr)
			assertUnchanged(t, payment, before)
		})
	}
}

// An in-progress bank operation key is normalized and attached only to the matching lifecycle
// state; recording it does not itself advance the Payment or alter its timestamp.
func TestOperationKeySetters(t *testing.T) {
	tests := []struct {
		name       string
		newPayment func() (*domain.Payment, error)
		set        func(*domain.Payment, string) error
		key        func(*domain.Payment) string
		wantStatus domain.PaymentStatus
	}{
		{"capture", func() (*domain.Payment, error) { return newAuthorizedPayment(createdAt) }, (*domain.Payment).SetCaptureBankOperationKey, (*domain.Payment).CaptureBankOperationKey, domain.PaymentStatusAuthorized},
		{"void", func() (*domain.Payment, error) { return newAuthorizedPayment(createdAt) }, (*domain.Payment).SetVoidBankOperationKey, (*domain.Payment).VoidBankOperationKey, domain.PaymentStatusAuthorized},
		{"refund", func() (*domain.Payment, error) { return newCapturedPayment(createdAt) }, (*domain.Payment).SetRefundBankOperationKey, (*domain.Payment).RefundBankOperationKey, domain.PaymentStatusCaptured},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payment, err := tt.newPayment()
			require.NoError(t, err)
			before := snapshot(payment)

			require.NoError(t, tt.set(payment, " key-1 "))
			assert.Equal(t, "key-1", tt.key(payment))
			assert.Equal(t, tt.wantStatus, payment.Status())
			assert.Equal(t, before.updatedAt, payment.UpdatedAt())
		})
	}
}

// Operation keys cannot be attached outside their permitted lifecycle state or with blank
// values, and rejected attempts leave every Payment field intact.
func TestOperationKeySettersRejectInvalidValuesWithoutMutation(t *testing.T) {
	tests := []struct {
		name       string
		newPayment func() (*domain.Payment, error)
		set        func(*domain.Payment, string) error
		key        string
		wantErr    error
	}{
		{"capture invalid status", func() (*domain.Payment, error) { return newPendingPayment(createdAt) }, (*domain.Payment).SetCaptureBankOperationKey, "bok-capture", domain.ErrInvalidPaymentStatus},
		{"capture blank key", func() (*domain.Payment, error) { return newAuthorizedPayment(createdAt) }, (*domain.Payment).SetCaptureBankOperationKey, " ", domain.ErrInvalidBankOperationKey},
		{"void invalid status", func() (*domain.Payment, error) { return newPendingPayment(createdAt) }, (*domain.Payment).SetVoidBankOperationKey, "bok-void", domain.ErrInvalidPaymentStatus},
		{"void blank key", func() (*domain.Payment, error) { return newAuthorizedPayment(createdAt) }, (*domain.Payment).SetVoidBankOperationKey, " ", domain.ErrInvalidBankOperationKey},
		{"refund invalid status", func() (*domain.Payment, error) { return newAuthorizedPayment(createdAt) }, (*domain.Payment).SetRefundBankOperationKey, "bok-refund", domain.ErrInvalidPaymentStatus},
		{"refund blank key", func() (*domain.Payment, error) { return newCapturedPayment(createdAt) }, (*domain.Payment).SetRefundBankOperationKey, " ", domain.ErrInvalidBankOperationKey},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payment, err := tt.newPayment()
			require.NoError(t, err)
			before := snapshot(payment)

			assert.ErrorIs(t, tt.set(payment, tt.key), tt.wantErr)
			assertUnchanged(t, payment, before)
		})
	}
}

// Rehydration rejects persisted bank-operation fields that are missing for a terminal state or
// incompatible with its lifecycle, preventing corrupt Payments from entering the domain.
func TestLoadPaymentRejectsInvalidPersistedLifecycleFields(t *testing.T) {
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
		{"pending capture id", domain.PaymentStatusPending, "cap-1", "", "", "", "", "", domain.ErrInvalidBankCaptureID},
		{"expired capture operation key", domain.PaymentStatusExpired, "", "bok-capture", "", "", "", "", domain.ErrInvalidBankOperationKey},
		{"captured without capture id", domain.PaymentStatusCaptured, "", "bok-capture", "", "", "", "", domain.ErrInvalidBankCaptureID},
		{"captured without capture operation key", domain.PaymentStatusCaptured, "cap-1", "", "", "", "", "", domain.ErrInvalidBankOperationKey},
		{"voided capture id", domain.PaymentStatusVoided, "cap-1", "", "", "", "void-1", "bok-void", domain.ErrInvalidBankCaptureID},
		{"voided without void id", domain.PaymentStatusVoided, "", "", "", "", "", "bok-void", domain.ErrInvalidBankVoidID},
		{"voided without void operation key", domain.PaymentStatusVoided, "", "", "", "", "void-1", "", domain.ErrInvalidBankOperationKey},
		{"refunded without capture id", domain.PaymentStatusRefunded, "", "bok-capture", "ref-1", "bok-refund", "", "", domain.ErrInvalidBankCaptureID},
		{"refunded without capture operation key", domain.PaymentStatusRefunded, "cap-1", "", "ref-1", "bok-refund", "", "", domain.ErrInvalidBankOperationKey},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bankAuthorizationID := "auth-1"
			authorizationExpiresAt := createdAt.Add(time.Hour)
			if tt.status == domain.PaymentStatusPending {
				bankAuthorizationID = ""
				authorizationExpiresAt = time.Time{}
			}
			_, err := domain.LoadPayment(validPaymentID, "order-1", "customer-1", 1299, domain.CurrencyUSD, tt.status, bankAuthorizationID, authorizationExpiresAt, "bok-auth", "fingerprint-1", tt.bankCaptureID, tt.captureBankOperationKey, tt.bankRefundID, tt.refundBankOperationKey, tt.bankVoidID, tt.voidBankOperationKey, "", createdAt, createdAt)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

// Pending Payment creation validates every required identity, retry field, and timestamp before
// the aggregate is initialized.
func TestNewPendingPaymentRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name             string
		id               domain.PaymentID
		orderID          string
		customerID       string
		amountCents      int64
		bankOperationKey string
		fingerprint      string
		now              time.Time
		wantErr          error
	}{
		{"payment id without prefix", "550e8400-e29b-41d4-a716-446655440000", "order-1", "customer-1", 100, "bok-auth", "fingerprint-1", createdAt, domain.ErrInvalidPaymentID},
		{"payment id without uuid", "pay_123", "order-1", "customer-1", 100, "bok-auth", "fingerprint-1", createdAt, domain.ErrInvalidPaymentID},
		{"payment id with undashed uuid", "pay_550e8400e29b41d4a716446655440000", "order-1", "customer-1", 100, "bok-auth", "fingerprint-1", createdAt, domain.ErrInvalidPaymentID},
		{"payment id with urn uuid", "pay_urn:uuid:550e8400-e29b-41d4-a716-446655440000", "order-1", "customer-1", 100, "bok-auth", "fingerprint-1", createdAt, domain.ErrInvalidPaymentID},
		{"order id", validPaymentID, " ", "customer-1", 100, "bok-auth", "fingerprint-1", createdAt, domain.ErrInvalidOrderID},
		{"customer id", validPaymentID, "order-1", " ", 100, "bok-auth", "fingerprint-1", createdAt, domain.ErrInvalidCustomerID},
		{"amount", validPaymentID, "order-1", "customer-1", 0, "bok-auth", "fingerprint-1", createdAt, domain.ErrInvalidAmount},
		{"bank operation key", validPaymentID, "order-1", "customer-1", 100, " ", "fingerprint-1", createdAt, domain.ErrInvalidBankOperationKey},
		{"card fingerprint", validPaymentID, "order-1", "customer-1", 100, "bok-auth", " ", createdAt, domain.ErrInvalidAuthorizationCardFingerprint},
		{"creation timestamp", validPaymentID, "order-1", "customer-1", 100, "bok-auth", "fingerprint-1", time.Time{}, domain.ErrInvalidPaymentTimestamp},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := domain.NewPendingPayment(tt.id, tt.orderID, tt.customerID, tt.amountCents, tt.bankOperationKey, tt.fingerprint, tt.now)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}
