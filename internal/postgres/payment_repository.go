package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
)

type PaymentRepository struct {
	db *sql.DB
}

func NewPaymentRepository(db *sql.DB) *PaymentRepository {
	return &PaymentRepository{db: db}
}

func (r *PaymentRepository) Create(ctx context.Context, payment *domain.Payment) error {
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO payments (
		     id,
		     order_id,
		     customer_id,
		     amount_cents,
		     currency,
		     status,
		     bank_authorization_id,
		     authorization_bank_operation_key,
		     decline_reason,
		     created_at,
		     updated_at
		 )
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		payment.ID(),
		payment.OrderID(),
		payment.CustomerID(),
		payment.AmountCents(),
		payment.Currency(),
		payment.Status(),
		nullableString(payment.BankAuthorizationID()),
		payment.AuthorizationBankOperationKey(),
		nullableString(string(payment.DeclineReason())),
		payment.CreatedAt(),
		payment.UpdatedAt(),
	)
	return err
}

func (r *PaymentRepository) FindByID(ctx context.Context, id domain.PaymentID) (*domain.Payment, error) {
	var (
		orderID                       string
		customerID                    string
		amountCents                   int64
		currency                      string
		status                        domain.PaymentStatus
		bankAuthorizationID           sql.NullString
		authorizationBankOperationKey string
		declineReason                 sql.NullString
		createdAt                     sql.NullTime
		updatedAt                     sql.NullTime
	)
	err := r.db.QueryRowContext(
		ctx,
		`SELECT order_id,
		        customer_id,
		        amount_cents,
		        currency,
		        status,
		        bank_authorization_id,
		        authorization_bank_operation_key,
		        decline_reason,
		        created_at,
		        updated_at
		   FROM payments
		  WHERE id = $1`,
		id,
	).Scan(
		&orderID,
		&customerID,
		&amountCents,
		&currency,
		&status,
		&bankAuthorizationID,
		&authorizationBankOperationKey,
		&declineReason,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, app.ErrPaymentNotFound
		}
		return nil, err
	}

	return domain.LoadPayment(
		id,
		orderID,
		customerID,
		amountCents,
		currency,
		status,
		nullStringValue(bankAuthorizationID),
		authorizationBankOperationKey,
		domain.DeclineReason(nullStringValue(declineReason)),
		createdAt.Time,
		updatedAt.Time,
	)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}
