package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/roigada/payment-gateway/internal/postgres"
	"github.com/roigada/payment-gateway/internal/testsupport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const testIdempotencyClaimStuckAfter = 5 * time.Minute

var testNonExpiringBusinessTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

func TestPaymentStoreFindByIDRejectsMissingBusinessTime(t *testing.T) {
	store := postgres.NewPaymentStore(nil)

	_, err := store.FindByID(context.Background(), domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"), time.Time{})

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInternal))
}

func TestPaymentStoreClaimExistingPaymentCommandRejectsMissingBusinessTime(t *testing.T) {
	store := postgres.NewPaymentStore(nil)
	request := app.NewCaptureClaimRequest(
		"key-1",
		"fingerprint-1",
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"bok_550e8400-e29b-41d4-a716-446655440001",
		time.Time{},
		testIdempotencyClaimStuckAfter,
	)

	_, err := store.ClaimExistingPaymentCommand(context.Background(), request)

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInternal))
}

func TestPaymentStoreClaimAuthorizationStartRejectsMissingBusinessTime(t *testing.T) {
	store := postgres.NewPaymentStore(nil)

	_, err := store.ClaimAuthorizationStart(context.Background(), app.AuthorizationStartClaimRequest{})

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInternal))
}

func TestPaymentStoreRejectsMissingBusinessTimeForSearchCompletionAndCleanup(t *testing.T) {
	store := postgres.NewPaymentStore(nil)
	query, err := app.NewSearchPaymentsQuery("order-1", "", "")
	require.NoError(t, err)

	_, err = store.Search(context.Background(), query, time.Time{})
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInternal))

	err = store.CompletePaymentCommand(context.Background(), app.PaymentCommandClaim{}, app.PaymentCommandResult{}, time.Time{})
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInternal))

	_, err = store.CleanupCompletedIdempotencyRecords(context.Background(), time.Time{})
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInternal))
}

func TestPaymentStorePersistsAuthorizedPayment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment, err := testsupport.NewAuthorizedPayment(
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

	saved, err := store.FindByID(ctx, payment.ID(), testNonExpiringBusinessTime)
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

	_, err = store.FindByID(ctx, domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440999"), testNonExpiringBusinessTime)
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
	payment, err := testsupport.NewDeclinedPayment(
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

	saved, err := store.FindByID(ctx, payment.ID(), testNonExpiringBusinessTime)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusDeclined, saved.Status())
	assert.Equal(t, domain.DeclineReasonExpiredCard, saved.DeclineReason())
	assert.Empty(t, saved.BankAuthorizationID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440001", saved.AuthorizationBankOperationKey())
	assert.Equal(t, "fingerprint-1", saved.AuthorizationCardFingerprint())
	assert.True(t, saved.CreatedAt().Equal(now), "created_at should round-trip as the same instant")
	assert.True(t, saved.UpdatedAt().Equal(now), "updated_at should round-trip as the same instant")
}

func TestPaymentStoreReportsAggregatePendingMetricsWithoutChangingPaymentStatuses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Now().UTC()
	pending, err := domain.NewPendingPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-pending",
		"customer-pending",
		1299,
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-pending",
		now.Add(-6*time.Minute),
	)
	require.NoError(t, err)
	declined, err := testsupport.NewDeclinedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440002"),
		"order-declined",
		"customer-declined",
		1299,
		domain.DeclineReasonExpiredCard,
		"bok_550e8400-e29b-41d4-a716-446655440003",
		"fingerprint-declined",
		now.Add(-10*time.Minute),
	)
	require.NoError(t, err)
	insertPaymentFixture(t, db, pending)
	insertPaymentFixture(t, db, declined)

	count, oldestAgeSeconds, err := store.PendingPaymentMetrics(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	assert.GreaterOrEqual(t, oldestAgeSeconds, float64(6*60))

	saved, err := store.FindByID(ctx, pending.ID(), testNonExpiringBusinessTime)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusPending, saved.Status())
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

	saved, err := store.FindByID(ctx, payment.ID(), testNonExpiringBusinessTime)
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
	payment, err := testsupport.NewAuthorizedPayment(
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

	saved, err := store.FindByID(ctx, payment.ID(), testNonExpiringBusinessTime)
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
	payment, err := testsupport.NewAuthorizedPayment(
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

	saved, err := store.FindByID(ctx, payment.ID(), testNonExpiringBusinessTime)
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
	authorized, err := store.Search(ctx, authorizedQuery, testNonExpiringBusinessTime)

	require.NoError(t, err)
	require.Len(t, authorized, 100)
	assert.Equal(t, domain.PaymentID("pay_00000000-0000-4000-8000-000000000104"), authorized[0].ID())
	assert.Equal(t, domain.PaymentID("pay_00000000-0000-4000-8000-000000000005"), authorized[99].ID())

	byCustomerQuery, err := app.NewSearchPaymentsQuery("", "customer-1", "")
	require.NoError(t, err)
	byCustomer, err := store.Search(ctx, byCustomerQuery, testNonExpiringBusinessTime)

	require.NoError(t, err)
	require.Len(t, byCustomer, 100)
	assert.Equal(t, declined.ID(), byCustomer[0].ID())
	assert.Equal(t, otherOrder.ID(), byCustomer[1].ID())
}

func TestPaymentStoreFindByIDPersistsExpiredStatusWhenAuthorizationExpires(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	authorizedAt := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment := newStorePayment(t, 1, "order-1", "customer-1", domain.PaymentStatusAuthorized, authorizedAt)
	insertPaymentFixture(t, db, payment)

	saved, err := store.FindByID(ctx, payment.ID(), payment.AuthorizationExpiresAt())
	require.NoError(t, err)

	assert.Equal(t, domain.PaymentStatusExpired, saved.Status())
	assert.True(t, saved.UpdatedAt().Equal(payment.AuthorizationExpiresAt()), "updated_at should be the read expiration instant")

	persisted, err := store.FindByID(ctx, payment.ID(), testNonExpiringBusinessTime)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusExpired, persisted.Status())
	assert.True(t, persisted.UpdatedAt().Equal(payment.AuthorizationExpiresAt()), "expired status should be persisted by the read")
}

