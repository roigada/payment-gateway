package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/roigada/payment-gateway/internal/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestPaymentStorePersistsAuthorizedPayment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment, err := domain.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440000",
		now.Add(time.Hour),
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		now,
	)
	require.NoError(t, err)

	insertPaymentFixture(t, db, payment)

	saved, err := store.FindByID(ctx, payment.ID())
	require.NoError(t, err)
	assert.Equal(t, payment.ID(), saved.ID())
	assert.Equal(t, "order-1", saved.OrderID())
	assert.Equal(t, "customer-1", saved.CustomerID())
	assert.Equal(t, int64(1299), saved.AmountCents())
	assert.Equal(t, domain.CurrencyUSD, saved.Currency())
	assert.Equal(t, domain.PaymentStatusAuthorized, saved.Status())
	assert.Equal(t, "auth_550e8400-e29b-41d4-a716-446655440000", saved.BankAuthorizationID())
	assert.True(t, saved.AuthorizationExpiresAt().Equal(now.Add(time.Hour)), "authorization_expires_at should round-trip as the same instant")
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440001", saved.AuthorizationBankOperationKey())
	assert.Equal(t, "fingerprint-1", saved.AuthorizationCardFingerprint())
	assert.True(t, saved.CreatedAt().Equal(now), "created_at should round-trip as the same instant")
	assert.True(t, saved.UpdatedAt().Equal(now), "updated_at should round-trip as the same instant")

	_, err = store.FindByID(ctx, domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440999"))
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorNotFound))
}

func TestPaymentStorePersistsDeclinedPayment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment, err := domain.NewDeclinedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		domain.DeclineReasonExpiredCard,
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		now,
	)
	require.NoError(t, err)

	insertPaymentFixture(t, db, payment)

	saved, err := store.FindByID(ctx, payment.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusDeclined, saved.Status())
	assert.Equal(t, domain.DeclineReasonExpiredCard, saved.DeclineReason())
	assert.Empty(t, saved.BankAuthorizationID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440001", saved.AuthorizationBankOperationKey())
	assert.Equal(t, "fingerprint-1", saved.AuthorizationCardFingerprint())
	assert.True(t, saved.CreatedAt().Equal(now), "created_at should round-trip as the same instant")
	assert.True(t, saved.UpdatedAt().Equal(now), "updated_at should round-trip as the same instant")
}

func TestPaymentStoreUpdatesPendingAuthorizationResult(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment, err := domain.NewPendingPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		now,
	)
	require.NoError(t, err)
	insertPaymentFixture(t, db, payment)

	require.NoError(t, payment.MarkAuthorized("auth_550e8400-e29b-41d4-a716-446655440000", now.Add(time.Hour), now.Add(time.Minute)))
	updatePaymentFixture(t, db, payment, domain.PaymentStatusPending)

	saved, err := store.FindByID(ctx, payment.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusAuthorized, saved.Status())
	assert.Equal(t, "auth_550e8400-e29b-41d4-a716-446655440000", saved.BankAuthorizationID())
	assert.Equal(t, "fingerprint-1", saved.AuthorizationCardFingerprint())
	assert.True(t, saved.CreatedAt().Equal(now), "created_at should stay as the original instant")
	assert.True(t, saved.UpdatedAt().Equal(now.Add(time.Minute)), "updated_at should round-trip as the transition instant")
}

func TestPaymentStoreUpdatesVoidedPayment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment, err := domain.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440000",
		now.Add(time.Hour),
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		now,
	)
	require.NoError(t, err)
	insertPaymentFixture(t, db, payment)

	voidedAt := now.Add(time.Minute)
	require.NoError(t, payment.MarkVoided(
		"void_550e8400-e29b-41d4-a716-446655440003",
		"bok_550e8400-e29b-41d4-a716-446655440002",
		voidedAt,
	))
	updatePaymentFixture(t, db, payment, domain.PaymentStatusAuthorized)

	saved, err := store.FindByID(ctx, payment.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusVoided, saved.Status())
	assert.Equal(t, "void_550e8400-e29b-41d4-a716-446655440003", saved.BankVoidID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440002", saved.VoidBankOperationKey())
	assert.True(t, saved.UpdatedAt().Equal(voidedAt), "updated_at should round-trip as the void transition instant")
}

