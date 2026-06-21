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
		     bank_capture_id,
		     capture_bank_operation_key,
		     decline_reason,
		     created_at,
		     updated_at
		 )
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		payment.ID(),
		payment.OrderID(),
		payment.CustomerID(),
		payment.AmountCents(),
		payment.Currency(),
		payment.Status(),
		nullableString(payment.BankAuthorizationID()),
		payment.AuthorizationBankOperationKey(),
		payment.AuthorizationCardFingerprint(),
		nullableString(payment.BankCaptureID()),
		nullableString(payment.CaptureBankOperationKey()),
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

func (r *PaymentRepository) ClaimCapture(ctx context.Context, payment *domain.Payment, record app.IdempotencyRecord) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	defer rollback(tx)

	insertResult, err := tx.ExecContext(
		ctx,
		`INSERT INTO idempotency_records (
		     operation,
		     key,
		     request_fingerprint,
		     response_body,
		     completed
		 )
		 VALUES ($1, $2, $3, '{}'::jsonb, false)
		 ON CONFLICT (operation, key) DO NOTHING`,
		record.Operation,
		record.Key,
		record.RequestFingerprint,
	)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	inserted, err := insertResult.RowsAffected()
	if err != nil {
		return app.NewInternalPaymentError(err)
	}

	var (
		storedFingerprint string
		completed         bool
	)
	err = tx.QueryRowContext(
		ctx,
		`SELECT request_fingerprint,
		        completed
		   FROM idempotency_records
		  WHERE operation = $1
		    AND key = $2`,
		record.Operation,
		record.Key,
	).Scan(&storedFingerprint, &completed)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	if storedFingerprint != record.RequestFingerprint || completed {
		return app.NewPaymentIdempotencyConflict(nil)
	}
	if inserted > 0 {
		if err := updatePayment(ctx, tx, payment); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return app.NewInternalPaymentError(err)
	}
	return nil
}

func (r *PaymentRepository) CompleteCapture(ctx context.Context, payment *domain.Payment, record app.IdempotencyRecord) error {
	responseBody, err := encodePaymentResultSnapshot(record.Result)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	defer rollback(tx)

	if err := updatePayment(ctx, tx, payment); err != nil {
		return err
	}

	result, err := tx.ExecContext(
		ctx,
		`UPDATE idempotency_records
		    SET response_body = $4::jsonb,
		        completed = true
		  WHERE operation = $1
		    AND key = $2
		    AND request_fingerprint = $3
		    AND completed = false`,
		record.Operation,
		record.Key,
		record.RequestFingerprint,
		string(responseBody),
	)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	if affected == 0 {
		return app.NewPaymentIdempotencyConflict(nil)
	}

	if err := tx.Commit(); err != nil {
		return app.NewInternalPaymentError(err)
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
		bankCaptureID                 sql.NullString
		captureBankOperationKey       sql.NullString
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
		        bank_capture_id,
		        capture_bank_operation_key,
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
		&bankCaptureID,
		&captureBankOperationKey,
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
		nullStringValue(bankCaptureID),
		nullStringValue(captureBankOperationKey),
		domain.DeclineReason(nullStringValue(declineReason)),
		createdAt.Time,
		updatedAt.Time,
	)
	if err != nil {
		return nil, app.NewInternalPaymentError(err)
	}
	return payment, nil
}

func (r *PaymentRepository) Search(ctx context.Context, query app.SearchPaymentsQuery) ([]*domain.Payment, error) {
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
		        bank_capture_id,
		        capture_bank_operation_key,
		        decline_reason,
		        created_at,
		        updated_at
		   FROM payments
		  WHERE ($1 = '' OR order_id = $1)
		    AND ($2 = '' OR customer_id = $2)
		    AND ($3 = '' OR status = $3)
		  ORDER BY created_at DESC, id DESC
		  LIMIT 100`,
		query.OrderID,
		query.CustomerID,
		query.Status,
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
		bankCaptureID                 sql.NullString
		captureBankOperationKey       sql.NullString
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
		&bankCaptureID,
		&captureBankOperationKey,
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
		nullStringValue(bankCaptureID),
		nullStringValue(captureBankOperationKey),
		domain.DeclineReason(nullStringValue(declineReason)),
		createdAt.Time,
		updatedAt.Time,
	)
	if err != nil {
		return nil, app.NewInternalPaymentError(err)
	}
	return payment, nil
}

type paymentUpdater interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func updatePayment(ctx context.Context, db paymentUpdater, payment *domain.Payment) error {
	result, err := db.ExecContext(
		ctx,
		`UPDATE payments
		    SET status = $2,
		        bank_authorization_id = $3,
		        authorization_bank_operation_key = $4,
		        authorization_card_fingerprint = $5,
		        bank_capture_id = $6,
		        capture_bank_operation_key = $7,
		        decline_reason = $8,
		        updated_at = $9
		  WHERE id = $1`,
		payment.ID(),
		payment.Status(),
		nullableString(payment.BankAuthorizationID()),
		payment.AuthorizationBankOperationKey(),
		payment.AuthorizationCardFingerprint(),
		nullableString(payment.BankCaptureID()),
		nullableString(payment.CaptureBankOperationKey()),
		nullableString(string(payment.DeclineReason())),
		payment.UpdatedAt(),
	)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	if affected == 0 {
		return app.NewPaymentNotFound(string(payment.ID()), sql.ErrNoRows)
	}
	return nil
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
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