func TestPaymentStoreSearchRefreshesExpiredAuthorizationsBeforeStatusFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	base := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	expiredBeforeRead := newStorePayment(t, 1, "order-1", "customer-1", domain.PaymentStatusAuthorized, base)
	stillAuthorized := newStorePayment(t, 2, "order-2", "customer-1", domain.PaymentStatusAuthorized, base.Add(30*time.Minute))
	insertPaymentFixture(t, db, expiredBeforeRead)
	insertPaymentFixture(t, db, stillAuthorized)
	readNow := expiredBeforeRead.AuthorizationExpiresAt()

	expiredQuery, err := app.NewSearchPaymentsQuery("order-1", "customer-1", "expired")
	require.NoError(t, err)
	expired, err := store.Search(ctx, expiredQuery, readNow)
	require.NoError(t, err)
	require.Len(t, expired, 1)
	assert.Equal(t, expiredBeforeRead.ID(), expired[0].ID())
	assert.Equal(t, domain.PaymentStatusExpired, expired[0].Status())

	authorizedQuery, err := app.NewSearchPaymentsQuery("order-1", "customer-1", "authorized")
	require.NoError(t, err)
	authorized, err := store.Search(ctx, authorizedQuery, readNow)
	require.NoError(t, err)
	assert.Empty(t, authorized)

	outOfScope, err := store.FindByID(ctx, stillAuthorized.ID(), testNonExpiringBusinessTime)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusAuthorized, outOfScope.Status())
}