func TestPaymentStoreSavesVoidBankOperationKeyWithoutChangingStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment, err := domain.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440000",
		now.Add(time.Hour),
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		now,
	)
	require.NoError(t, err)
	insertPaymentFixture(t, db, payment)
	require.NoError(t, payment.SetVoidBankOperationKey("bok_550e8400-e29b-41d4-a716-446655440002"))

	saveBankOperationKeyFixture(t, db, payment, app.BankOperationKeyVoid)

	saved, err := store.FindByID(ctx, payment.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusAuthorized, saved.Status())
	assert.Empty(t, saved.BankVoidID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440002", saved.VoidBankOperationKey())
	assert.True(t, saved.UpdatedAt().Equal(now), "updated_at should stay unchanged")
}

func TestPaymentStoreSearchesPaymentsByFiltersNewestFirstAndCapped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	base := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	for i := 0; i < 105; i++ {
		payment := newStorePayment(t, i, "order-1", "customer-1", domain.PaymentStatusAuthorized, base.Add(time.Duration(i)*time.Minute))
		insertPaymentFixture(t, db, payment)
	}
	otherOrder := newStorePayment(t, 105, "order-2", "customer-1", domain.PaymentStatusAuthorized, base.Add(105*time.Minute))
	insertPaymentFixture(t, db, otherOrder)
	declined := newStorePayment(t, 106, "order-1", "customer-1", domain.PaymentStatusDeclined, base.Add(106*time.Minute))
	insertPaymentFixture(t, db, declined)

	authorizedQuery, err := app.NewSearchPaymentsQuery("order-1", "customer-1", "authorized")
	require.NoError(t, err)
	authorized, err := store.Search(ctx, authorizedQuery)

	require.NoError(t, err)
	require.Len(t, authorized, 100)
	assert.Equal(t, domain.PaymentID("pay_00000000-0000-4000-8000-000000000104"), authorized[0].ID())
	assert.Equal(t, domain.PaymentID("pay_00000000-0000-4000-8000-000000000005"), authorized[99].ID())

	byCustomerQuery, err := app.NewSearchPaymentsQuery("", "customer-1", "")
	require.NoError(t, err)
	byCustomer, err := store.Search(ctx, byCustomerQuery)

	require.NoError(t, err)
	require.Len(t, byCustomer, 100)
	assert.Equal(t, declined.ID(), byCustomer[0].ID())
	assert.Equal(t, otherOrder.ID(), byCustomer[1].ID())
}

func TestPaymentStoreUpdatesCapturedPayment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	authorizedAt := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment, err := domain.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440000",
		authorizedAt.Add(time.Hour),
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		authorizedAt,
	)
	require.NoError(t, err)
	insertPaymentFixture(t, db, payment)

	capturedAt := time.Date(2026, 6, 19, 10, 45, 0, 0, time.UTC)
	require.NoError(t, payment.Capture(
		"cap_550e8400-e29b-41d4-a716-446655440002",
		"bok_550e8400-e29b-41d4-a716-446655440003",
		capturedAt,
	))
	updatePaymentFixture(t, db, payment, domain.PaymentStatusAuthorized)

	saved, err := store.FindByID(ctx, payment.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusCaptured, saved.Status())
	assert.Equal(t, "auth_550e8400-e29b-41d4-a716-446655440000", saved.BankAuthorizationID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440001", saved.AuthorizationBankOperationKey())
	assert.Equal(t, "cap_550e8400-e29b-41d4-a716-446655440002", saved.BankCaptureID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440003", saved.CaptureBankOperationKey())
	assert.True(t, saved.CreatedAt().Equal(authorizedAt), "created_at should be unchanged")
	assert.True(t, saved.UpdatedAt().Equal(capturedAt), "updated_at should be the capture instant")
}

func TestPaymentStoreSavesCaptureBankOperationKeyWithoutChangingStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	authorizedAt := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment, err := domain.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440000",
		authorizedAt.Add(time.Hour),
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		authorizedAt,
	)
	require.NoError(t, err)
	insertPaymentFixture(t, db, payment)
	require.NoError(t, payment.SetCaptureBankOperationKey("bok_550e8400-e29b-41d4-a716-446655440002"))

	saveBankOperationKeyFixture(t, db, payment, app.BankOperationKeyCapture)

	saved, err := store.FindByID(ctx, payment.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusAuthorized, saved.Status())
	assert.Empty(t, saved.BankCaptureID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440002", saved.CaptureBankOperationKey())
	assert.True(t, saved.UpdatedAt().Equal(authorizedAt), "updated_at should stay unchanged")
}

