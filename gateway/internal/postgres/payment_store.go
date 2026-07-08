package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
)

type PaymentStore struct {
	db *sql.DB
}

type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func NewPaymentStore(db *sql.DB) *PaymentStore {
	return &PaymentStore{db: db}
}

func (r *PaymentStore) ClaimPaymentCommand(ctx context.Context, request app.PaymentCommandClaimRequest) (app.PaymentCommandClaim, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return app.PaymentCommandClaim{}, app.NewInternalPaymentError(err)
	}
	defer tx.Rollback()

	record, outcome, err := claimIdempotency(ctx, tx, request.Operation(), request.Key(), request.RequestFingerprint())
	if err != nil {
		return app.PaymentCommandClaim{}, err
	}
	if outcome != idempotencyClaimAcquired {
		if err := tx.Commit(); err != nil {
			return app.PaymentCommandClaim{}, app.NewInternalPaymentError(err)
		}
		return replayOrError(request, record)
	}

	var claimedPayment *domain.Payment
	if request.Payment() != nil {
		if err := insertPayment(ctx, tx, request.Payment()); err != nil {
			return app.PaymentCommandClaim{}, err
		}
		claimedPayment = request.Payment()
	} else if request.PaymentID() != "" {
		payment, err := findPaymentByID(ctx, tx, request.PaymentID(), true)
		if err != nil {
			return app.PaymentCommandClaim{}, err
		}
		if request.ExpectedStatus() != "" && payment.Status() != request.ExpectedStatus() {
			return app.PaymentCommandClaim{}, app.NewPaymentStatusConflictError(nil)
		}
		if request.AuthorizationCardFingerprint() != "" && request.AuthorizationCardFingerprint() != payment.AuthorizationCardFingerprint() {
			return app.PaymentCommandClaim{}, app.NewPaymentStatusConflictError(nil)
		}
		if shouldExpireBeforeNewBankCall(request, payment) {
			if err := payment.MarkExpired(request.Now()); err != nil {
				return app.PaymentCommandClaim{}, app.NewInternalPaymentError(err)
			}
			if err := updatePayment(ctx, tx, payment, domain.PaymentStatusAuthorized); err != nil {
				return app.PaymentCommandClaim{}, err
			}
			if err := deleteIdempotencyClaim(ctx, tx, request.Operation(), request.Key()); err != nil {
				return app.PaymentCommandClaim{}, err
			}
			if err := tx.Commit(); err != nil {
				return app.PaymentCommandClaim{}, app.NewInternalPaymentError(err)
			}
			return app.PaymentCommandClaim{}, app.NewPaymentAuthorizationExpiredError(nil)
		}
		if request.BankOperationKeyKind() != "" {
			if err := ensureBankOperationKey(ctx, tx, payment, request.BankOperationKeyKind(), request.BankOperationKey()); err != nil {
				return app.PaymentCommandClaim{}, err
			}
			payment, err = findPaymentByID(ctx, tx, request.PaymentID(), false)
			if err != nil {
				return app.PaymentCommandClaim{}, err
			}
		}
		claimedPayment = payment
	}

	if err := tx.Commit(); err != nil {
		return app.PaymentCommandClaim{}, app.NewInternalPaymentError(err)
	}
	return app.NewClaimedPaymentCommand(request, claimedPayment), nil
}

func shouldExpireBeforeNewBankCall(request app.PaymentCommandClaimRequest, payment *domain.Payment) bool {
	if request.Now().IsZero() || payment.Status() != domain.PaymentStatusAuthorized || !payment.AuthorizationExpired(request.Now()) {
		return false
	}
	switch request.BankOperationKeyKind() {
	case app.BankOperationKeyCapture:
		return payment.CaptureBankOperationKey() == ""
	case app.BankOperationKeyVoid:
		return payment.VoidBankOperationKey() == ""
	default:
		return false
	}
}

func (r *PaymentStore) CompletePaymentCommand(ctx context.Context, claim app.PaymentCommandClaim, result app.PaymentCommandResult) error {
	paymentResult, err := encodePaymentResultSnapshot(result.Payment)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	defer tx.Rollback()

	if err := updatePayment(ctx, tx, claim.Payment(), claim.ExpectedStatus()); err != nil {
		return err
	}
	completion, err := tx.ExecContext(
		ctx,
		`UPDATE idempotency_records
		    SET status = 'completed',
		        http_status = $4,
		        payment_result = $5::jsonb
		  WHERE operation = $1
		    AND key = $2
		    AND request_fingerprint = $3
		    AND status = 'in_progress'`,
		claim.Operation(),
		claim.Key(),
		claim.RequestFingerprint(),
		result.HTTPStatus,
		string(paymentResult),
	)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	rowsAffected, err := completion.RowsAffected()
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	if rowsAffected != 1 {
		return app.NewPaymentIdempotencyConflictError(nil)
	}
	if err := tx.Commit(); err != nil {
		return app.NewInternalPaymentError(err)
	}
	return nil
}

