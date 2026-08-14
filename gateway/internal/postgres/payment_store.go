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

type idempotencyRecordStatus string

const (
	idempotencyRecordInProgress idempotencyRecordStatus = "in_progress"
	idempotencyRecordCompleted  idempotencyRecordStatus = "completed"
)

type idempotencyClaimOutcome int

const (
	idempotencyClaimAcquired idempotencyClaimOutcome = iota
	idempotencyClaimRecovered
	idempotencyClaimReplayed
	idempotencyClaimInProgress
	idempotencyClaimConflict
)

type idempotencyRecord struct {
	operation          string
	key                string
	requestFingerprint string
	paymentID          domain.PaymentID
	status             idempotencyRecordStatus
	claimedAt          time.Time
	result             app.PaymentCommandResult
}

type paymentResultSnapshot struct {
	ID                     string    `json:"id"`
	OrderID                string    `json:"order_id"`
	CustomerID             string    `json:"customer_id"`
	AmountCents            int64     `json:"amount"`
	Currency               string    `json:"currency"`
	Status                 string    `json:"status"`
	DeclineReason          string    `json:"decline_reason,omitempty"`
	AuthorizationExpiresAt time.Time `json:"authorization_expires_at"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

func NewPaymentStore(db *sql.DB) *PaymentStore {
	return &PaymentStore{db: db}
}

// Payment command lifecycle.

// ClaimAuthorizationStart claims the public idempotency record before starting
// a new Payment authorization.
func (r *PaymentStore) ClaimAuthorizationStart(ctx context.Context, request app.AuthorizationStartClaimRequest) (app.PaymentCommandClaim, error) {
	if request.Now().IsZero() {
		return app.PaymentCommandClaim{}, app.NewInternalPaymentError(errors.New("payment store business time is required"))
	}
	payment := request.Payment()
	if payment == nil {
		return app.PaymentCommandClaim{}, app.NewInternalPaymentError(errors.New("authorization start claim requires a payment"))
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return app.PaymentCommandClaim{}, app.NewInternalPaymentError(err)
	}
	defer tx.Rollback()

	record, outcome, err := claimIdempotency(ctx, tx, request, payment.ID())
	if err != nil {
		return app.PaymentCommandClaim{}, app.NewInternalPaymentError(err)
	}

	var (
		claim    app.PaymentCommandClaim
		claimErr error
	)
	switch outcome {
	case idempotencyClaimReplayed:
		claim = app.NewReplayedPaymentCommand(request, record.result)
	case idempotencyClaimInProgress:
		claimErr = app.NewPaymentIdempotencyInProgressError(nil)
	case idempotencyClaimConflict:
		claimErr = app.NewPaymentIdempotencyConflictError(nil)
	case idempotencyClaimAcquired:
		if err := insertPayment(ctx, tx, payment); err != nil {
			return app.PaymentCommandClaim{}, app.NewInternalPaymentError(err)
		}
		claim = app.NewClaimedPaymentCommand(request, payment)
	case idempotencyClaimRecovered:
		payment, err = findRecoveredPayment(ctx, tx, record.paymentID, false)
		if err != nil {
			return app.PaymentCommandClaim{}, err
		}
		if payment.Status() != request.ExpectedStatus() {
			return app.PaymentCommandClaim{}, app.NewPaymentStatusConflictError(nil)
		}
		claim = app.NewRecoveredPaymentCommand(request, payment)
	default:
		return app.PaymentCommandClaim{}, app.NewInternalPaymentError(errors.New("unknown idempotency claim outcome"))
	}
	if err := tx.Commit(); err != nil {
		return app.PaymentCommandClaim{}, app.NewInternalPaymentError(err)
	}
	return claim, claimErr
}

// ClaimExistingPaymentCommand claims the public idempotency record before an
// operation on an existing Payment.
func (r *PaymentStore) ClaimExistingPaymentCommand(ctx context.Context, request app.ExistingPaymentCommandClaimRequest) (app.PaymentCommandClaim, error) {
	if request.Now().IsZero() {
		return app.PaymentCommandClaim{}, app.NewInternalPaymentError(errors.New("payment store business time is required"))
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return app.PaymentCommandClaim{}, app.NewInternalPaymentError(err)
	}
	defer tx.Rollback()

	record, outcome, err := claimIdempotency(ctx, tx, request, request.PaymentID())
	if err != nil {
		return app.PaymentCommandClaim{}, app.NewInternalPaymentError(err)
	}

	var (
		claim    app.PaymentCommandClaim
		claimErr error
		payment  *domain.Payment
	)
	switch outcome {
	case idempotencyClaimReplayed:
		claim = app.NewReplayedPaymentCommand(request, record.result)
	case idempotencyClaimInProgress:
		claimErr = app.NewPaymentIdempotencyInProgressError(nil)
	case idempotencyClaimConflict:
		claimErr = app.NewPaymentIdempotencyConflictError(nil)
	case idempotencyClaimAcquired:
		payment, err = findPaymentByID(ctx, tx, request.PaymentID(), true)
		if err != nil {
			return app.PaymentCommandClaim{}, err
		}
		if err := validateAcquiredPaymentCommand(request, payment); err != nil {
			return app.PaymentCommandClaim{}, err
		}
		if shouldExpireBeforeNewBankCall(request, payment) {
			if err := expirePaymentBeforeNewBankCall(ctx, tx, request, payment); err != nil {
				return app.PaymentCommandClaim{}, err
			}
			claimErr = app.NewPaymentAuthorizationExpiredError(nil)
		} else if request.BankOperationKeyKind() != "" {
			if err := ensureBankOperationKey(ctx, tx, payment, request.BankOperationKeyKind(), request.BankOperationKey()); err != nil {
				return app.PaymentCommandClaim{}, err
			}
			payment, err = findPaymentByID(ctx, tx, request.PaymentID(), false)
			if err != nil {
				return app.PaymentCommandClaim{}, err
			}
		}
		if claimErr == nil {
			claim = app.NewClaimedPaymentCommand(request, payment)
		}
	case idempotencyClaimRecovered:
		payment, err = findRecoveredPayment(ctx, tx, record.paymentID, true)
		if err != nil {
			return app.PaymentCommandClaim{}, err
		}
		if request.PaymentID() != record.paymentID {
			return app.PaymentCommandClaim{}, app.NewIdempotencyRecoveryError(app.IdempotencyRecoveryConflict, app.NewPaymentIdempotencyConflictError(nil))
		}
		if request.ExpectedStatus() != "" && payment.Status() != request.ExpectedStatus() {
			return app.PaymentCommandClaim{}, app.NewIdempotencyRecoveryError(app.IdempotencyRecoveryConflict, app.NewPaymentStatusConflictError(nil))
		}
		if request.AuthorizationCardFingerprint() != "" && request.AuthorizationCardFingerprint() != payment.AuthorizationCardFingerprint() {
			return app.PaymentCommandClaim{}, app.NewIdempotencyRecoveryError(app.IdempotencyRecoveryConflict, app.NewPaymentIdempotencyConflictError(nil))
		}
		if err := ensureRecoveredBankOperationKey(payment, request.BankOperationKeyKind()); err != nil {
			return app.PaymentCommandClaim{}, err
		}
		claim = app.NewRecoveredPaymentCommand(request, payment)
	default:
		return app.PaymentCommandClaim{}, app.NewInternalPaymentError(errors.New("unknown idempotency claim outcome"))
	}
	if err := tx.Commit(); err != nil {
		return app.PaymentCommandClaim{}, app.NewInternalPaymentError(err)
	}
	return claim, claimErr
}

func (r *PaymentStore) CompletePaymentCommand(ctx context.Context, claim app.PaymentCommandClaim, result app.PaymentCommandResult, completedAt time.Time) error {
	if completedAt.IsZero() {
		return app.NewInternalPaymentError(errors.New("payment store business time is required"))
	}
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
		        payment_result = $5::jsonb,
		        completed_at = $6
		  WHERE operation = $1
		    AND key = $2
		    AND request_fingerprint = $3
		    AND status = 'in_progress'`,
		claim.Operation(),
		claim.Key(),
		claim.RequestFingerprint(),
		result.HTTPStatus,
		string(paymentResult),
		completedAt,
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
	_, err := r.db.ExecContext(
		ctx,
		`DELETE FROM idempotency_records
		  WHERE operation = $1
		    AND key = $2
		    AND status = 'in_progress'`,
		claim.Operation(),
		claim.Key(),
	)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	return nil
}

