package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

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
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	return nil
}

func (r *PaymentRepository) UpdateRefundResult(ctx context.Context, payment *domain.Payment) error {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE payments
		    SET status = $2,
		        bank_refund_id = $3,
		        refund_bank_operation_key = $4,
		        updated_at = $5
		  WHERE id = $1
		    AND status = $6`,
		payment.ID(),
		payment.Status(),
		nullableString(payment.BankRefundID()),
		nullableString(payment.RefundBankOperationKey()),
		payment.UpdatedAt(),
		domain.PaymentStatusCaptured,
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

func (r *PaymentRepository) SaveRefundBankOperationKey(ctx context.Context, payment *domain.Payment) error {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE payments
		    SET refund_bank_operation_key = $2
		  WHERE id = $1
		    AND status = $3`,
		payment.ID(),
		nullableString(payment.RefundBankOperationKey()),
		domain.PaymentStatusCaptured,
	)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	return ensurePaymentUpdateAffected(ctx, r, result, payment.ID())
}

func (r *PaymentRepository) UpdateVoidResult(ctx context.Context, payment *domain.Payment) error {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE payments
		    SET status = $2,
		        bank_void_id = $3,
		        void_bank_operation_key = $4,
		        capture_bank_operation_key = NULL,
		        updated_at = $5
		  WHERE id = $1
		    AND status = $6`,
		payment.ID(),
		payment.Status(),
		nullableString(payment.BankVoidID()),
		payment.VoidBankOperationKey(),
		payment.UpdatedAt(),
		domain.PaymentStatusAuthorized,
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

func (r *PaymentRepository) UpdateExpiredResult(ctx context.Context, payment *domain.Payment) error {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE payments
		    SET status = $2,
		        capture_bank_operation_key = NULL,
		        void_bank_operation_key = NULL,
		        updated_at = $3
		  WHERE id = $1
		    AND status = $4`,
		payment.ID(),
		payment.Status(),
		payment.UpdatedAt(),
		domain.PaymentStatusAuthorized,
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

func (r *PaymentRepository) RefreshExpiredAuthorizations(ctx context.Context, query app.SearchPaymentsQuery, now time.Time) error {
	_, err := r.db.ExecContext(
		ctx,
		`UPDATE payments
		    SET status = $4,
		        capture_bank_operation_key = NULL,
		        void_bank_operation_key = NULL,
		        updated_at = $3
		  WHERE id IN (
		        SELECT id
		          FROM payments
		         WHERE status = $5
		           AND authorization_expires_at <= $3
		           AND capture_bank_operation_key IS NULL
		           AND void_bank_operation_key IS NULL
		           AND ($1 = '' OR order_id = $1)
		           AND ($2 = '' OR customer_id = $2)
		  )`,
		query.OrderID,
		query.CustomerID,
		now,
		domain.PaymentStatusExpired,
		domain.PaymentStatusAuthorized,
	)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	return nil
}

func (r *PaymentRepository) SaveVoidBankOperationKey(ctx context.Context, payment *domain.Payment) error {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE payments
		    SET void_bank_operation_key = $2
		  WHERE id = $1
		    AND status = $3`,
		payment.ID(),
		nullableString(payment.VoidBankOperationKey()),
		domain.PaymentStatusAuthorized,
	)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	return ensurePaymentUpdateAffected(ctx, r, result, payment.ID())
}

func (r *PaymentRepository) UpdateAuthorizationResult(ctx context.Context, payment *domain.Payment) error {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE payments
		    SET status = $2,
		        bank_authorization_id = $3,
		        authorization_expires_at = $4,
		        decline_reason = $5,
		        updated_at = $6
		  WHERE id = $1
		    AND status = $7`,
		payment.ID(),
		payment.Status(),
		nullableString(payment.BankAuthorizationID()),
		nullableTime(payment.AuthorizationExpiresAt()),
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

func (r *PaymentRepository) UpdateCaptureResult(ctx context.Context, payment *domain.Payment) error {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE payments
		    SET status = $2,
		        bank_capture_id = $3,
		        capture_bank_operation_key = $4,
		        void_bank_operation_key = NULL,
		        updated_at = $5
		  WHERE id = $1
		    AND status = $6`,
		payment.ID(),
		payment.Status(),
		nullableString(payment.BankCaptureID()),
		nullableString(payment.CaptureBankOperationKey()),
		payment.UpdatedAt(),
		domain.PaymentStatusAuthorized,
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

func (r *PaymentRepository) SaveCaptureBankOperationKey(ctx context.Context, payment *domain.Payment) error {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE payments
		    SET capture_bank_operation_key = $2
		  WHERE id = $1
		    AND status = $3`,
		payment.ID(),
		nullableString(payment.CaptureBankOperationKey()),
		domain.PaymentStatusAuthorized,
	)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	return ensurePaymentUpdateAffected(ctx, r, result, payment.ID())
}

func (r *PaymentRepository) FindByID(ctx context.Context, id domain.PaymentID) (*domain.Payment, error) {
	var (
		orderID                       string
		customerID                    string
		amountCents                   int64
		currency                      string
		status                        domain.PaymentStatus
		bankAuthorizationID           sql.NullString
		authorizationExpiresAt        sql.NullTime
		authorizationBankOperationKey string
		authorizationCardFingerprint  string
		bankCaptureID                 sql.NullString
		captureBankOperationKey       sql.NullString
		bankRefundID                  sql.NullString
		refundBankOperationKey        sql.NullString
		bankVoidID                    sql.NullString
		voidBankOperationKey          sql.NullString
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
		&authorizationExpiresAt,
		&authorizationBankOperationKey,
		&authorizationCardFingerprint,
		&bankCaptureID,
		&captureBankOperationKey,
		&bankRefundID,
		&refundBankOperationKey,
		&bankVoidID,
		&voidBankOperationKey,
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
		nullTimeValue(authorizationExpiresAt),
		authorizationBankOperationKey,
		authorizationCardFingerprint,
		nullStringValue(bankCaptureID),
		nullStringValue(captureBankOperationKey),
		nullStringValue(bankRefundID),
		nullStringValue(refundBankOperationKey),
		nullStringValue(bankVoidID),
		nullStringValue(voidBankOperationKey),
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
		authorizationExpiresAt        sql.NullTime
		authorizationBankOperationKey string
		authorizationCardFingerprint  string
		bankCaptureID                 sql.NullString
		captureBankOperationKey       sql.NullString
		bankRefundID                  sql.NullString
		refundBankOperationKey        sql.NullString
		bankVoidID                    sql.NullString
		voidBankOperationKey          sql.NullString
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
		&authorizationExpiresAt,
		&authorizationBankOperationKey,
		&authorizationCardFingerprint,
		&bankCaptureID,
		&captureBankOperationKey,
		&bankRefundID,
		&refundBankOperationKey,
		&bankVoidID,
		&voidBankOperationKey,
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
		nullTimeValue(authorizationExpiresAt),
		authorizationBankOperationKey,
		authorizationCardFingerprint,
		nullStringValue(bankCaptureID),
		nullStringValue(captureBankOperationKey),
		nullStringValue(bankRefundID),
		nullStringValue(refundBankOperationKey),
		nullStringValue(bankVoidID),
		nullStringValue(voidBankOperationKey),
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
		        authorization_expires_at = $4,
		        authorization_bank_operation_key = $5,
		        authorization_card_fingerprint = $6,
		        bank_capture_id = $7,
		        capture_bank_operation_key = $8,
		        decline_reason = $9,
		        updated_at = $10
		  WHERE id = $1`,
		payment.ID(),
		payment.Status(),
		nullableString(payment.BankAuthorizationID()),
		nullableTime(payment.AuthorizationExpiresAt()),
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

func ensurePaymentUpdateAffected(ctx context.Context, r *PaymentRepository, result sql.Result, id domain.PaymentID) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	if affected == 0 {
		_, err := r.FindByID(ctx, id)
		if err != nil {
			return err
		}
		return app.NewPaymentInvalidStatusConflict(nil)
	}
	return nil
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

func nullTimeValue(value sql.NullTime) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time
}

func nullStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}