func TestPaymentStoreUpdatesCapturedPayment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	authorizedAt := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment, err := testsupport.NewAuthorizedPayment(
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
	require.NoError(t, payment.MarkCaptured(
		"cap_550e8400-e29b-41d4-a716-446655440002",
		"bok_550e8400-e29b-41d4-a716-446655440003",
		capturedAt,
	))
	updatePaymentFixture(t, db, payment, domain.PaymentStatusAuthorized)

	saved, err := store.FindByID(ctx, payment.ID(), testNonExpiringBusinessTime)
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
	payment, err := testsupport.NewAuthorizedPayment(
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

	saved, err := store.FindByID(ctx, payment.ID(), testNonExpiringBusinessTime)
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
	payment, err := testsupport.NewAuthorizedPayment(
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
	require.NoError(t, payment.MarkCaptured(
		"cap_550e8400-e29b-41d4-a716-446655440002",
		"bok_550e8400-e29b-41d4-a716-446655440003",
		capturedAt,
	))
	insertPaymentFixture(t, db, payment)

	refundedAt := time.Date(2026, 6, 19, 11, 0, 0, 0, time.UTC)
	require.NoError(t, payment.MarkRefunded(
		"ref_550e8400-e29b-41d4-a716-446655440004",
		"bok_550e8400-e29b-41d4-a716-446655440005",
		refundedAt,
	))
	updatePaymentFixture(t, db, payment, domain.PaymentStatusCaptured)

	saved, err := store.FindByID(ctx, payment.ID(), testNonExpiringBusinessTime)
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
	payment, err := testsupport.NewAuthorizedPayment(
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
	require.NoError(t, payment.MarkCaptured(
		"cap_550e8400-e29b-41d4-a716-446655440002",
		"bok_550e8400-e29b-41d4-a716-446655440003",
		capturedAt,
	))
	insertPaymentFixture(t, db, payment)
	require.NoError(t, payment.SetRefundBankOperationKey("bok_550e8400-e29b-41d4-a716-446655440004"))

	saveBankOperationKeyFixture(t, db, payment, app.BankOperationKeyRefund)

	saved, err := store.FindByID(ctx, payment.ID(), testNonExpiringBusinessTime)
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
	request := app.NewAuthorizationStartClaimRequest("public-key-1", "fingerprint-1", payment, now, testIdempotencyClaimStuckAfter)

	claimed, err := store.ClaimAuthorizationStart(ctx, request)
	require.NoError(t, err)
	assert.Same(t, payment, claimed.Payment())
	require.NoError(t, payment.MarkDeclined(domain.DeclineReasonInvalidCard, now))
	require.NoError(t, store.CompletePaymentCommand(ctx, claimed, result, now.Add(time.Minute)))

	saved, err := store.ClaimAuthorizationStart(ctx, request)
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
	missing, err := store.ClaimAuthorizationStart(ctx, app.NewAuthorizationStartClaimRequest("missing-key", "fingerprint-1", missingPayment, now, testIdempotencyClaimStuckAfter))
	require.NoError(t, err)
	assert.Same(t, missingPayment, missing.Payment())
}

func TestPaymentStoreCleansOnlyCompletedIdempotencyRecordsBeforeCutoff(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	completedAt := now.Add(time.Minute)
	payment := newStorePayment(t, 90, "order-1", "customer-1", domain.PaymentStatusPending, now)
	request := app.NewAuthorizationStartClaimRequest("completed-key", "fingerprint-1", payment, now, testIdempotencyClaimStuckAfter)
	claim, err := store.ClaimAuthorizationStart(ctx, request)
	require.NoError(t, err)
	require.NoError(t, claim.Payment().MarkDeclined(domain.DeclineReasonInvalidCard, completedAt))
	require.NoError(t, store.CompletePaymentCommand(ctx, claim, newStorePaymentCommandResult(claim.Payment(), 201), completedAt))

	var (
		status      string
		paymentData []byte
		storedAt    time.Time
	)
	err = db.QueryRowContext(ctx, `SELECT status, payment_result, completed_at FROM idempotency_records WHERE operation = $1 AND key = $2`, app.AuthorizePaymentOperation, "completed-key").Scan(&status, &paymentData, &storedAt)
	require.NoError(t, err)
	assert.Equal(t, "completed", status)
	assert.NotEmpty(t, paymentData)
	assert.True(t, storedAt.Equal(completedAt), "completed_at = %s, want %s", storedAt, completedAt)

	inProgressPayment := newStorePayment(t, 91, "order-2", "customer-1", domain.PaymentStatusPending, now)
	insertPaymentFixture(t, db, inProgressPayment)
	insertIdempotencyClaimFixture(t, db, app.AuthorizePaymentOperation, "in-progress-key", "fingerprint-2", inProgressPayment.ID(), now.Add(-48*time.Hour))

	removed, err := store.CleanupCompletedIdempotencyRecords(ctx, completedAt)
	require.NoError(t, err)
	assert.Zero(t, removed)
	_, err = store.ClaimAuthorizationStart(ctx, request)
	require.NoError(t, err)

	removed, err = store.CleanupCompletedIdempotencyRecords(ctx, completedAt.Add(time.Microsecond))
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	replacement := newStorePayment(t, 92, "order-3", "customer-1", domain.PaymentStatusPending, now)
	newClaim, err := store.ClaimAuthorizationStart(ctx, app.NewAuthorizationStartClaimRequest("completed-key", "fingerprint-3", replacement, now, testIdempotencyClaimStuckAfter))
	require.NoError(t, err)
	assert.Same(t, replacement, newClaim.Payment())

	var inProgressCount int
	err = db.QueryRowContext(ctx, `SELECT count(*) FROM idempotency_records WHERE operation = $1 AND key = $2 AND status = 'in_progress'`, app.AuthorizePaymentOperation, "in-progress-key").Scan(&inProgressCount)
	require.NoError(t, err)
	assert.Equal(t, 1, inProgressCount)
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
	request := app.NewCaptureClaimRequest("public-key-1", "fingerprint-1", payment.ID(), "bok_550e8400-e29b-41d4-a716-446655440010", testNonExpiringBusinessTime, testIdempotencyClaimStuckAfter)
	claim, err := store.ClaimExistingPaymentCommand(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, claim.Payment())

	_, err = store.ClaimExistingPaymentCommand(ctx, request)
	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyInProgress))

	require.NoError(t, store.ReleasePaymentCommand(ctx, claim))
	reclaimed, err := store.ClaimExistingPaymentCommand(ctx, request)
	require.NoError(t, err)
	assert.NotNil(t, reclaimed.Payment())
}

func TestPaymentStoreRecoversStuckAuthorizationClaimAndCompletesReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	original := newStorePayment(t, 81, "order-1", "customer-1", domain.PaymentStatusPending, now.Add(-10*time.Minute))
	insertPaymentFixture(t, db, original)
	insertIdempotencyClaimFixture(t, db, app.AuthorizePaymentOperation, "public-key-1", "fingerprint-1", original.ID(), now.Add(-6*time.Minute))
	assertRecoverableClaimFixture(t, db, app.AuthorizePaymentOperation, "public-key-1", "fingerprint-1", now.Add(-5*time.Minute))
	candidate := newStorePayment(t, 82, "order-1", "customer-1", domain.PaymentStatusPending, now)
	request := app.NewAuthorizationStartClaimRequest("public-key-1", "fingerprint-1", candidate, now, 5*time.Minute)

	claim, err := store.ClaimAuthorizationStart(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, claim.Payment())
	assert.Equal(t, original.ID(), claim.Payment().ID())
	assert.Equal(t, original.AuthorizationBankOperationKey(), claim.Payment().AuthorizationBankOperationKey())
	assertClaimedAt(t, db, app.AuthorizePaymentOperation, "public-key-1", now)

	require.NoError(t, claim.Payment().MarkAuthorized("auth_550e8400-e29b-41d4-a716-446655440000", now.Add(time.Hour), now))
	result := newStorePaymentCommandResult(claim.Payment(), 201)
	require.NoError(t, store.CompletePaymentCommand(ctx, claim, result, now.Add(time.Minute)))

	replayed, err := store.ClaimAuthorizationStart(ctx, request)
	require.NoError(t, err)
	replayResult, ok := replayed.ReplayResult()
	require.True(t, ok)
	assert.Equal(t, result.HTTPStatus, replayResult.HTTPStatus)
	assert.Equal(t, result.Payment.ID, replayResult.Payment.ID)
	assert.Equal(t, result.Payment.OrderID, replayResult.Payment.OrderID)
	assert.Equal(t, result.Payment.CustomerID, replayResult.Payment.CustomerID)
	assert.Equal(t, result.Payment.AmountCents, replayResult.Payment.AmountCents)
	assert.Equal(t, result.Payment.Currency, replayResult.Payment.Currency)
	assert.Equal(t, result.Payment.Status, replayResult.Payment.Status)
	assert.Equal(t, result.Payment.DeclineReason, replayResult.Payment.DeclineReason)
	assert.True(t, replayResult.Payment.AuthorizationExpiresAt.Equal(result.Payment.AuthorizationExpiresAt), "authorization_expires_at should round-trip as the same instant")
	assert.True(t, replayResult.Payment.CreatedAt.Equal(result.Payment.CreatedAt), "created_at should round-trip as the same instant")
	assert.True(t, replayResult.Payment.UpdatedAt.Equal(result.Payment.UpdatedAt), "updated_at should round-trip as the same instant")
}