// Payment queries.

func (r *PaymentStore) FindByID(ctx context.Context, id domain.PaymentID, now time.Time) (*domain.Payment, error) {
	if now.IsZero() {
		return nil, app.NewInternalPaymentError(errors.New("payment store business time is required"))
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, app.NewInternalPaymentError(err)
	}
	defer tx.Rollback()

	payment, err := findPaymentByID(ctx, tx, id, true)
	if err != nil {
		return nil, err
	}
	if err := refreshReadExpiration(ctx, tx, payment, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, app.NewInternalPaymentError(err)
	}
	return payment, nil
}

func (r *PaymentStore) Search(ctx context.Context, query app.SearchPaymentsQuery, now time.Time) ([]*domain.Payment, error) {
	if now.IsZero() {
		return nil, app.NewInternalPaymentError(errors.New("payment store business time is required"))
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, app.NewInternalPaymentError(err)
	}
	defer tx.Rollback()

	if err := refreshExpiredAuthorizations(ctx, tx, query, now); err != nil {
		return nil, app.NewInternalPaymentError(err)
	}

	rows, err := tx.QueryContext(
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

	var payments []*domain.Payment
	for rows.Next() {
		payment, err := scanPayment(rows)
		if err != nil {
			return nil, app.NewInternalPaymentError(err)
		}
		payments = append(payments, payment)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, app.NewInternalPaymentError(err)
	}
	if err := rows.Close(); err != nil {
		return nil, app.NewInternalPaymentError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, app.NewInternalPaymentError(err)
	}
	return payments, nil
}

// Operational maintenance and visibility.

func (r *PaymentStore) CleanupCompletedIdempotencyRecords(ctx context.Context, completedBefore time.Time) (int, error) {
	if completedBefore.IsZero() {
		return 0, app.NewInternalPaymentError(errors.New("payment store business time is required"))
	}
	result, err := r.db.ExecContext(
		ctx,
		`DELETE FROM idempotency_records
		  WHERE status = 'completed'
		    AND completed_at < $1`,
		completedBefore,
	)
	if err != nil {
		return 0, app.NewInternalPaymentError(err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, app.NewInternalPaymentError(err)
	}
	return int(rowsAffected), nil
}

// PendingPaymentMetrics returns aggregate current Pending Payment visibility. The
// query is read-only and intentionally returns no Payment, order, customer, bank,
// fingerprint, or card data.
func (r *PaymentStore) PendingPaymentMetrics(ctx context.Context) (int64, float64, error) {
	var count int64
	var oldestAgeSeconds float64
	err := r.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*), COALESCE(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP - MIN(created_at)), 0)
		   FROM payments
		  WHERE status = $1`,
		domain.PaymentStatusPending,
	).Scan(&count, &oldestAgeSeconds)
	if err != nil {
		return 0, 0, app.NewInternalPaymentError(err)
	}
	return count, oldestAgeSeconds, nil
}

// Command claim and recovery helpers.

func claimIdempotency(ctx context.Context, tx *sql.Tx, request app.PaymentCommandClaimRequest, paymentID domain.PaymentID) (idempotencyRecord, idempotencyClaimOutcome, error) {
	now := request.Now()
	recovered, ok, err := recoverIdempotencyClaim(ctx, tx, request, now)
	if err != nil {
		return idempotencyRecord{}, 0, err
	}
	if ok {
		return recovered, idempotencyClaimRecovered, nil
	}
	insert, err := tx.ExecContext(
		ctx,
		`INSERT INTO idempotency_records (
		     operation,
		     key,
		     request_fingerprint,
		     payment_id,
		     status,
		     claimed_at
		 )
		 VALUES ($1, $2, $3, $4, 'in_progress', $5)
		 ON CONFLICT (operation, key) DO NOTHING`,
		request.Operation(),
		request.Key(),
		request.RequestFingerprint(),
		nullableString(string(paymentID)),
		now,
	)
	if err != nil {
		return idempotencyRecord{}, 0, err
	}
	rowsAffected, err := insert.RowsAffected()
	if err != nil {
		return idempotencyRecord{}, 0, err
	}
	if rowsAffected == 1 {
		return idempotencyRecord{
			operation:          request.Operation(),
			key:                request.Key(),
			requestFingerprint: request.RequestFingerprint(),
			paymentID:          paymentID,
		}, idempotencyClaimAcquired, nil
	}

	record, err := selectIdempotencyRecord(ctx, tx, request.Operation(), request.Key())
	if err != nil {
		return idempotencyRecord{}, 0, err
	}

	if record.requestFingerprint != request.RequestFingerprint() {
		return record, idempotencyClaimConflict, nil
	}
	switch record.status {
	case idempotencyRecordInProgress:
		return record, idempotencyClaimInProgress, nil
	case idempotencyRecordCompleted:
		return record, idempotencyClaimReplayed, nil
	default:
		return idempotencyRecord{}, 0, errors.New("unknown idempotency record status")
	}
}

func recoverIdempotencyClaim(ctx context.Context, tx *sql.Tx, request app.PaymentCommandClaimRequest, now time.Time) (idempotencyRecord, bool, error) {
	// Refresh ownership atomically so concurrent retriers observe this claim as
	// in progress instead of both proceeding to the Mock Bank.
	var paymentID sql.NullString
	err := tx.QueryRowContext(
		ctx,
		`UPDATE idempotency_records
		    SET claimed_at = $5
		  WHERE operation = $1
		    AND key = $2
		    AND request_fingerprint = $3
		    AND status = 'in_progress'
		    AND claimed_at <= $4
		RETURNING payment_id`,
		request.Operation(),
		request.Key(),
		request.RequestFingerprint(),
		now.Add(-request.ClaimStuckAfter()),
		now,
	).Scan(&paymentID)
	if errors.Is(err, sql.ErrNoRows) {
		return idempotencyRecord{}, false, nil
	}
	if err != nil {
		return idempotencyRecord{}, false, err
	}
	return idempotencyRecord{
		operation:          request.Operation(),
		key:                request.Key(),
		requestFingerprint: request.RequestFingerprint(),
		paymentID:          domain.PaymentID(nullStringValue(paymentID)),
		status:             idempotencyRecordInProgress,
		claimedAt:          now,
	}, true, nil
}

func selectIdempotencyRecord(ctx context.Context, tx *sql.Tx, operation string, key string) (idempotencyRecord, error) {
	var (
		record      idempotencyRecord
		paymentData []byte
		httpStatus  sql.NullInt64
		paymentID   sql.NullString
		claimedAt   time.Time
	)
	err := tx.QueryRowContext(
		ctx,
		`SELECT request_fingerprint,
		        payment_id,
		        status,
		        http_status,
		        payment_result,
		        claimed_at
		   FROM idempotency_records
		  WHERE operation = $1
		    AND key = $2`,
		operation,
		key,
	).Scan(&record.requestFingerprint, &paymentID, &record.status, &httpStatus, &paymentData, &claimedAt)
	if err != nil {
		return idempotencyRecord{}, err
	}

	record.operation = operation
	record.key = key
	record.paymentID = domain.PaymentID(nullStringValue(paymentID))
	record.claimedAt = claimedAt
	if record.status == idempotencyRecordCompleted {
		paymentResult, err := decodePaymentResultSnapshot(paymentData)
		if err != nil {
			return idempotencyRecord{}, err
		}
		record.result = app.PaymentCommandResult{
			Payment:    paymentResult,
			HTTPStatus: int(httpStatus.Int64),
		}
	}
	return record, nil
}

func findRecoveredPayment(ctx context.Context, tx *sql.Tx, paymentID domain.PaymentID, forUpdate bool) (*domain.Payment, error) {
	payment, err := findPaymentByID(ctx, tx, paymentID, forUpdate)
	if err != nil && app.HasPaymentErrorKind(err, app.PaymentErrorNotFound) {
		return nil, app.NewIdempotencyRecoveryError(app.IdempotencyRecoveryUnrecoverable, app.NewInternalPaymentError(err))
	}
	return payment, err
}

func validateAcquiredPaymentCommand(request app.ExistingPaymentCommandClaimRequest, payment *domain.Payment) error {
	if request.ExpectedStatus() != "" && payment.Status() != request.ExpectedStatus() {
		return app.NewPaymentStatusConflictError(nil)
	}
	if request.AuthorizationCardFingerprint() != "" && request.AuthorizationCardFingerprint() != payment.AuthorizationCardFingerprint() {
		return app.NewPaymentStatusConflictError(nil)
	}
	return nil
}

func expirePaymentBeforeNewBankCall(ctx context.Context, tx *sql.Tx, request app.ExistingPaymentCommandClaimRequest, payment *domain.Payment) error {
	if err := payment.MarkExpired(request.Now()); err != nil {
		return app.NewInternalPaymentError(err)
	}
	if err := updatePayment(ctx, tx, payment, domain.PaymentStatusAuthorized); err != nil {
		return err
	}
	return deleteIdempotencyClaim(ctx, tx, request.Operation(), request.Key())
}

func shouldExpireBeforeNewBankCall(request app.ExistingPaymentCommandClaimRequest, payment *domain.Payment) bool {
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

func ensureBankOperationKey(ctx context.Context, tx *sql.Tx, payment *domain.Payment, operation app.BankOperationKeyKind, newKey string) error {
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

	// Persist the key before the bank call so a recovered claim repeats the
	// original bank operation rather than creating a second one.
	result, err := tx.ExecContext(
		ctx,
		`UPDATE payments SET `+column+` = $2 WHERE id = $1 AND status = $3`,
		payment.ID(),
		value,
		payment.Status(),
	)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	return ensurePaymentUpdateAffected(ctx, tx, result, payment.ID())
}

func ensureRecoveredBankOperationKey(payment *domain.Payment, operation app.BankOperationKeyKind) error {
	var key string
	switch operation {
	case "":
		return nil
	case app.BankOperationKeyCapture:
		key = payment.CaptureBankOperationKey()
	case app.BankOperationKeyVoid:
		key = payment.VoidBankOperationKey()
	case app.BankOperationKeyRefund:
		key = payment.RefundBankOperationKey()
	default:
		return app.NewIdempotencyRecoveryError(app.IdempotencyRecoveryUnrecoverable, app.NewInternalPaymentError(errors.New("unknown bank operation")))
	}
	if key == "" {
		return app.NewIdempotencyRecoveryError(app.IdempotencyRecoveryUnrecoverable, app.NewInternalPaymentError(errors.New("missing recovered bank operation key")))
	}
	return nil
}

func deleteIdempotencyClaim(ctx context.Context, tx *sql.Tx, operation string, key string) error {
	_, err := tx.ExecContext(
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

// Payment persistence and mapping helpers.

func insertPayment(ctx context.Context, tx *sql.Tx, payment *domain.Payment) error {
	_, err := tx.ExecContext(
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
		return err
	}
	return nil
}

func updatePayment(ctx context.Context, tx *sql.Tx, payment *domain.Payment, expectedStatus domain.PaymentStatus) error {
	result, err := tx.ExecContext(
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
	return ensurePaymentUpdateAffected(ctx, tx, result, payment.ID())
}

func ensurePaymentUpdateAffected(ctx context.Context, tx *sql.Tx, result sql.Result, id domain.PaymentID) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	if affected == 0 {
		_, err := findPaymentByID(ctx, tx, id, false)
		if err != nil {
			return err
		}
		return app.NewPaymentStatusConflictError(nil)
	}
	return nil
}

func findPaymentByID(ctx context.Context, tx *sql.Tx, id domain.PaymentID, forUpdate bool) (*domain.Payment, error) {
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
	err := tx.QueryRowContext(
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

func scanPayment(rows *sql.Rows) (*domain.Payment, error) {
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
	err := rows.Scan(
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
		return nil, err
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
		return nil, err
	}
	return payment, nil
}

func refreshExpiredAuthorizations(ctx context.Context, tx *sql.Tx, query app.SearchPaymentsQuery, now time.Time) error {
	if now.IsZero() {
		return nil
	}
	_, err := tx.ExecContext(
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
		return err
	}
	return nil
}

func refreshReadExpiration(ctx context.Context, tx *sql.Tx, payment *domain.Payment, now time.Time) error {
	if now.IsZero() || payment.Status() != domain.PaymentStatusAuthorized || !payment.AuthorizationExpired(now) {
		return nil
	}
	if payment.CaptureBankOperationKey() != "" || payment.VoidBankOperationKey() != "" {
		return nil
	}
	if err := payment.MarkExpired(now); err != nil {
		return app.NewInternalPaymentError(err)
	}
	return updatePayment(ctx, tx, payment, domain.PaymentStatusAuthorized)
}

// Database boundary conversions.

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