func (r *PaymentStore) ReleasePaymentCommand(ctx context.Context, claim app.PaymentCommandClaim) error {
	return deleteIdempotencyClaim(ctx, r.db, claim.Operation(), claim.Key())
}

func deleteIdempotencyClaim(ctx context.Context, exec sqlExecutor, operation string, key string) error {
	_, err := exec.ExecContext(
		ctx,
		`DELETE FROM idempotency_records
		  WHERE operation = $1
		    AND key = $2
		    AND status = 'in_progress'`,
		operation,
		key,
	)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	return nil
}

func insertPayment(ctx context.Context, exec sqlExecutor, payment *domain.Payment) error {
	_, err := exec.ExecContext(
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

func (r *PaymentStore) ExpireAuthorization(ctx context.Context, payment *domain.Payment, expectedStatus domain.PaymentStatus) error {
	return updatePayment(ctx, r.db, payment, expectedStatus)
}

func (r *PaymentStore) RefreshExpiredAuthorizations(ctx context.Context, query app.SearchPaymentsQuery, now time.Time) error {
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
		query.OrderID(),
		query.CustomerID(),
		now,
		domain.PaymentStatusExpired,
		domain.PaymentStatusAuthorized,
	)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	return nil
}

func claimIdempotency(ctx context.Context, exec sqlExecutor, operation string, key string, requestFingerprint string) (idempotencyRecord, idempotencyClaimOutcome, error) {
	insert, err := exec.ExecContext(
		ctx,
		`INSERT INTO idempotency_records (
		     operation,
		     key,
		     request_fingerprint,
		     status
		 )
		 VALUES ($1, $2, $3, 'in_progress')
		 ON CONFLICT (operation, key) DO NOTHING`,
		operation,
		key,
		requestFingerprint,
	)
	if err != nil {
		return idempotencyRecord{}, 0, app.NewInternalPaymentError(err)
	}
	rowsAffected, err := insert.RowsAffected()
	if err != nil {
		return idempotencyRecord{}, 0, app.NewInternalPaymentError(err)
	}
	if rowsAffected == 1 {
		return idempotencyRecord{
			operation:          operation,
			key:                key,
			requestFingerprint: requestFingerprint,
		}, idempotencyClaimAcquired, nil
	}

	var (
		record      idempotencyRecord
		status      idempotencyRecordStatus
		paymentData []byte
		httpStatus  sql.NullInt64
	)
	err = exec.QueryRowContext(
		ctx,
		`SELECT request_fingerprint,
		        status,
		        http_status,
		        payment_result
		   FROM idempotency_records
		  WHERE operation = $1
		    AND key = $2`,
		operation,
		key,
	).Scan(&record.requestFingerprint, &status, &httpStatus, &paymentData)
	if err != nil {
		return idempotencyRecord{}, 0, app.NewInternalPaymentError(err)
	}

	record.operation = operation
	record.key = key
	record.status = status
	if status == idempotencyRecordCompleted {
		paymentResult, err := decodePaymentResultSnapshot(paymentData)
		if err != nil {
			return idempotencyRecord{}, 0, app.NewInternalPaymentError(err)
		}
		record.result = app.PaymentCommandResult{
			Payment:    paymentResult,
			HTTPStatus: int(httpStatus.Int64),
		}
	}

	return record, idempotencyClaimExisting, nil
}

type idempotencyRecord struct {
	operation          string
	key                string
	requestFingerprint string
	status             idempotencyRecordStatus
	result             app.PaymentCommandResult
}

type idempotencyRecordStatus string

const (
	idempotencyRecordInProgress idempotencyRecordStatus = "in_progress"
	idempotencyRecordCompleted  idempotencyRecordStatus = "completed"
)

type idempotencyClaimOutcome int

const (
	idempotencyClaimAcquired idempotencyClaimOutcome = iota
	idempotencyClaimExisting
)

func replayOrError(request app.PaymentCommandClaimRequest, record idempotencyRecord) (app.PaymentCommandClaim, error) {
	if record.requestFingerprint != request.RequestFingerprint() {
		return app.PaymentCommandClaim{}, app.NewPaymentIdempotencyConflictError(nil)
	}
	if record.status == idempotencyRecordInProgress {
		return app.PaymentCommandClaim{}, app.NewPaymentIdempotencyInProgressError(nil)
	}
	return app.NewReplayedPaymentCommand(request, record.result), nil
}

func ensureBankOperationKey(ctx context.Context, exec sqlExecutor, payment *domain.Payment, operation app.BankOperationKeyKind, newKey string) error {
	var (
		column string
		value  string
	)
	switch operation {
	case app.BankOperationKeyCapture:
		if payment.CaptureBankOperationKey() != "" {
			return nil
		}
		if err := payment.SetCaptureBankOperationKey(newKey); err != nil {
			return app.NewInternalPaymentError(err)
		}
		column = "capture_bank_operation_key"
		value = payment.CaptureBankOperationKey()
	case app.BankOperationKeyVoid:
		if payment.VoidBankOperationKey() != "" {
			return nil
		}
		if err := payment.SetVoidBankOperationKey(newKey); err != nil {
			return app.NewInternalPaymentError(err)
		}
		column = "void_bank_operation_key"
		value = payment.VoidBankOperationKey()
	case app.BankOperationKeyRefund:
		if payment.RefundBankOperationKey() != "" {
			return nil
		}
		if err := payment.SetRefundBankOperationKey(newKey); err != nil {
			return app.NewInternalPaymentError(err)
		}
		column = "refund_bank_operation_key"
		value = payment.RefundBankOperationKey()
	default:
		return app.NewInternalPaymentError(errors.New("unknown bank operation"))
	}

	result, err := exec.ExecContext(
		ctx,
		`UPDATE payments SET `+column+` = $2 WHERE id = $1 AND status = $3`,
		payment.ID(),
		value,
		payment.Status(),
	)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	return ensurePaymentUpdateAffected(ctx, exec, result, payment.ID())
}

func (r *PaymentStore) FindByID(ctx context.Context, id domain.PaymentID) (*domain.Payment, error) {
	return findPaymentByID(ctx, r.db, id, false)
}

func findPaymentByID(ctx context.Context, exec sqlExecutor, id domain.PaymentID, forUpdate bool) (*domain.Payment, error) {
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
	query := `SELECT order_id,
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
	  WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	err := exec.QueryRowContext(
		ctx,
		query,
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
			return nil, app.NewPaymentNotFoundError(string(id), err)
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

func (r *PaymentStore) Search(ctx context.Context, query app.SearchPaymentsQuery) ([]*domain.Payment, error) {
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
		query.OrderID(),
		query.CustomerID(),
		query.Status(),
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

func updatePayment(ctx context.Context, exec sqlExecutor, payment *domain.Payment, expectedStatus domain.PaymentStatus) error {
	result, err := exec.ExecContext(
		ctx,
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
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	return ensurePaymentUpdateAffected(ctx, exec, result, payment.ID())
}

func ensurePaymentUpdateAffected(ctx context.Context, exec sqlExecutor, result sql.Result, id domain.PaymentID) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	if affected == 0 {
		_, err := findPaymentByID(ctx, exec, id, false)
		if err != nil {
			return err
		}
		return app.NewPaymentStatusConflictError(nil)
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

type paymentResultSnapshot struct {
	ID                     string    `json:"id"`
	OrderID                string    `json:"order_id"`
	CustomerID             string    `json:"customer_id"`
	AmountCents            int64     `json:"amount"`
	Currency               string    `json:"currency"`
	Status                 string    `json:"status"`
	DeclineReason          string    `json:"decline_reason,omitempty"`
	AuthorizationExpiresAt time.Time `json:"authorization_expires_at,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

func encodePaymentResultSnapshot(result app.PaymentResult) ([]byte, error) {
	return json.Marshal(paymentResultSnapshot{
		ID:                     result.ID,
		OrderID:                result.OrderID,
		CustomerID:             result.CustomerID,
		AmountCents:            result.AmountCents,
		Currency:               result.Currency,
		Status:                 result.Status,
		DeclineReason:          result.DeclineReason,
		AuthorizationExpiresAt: result.AuthorizationExpiresAt,
		CreatedAt:              result.CreatedAt,
		UpdatedAt:              result.UpdatedAt,
	})
}

func decodePaymentResultSnapshot(data []byte) (app.PaymentResult, error) {
	var snapshot paymentResultSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return app.PaymentResult{}, err
	}

	return app.PaymentResult{
		ID:                     snapshot.ID,
		OrderID:                snapshot.OrderID,
		CustomerID:             snapshot.CustomerID,
		AmountCents:            snapshot.AmountCents,
		Currency:               snapshot.Currency,
		Status:                 snapshot.Status,
		DeclineReason:          snapshot.DeclineReason,
		AuthorizationExpiresAt: snapshot.AuthorizationExpiresAt,
		CreatedAt:              snapshot.CreatedAt,
		UpdatedAt:              snapshot.UpdatedAt,
	}, nil
}
