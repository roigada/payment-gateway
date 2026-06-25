package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/roigada/payment-gateway/internal/app"
)

type IdempotencyRepository struct {
	db *sql.DB
}

func NewIdempotencyRepository(db *sql.DB) *IdempotencyRepository {
	return &IdempotencyRepository{db: db}
}

func (r *IdempotencyRepository) Claim(ctx context.Context, operation string, key string, requestFingerprint string) (app.IdempotencyRecord, app.IdempotencyClaimStatus, error) {
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO idempotency_records (
		     operation,
		     key,
		     request_fingerprint,
		     status
		 )
		 VALUES ($1, $2, $3, 'in_progress')`,
		operation,
		key,
		requestFingerprint,
	)
	if err == nil {
		return app.IdempotencyRecord{
			Operation:          operation,
			Key:                key,
			RequestFingerprint: requestFingerprint,
		}, app.IdempotencyClaimed, nil
	}
	if !isUniqueViolation(err) {
		return app.IdempotencyRecord{}, "", app.NewInternalPaymentError(err)
	}

	var (
		record       app.IdempotencyRecord
		status       string
		responseBody []byte
		responseCode sql.NullInt64
	)
	err = r.db.QueryRowContext(
		ctx,
		`SELECT request_fingerprint,
			        status,
			        response_status,
			        response_body
			   FROM idempotency_records
			  WHERE operation = $1
			    AND key = $2`,
		operation,
		key,
	).Scan(&record.RequestFingerprint, &status, &responseCode, &responseBody)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return app.IdempotencyRecord{}, "", app.NewInternalPaymentError(err)
		}
		return app.IdempotencyRecord{}, "", app.NewInternalPaymentError(err)
	}

	record.Operation = operation
	record.Key = key
	if status == string(app.IdempotencyCompleted) {
		result, err := decodePaymentResultSnapshot(responseBody)
		if err != nil {
			return app.IdempotencyRecord{}, "", app.NewInternalPaymentError(err)
		}
		record.Result = result
		record.ResponseStatus = int(responseCode.Int64)
		record.Result.ResponseStatus = record.ResponseStatus
	}

	return record, app.IdempotencyClaimStatus(status), nil
}

func (r *IdempotencyRepository) Complete(ctx context.Context, record app.IdempotencyRecord) error {
	responseBody, err := encodePaymentResultSnapshot(record.Result)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}

	result, err := r.db.ExecContext(
		ctx,
		`UPDATE idempotency_records
		    SET status = 'completed',
		        response_status = $4,
		        response_body = $5::jsonb
		  WHERE operation = $1
		    AND key = $2
		    AND request_fingerprint = $3
		    AND status = 'in_progress'`,
		record.Operation,
		record.Key,
		record.RequestFingerprint,
		record.ResponseStatus,
		string(responseBody),
	)
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return app.NewInternalPaymentError(err)
	}
	if rowsAffected != 1 {
		return app.NewPaymentIdempotencyConflict(nil)
	}
	return nil
}

func (r *IdempotencyRepository) Release(ctx context.Context, operation string, key string) error {
	_, err := r.db.ExecContext(
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

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
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