func TestPaymentStoreDoesNotRecoverNonStuckAuthorizationClaim(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	original := newStorePayment(t, 81, "order-1", "customer-1", domain.PaymentStatusPending, now)
	insertPaymentFixture(t, db, original)
	insertIdempotencyClaimFixture(t, db, app.AuthorizePaymentOperation, "public-key-1", "fingerprint-1", original.ID(), now.Add(-4*time.Minute))
	candidate := newStorePayment(t, 82, "order-1", "customer-1", domain.PaymentStatusPending, now)

	_, err := store.ClaimAuthorizationStart(ctx, app.NewAuthorizationStartClaimRequest("public-key-1", "fingerprint-1", candidate, now, 5*time.Minute))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyInProgress))
	assertClaimedAt(t, db, app.AuthorizePaymentOperation, "public-key-1", now.Add(-4*time.Minute))
}

func TestPaymentStoreRejectsAuthorizationFingerprintMismatchWithoutRefreshingClaimedAt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	original := newStorePayment(t, 81, "order-1", "customer-1", domain.PaymentStatusPending, now)
	insertPaymentFixture(t, db, original)
	stuckClaimedAt := now.Add(-6 * time.Minute)
	insertIdempotencyClaimFixture(t, db, app.AuthorizePaymentOperation, "public-key-1", "fingerprint-1", original.ID(), stuckClaimedAt)
	candidate := newStorePayment(t, 82, "order-1", "customer-1", domain.PaymentStatusPending, now)

	_, err := store.ClaimAuthorizationStart(ctx, app.NewAuthorizationStartClaimRequest("public-key-1", "fingerprint-2", candidate, now, 5*time.Minute))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	assertClaimedAt(t, db, app.AuthorizePaymentOperation, "public-key-1", stuckClaimedAt)
}

