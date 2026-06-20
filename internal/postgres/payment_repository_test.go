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

func TestPaymentRepositoryPersistsAuthorizedPayment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	repository := postgres.NewPaymentRepository(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	payment, err := domain.NewAuthorizedPayment(
		domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"),
		"order-1",
		"customer-1",
		1299,
		"auth_550e8400-e29b-41d4-a716-446655440000",
		"bok_550e8400-e29b-41d4-a716-446655440001",
		"fingerprint-1",
		now,
	)
	require.NoError(t, err)

	require.NoError(t, repository.Create(ctx, payment))

	saved, err := repository.FindByID(ctx, payment.ID())
	require.NoError(t, err)
	assert.Equal(t, payment.ID(), saved.ID())
	assert.Equal(t, "order-1", saved.OrderID())
	assert.Equal(t, "customer-1", saved.CustomerID())
	assert.Equal(t, int64(1299), saved.AmountCents())
	assert.Equal(t, domain.CurrencyUSD, saved.Currency())
	assert.Equal(t, domain.PaymentStatusAuthorized, saved.Status())
	assert.Equal(t, "auth_550e8400-e29b-41d4-a716-446655440000", saved.BankAuthorizationID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440001", saved.AuthorizationBankOperationKey())
	assert.Equal(t, "fingerprint-1", saved.AuthorizationCardFingerprint())
	assert.True(t, saved.CreatedAt().Equal(now), "created_at should round-trip as the same instant")
	assert.True(t, saved.UpdatedAt().Equal(now), "updated_at should round-trip as the same instant")

	_, err = repository.FindByID(ctx, domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440999"))
	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorNotFound))
}

func TestPaymentRepositoryPersistsDeclinedPayment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	repository := postgres.NewPaymentRepository(db)
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

	require.NoError(t, repository.Create(ctx, payment))

	saved, err := repository.FindByID(ctx, payment.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusDeclined, saved.Status())
	assert.Equal(t, domain.DeclineReasonExpiredCard, saved.DeclineReason())
	assert.Empty(t, saved.BankAuthorizationID())
	assert.Equal(t, "bok_550e8400-e29b-41d4-a716-446655440001", saved.AuthorizationBankOperationKey())
	assert.Equal(t, "fingerprint-1", saved.AuthorizationCardFingerprint())
	assert.True(t, saved.CreatedAt().Equal(now), "created_at should round-trip as the same instant")
	assert.True(t, saved.UpdatedAt().Equal(now), "updated_at should round-trip as the same instant")
}

func TestPaymentRepositoryUpdatesPendingAuthorizationResult(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	repository := postgres.NewPaymentRepository(db)
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
	require.NoError(t, repository.Create(ctx, payment))

	require.NoError(t, payment.MarkAuthorized("auth_550e8400-e29b-41d4-a716-446655440000", now.Add(time.Minute)))
	require.NoError(t, repository.UpdateAuthorizationResult(ctx, payment))

	saved, err := repository.FindByID(ctx, payment.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusAuthorized, saved.Status())
	assert.Equal(t, "auth_550e8400-e29b-41d4-a716-446655440000", saved.BankAuthorizationID())
	assert.Equal(t, "fingerprint-1", saved.AuthorizationCardFingerprint())
	assert.True(t, saved.CreatedAt().Equal(now), "created_at should stay as the original instant")
	assert.True(t, saved.UpdatedAt().Equal(now.Add(time.Minute)), "updated_at should round-trip as the transition instant")
}

func TestPaymentRepositorySearchesPaymentsByFiltersNewestFirstAndCapped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	repository := postgres.NewPaymentRepository(db)
	ctx := context.Background()
	base := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	for i := 0; i < 105; i++ {
		payment := newRepositoryPayment(t, i, "order-1", "customer-1", domain.PaymentStatusAuthorized, base.Add(time.Duration(i)*time.Minute))
		require.NoError(t, repository.Create(ctx, payment))
	}
	otherOrder := newRepositoryPayment(t, 105, "order-2", "customer-1", domain.PaymentStatusAuthorized, base.Add(105*time.Minute))
	require.NoError(t, repository.Create(ctx, otherOrder))
	declined := newRepositoryPayment(t, 106, "order-1", "customer-1", domain.PaymentStatusDeclined, base.Add(106*time.Minute))
	require.NoError(t, repository.Create(ctx, declined))

	authorized, err := repository.Search(ctx, app.PaymentSearchFilter{
		OrderID:    "order-1",
		CustomerID: "customer-1",
		Status:     "authorized",
	})

	require.NoError(t, err)
	require.Len(t, authorized, 100)
	assert.Equal(t, domain.PaymentID("pay_00000000-0000-4000-8000-000000000104"), authorized[0].ID())
	assert.Equal(t, domain.PaymentID("pay_00000000-0000-4000-8000-000000000005"), authorized[99].ID())

	byCustomer, err := repository.Search(ctx, app.PaymentSearchFilter{CustomerID: "customer-1"})

	require.NoError(t, err)
	require.Len(t, byCustomer, 100)
	assert.Equal(t, declined.ID(), byCustomer[0].ID())
	assert.Equal(t, otherOrder.ID(), byCustomer[1].ID())
}

