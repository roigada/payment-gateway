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
		     authorization_card_fingerprint,
		     decline_reason,
		     created_at,
		     updated_at
		 )
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		payment.ID(),
		payment.OrderID(),
		payment.CustomerID(),
		payment.AmountCents(),
		payment.Currency(),
		payment.Status(),
		nullableString(payment.BankAuthorizationID()),
		payment.AuthorizationBankOperationKey(),
		payment.AuthorizationCardFingerprint(),
		nullableString(string(payment.DeclineReason())),
		payment.CreatedAt(),
		payment.UpdatedAt(),
	)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	return nil
}

func (r *PaymentRepository) UpdateAuthorizationResult(ctx context.Context, payment *domain.Payment) error {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE payments
		    SET status = $2,
		        bank_authorization_id = $3,
		        decline_reason = $4,
		        updated_at = $5
		  WHERE id = $1
		    AND status = $6`,
		payment.ID(),
		payment.Status(),
		nullableString(payment.BankAuthorizationID()),
		nullableString(string(payment.DeclineReason())),
		payment.UpdatedAt(),
		domain.PaymentStatusPending,
	)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	if affected == 0 {
		_, err := r.FindByID(ctx, payment.ID())
		if err != nil {
			return err
		}
		return app.NewPaymentInvalidStatusConflict(nil)
	}
	return nil
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
		authorizationCardFingerprint  string
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
		        authorization_card_fingerprint,
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
		&authorizationCardFingerprint,
		&declineReason,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, app.NewPaymentNotFound(string(id), err)
		}
		return nil, app.NewInternalPaymentError(err)
	}

	payment, err := domain.LoadPayment(
		id,
		orderID,
		customerID,
		amountCents,
		currency,
		status,
		nullStringValue(bankAuthorizationID),
		authorizationBankOperationKey,
		authorizationCardFingerprint,
		domain.DeclineReason(nullStringValue(declineReason)),
		createdAt.Time,
		updatedAt.Time,
	)
	if err != nil {
		return nil, app.NewInternalPaymentError(err)
	}
	return payment, nil
}

func (r *PaymentRepository) Search(ctx context.Context, filter app.PaymentSearchFilter) ([]*domain.Payment, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id,
		        order_id,
		        customer_id,
		        amount_cents,
		        currency,
		        status,
		        bank_authorization_id,
		        authorization_bank_operation_key,
		        authorization_card_fingerprint,
		        decline_reason,
		        created_at,
		        updated_at
		   FROM payments
		  WHERE ($1 = '' OR order_id = $1)
		    AND ($2 = '' OR customer_id = $2)
		    AND ($3 = '' OR status = $3)
		  ORDER BY created_at DESC, id DESC
		  LIMIT 100`,
		filter.OrderID,
		filter.CustomerID,
		filter.Status,
	)
	if err != nil {
		return nil, app.NewInternalPaymentError(err)
	}
	defer rows.Close()

	var payments []*domain.Payment
	for rows.Next() {
		payment, err := scanPayment(rows)
		if err != nil {
			return nil, err
		}
		payments = append(payments, payment)
	}
	if err := rows.Err(); err != nil {
		return nil, app.NewInternalPaymentError(err)
	}
	return payments, nil
}

type paymentScanner interface {
	Scan(dest ...any) error
}

func scanPayment(scanner paymentScanner) (*domain.Payment, error) {
	var (
		id                            domain.PaymentID
		orderID                       string
		customerID                    string
		amountCents                   int64
		currency                      string
		status                        domain.PaymentStatus
		bankAuthorizationID           sql.NullString
		authorizationBankOperationKey string
		authorizationCardFingerprint  string
		declineReason                 sql.NullString
		createdAt                     sql.NullTime
		updatedAt                     sql.NullTime
	)
	err := scanner.Scan(
		&id,
		&orderID,
		&customerID,
		&amountCents,
		&currency,
		&status,
		&bankAuthorizationID,
		&authorizationBankOperationKey,
		&authorizationCardFingerprint,
		&declineReason,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return nil, app.NewInternalPaymentError(err)
	}

	payment, err := domain.LoadPayment(
		id,
		orderID,
		customerID,
		amountCents,
		currency,
		status,
		nullStringValue(bankAuthorizationID),
		authorizationBankOperationKey,
		authorizationCardFingerprint,
		domain.DeclineReason(nullStringValue(declineReason)),
		createdAt.Time,
		updatedAt.Time,
	)
	if err != nil {
		return nil, app.NewInternalPaymentError(err)
	}
	return payment, nil
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