func TestPaymentStoreReturnsInternalErrorWhenRecoveredAuthorizationPaymentIsMissing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	missingPaymentID := domain.PaymentID("pay_00000000-0000-4000-8000-000000000081")
	insertIdempotencyClaimFixture(t, db, app.AuthorizePaymentOperation, "public-key-1", "fingerprint-1", missingPaymentID, now.Add(-6*time.Minute))
	candidate := newStorePayment(t, 82, "order-1", "customer-1", domain.PaymentStatusPending, now)

	_, err := store.ClaimAuthorizationStart(ctx, app.NewAuthorizationStartClaimRequest("public-key-1", "fingerprint-1", candidate, now, 5*time.Minute))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInternal))
	recoveryErr, ok := errors.AsType[*app.IdempotencyRecoveryError](err)
	require.True(t, ok)
	assert.Equal(t, app.IdempotencyRecoveryUnrecoverable, recoveryErr.Result())
}

func TestPaymentStoreRecoversStuckAuthorizationRetryClaim(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment := newStorePayment(t, 91, "order-1", "customer-1", domain.PaymentStatusPending, now.Add(-10*time.Minute))
	insertPaymentFixture(t, db, payment)
	insertIdempotencyClaimFixture(t, db, app.RetryAuthorizationOperation, "retry-key-1", "fingerprint-1", payment.ID(), now.Add(-6*time.Minute))

	claim, err := store.ClaimExistingPaymentCommand(ctx, app.NewAuthorizationRetryClaimRequest(
		"retry-key-1",
		"fingerprint-1",
		payment.ID(),
		payment.AuthorizationCardFingerprint(),
		now,
		5*time.Minute,
	))

	require.NoError(t, err)
	require.NotNil(t, claim.Payment())
	assert.True(t, claim.Recovered())
	assert.Equal(t, payment.ID(), claim.Payment().ID())
	assert.Equal(t, payment.AuthorizationBankOperationKey(), claim.Payment().AuthorizationBankOperationKey())
	assertClaimedAt(t, db, app.RetryAuthorizationOperation, "retry-key-1", now)
}

func TestPaymentStoreRecoveredAuthorizationRetryCardFingerprintMismatchIsIdempotencyConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment := newStorePayment(t, 92, "order-1", "customer-1", domain.PaymentStatusPending, now.Add(-10*time.Minute))
	insertPaymentFixture(t, db, payment)
	stuckClaimedAt := now.Add(-6 * time.Minute)
	insertIdempotencyClaimFixture(t, db, app.RetryAuthorizationOperation, "retry-key-1", "fingerprint-1", payment.ID(), stuckClaimedAt)

	_, err := store.ClaimExistingPaymentCommand(ctx, app.NewAuthorizationRetryClaimRequest(
		"retry-key-1",
		"fingerprint-1",
		payment.ID(),
		"different-card-fingerprint",
		now,
		5*time.Minute,
	))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	assertClaimedAt(t, db, app.RetryAuthorizationOperation, "retry-key-1", stuckClaimedAt)
}