func TestPaymentStoreUpdatesRefundedPayment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	authorizedAt := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment, err := domain.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440000",
		authorizedAt.Add(time.Hour),
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		authorizedAt,
	)
	require.NoError(t, err)
	capturedAt := time.Date(2026, 6, 19, 10, 45, 0, 0, time.UTC)
	require.NoError(t, payment.Capture(
		"cap_550e8400-e29b-41d4-a716-446655440002",
		"bok_550e8400-e29b-41d4-a716-446655440003",
		capturedAt,
	))
	insertPaymentFixture(t, db, payment)

	refundedAt := time.Date(2026, 6, 19, 11, 0, 0, 0, time.UTC)
	require.NoError(t, payment.Refund(
		"ref_550e8400-e29b-41d4-a716-446655440004",
		"bok_550e8400-e29b-41d4-a716-446655440005",
		refundedAt,
	))
	updatePaymentFixture(t, db, payment, domain.PaymentStatusCaptured)

	saved, err := store.FindByID(ctx, payment.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusRefunded, saved.Status())
	assert.Equal(t, "cap_550e8400-e29b-41d4-a716-446655440002", saved.BankCaptureID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440003", saved.CaptureBankOperationKey())
	assert.Equal(t, "ref_550e8400-e29b-41d4-a716-446655440004", saved.BankRefundID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440005", saved.RefundBankOperationKey())
	assert.True(t, saved.CreatedAt().Equal(authorizedAt), "created_at should be unchanged")
	assert.True(t, saved.UpdatedAt().Equal(refundedAt), "updated_at should be the refund instant")
}

func TestPaymentStoreSavesRefundBankOperationKeyWithoutChangingStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	authorizedAt := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment, err := domain.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440000",
		authorizedAt.Add(time.Hour),
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		authorizedAt,
	)
	require.NoError(t, err)
	capturedAt := time.Date(2026, 6, 19, 10, 45, 0, 0, time.UTC)
	require.NoError(t, payment.Capture(
		"cap_550e8400-e29b-41d4-a716-446655440002",
		"bok_550e8400-e29b-41d4-a716-446655440003",
		capturedAt,
	))
	insertPaymentFixture(t, db, payment)
	require.NoError(t, payment.SetRefundBankOperationKey("bok_550e8400-e29b-41d4-a716-446655440004"))

	saveBankOperationKeyFixture(t, db, payment, app.BankOperationKeyRefund)

	saved, err := store.FindByID(ctx, payment.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusCaptured, saved.Status())
	assert.Empty(t, saved.BankRefundID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440004", saved.RefundBankOperationKey())
	assert.True(t, saved.UpdatedAt().Equal(capturedAt), "updated_at should stay unchanged")
}

