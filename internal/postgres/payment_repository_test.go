package postgres_test

import (
	"context"
	"database/sql"
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
	assert.True(t, saved.CreatedAt().Equal(now), "created_at should round-trip as the same instant")
	assert.True(t, saved.UpdatedAt().Equal(now), "updated_at should round-trip as the same instant")

	_, err = repository.FindByID(ctx, domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440999"))
	assert.ErrorIs(t, err, app.ErrPaymentNotFound)
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
		tcpostgres.WithInitScripts(filepath.Join("..", "..", "migrations", "000001_create_payments.up.sql")),
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