func TestIdempotencyRepositoryPersistsCompletedDeclinedResult(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	repository := postgres.NewIdempotencyRepository(db)
	ctx := context.Background()
	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	record := app.IdempotencyRecord{
		Operation:          "authorize_payment",
		Key:                "public-key-1",
		RequestFingerprint: "fingerprint-1",
		Result: app.PaymentResult{
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

	require.NoError(t, repository.SaveCompleted(ctx, record))

	saved, found, err := repository.FindCompleted(ctx, "authorize_payment", "public-key-1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, record.Operation, saved.Operation)
	assert.Equal(t, record.Key, saved.Key)
	assert.Equal(t, record.RequestFingerprint, saved.RequestFingerprint)
	assert.Equal(t, record.Result.ID, saved.Result.ID)
	assert.Equal(t, record.Result.OrderID, saved.Result.OrderID)
	assert.Equal(t, record.Result.CustomerID, saved.Result.CustomerID)
	assert.Equal(t, record.Result.AmountCents, saved.Result.AmountCents)
	assert.Equal(t, record.Result.Currency, saved.Result.Currency)
	assert.Equal(t, record.Result.Status, saved.Result.Status)
	assert.Equal(t, record.Result.DeclineReason, saved.Result.DeclineReason)
	assert.True(t, saved.Result.CreatedAt.Equal(now), "created_at should round-trip as the same instant")
	assert.True(t, saved.Result.UpdatedAt.Equal(now), "updated_at should round-trip as the same instant")

	_, found, err = repository.FindCompleted(ctx, "authorize_payment", "missing-key")
	require.NoError(t, err)
	assert.False(t, found)
}

func newRepositoryPayment(t *testing.T, sequence int, orderID string, customerID string, status domain.PaymentStatus, now time.Time) *domain.Payment {
	t.Helper()

	id := domain.PaymentID(fmt.Sprintf("pay_00000000-0000-4000-8000-%012d", sequence))
	bankOperationKey := fmt.Sprintf("bok_00000000-0000-4000-8000-%012d", sequence)
	cardFingerprint := fmt.Sprintf("fingerprint-%d", sequence)
	switch status {
	case domain.PaymentStatusAuthorized:
		payment, err := domain.NewAuthorizedPayment(
			id,
			orderID,
			customerID,
			1299,
			fmt.Sprintf("auth_00000000-0000-4000-8000-%012d", sequence),
			bankOperationKey,
			cardFingerprint,
			now,
		)
		require.NoError(t, err)
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

func TestIdempotencyRepositoryReturnsConflictForDuplicateCompletedRecord(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in short mode")
	}

	db := newTestDatabase(t)
	repository := postgres.NewIdempotencyRepository(db)
	ctx := context.Background()
	record := app.IdempotencyRecord{
		Operation:          "authorize_payment",
		Key:                "public-key-1",
		RequestFingerprint: "fingerprint-1",
		Result: app.PaymentResult{
			ID:          "pay_550e8400-e29b-41d4-a716-446655440000",
			OrderID:     "order-1",
			CustomerID:  "customer-1",
			AmountCents: 1299,
			Currency:    "USD",
			Status:      "authorized",
			CreatedAt:   time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC),
			UpdatedAt:   time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC),
		},
	}

	require.NoError(t, repository.SaveCompleted(ctx, record))
	err := repository.SaveCompleted(ctx, record)

	require.Error(t, err)
	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorIdempotencyConflict))
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
			filepath.Join("..", "..", "migrations", "000002_add_declined_payments_and_idempotency.up.sql"),
			filepath.Join("..", "..", "migrations", "000003_add_pending_authorization_retry.up.sql"),
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