func TestPaymentStorePersistsCompletedDeclinedResult(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment, err := domain.NewPendingPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		now,
	)
	require.NoError(t, err)
	result := app.PaymentCommandResult{
		HTTPStatus: 201,
		Payment: app.PaymentResult{
			ID:            "pay_550e8400-e29b-41d4-a716-446655440000",
			OrderID:       "order-1",
			CustomerID:    "customer-1",
			AmountCents:   1299,
			Currency:      "USD",
			Status:        "declined",
			DeclineReason: "invalid_card",
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}
	request := app.NewAuthorizationStartClaim("public-key-1", "fingerprint-1", payment)

	claimed, err := store.ClaimPaymentCommand(ctx, request)
	require.NoError(t, err)
	assert.Same(t, payment, claimed.Payment())
	require.NoError(t, payment.MarkDeclined(domain.DeclineReasonInvalidCard, now))
	require.NoError(t, store.CompletePaymentCommand(ctx, claimed, result))

	saved, err := store.ClaimPaymentCommand(ctx, request)
	require.NoError(t, err)
	replayed, ok := saved.ReplayResult()
	require.True(t, ok)
	assert.Equal(t, result.HTTPStatus, replayed.HTTPStatus)
	assert.Equal(t, result.Payment.ID, replayed.Payment.ID)
	assert.Equal(t, result.Payment.OrderID, replayed.Payment.OrderID)
	assert.Equal(t, result.Payment.CustomerID, replayed.Payment.CustomerID)
	assert.Equal(t, result.Payment.AmountCents, replayed.Payment.AmountCents)
	assert.Equal(t, result.Payment.Currency, replayed.Payment.Currency)
	assert.Equal(t, result.Payment.Status, replayed.Payment.Status)
	assert.Equal(t, result.Payment.DeclineReason, replayed.Payment.DeclineReason)
	assert.True(t, replayed.Payment.CreatedAt.Equal(now), "created_at should round-trip as the same instant")
	assert.True(t, replayed.Payment.UpdatedAt.Equal(now), "updated_at should round-trip as the same instant")

	missingPayment, err := domain.NewPendingPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440001"),
		"order-2",
		"customer-1",
		1299,
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		now,
	)
	require.NoError(t, err)
	missing, err := store.ClaimPaymentCommand(ctx, app.NewAuthorizationStartClaim("missing-key", "fingerprint-1", missingPayment))
	require.NoError(t, err)
	assert.Same(t, missingPayment, missing.Payment())
}

func TestPaymentStoreReturnsInProgressErrorForDuplicateClaim(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()

	payment := newStorePayment(t, 1, "order-1", "customer-1", domain.PaymentStatusAuthorized, time.Now())
	insertPaymentFixture(t, db, payment)
	request := app.NewCaptureClaim("public-key-1", "fingerprint-1", payment.ID(), "bok_550e8400-e29b-41d4-a716-446655440010", time.Time{})
	claim, err := store.ClaimPaymentCommand(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, claim.Payment())

	_, err = store.ClaimPaymentCommand(ctx, request)
	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyInProgress))

	require.NoError(t, store.ReleasePaymentCommand(ctx, claim))
	reclaimed, err := store.ClaimPaymentCommand(ctx, request)
	require.NoError(t, err)
	assert.NotNil(t, reclaimed.Payment())
}

func TestPaymentStoreRejectsPaymentCommandClaimPreconditionFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	tests := []struct {
		name    string
		payment *domain.Payment
		request func(*domain.Payment) app.PaymentCommandClaimRequest
	}{
		{
			name:    "retry authorization requires pending payment",
			payment: newStorePayment(t, 1, "order-1", "customer-1", domain.PaymentStatusAuthorized, time.Now()),
			request: func(payment *domain.Payment) app.PaymentCommandClaimRequest {
				return app.NewAuthorizationRetryClaim("retry-key-1", "fingerprint-1", payment.ID(), payment.AuthorizationCardFingerprint())
			},
		},
		{
			name:    "retry authorization requires matching authorization card fingerprint",
			payment: newStorePayment(t, 2, "order-1", "customer-1", domain.PaymentStatusPending, time.Now()),
			request: func(payment *domain.Payment) app.PaymentCommandClaimRequest {
				return app.NewAuthorizationRetryClaim("retry-key-2", "fingerprint-2", payment.ID(), "different-fingerprint")
			},
		},
		{
			name:    "capture requires authorized payment",
			payment: newStorePayment(t, 3, "order-1", "customer-1", domain.PaymentStatusPending, time.Now()),
			request: func(payment *domain.Payment) app.PaymentCommandClaimRequest {
				return app.NewCaptureClaim("capture-key-1", "fingerprint-3", payment.ID(), "bok_00000000-0000-4000-8000-000000000103", time.Time{})
			},
		},
		{
			name:    "void requires authorized payment",
			payment: newStorePayment(t, 4, "order-1", "customer-1", domain.PaymentStatusCaptured, time.Now()),
			request: func(payment *domain.Payment) app.PaymentCommandClaimRequest {
				return app.NewVoidClaim("void-key-1", "fingerprint-4", payment.ID(), "bok_00000000-0000-4000-8000-000000000104", time.Time{})
			},
		},
		{
			name:    "refund requires captured payment",
			payment: newStorePayment(t, 5, "order-1", "customer-1", domain.PaymentStatusAuthorized, time.Now()),
			request: func(payment *domain.Payment) app.PaymentCommandClaimRequest {
				return app.NewRefundClaim("refund-key-1", "fingerprint-5", payment.ID(), "bok_00000000-0000-4000-8000-000000000105")
			},
		},
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			insertPaymentFixture(t, db, tt.payment)

			_, err := store.ClaimPaymentCommand(ctx, tt.request(tt.payment))

			require.Error(t, err)
			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidStatusConflict))
		})
	}
}

