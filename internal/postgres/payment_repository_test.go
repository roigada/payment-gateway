package postgres_test

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/roigada/payment-gateway/internal/postgres"
	"github.com/stretchr/testify/require"
)

func TestPaymentRepositoryCreatePersistsAuthorizedPaymentWithPrivateBankFields(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	payment, err := domain.NewAuthorizedPayment(
		domain.PaymentID("pay_123"),
		"order-1",
		"customer-1",
		1299,
		"bank-auth-1",
		"bok_123",
		now,
	)
	require.NoError(t, err)

	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO payments (
		     id,
		     order_id,
		     customer_id,
		     amount_cents,
		     currency,
		     status,
		     authorization_bank_reference,
		     authorization_bank_operation_key,
		     created_at,
		     updated_at
		 )
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`)).
		WithArgs(
			domain.PaymentID("pay_123"),
			"order-1",
			"customer-1",
			int64(1299),
			"USD",
			domain.PaymentStatusAuthorized,
			"bank-auth-1",
			"bok_123",
			now,
			now,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	repository := postgres.NewPaymentRepository(db)
	require.NoError(t, repository.Create(context.Background(), payment))
	require.NoError(t, mock.ExpectationsWereMet())
}