func TestPaymentStoreRecoversStuckCommandClaimsUsingPersistedBankOperationKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	tests := []struct {
		name              string
		operation         string
		publicKey         string
		fingerprint       string
		payment           func(t *testing.T, now time.Time) *domain.Payment
		persistRecovery   func(t *testing.T, db *sql.DB, payment *domain.Payment)
		request           func(payment *domain.Payment, now time.Time) app.ExistingPaymentCommandClaimRequest
		recoveredKey      func(payment *domain.Payment) string
		generatedFreshKey string
	}{
		{
			name:        "capture",
			operation:   app.CapturePaymentOperation,
			publicKey:   "capture-key-1",
			fingerprint: "capture-fingerprint-1",
			payment: func(t *testing.T, now time.Time) *domain.Payment {
				t.Helper()
				return newStorePayment(t, 93, "order-1", "customer-1", domain.PaymentStatusAuthorized, now.Add(-10*time.Minute))
			},
			persistRecovery: func(t *testing.T, db *sql.DB, payment *domain.Payment) {
				t.Helper()
				require.NoError(t, payment.SetCaptureBankOperationKey("bok_00000000-0000-4000-8000-000000000931"))
				saveBankOperationKeyFixture(t, db, payment, app.BankOperationKeyCapture)
			},
			request: func(payment *domain.Payment, now time.Time) app.ExistingPaymentCommandClaimRequest {
				return app.NewCaptureClaimRequest("capture-key-1", "capture-fingerprint-1", payment.ID(), "bok_00000000-0000-4000-8000-000000000999", now, 5*time.Minute)
			},
			recoveredKey: func(payment *domain.Payment) string {
				return payment.CaptureBankOperationKey()
			},
			generatedFreshKey: "bok_00000000-0000-4000-8000-000000000999",
		},
		{
			name:        "void",
			operation:   app.VoidPaymentOperation,
			publicKey:   "void-key-1",
			fingerprint: "void-fingerprint-1",
			payment: func(t *testing.T, now time.Time) *domain.Payment {
				t.Helper()
				return newStorePayment(t, 94, "order-1", "customer-1", domain.PaymentStatusAuthorized, now.Add(-10*time.Minute))
			},
			persistRecovery: func(t *testing.T, db *sql.DB, payment *domain.Payment) {
				t.Helper()
				require.NoError(t, payment.SetVoidBankOperationKey("bok_00000000-0000-4000-8000-000000000941"))
				saveBankOperationKeyFixture(t, db, payment, app.BankOperationKeyVoid)
			},
			request: func(payment *domain.Payment, now time.Time) app.ExistingPaymentCommandClaimRequest {
				return app.NewVoidClaimRequest("void-key-1", "void-fingerprint-1", payment.ID(), "bok_00000000-0000-4000-8000-000000000999", now, 5*time.Minute)
			},
			recoveredKey: func(payment *domain.Payment) string {
				return payment.VoidBankOperationKey()
			},
			generatedFreshKey: "bok_00000000-0000-4000-8000-000000000999",
		},
		{
			name:        "refund",
			operation:   app.RefundPaymentOperation,
			publicKey:   "refund-key-1",
			fingerprint: "refund-fingerprint-1",
			payment: func(t *testing.T, now time.Time) *domain.Payment {
				t.Helper()
				return newStorePayment(t, 95, "order-1", "customer-1", domain.PaymentStatusCaptured, now.Add(-10*time.Minute))
			},
			persistRecovery: func(t *testing.T, db *sql.DB, payment *domain.Payment) {
				t.Helper()
				require.NoError(t, payment.SetRefundBankOperationKey("bok_00000000-0000-4000-8000-000000000951"))
				saveBankOperationKeyFixture(t, db, payment, app.BankOperationKeyRefund)
			},
			request: func(payment *domain.Payment, now time.Time) app.ExistingPaymentCommandClaimRequest {
				return app.NewRefundClaimRequest("refund-key-1", "refund-fingerprint-1", payment.ID(), "bok_00000000-0000-4000-8000-000000000999", now, 5*time.Minute)
			},
			recoveredKey: func(payment *domain.Payment) string {
				return payment.RefundBankOperationKey()
			},
			generatedFreshKey: "bok_00000000-0000-4000-8000-000000000999",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDatabase(t)
			store := postgres.NewPaymentStore(db)
			ctx := context.Background()
			now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
			payment := tt.payment(t, now)
			insertPaymentFixture(t, db, payment)
			tt.persistRecovery(t, db, payment)
			insertIdempotencyClaimFixture(t, db, tt.operation, tt.publicKey, tt.fingerprint, payment.ID(), now.Add(-6*time.Minute))

			claim, err := store.ClaimExistingPaymentCommand(ctx, tt.request(payment, now))

			require.NoError(t, err)
			require.NotNil(t, claim.Payment())
			assert.True(t, claim.Recovered())
			assert.Equal(t, payment.ID(), claim.Payment().ID())
			assert.Equal(t, tt.recoveredKey(payment), tt.recoveredKey(claim.Payment()))
			assert.NotEqual(t, tt.generatedFreshKey, tt.recoveredKey(claim.Payment()))
			assertClaimedAt(t, db, tt.operation, tt.publicKey, now)
		})
	}
}

func TestPaymentStoreRecoveredCommandMissingBankOperationKeyIsUnrecoverable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment := newStorePayment(t, 96, "order-1", "customer-1", domain.PaymentStatusAuthorized, now.Add(-10*time.Minute))
	insertPaymentFixture(t, db, payment)
	stuckClaimedAt := now.Add(-6 * time.Minute)
	insertIdempotencyClaimFixture(t, db, app.CapturePaymentOperation, "capture-key-1", "capture-fingerprint-1", payment.ID(), stuckClaimedAt)

	_, err := store.ClaimExistingPaymentCommand(ctx, app.NewCaptureClaimRequest("capture-key-1", "capture-fingerprint-1", payment.ID(), "bok_00000000-0000-4000-8000-000000000999", now, 5*time.Minute))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInternal))
	saved, findErr := store.FindByID(ctx, payment.ID(), testNonExpiringBusinessTime)
	require.NoError(t, findErr)
	assert.Empty(t, saved.CaptureBankOperationKey())
	assertClaimedAt(t, db, app.CapturePaymentOperation, "capture-key-1", stuckClaimedAt)
}

func TestPaymentStoreRecoveredCommandInvalidPaymentStatusRemainsPaymentStatusConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment := newStorePayment(t, 97, "order-1", "customer-1", domain.PaymentStatusCaptured, now.Add(-10*time.Minute))
	insertPaymentFixture(t, db, payment)
	insertIdempotencyClaimFixture(t, db, app.CapturePaymentOperation, "capture-key-1", "capture-fingerprint-1", payment.ID(), now.Add(-6*time.Minute))

	_, err := store.ClaimExistingPaymentCommand(ctx, app.NewCaptureClaimRequest("capture-key-1", "capture-fingerprint-1", payment.ID(), "bok_00000000-0000-4000-8000-000000000999", now, 5*time.Minute))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorPaymentStatusConflict))
	recoveryErr, ok := errors.AsType[*app.IdempotencyRecoveryError](err)
	require.True(t, ok)
	assert.Equal(t, app.IdempotencyRecoveryConflict, recoveryErr.Result())
}