func TestPaymentStoreCompletionRollsBackAuthorizationTransitionWhenIdempotencyCompletionFails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment, err := domain.NewPendingPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"bok_550e8400-e29b-41d4-a716-446655440000",
		"fingerprint-1",
		now,
	)
	require.NoError(t, err)
	request := app.NewAuthorizationStartClaim("public-key-1", "fingerprint-1", payment)
	claim, err := store.ClaimPaymentCommand(ctx, request)
	require.NoError(t, err)
	require.Same(t, payment, claim.Payment())
	require.NoError(t, payment.MarkAuthorized("auth_550e8400-e29b-41d4-a716-446655440000", now.Add(time.Hour), now.Add(time.Minute)))
	_, err = db.ExecContext(ctx, `DELETE FROM idempotency_records WHERE operation = $1 AND key = $2`, "authorize_payment", "public-key-1")
	require.NoError(t, err)

	err = store.CompletePaymentCommand(ctx, claim, newStorePaymentCommandResult(payment, 201))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	saved, err := store.FindByID(ctx, payment.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusPending, saved.Status())
	assert.Empty(t, saved.BankAuthorizationID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440000", saved.AuthorizationBankOperationKey())
}

func TestPaymentStoreCompletionRollsBackCaptureTransitionWhenIdempotencyCompletionFails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment := newStorePayment(t, 1, "order-1", "customer-1", domain.PaymentStatusAuthorized, now)
	insertPaymentFixture(t, db, payment)
	request := app.NewCaptureClaim("public-capture-key-1", "fingerprint-1", payment.ID(), "bok_550e8400-e29b-41d4-a716-446655440010", time.Time{})
	claim, err := store.ClaimPaymentCommand(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, claim.Payment())
	require.NoError(t, claim.Payment().Capture("cap_550e8400-e29b-41d4-a716-446655440000", claim.Payment().CaptureBankOperationKey(), now.Add(time.Minute)))
	_, err = db.ExecContext(ctx, `DELETE FROM idempotency_records WHERE operation = $1 AND key = $2`, "capture_payment", "public-capture-key-1")
	require.NoError(t, err)

	err = store.CompletePaymentCommand(ctx, claim, newStorePaymentCommandResult(claim.Payment(), 200))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	saved, err := store.FindByID(ctx, payment.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusAuthorized, saved.Status())
	assert.Empty(t, saved.BankCaptureID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440010", saved.CaptureBankOperationKey())
	claimStatus, err := store.ClaimPaymentCommand(ctx, request)
	require.NoError(t, err)
	assert.NotNil(t, claimStatus.Payment())
}

func newStorePayment(t *testing.T, sequence int, orderID string, customerID string, status domain.PaymentStatus, now time.Time) *domain.Payment {
	t.Helper()

	id := domain.PaymentID(fmt.Sprintf("pay_00000000-0000-4000-8000-%012d", sequence))
	bankOperationKey := fmt.Sprintf("bok_00000000-0000-4000-8000-%012d", sequence)
	cardFingerprint := fmt.Sprintf("fingerprint-%d", sequence)
	switch status {
	case domain.PaymentStatusPending:
		payment, err := domain.NewPendingPayment(
			id,
			orderID,
			customerID,
			1299,
			bankOperationKey,
			cardFingerprint,
			now,
		)
		require.NoError(t, err)
		return payment
	case domain.PaymentStatusAuthorized:
		payment, err := domain.NewAuthorizedPayment(
			id,
			orderID,
			customerID,
			1299,
			fmt.Sprintf("auth_00000000-0000-4000-8000-%012d", sequence),
			now.Add(time.Hour),
			bankOperationKey,
			cardFingerprint,
			now,
		)
		require.NoError(t, err)
		return payment
	case domain.PaymentStatusCaptured:
		payment := newStorePayment(t, sequence, orderID, customerID, domain.PaymentStatusAuthorized, now)
		require.NoError(t, payment.Capture(
			fmt.Sprintf("cap_00000000-0000-4000-8000-%012d", sequence),
			fmt.Sprintf("bok_00000000-0000-4000-8000-%012d", sequence+1000),
			now.Add(time.Minute),
		))
		return payment
	case domain.PaymentStatusVoided:
		payment := newStorePayment(t, sequence, orderID, customerID, domain.PaymentStatusAuthorized, now)
		require.NoError(t, payment.MarkVoided(
			fmt.Sprintf("void_00000000-0000-4000-8000-%012d", sequence),
			fmt.Sprintf("bok_00000000-0000-4000-8000-%012d", sequence+1000),
			now.Add(time.Minute),
		))
		return payment
	case domain.PaymentStatusRefunded:
		payment := newStorePayment(t, sequence, orderID, customerID, domain.PaymentStatusCaptured, now)
		require.NoError(t, payment.Refund(
			fmt.Sprintf("ref_00000000-0000-4000-8000-%012d", sequence),
			fmt.Sprintf("bok_00000000-0000-4000-8000-%012d", sequence+2000),
			now.Add(2*time.Minute),
		))
		return payment
	case domain.PaymentStatusDeclined:
		payment, err := domain.NewDeclinedPayment(
			id,
			orderID,
			customerID,
			1299,
			domain.DeclineReasonInvalidCard,
			bankOperationKey,
			cardFingerprint,
			now,
		)
		require.NoError(t, err)
		return payment
	default:
		t.Fatalf("unsupported status %q", status)
		return nil
	}
}

func newStorePaymentCommandResult(payment *domain.Payment, httpStatus int) app.PaymentCommandResult {
	return app.PaymentCommandResult{
		HTTPStatus: httpStatus,
		Payment: app.PaymentResult{
			ID:                     string(payment.ID()),
			OrderID:                payment.OrderID(),
			CustomerID:             payment.CustomerID(),
			AmountCents:            payment.AmountCents(),
			Currency:               payment.Currency(),
			Status:                 string(payment.Status()),
			DeclineReason:          string(payment.DeclineReason()),
			AuthorizationExpiresAt: payment.AuthorizationExpiresAt(),
			CreatedAt:              payment.CreatedAt(),
			UpdatedAt:              payment.UpdatedAt(),
		},
	}
}

func TestPaymentStoreReturnsConflictWhenCompletingUnclaimedCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment, err := domain.NewPendingPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		now,
	)
	require.NoError(t, err)
	insertPaymentFixture(t, db, payment)
	require.NoError(t, payment.MarkAuthorized("auth_550e8400-e29b-41d4-a716-446655440000", now.Add(time.Hour), now))

	request := app.NewAuthorizationStartClaim("public-key-1", "fingerprint-1", payment)
	claim := app.NewClaimedPaymentCommand(request, payment)
	err = store.CompletePaymentCommand(ctx, claim, newStorePaymentCommandResult(payment, 201))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	saved, err := store.FindByID(ctx, payment.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusPending, saved.Status())
}