func TestPaymentStoreConcurrentStuckAuthorizationRecoveryAllowsOneRetriever(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	original := newStorePayment(t, 81, "order-1", "customer-1", domain.PaymentStatusPending, now.Add(-10*time.Minute))
	insertPaymentFixture(t, db, original)
	insertIdempotencyClaimFixture(t, db, app.AuthorizePaymentOperation, "public-key-1", "fingerprint-1", original.ID(), now.Add(-6*time.Minute))

	candidates := []*domain.Payment{
		newStorePayment(t, 82, "order-1", "customer-1", domain.PaymentStatusPending, now),
		newStorePayment(t, 83, "order-1", "customer-1", domain.PaymentStatusPending, now),
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(sequence int) {
			defer wg.Done()
			<-start
			_, err := store.ClaimAuthorizationStart(ctx, app.NewAuthorizationStartClaimRequest("public-key-1", "fingerprint-1", candidates[sequence], now, 5*time.Minute))
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	var successes int
	var inProgress int
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyInProgress) {
			inProgress++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, inProgress)
	assertClaimedAt(t, db, app.AuthorizePaymentOperation, "public-key-1", now)
}

func TestPaymentStoreRejectsPaymentCommandClaimPreconditionFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	tests := []struct {
		name    string
		payment *domain.Payment
		request func(*domain.Payment) app.ExistingPaymentCommandClaimRequest
	}{
		{
			name:    "retry authorization requires pending payment",
			payment: newStorePayment(t, 1, "order-1", "customer-1", domain.PaymentStatusAuthorized, time.Now()),
			request: func(payment *domain.Payment) app.ExistingPaymentCommandClaimRequest {
				return app.NewAuthorizationRetryClaimRequest("retry-key-1", "fingerprint-1", payment.ID(), payment.AuthorizationCardFingerprint(), time.Now(), testIdempotencyClaimStuckAfter)
			},
		},
		{
			name:    "retry authorization requires matching authorization card fingerprint",
			payment: newStorePayment(t, 2, "order-1", "customer-1", domain.PaymentStatusPending, time.Now()),
			request: func(payment *domain.Payment) app.ExistingPaymentCommandClaimRequest {
				return app.NewAuthorizationRetryClaimRequest("retry-key-2", "fingerprint-2", payment.ID(), "different-fingerprint", time.Now(), testIdempotencyClaimStuckAfter)
			},
		},
		{
			name:    "capture requires authorized payment",
			payment: newStorePayment(t, 3, "order-1", "customer-1", domain.PaymentStatusPending, time.Now()),
			request: func(payment *domain.Payment) app.ExistingPaymentCommandClaimRequest {
				return app.NewCaptureClaimRequest("capture-key-1", "fingerprint-3", payment.ID(), "bok_00000000-0000-4000-8000-000000000103", testNonExpiringBusinessTime, testIdempotencyClaimStuckAfter)
			},
		},
		{
			name:    "void requires authorized payment",
			payment: newStorePayment(t, 4, "order-1", "customer-1", domain.PaymentStatusCaptured, time.Now()),
			request: func(payment *domain.Payment) app.ExistingPaymentCommandClaimRequest {
				return app.NewVoidClaimRequest("void-key-1", "fingerprint-4", payment.ID(), "bok_00000000-0000-4000-8000-000000000104", testNonExpiringBusinessTime, testIdempotencyClaimStuckAfter)
			},
		},
		{
			name:    "refund requires captured payment",
			payment: newStorePayment(t, 5, "order-1", "customer-1", domain.PaymentStatusAuthorized, time.Now()),
			request: func(payment *domain.Payment) app.ExistingPaymentCommandClaimRequest {
				return app.NewRefundClaimRequest("refund-key-1", "fingerprint-5", payment.ID(), "bok_00000000-0000-4000-8000-000000000105", time.Now(), testIdempotencyClaimStuckAfter)
			},
		},
	}

	db := newTestDatabase(t)
	store := postgres.NewPaymentStore(db)
	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			insertPaymentFixture(t, db, tt.payment)

			_, err := store.ClaimExistingPaymentCommand(ctx, tt.request(tt.payment))

			require.Error(t, err)
			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorPaymentStatusConflict))
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
	request := app.NewAuthorizationStartClaimRequest("public-key-1", "fingerprint-1", payment, now, testIdempotencyClaimStuckAfter)
	claim, err := store.ClaimAuthorizationStart(ctx, request)
	require.NoError(t, err)
	require.Same(t, payment, claim.Payment())
	require.NoError(t, payment.MarkAuthorized("auth_550e8400-e29b-41d4-a716-446655440000", now.Add(time.Hour), now.Add(time.Minute)))
	_, err = db.ExecContext(ctx, `DELETE FROM idempotency_records WHERE operation = $1 AND key = $2`, "authorize_payment", "public-key-1")
	require.NoError(t, err)

	err = store.CompletePaymentCommand(ctx, claim, newStorePaymentCommandResult(payment, 201), now.Add(2*time.Minute))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	saved, err := store.FindByID(ctx, payment.ID(), testNonExpiringBusinessTime)
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
	request := app.NewCaptureClaimRequest("public-capture-key-1", "fingerprint-1", payment.ID(), "bok_550e8400-e29b-41d4-a716-446655440010", testNonExpiringBusinessTime, testIdempotencyClaimStuckAfter)
	claim, err := store.ClaimExistingPaymentCommand(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, claim.Payment())
	require.NoError(t, claim.Payment().MarkCaptured("cap_550e8400-e29b-41d4-a716-446655440000", claim.Payment().CaptureBankOperationKey(), now.Add(time.Minute)))
	_, err = db.ExecContext(ctx, `DELETE FROM idempotency_records WHERE operation = $1 AND key = $2`, "capture_payment", "public-capture-key-1")
	require.NoError(t, err)

	err = store.CompletePaymentCommand(ctx, claim, newStorePaymentCommandResult(claim.Payment(), 200), now.Add(2*time.Minute))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	saved, err := store.FindByID(ctx, payment.ID(), testNonExpiringBusinessTime)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusAuthorized, saved.Status())
	assert.Empty(t, saved.BankCaptureID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440010", saved.CaptureBankOperationKey())
	claimStatus, err := store.ClaimExistingPaymentCommand(ctx, request)
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
		payment, err := testsupport.NewAuthorizedPayment(
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
		require.NoError(t, payment.MarkCaptured(
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
		require.NoError(t, payment.MarkRefunded(
			fmt.Sprintf("ref_00000000-0000-4000-8000-%012d", sequence),
			fmt.Sprintf("bok_00000000-0000-4000-8000-%012d", sequence+2000),
			now.Add(2*time.Minute),
		))
		return payment
	case domain.PaymentStatusDeclined:
		payment, err := testsupport.NewDeclinedPayment(
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

	request := app.NewAuthorizationStartClaimRequest("public-key-1", "fingerprint-1", payment, now, testIdempotencyClaimStuckAfter)
	claim := app.NewClaimedPaymentCommand(request, payment)
	err = store.CompletePaymentCommand(ctx, claim, newStorePaymentCommandResult(payment, 201), now.Add(time.Minute))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
	saved, err := store.FindByID(ctx, payment.ID(), testNonExpiringBusinessTime)
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

func insertIdempotencyClaimFixture(t *testing.T, db *sql.DB, operation string, key string, fingerprint string, paymentID domain.PaymentID, claimedAt time.Time) {
	t.Helper()

	_, err := db.ExecContext(
		context.Background(),
		`INSERT INTO idempotency_records (
		     operation,
		     key,
		     request_fingerprint,
		     payment_id,
		     status,
		     created_at,
		     claimed_at
		 )
		 VALUES ($1, $2, $3, $4, 'in_progress', $5, $5)`,
		operation,
		key,
		fingerprint,
		paymentID,
		claimedAt,
	)
	require.NoError(t, err)
}

func assertClaimedAt(t *testing.T, db *sql.DB, operation string, key string, want time.Time) {
	t.Helper()

	var got time.Time
	err := db.QueryRowContext(
		context.Background(),
		`SELECT claimed_at FROM idempotency_records WHERE operation = $1 AND key = $2`,
		operation,
		key,
	).Scan(&got)
	require.NoError(t, err)
	assert.True(t, got.Equal(want), "claimed_at = %s, want %s", got, want)
}

func assertRecoverableClaimFixture(t *testing.T, db *sql.DB, operation string, key string, fingerprint string, cutoff time.Time) {
	t.Helper()

	var matches int
	err := db.QueryRowContext(
		context.Background(),
		`SELECT count(*)
		   FROM idempotency_records
		  WHERE operation = $1
		    AND key = $2
		    AND request_fingerprint = $3
		    AND status = 'in_progress'
		    AND claimed_at <= $4`,
		operation,
		key,
		fingerprint,
		cutoff,
	).Scan(&matches)
	require.NoError(t, err)
	require.Equal(t, 1, matches)
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
			filepath.Join("..", "..", "migrations", "000002_add_idempotency_completion_time.up.sql"),
		),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	testcontainers.CleanupContainer(t, container)

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := postgres.Open(ctx, postgres.Options{URL: databaseURL})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})

	return db
}