func insertPaymentFixture(t *testing.T, db *sql.DB, payment *domain.Payment) {
	t.Helper()

	_, err := db.ExecContext(
		context.Background(),
		`INSERT INTO payments (
		     id,
		     order_id,
		     customer_id,
		     amount_cents,
		     currency,
		     status,
		     bank_authorization_id,
		     authorization_expires_at,
		     authorization_bank_operation_key,
		     authorization_card_fingerprint,
		     bank_capture_id,
		     capture_bank_operation_key,
		     bank_refund_id,
		     refund_bank_operation_key,
		     bank_void_id,
		     void_bank_operation_key,
		     decline_reason,
		     created_at,
		     updated_at
		 )
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`,
		payment.ID(),
		payment.OrderID(),
		payment.CustomerID(),
		payment.AmountCents(),
		payment.Currency(),
		payment.Status(),
		nullableString(payment.BankAuthorizationID()),
		nullableTime(payment.AuthorizationExpiresAt()),
		payment.AuthorizationBankOperationKey(),
		payment.AuthorizationCardFingerprint(),
		nullableString(payment.BankCaptureID()),
		nullableString(payment.CaptureBankOperationKey()),
		nullableString(payment.BankRefundID()),
		nullableString(payment.RefundBankOperationKey()),
		nullableString(payment.BankVoidID()),
		nullableString(payment.VoidBankOperationKey()),
		nullableString(string(payment.DeclineReason())),
		payment.CreatedAt(),
		payment.UpdatedAt(),
	)
	require.NoError(t, err)
}

func updatePaymentFixture(t *testing.T, db *sql.DB, payment *domain.Payment, expectedStatus domain.PaymentStatus) {
	t.Helper()

	result, err := db.ExecContext(
		context.Background(),
		`UPDATE payments
		    SET status = $2,
		        bank_authorization_id = $3,
		        authorization_expires_at = $4,
		        authorization_bank_operation_key = $5,
		        authorization_card_fingerprint = $6,
		        bank_capture_id = $7,
		        capture_bank_operation_key = $8,
		        bank_refund_id = $9,
		        refund_bank_operation_key = $10,
		        bank_void_id = $11,
		        void_bank_operation_key = $12,
		        decline_reason = $13,
		        updated_at = $14
		  WHERE id = $1
		    AND status = $15`,
		payment.ID(),
		payment.Status(),
		nullableString(payment.BankAuthorizationID()),
		nullableTime(payment.AuthorizationExpiresAt()),
		payment.AuthorizationBankOperationKey(),
		payment.AuthorizationCardFingerprint(),
		nullableString(payment.BankCaptureID()),
		nullableString(payment.CaptureBankOperationKey()),
		nullableString(payment.BankRefundID()),
		nullableString(payment.RefundBankOperationKey()),
		nullableString(payment.BankVoidID()),
		nullableString(payment.VoidBankOperationKey()),
		nullableString(string(payment.DeclineReason())),
		payment.UpdatedAt(),
		expectedStatus,
	)
	require.NoError(t, err)
	rowsAffected, err := result.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), rowsAffected)
}

func saveBankOperationKeyFixture(t *testing.T, db *sql.DB, payment *domain.Payment, operation app.BankOperationKeyKind) {
	t.Helper()

	var (
		column         string
		value          any
		expectedStatus domain.PaymentStatus
	)
	switch operation {
	case app.BankOperationKeyCapture:
		column = "capture_bank_operation_key"
		value = nullableString(payment.CaptureBankOperationKey())
		expectedStatus = domain.PaymentStatusAuthorized
	case app.BankOperationKeyVoid:
		column = "void_bank_operation_key"
		value = nullableString(payment.VoidBankOperationKey())
		expectedStatus = domain.PaymentStatusAuthorized
	case app.BankOperationKeyRefund:
		column = "refund_bank_operation_key"
		value = nullableString(payment.RefundBankOperationKey())
		expectedStatus = domain.PaymentStatusCaptured
	default:
		t.Fatalf("unsupported bank operation key kind %q", operation)
	}

	result, err := db.ExecContext(
		context.Background(),
		`UPDATE payments SET `+column+` = $2 WHERE id = $1 AND status = $3`,
		payment.ID(),
		value,
		expectedStatus,
	)
	require.NoError(t, err)
	rowsAffected, err := result.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), rowsAffected)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func newTestDatabase(t *testing.T) *sql.DB {
	t.Helper()

	ctx := context.Background()
	container, err := tcpostgres.Run(
		ctx,
		"postgres:17-alpine",
		tcpostgres.WithDatabase("payment_gateway_test"),
		tcpostgres.WithUsername("payment_gateway"),
		tcpostgres.WithPassword("payment_gateway"),
		tcpostgres.WithInitScripts(
			filepath.Join("..", "..", "migrations", "000001_create_payments.up.sql"),
		),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	testcontainers.CleanupContainer(t, container)

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := postgres.Connect(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})

	return db
}
